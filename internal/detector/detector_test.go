package detector

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/binance-withdrawal/internal/binance"
	"github.com/binance-withdrawal/internal/model"
)

// recordingHandler collects transfers for testing
type recordingHandler struct {
	mu        sync.Mutex
	transfers []*model.TransferRecord
}

func (h *recordingHandler) handle(t *model.TransferRecord) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.transfers = append(h.transfers, t)
}

func (h *recordingHandler) getTransfers() []*model.TransferRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	result := make([]*model.TransferRecord, len(h.transfers))
	copy(result, h.transfers)
	return result
}

func TestNewDetector(t *testing.T) {
	client := binance.NewClient("api-key", "secret-key")
	handler := &recordingHandler{}

	d := New(client, handler.handle)

	if d.client != client {
		t.Error("client not set")
	}
	if d.handler == nil {
		t.Error("handler is nil")
	}
	if d.lookbackDuration != 5*time.Minute {
		t.Errorf("lookbackDuration = %v, want 5m", d.lookbackDuration)
	}
}

func TestDetectorWithOptions(t *testing.T) {
	client := binance.NewClient("api-key", "secret-key")
	handler := &recordingHandler{}
	customLogger := slog.Default()
	customLookback := 10 * time.Minute

	d := New(client, handler.handle,
		WithLogger(customLogger),
		WithLookbackDuration(customLookback),
	)

	if d.logger != customLogger {
		t.Error("logger not set correctly")
	}
	if d.lookbackDuration != customLookback {
		t.Errorf("lookbackDuration = %v, want %v", d.lookbackDuration, customLookback)
	}
}

func TestProcessTransfer(t *testing.T) {
	client := binance.NewClient("api-key", "secret-key")
	handler := &recordingHandler{}
	d := New(client, handler.handle)

	transfer := &binance.SubAccountTransfer{
		TranID:       123456,
		FromEmail:    "sub@example.com",
		ToEmail:      "master@example.com",
		Asset:        "SOL",
		Qty:          "100.5",
		Status:       "SUCCESS",
		CreateTimeAt: time.Now(),
	}

	d.processTransfer(transfer)

	transfers := handler.getTransfers()
	if len(transfers) != 1 {
		t.Fatalf("len(transfers) = %d, want 1", len(transfers))
	}

	rec := transfers[0]
	if rec.TranID != "123456" {
		t.Errorf("TranID = %q, want 123456", rec.TranID)
	}
	if rec.SubAccount != "sub@example.com" {
		t.Errorf("SubAccount = %q, want sub@example.com", rec.SubAccount)
	}
	if rec.Asset != "SOL" {
		t.Errorf("Asset = %q, want SOL", rec.Asset)
	}
	if rec.Amount != "100.5" {
		t.Errorf("Amount = %q, want 100.5", rec.Amount)
	}
	if rec.Status != string(model.StatusPending) {
		t.Errorf("Status = %q, want PENDING", rec.Status)
	}
	if rec.DetectedAt.IsZero() {
		t.Error("DetectedAt not set")
	}
}

func TestLastProcessedTimeTracking(t *testing.T) {
	client := binance.NewClient("api-key", "secret-key")
	handler := &recordingHandler{}
	d := New(client, handler.handle)

	now := time.Now()

	// Process first transfer
	transfer1 := &binance.SubAccountTransfer{
		TranID:       1,
		FromEmail:    "sub@example.com",
		Asset:        "SOL",
		Qty:          "10",
		CreateTimeAt: now,
	}
	d.processTransfer(transfer1)

	if !d.GetLastProcessedTime().Equal(now) {
		t.Errorf("lastProcessedTime = %v, want %v", d.GetLastProcessedTime(), now)
	}

	// Process earlier transfer (should not update lastProcessedTime)
	earlier := now.Add(-1 * time.Hour)
	transfer2 := &binance.SubAccountTransfer{
		TranID:       2,
		FromEmail:    "sub@example.com",
		Asset:        "ETH",
		Qty:          "5",
		CreateTimeAt: earlier,
	}
	d.processTransfer(transfer2)

	if !d.GetLastProcessedTime().Equal(now) {
		t.Errorf("lastProcessedTime = %v, want %v (should not have changed)", d.GetLastProcessedTime(), now)
	}

	// Process later transfer (should update lastProcessedTime)
	later := now.Add(1 * time.Hour)
	transfer3 := &binance.SubAccountTransfer{
		TranID:       3,
		FromEmail:    "sub@example.com",
		Asset:        "BTC",
		Qty:          "1",
		CreateTimeAt: later,
	}
	d.processTransfer(transfer3)

	if !d.GetLastProcessedTime().Equal(later) {
		t.Errorf("lastProcessedTime = %v, want %v", d.GetLastProcessedTime(), later)
	}
}

