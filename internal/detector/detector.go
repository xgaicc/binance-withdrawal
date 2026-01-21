// Package detector provides real-time detection of incoming sub-account transfers.
package detector

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/binance-withdrawal/internal/binance"
	"github.com/binance-withdrawal/internal/model"
)

// TransferHandler is called when a new transfer is detected.
// The handler receives the full transfer details retrieved from Binance.
type TransferHandler func(transfer *model.TransferRecord)

// Detector monitors the Binance User Data Stream for incoming transfers
// and queries the transfer history API for full details.
type Detector struct {
	client     *binance.Client
	stream     *binance.UserDataStream
	handler    TransferHandler
	logger     *slog.Logger

	// Track when we last processed transfers (for lookback on reconnect)
	lastProcessedTime time.Time
	lastProcessedMu   sync.RWMutex

	// Configuration
	lookbackDuration time.Duration

	// Channel to signal when balance updates occur
	balanceUpdateCh chan *binance.BalanceUpdateEvent

	// Context for the detector
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// DetectorOption configures the Detector.
type DetectorOption func(*Detector)

// WithLogger sets a custom logger.
func WithLogger(l *slog.Logger) DetectorOption {
	return func(d *Detector) {
		d.logger = l
	}
}

// WithLookbackDuration sets how far back to check on startup/reconnect.
func WithLookbackDuration(dur time.Duration) DetectorOption {
	return func(d *Detector) {
		d.lookbackDuration = dur
	}
}

// WithStreamURL sets a custom WebSocket URL.
func WithStreamURL(url string) DetectorOption {
	return func(d *Detector) {
		if d.stream != nil {
			d.stream = binance.NewUserDataStream(d.client, d, binance.WithStreamURL(url))
		}
	}
}

// New creates a new transfer detector.
func New(client *binance.Client, handler TransferHandler, opts ...DetectorOption) *Detector {
	d := &Detector{
		client:           client,
		handler:          handler,
		logger:           slog.Default(),
		lookbackDuration: 5 * time.Minute,
		balanceUpdateCh:  make(chan *binance.BalanceUpdateEvent, 100),
	}

	// Apply options first
	for _, opt := range opts {
		opt(d)
	}

	// Create the User Data Stream with the detector as the event handler
	d.stream = binance.NewUserDataStream(client, d)

	return d
}

// Start begins monitoring for transfers.
// It connects to the WebSocket and starts the event processing loop.
func (d *Detector) Start(ctx context.Context) error {
	d.ctx, d.cancel = context.WithCancel(ctx)

	// Connect to the User Data Stream
	if err := d.stream.Connect(d.ctx); err != nil {
		return fmt.Errorf("connecting to user data stream: %w", err)
	}

	// Poll for any transfers that may have been missed
	if err := d.pollRecentTransfers(); err != nil {
		d.logger.Warn("failed to poll recent transfers on startup", "error", err)
	}

	// Start the event processing loop
	d.wg.Add(1)
	go d.processLoop()

	return nil
}

// Stop stops the detector and closes the WebSocket connection.
func (d *Detector) Stop() error {
	if d.cancel != nil {
		d.cancel()
	}
	d.wg.Wait()
	return d.stream.Close()
}

// processLoop handles balance update events and queries for transfer details.
func (d *Detector) processLoop() {
	defer d.wg.Done()

	for {
		select {
		case <-d.ctx.Done():
			return
		case event := <-d.balanceUpdateCh:
			d.handleBalanceUpdate(event)
		}
	}
}

// handleBalanceUpdate processes a balance update event by querying
// for the corresponding transfer details.
func (d *Detector) handleBalanceUpdate(event *binance.BalanceUpdateEvent) {
	d.logger.Debug("processing balance update",
		"asset", event.Asset,
		"delta", event.BalanceDelta,
		"clear_time", event.ClearTimeAt,
	)

	// Only process positive balance changes (incoming transfers)
	delta, err := strconv.ParseFloat(event.BalanceDelta, 64)
	if err != nil {
		d.logger.Error("failed to parse balance delta", "delta", event.BalanceDelta, "error", err)
		return
	}
	if delta <= 0 {
		d.logger.Debug("ignoring non-positive balance update", "delta", delta)
		return
	}

	// Query transfer history to find the corresponding transfer
	// Use a small time window around the event
	startTime := event.ClearTimeAt.Add(-1 * time.Minute)
	endTime := event.ClearTimeAt.Add(1 * time.Minute)

	transfers, err := d.client.GetSubAccountTransferHistory(startTime, endTime, 100)
	if err != nil {
		d.logger.Error("failed to get transfer history", "error", err)
		return
	}

	// Find transfers that match the asset and approximate amount
	for _, t := range transfers {
		// Match by asset
		if t.Asset != event.Asset {
			continue
		}

		// The amount might not be exact due to precision, so compare with tolerance
		transferAmt, err := strconv.ParseFloat(t.Qty, 64)
		if err != nil {
			continue
		}

		// Allow for small floating point differences
		if absFloat(transferAmt-delta) < 0.000001 || transferAmt == delta {
			d.processTransfer(&t)
		}
	}
}

// processTransfer converts a Binance transfer to our model and calls the handler.
func (d *Detector) processTransfer(t *binance.SubAccountTransfer) {
	now := time.Now()

	record := &model.TransferRecord{
		TranID:       strconv.FormatInt(t.TranID, 10),
		SubAccount:   t.FromEmail,
		Asset:        t.Asset,
		Amount:       t.Qty,
		Status:       string(model.StatusPending),
		TransferTime: t.CreateTimeAt,
		DetectedAt:   now,
	}

	d.logger.Info("transfer detected",
		"tran_id", record.TranID,
		"sub_account", record.SubAccount,
		"asset", record.Asset,
		"amount", record.Amount,
	)

	// Update last processed time
	d.lastProcessedMu.Lock()
	if t.CreateTimeAt.After(d.lastProcessedTime) {
		d.lastProcessedTime = t.CreateTimeAt
	}
	d.lastProcessedMu.Unlock()

	// Call the handler
	d.handler(record)
}

// pollRecentTransfers queries for transfers in the recent past.
// This is used on startup and after reconnection to catch any missed transfers.
func (d *Detector) pollRecentTransfers() error {
	d.lastProcessedMu.RLock()
	lastTime := d.lastProcessedTime
	d.lastProcessedMu.RUnlock()

	startTime := lastTime
	if startTime.IsZero() {
		startTime = time.Now().Add(-d.lookbackDuration)
	}
	endTime := time.Now()

	d.logger.Info("polling recent transfers",
		"start_time", startTime,
		"end_time", endTime,
	)

	transfers, err := d.client.GetSubAccountTransferHistory(startTime, endTime, 500)
	if err != nil {
		return fmt.Errorf("getting transfer history: %w", err)
	}

	d.logger.Info("found transfers in lookback period", "count", len(transfers))

	for i := range transfers {
		d.processTransfer(&transfers[i])
	}

	return nil
}

// EventHandler interface implementation for UserDataStream

// OnBalanceUpdate is called when a balance update event is received.
func (d *Detector) OnBalanceUpdate(event *binance.BalanceUpdateEvent) {
	select {
	case d.balanceUpdateCh <- event:
	default:
		d.logger.Warn("balance update channel full, dropping event")
	}
}

// OnError is called when an error occurs.
func (d *Detector) OnError(err error) {
	d.logger.Error("WebSocket error", "error", err)
}

// OnConnected is called when the WebSocket connection is established.
func (d *Detector) OnConnected() {
	d.logger.Info("WebSocket connected to User Data Stream")
}

// OnDisconnected is called when the WebSocket connection is lost.
func (d *Detector) OnDisconnected() {
	d.logger.Info("WebSocket disconnected from User Data Stream")
}

// OnReconnected is called when the WebSocket reconnects after a disconnection.
// This triggers a poll of recent transfers to catch any that were missed.
func (d *Detector) OnReconnected() {
	d.logger.Info("WebSocket reconnected to User Data Stream, polling for missed transfers")
	if err := d.pollRecentTransfers(); err != nil {
		d.logger.Warn("failed to poll recent transfers after reconnection", "error", err)
	}
}

// IsConnected returns whether the detector is connected to the WebSocket.
func (d *Detector) IsConnected() bool {
	return d.stream.IsConnected()
}

// GetLastProcessedTime returns the timestamp of the last processed transfer.
func (d *Detector) GetLastProcessedTime() time.Time {
	d.lastProcessedMu.RLock()
	defer d.lastProcessedMu.RUnlock()
	return d.lastProcessedTime
}

// absFloat returns the absolute value of a float64.
func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
