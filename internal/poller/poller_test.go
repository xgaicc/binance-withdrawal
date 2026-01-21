package poller

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/binance-withdrawal/internal/audit"
	"github.com/binance-withdrawal/internal/binance"
	"github.com/binance-withdrawal/internal/model"
	"github.com/binance-withdrawal/internal/store"
)

func TestPollerCompletesWithdrawal(t *testing.T) {
	// Create a mock Binance server that returns a completed withdrawal
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/sapi/v1/capital/withdraw/history") {
			// Return a completed withdrawal
			json.NewEncoder(w).Encode([]map[string]interface{}{
				{
					"id":             "withdraw-001",
					"amount":         "100.5",
					"transactionFee": "0.1",
					"coin":           "USDT",
					"status":         binance.WithdrawalStatusCompleted,
					"address":        "0x1234",
					"txId":           "0xabc123",
					"applyTime":      "2024-01-15 10:30:00",
					"network":        "ETH",
					"withdrawOrderId": "tran-123",
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	// Create temporary directory for test store
	tmpDir, err := os.MkdirTemp("", "poller-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create store
	st, err := store.Open(tmpDir + "/test.db")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer st.Close()

	// Create a FORWARDED record
	rec := &model.TransferRecord{
		TranID:      "tran-123",
		SubAccount:  "test@example.com",
		Asset:       "USDT",
		Amount:      "100.5",
		Status:      string(model.StatusForwarded),
		WithdrawID:  "withdraw-001",
		DestAddress: "0x1234",
		DestNetwork: "ETH",
		ForwardedAt: time.Now(),
	}
	if err := st.Create(rec); err != nil {
		t.Fatalf("failed to create record: %v", err)
	}

	// Create audit logger that captures output
	var auditBuf bytes.Buffer
	auditHandler := slog.NewJSONHandler(&auditBuf, &slog.HandlerOptions{Level: slog.LevelWarn})
	auditLogger := audit.New(audit.WithLogger(slog.New(auditHandler)))

	// Create Binance client pointing to mock server
	client := binance.NewClient("test-api-key", "test-secret", binance.WithBaseURL(server.URL))

	// Create poller
	p := New(client, st,
		WithAuditLogger(auditLogger),
		WithInterval(100*time.Millisecond),
	)

	// Start poller
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("failed to start poller: %v", err)
	}

	// Wait for poll to complete
	time.Sleep(500 * time.Millisecond)

	p.Stop()

	// Verify the record was updated to COMPLETED
	updated, err := st.Get("tran-123")
	if err != nil {
		t.Fatalf("failed to get updated record: %v", err)
	}

	if updated.GetStatus() != model.StatusCompleted {
		t.Errorf("expected status COMPLETED, got %s", updated.Status)
	}

	if updated.WithdrawTxID != "0xabc123" {
		t.Errorf("expected txid 0xabc123, got %s", updated.WithdrawTxID)
	}

	if updated.WithdrawFee != "0.1" {
		t.Errorf("expected fee 0.1, got %s", updated.WithdrawFee)
	}

	// Verify WITHDRAWAL_COMPLETED audit log was emitted
	auditOutput := auditBuf.String()
	if !strings.Contains(auditOutput, audit.EventWithdrawalCompleted) {
		t.Errorf("expected WITHDRAWAL_COMPLETED audit log, got: %s", auditOutput)
	}
	if !strings.Contains(auditOutput, "tran-123") {
		t.Errorf("expected tran_id in audit log, got: %s", auditOutput)
	}
	if !strings.Contains(auditOutput, "0xabc123") {
		t.Errorf("expected txid in audit log, got: %s", auditOutput)
	}
}

func TestPollerHandlesFailedWithdrawal(t *testing.T) {
	// Create a mock Binance server that returns a failed withdrawal
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/sapi/v1/capital/withdraw/history") {
			json.NewEncoder(w).Encode([]map[string]interface{}{
				{
					"id":             "withdraw-002",
					"amount":         "50.0",
					"transactionFee": "0",
					"coin":           "BTC",
					"status":         binance.WithdrawalStatusFailure,
					"address":        "bc1qtest",
					"txId":           "",
					"applyTime":      "2024-01-15 11:00:00",
					"network":        "BTC",
					"withdrawOrderId": "tran-456",
					"info":           "insufficient balance",
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	tmpDir, err := os.MkdirTemp("", "poller-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	st, err := store.Open(tmpDir + "/test.db")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer st.Close()

	rec := &model.TransferRecord{
		TranID:      "tran-456",
		SubAccount:  "test@example.com",
		Asset:       "BTC",
		Amount:      "50.0",
		Status:      string(model.StatusForwarded),
		WithdrawID:  "withdraw-002",
		DestAddress: "bc1qtest",
		DestNetwork: "BTC",
		ForwardedAt: time.Now(),
	}
	if err := st.Create(rec); err != nil {
		t.Fatalf("failed to create record: %v", err)
	}

	client := binance.NewClient("test-api-key", "test-secret", binance.WithBaseURL(server.URL))

	p := New(client, st, WithInterval(100*time.Millisecond))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("failed to start poller: %v", err)
	}

	time.Sleep(500 * time.Millisecond)
	p.Stop()

	updated, err := st.Get("tran-456")
	if err != nil {
		t.Fatalf("failed to get updated record: %v", err)
	}

	if updated.GetStatus() != model.StatusFailed {
		t.Errorf("expected status FAILED, got %s", updated.Status)
	}

	if !strings.Contains(updated.Error, "insufficient balance") {
		t.Errorf("expected error message to contain 'insufficient balance', got: %s", updated.Error)
	}
}

func TestPollerSkipsNonForwardedRecords(t *testing.T) {
	// The poller should only check FORWARDED records, not PENDING or COMPLETED
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Should not be called for non-FORWARDED records
		t.Error("unexpected API call for non-FORWARDED record")
		http.NotFound(w, r)
	}))
	defer server.Close()

	tmpDir, err := os.MkdirTemp("", "poller-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	st, err := store.Open(tmpDir + "/test.db")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer st.Close()

	// Create records in various states
	records := []*model.TransferRecord{
		{TranID: "pending-1", Status: string(model.StatusPending), Asset: "USDT", Amount: "100"},
		{TranID: "completed-1", Status: string(model.StatusCompleted), Asset: "BTC", Amount: "1"},
		{TranID: "failed-1", Status: string(model.StatusFailed), Asset: "ETH", Amount: "5"},
	}
	for _, rec := range records {
		if err := st.Create(rec); err != nil {
			t.Fatalf("failed to create record: %v", err)
		}
	}

	client := binance.NewClient("test-api-key", "test-secret", binance.WithBaseURL(server.URL))

	p := New(client, st, WithInterval(100*time.Millisecond))

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("failed to start poller: %v", err)
	}

	time.Sleep(300 * time.Millisecond)
	p.Stop()

	// No API calls should have been made since no FORWARDED records exist
}