func TestHandleBalanceUpdate_PositiveDelta(t *testing.T) {
	now := time.Now()
	transfers := []binance.SubAccountTransfer{
		{
			TranID:       12345,
			FromEmail:    "sub@example.com",
			Asset:        "SOL",
			Qty:          "50.0",
			Status:       "SUCCESS",
			CreateTime:   now.UnixMilli(),
			CreateTimeAt: now,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(transfers)
	}))
	defer server.Close()

	client := binance.NewClient("api-key", "secret-key", binance.WithBaseURL(server.URL))
	handler := &recordingHandler{}
	d := New(client, handler.handle)

	event := &binance.BalanceUpdateEvent{
		EventType:    "balanceUpdate",
		Asset:        "SOL",
		BalanceDelta: "50.0",
		ClearTimeAt:  now,
	}

	d.handleBalanceUpdate(event)

	got := handler.getTransfers()
	if len(got) != 1 {
		t.Fatalf("len(transfers) = %d, want 1", len(got))
	}
	if got[0].TranID != "12345" {
		t.Errorf("TranID = %q, want 12345", got[0].TranID)
	}
}

func TestHandleBalanceUpdate_NegativeDelta(t *testing.T) {
	client := binance.NewClient("api-key", "secret-key")
	handler := &recordingHandler{}
	d := New(client, handler.handle)

	// Negative delta should be ignored (outgoing transfer)
	event := &binance.BalanceUpdateEvent{
		EventType:    "balanceUpdate",
		Asset:        "SOL",
		BalanceDelta: "-50.0",
		ClearTimeAt:  time.Now(),
	}

	d.handleBalanceUpdate(event)

	if len(handler.getTransfers()) != 0 {
		t.Error("negative delta should not trigger transfer processing")
	}
}

func TestHandleBalanceUpdate_ZeroDelta(t *testing.T) {
	client := binance.NewClient("api-key", "secret-key")
	handler := &recordingHandler{}
	d := New(client, handler.handle)

	event := &binance.BalanceUpdateEvent{
		EventType:    "balanceUpdate",
		Asset:        "SOL",
		BalanceDelta: "0",
		ClearTimeAt:  time.Now(),
	}

	d.handleBalanceUpdate(event)

	if len(handler.getTransfers()) != 0 {
		t.Error("zero delta should not trigger transfer processing")
	}
}

func TestAbsFloat(t *testing.T) {
	tests := []struct {
		input float64
		want  float64
	}{
		{5.0, 5.0},
		{-5.0, 5.0},
		{0.0, 0.0},
		{-0.0, 0.0},
		{100.5, 100.5},
		{-100.5, 100.5},
	}

	for _, tt := range tests {
		got := absFloat(tt.input)
		if got != tt.want {
			t.Errorf("absFloat(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// TestEventHandlerInterface ensures Detector implements binance.EventHandler
func TestEventHandlerInterface(t *testing.T) {
	client := binance.NewClient("api-key", "secret-key")
	handler := &recordingHandler{}
	d := New(client, handler.handle)

	// Verify Detector implements EventHandler
	var _ binance.EventHandler = d
}

func TestOnBalanceUpdate_ChannelNotFull(t *testing.T) {
	client := binance.NewClient("api-key", "secret-key")
	handler := &recordingHandler{}
	d := New(client, handler.handle)

	event := &binance.BalanceUpdateEvent{
		Asset:        "SOL",
		BalanceDelta: "100",
	}

	// Should not panic or block
	d.OnBalanceUpdate(event)

	// Event should be in the channel
	select {
	case received := <-d.balanceUpdateCh:
		if received.Asset != "SOL" {
			t.Errorf("received.Asset = %q, want SOL", received.Asset)
		}
	default:
		t.Error("event not received in channel")
	}
}

func TestPollRecentTransfers(t *testing.T) {
	now := time.Now()
	transfers := []binance.SubAccountTransfer{
		{
			TranID:     1,
			FromEmail:  "sub1@example.com",
			Asset:      "SOL",
			Qty:        "10",
			CreateTime: now.Add(-2 * time.Minute).UnixMilli(),
		},
		{
			TranID:     2,
			FromEmail:  "sub2@example.com",
			Asset:      "ETH",
			Qty:        "5",
			CreateTime: now.Add(-1 * time.Minute).UnixMilli(),
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(transfers)
	}))
	defer server.Close()

	client := binance.NewClient("api-key", "secret-key", binance.WithBaseURL(server.URL))
	handler := &recordingHandler{}
	d := New(client, handler.handle)

	err := d.pollRecentTransfers()
	if err != nil {
		t.Fatalf("pollRecentTransfers() error = %v", err)
	}

	got := handler.getTransfers()
	if len(got) != 2 {
		t.Fatalf("len(transfers) = %d, want 2", len(got))
	}
}

func TestDetectorStop(t *testing.T) {
	// Create a mock server that returns empty responses
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"listenKey": "test-key"})
	}))
	defer server.Close()

	client := binance.NewClient("api-key", "secret-key", binance.WithBaseURL(server.URL))
	handler := &recordingHandler{}
	d := New(client, handler.handle)

	// Create a context we control
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start will fail because we can't connect to the mock WebSocket
	// but we can still test the Stop function
	d.ctx, d.cancel = context.WithCancel(ctx)

	// Stop should not panic
	err := d.Stop()
	if err != nil {
		// Some error is expected since we never connected
		t.Logf("Stop() error (expected): %v", err)
	}
}

