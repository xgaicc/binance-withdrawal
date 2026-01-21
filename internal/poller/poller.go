// Package poller provides a completion poller that monitors the status of
// forwarded withdrawals and marks them as completed when confirmed on-chain.
package poller

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/binance-withdrawal/internal/audit"
	"github.com/binance-withdrawal/internal/binance"
	"github.com/binance-withdrawal/internal/model"
	"github.com/binance-withdrawal/internal/sm"
	"github.com/binance-withdrawal/internal/store"
)

// Poller monitors forwarded withdrawals and updates their status when completed.
type Poller struct {
	client   *binance.Client
	store    *store.Store
	sm       *sm.StateMachine
	audit    *audit.Logger
	logger   *slog.Logger
	interval time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Option configures the Poller.
type Option func(*Poller)

// WithLogger sets a custom logger.
func WithLogger(l *slog.Logger) Option {
	return func(p *Poller) {
		p.logger = l
	}
}

// WithAuditLogger sets a custom audit logger.
func WithAuditLogger(l *audit.Logger) Option {
	return func(p *Poller) {
		p.audit = l
	}
}

// WithInterval sets the polling interval.
func WithInterval(d time.Duration) Option {
	return func(p *Poller) {
		p.interval = d
	}
}

// New creates a new completion poller.
func New(client *binance.Client, st *store.Store, opts ...Option) *Poller {
	p := &Poller{
		client:   client,
		store:    st,
		sm:       sm.New(),
		audit:    audit.New(),
		logger:   slog.Default(),
		interval: 30 * time.Second,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Start begins the polling loop.
func (p *Poller) Start(ctx context.Context) error {
	p.ctx, p.cancel = context.WithCancel(ctx)
	p.wg.Add(1)
	go p.pollLoop()
	return nil
}

// Stop stops the poller.
func (p *Poller) Stop() error {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	return nil
}

// pollLoop periodically checks for completed withdrawals.
func (p *Poller) pollLoop() {
	defer p.wg.Done()

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	// Initial poll on startup
	p.pollForwardedWithdrawals()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.pollForwardedWithdrawals()
		}
	}
}

// pollForwardedWithdrawals checks all FORWARDED records and updates their status.
func (p *Poller) pollForwardedWithdrawals() {
	records, err := p.store.GetByStatus(string(model.StatusForwarded))
	if err != nil {
		p.logger.Error("failed to get forwarded records", "error", err)
		return
	}

	if len(records) == 0 {
		return
	}

	p.logger.Debug("checking forwarded withdrawals", "count", len(records))

	for _, rec := range records {
		p.checkWithdrawalStatus(rec)
	}
}

// checkWithdrawalStatus checks the status of a single withdrawal and updates if completed.
func (p *Poller) checkWithdrawalStatus(rec *model.TransferRecord) {
	if rec.WithdrawID == "" {
		p.logger.Warn("forwarded record missing withdraw_id", "tran_id", rec.TranID)
		return
	}

	// Query Binance for the withdrawal status using the withdrawOrderId (which is our tranId)
	withdrawal, err := p.client.GetWithdrawalHistoryByOrderID(rec.TranID)
	if err != nil {
		p.logger.Error("failed to get withdrawal status",
			"tran_id", rec.TranID,
			"error", err,
		)
		return
	}

	if withdrawal == nil {
		p.logger.Debug("withdrawal not found yet", "tran_id", rec.TranID)
		return
	}

	// Check if the withdrawal is completed
	if withdrawal.Status == binance.WithdrawalStatusCompleted {
		p.markAsCompleted(rec, withdrawal)
	} else if withdrawal.Status == binance.WithdrawalStatusFailure ||
		withdrawal.Status == binance.WithdrawalStatusRejected ||
		withdrawal.Status == binance.WithdrawalStatusCancelled {
		// Mark as failed if the withdrawal failed on Binance side
		p.markAsFailed(rec, withdrawal)
	}
}

// markAsCompleted transitions the record to COMPLETED and logs an audit event.
func (p *Poller) markAsCompleted(rec *model.TransferRecord, withdrawal *binance.WithdrawalRecord) {
	now := time.Now()

	p.logger.Info("withdrawal completed",
		"tran_id", rec.TranID,
		"withdraw_id", withdrawal.ID,
		"txid", withdrawal.TxID,
		"fee", withdrawal.TransactionFee,
	)

	// Transition to COMPLETED
	_, err := p.sm.ToCompleted(rec, withdrawal.TxID, now)
	if err != nil {
		p.logger.Error("failed to transition to completed",
			"tran_id", rec.TranID,
			"error", err,
		)
		return
	}

	// Update additional fields
	rec.WithdrawTxID = withdrawal.TxID
	rec.WithdrawFee = withdrawal.TransactionFee

	if err := p.store.Update(rec); err != nil {
		p.logger.Error("failed to update record",
			"tran_id", rec.TranID,
			"error", err,
		)
		return
	}

	// Audit log: WITHDRAWAL_COMPLETED
	p.audit.LogWithdrawalCompleted(audit.WithdrawalCompletedEvent{
		TranID:     rec.TranID,
		WithdrawID: withdrawal.ID,
		TxID:       withdrawal.TxID,
		Fee:        withdrawal.TransactionFee,
		Timestamp:  now,
	})
}

// markAsFailed transitions the record to FAILED when the withdrawal fails on Binance.
func (p *Poller) markAsFailed(rec *model.TransferRecord, withdrawal *binance.WithdrawalRecord) {
	p.logger.Warn("withdrawal failed on Binance",
		"tran_id", rec.TranID,
		"withdraw_id", withdrawal.ID,
		"status", withdrawal.Status,
		"info", withdrawal.Info,
	)

	errMsg := "withdrawal failed on Binance"
	switch withdrawal.Status {
	case binance.WithdrawalStatusCancelled:
		errMsg = "withdrawal cancelled"
	case binance.WithdrawalStatusRejected:
		errMsg = "withdrawal rejected"
	case binance.WithdrawalStatusFailure:
		errMsg = "withdrawal failed"
		if withdrawal.Info != "" {
			errMsg = "withdrawal failed: " + withdrawal.Info
		}
	}

	_, err := p.sm.ToFailed(rec, errMsg)
	if err != nil {
		p.logger.Error("failed to transition to failed",
			"tran_id", rec.TranID,
			"error", err,
		)
		return
	}

	if err := p.store.Update(rec); err != nil {
		p.logger.Error("failed to update record",
			"tran_id", rec.TranID,
			"error", err,
		)
	}
}