func TestOnReconnectedPollsTransfers(t *testing.T) {
	now := time.Now()
	transfers := []binance.SubAccountTransfer{
		{
			TranID:     99999,
			FromEmail:  "sub@example.com",
			Asset:      "ETH",
			Qty:        "2.5",
			CreateTime: now.Add(-1 * time.Minute).UnixMilli(),
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(transfers)
	}))
	defer server.Close()

	client := binance.NewClient("api-key", "secret-key", binance.WithBaseURL(server.URL))
	handler := &recordingHandler{}
	d := New(client, handler.handle)

	// Call OnReconnected to simulate reconnection
	d.OnReconnected()

	got := handler.getTransfers()
	if len(got) != 1 {
		t.Fatalf("len(transfers) = %d, want 1", len(got))
	}
	if got[0].TranID != "99999" {
		t.Errorf("TranID = %q, want 99999", got[0].TranID)
	}
	if got[0].Asset != "ETH" {
		t.Errorf("Asset = %q, want ETH", got[0].Asset)
	}
}

func TestOnReconnectedUsesLastProcessedTime(t *testing.T) {
	// Track the request parameters
	var requestedStartTime, requestedEndTime int64
	now := time.Now()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Parse query parameters to verify time range
		requestedStartTime, _ = strconv.ParseInt(r.URL.Query().Get("startTime"), 10, 64)
		requestedEndTime, _ = strconv.ParseInt(r.URL.Query().Get("endTime"), 10, 64)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]binance.SubAccountTransfer{})
	}))
	defer server.Close()

	client := binance.NewClient("api-key", "secret-key", binance.WithBaseURL(server.URL))
	handler := &recordingHandler{}
	d := New(client, handler.handle, WithLookbackDuration(10*time.Minute))

	// Set a lastProcessedTime to simulate previous activity
	lastProcessed := now.Add(-2 * time.Minute)
	d.lastProcessedMu.Lock()
	d.lastProcessedTime = lastProcessed
	d.lastProcessedMu.Unlock()

	// Call OnReconnected
	d.OnReconnected()

	// Verify the start time used was the lastProcessedTime, not the lookback
	startTime := time.UnixMilli(requestedStartTime)
	// Allow 1 second tolerance for timing
	if startTime.Before(lastProcessed.Add(-1*time.Second)) || startTime.After(lastProcessed.Add(1*time.Second)) {
		t.Errorf("startTime = %v, expected close to lastProcessedTime %v", startTime, lastProcessed)
	}

	// End time should be close to now
	endTime := time.UnixMilli(requestedEndTime)
	if endTime.Before(now.Add(-2*time.Second)) || endTime.After(now.Add(2*time.Second)) {
		t.Errorf("endTime = %v, expected close to now %v", endTime, now)
	}
}

func TestOnConnectedDoesNotPoll(t *testing.T) {
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]binance.SubAccountTransfer{})
	}))
	defer server.Close()

	client := binance.NewClient("api-key", "secret-key", binance.WithBaseURL(server.URL))
	handler := &recordingHandler{}
	d := New(client, handler.handle)

	// OnConnected should NOT trigger polling (only startup and reconnection do)
	d.OnConnected()

	if requestCount != 0 {
		t.Errorf("OnConnected() made %d API requests, want 0", requestCount)
	}
}

func TestOnDisconnectedLogsOnly(t *testing.T) {
	client := binance.NewClient("api-key", "secret-key")
	handler := &recordingHandler{}
	d := New(client, handler.handle)

	// OnDisconnected should not panic and should not process transfers
	d.OnDisconnected()

	if len(handler.getTransfers()) != 0 {
		t.Error("OnDisconnected() should not process transfers")
	}
}
