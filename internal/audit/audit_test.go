package audit

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLogWithdrawalInitiated(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	logger := slog.New(handler)

	audit := New(WithLogger(logger))

	testTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

	audit.LogWithdrawalInitiated(WithdrawalInitiatedEvent{
		TranID:      "123456",
		Asset:       "USDT",
		Amount:      "1000.50",
		Destination: "0x1234567890abcdef",
		Network:     "ETH",
		WithdrawID:  "withdraw-001",
		Timestamp:   testTime,
	})

	output := buf.String()
	t.Logf("JSON output: %s", output)

	// Verify it's valid JSON
	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(output), &logEntry); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	// Verify the message is WITHDRAWAL_INITIATED
	if msg, ok := logEntry["msg"].(string); !ok || msg != EventWithdrawalInitiated {
		t.Errorf("expected msg=%q, got %v", EventWithdrawalInitiated, logEntry["msg"])
	}

	// Verify required fields are present
	requiredFields := []string{"tran_id", "asset", "amount", "dest", "timestamp"}
	for _, field := range requiredFields {
		if _, ok := logEntry[field]; !ok {
			t.Errorf("missing required field: %s", field)
		}
	}

	// Verify field values
	if logEntry["tran_id"] != "123456" {
		t.Errorf("tran_id = %v, want 123456", logEntry["tran_id"])
	}
	if logEntry["asset"] != "USDT" {
		t.Errorf("asset = %v, want USDT", logEntry["asset"])
	}
	if logEntry["amount"] != "1000.50" {
		t.Errorf("amount = %v, want 1000.50", logEntry["amount"])
	}
	if logEntry["dest"] != "0x1234567890abcdef" {
		t.Errorf("dest = %v, want 0x1234567890abcdef", logEntry["dest"])
	}

	// Verify timestamp is in RFC3339 format
	timestamp, ok := logEntry["timestamp"].(string)
	if !ok {
		t.Errorf("timestamp is not a string")
	} else {
		if _, err := time.Parse(time.RFC3339, timestamp); err != nil {
			t.Errorf("timestamp not in RFC3339 format: %v", err)
		}
	}

	// Verify log level is WARN
	if level, ok := logEntry["level"].(string); !ok || !strings.Contains(level, "WARN") {
		t.Errorf("expected WARN level, got %v", logEntry["level"])
	}
}

func TestLogWithdrawalCompleted(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	logger := slog.New(handler)

	audit := New(WithLogger(logger))

	testTime := time.Date(2024, 1, 15, 11, 45, 0, 0, time.UTC)

	audit.LogWithdrawalCompleted(WithdrawalCompletedEvent{
		TranID:     "123456",
		WithdrawID: "withdraw-001",
		TxID:       "0xabc123def456",
		Fee:        "0.001",
		Timestamp:  testTime,
	})

	output := buf.String()
	t.Logf("JSON output: %s", output)

	// Verify it's valid JSON
	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(output), &logEntry); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	// Verify the message is WITHDRAWAL_COMPLETED
	if msg, ok := logEntry["msg"].(string); !ok || msg != EventWithdrawalCompleted {
		t.Errorf("expected msg=%q, got %v", EventWithdrawalCompleted, logEntry["msg"])
	}

	// Verify required fields are present
	requiredFields := []string{"tran_id", "txid", "fee", "timestamp"}
	for _, field := range requiredFields {
		if _, ok := logEntry[field]; !ok {
			t.Errorf("missing required field: %s", field)
		}
	}

	// Verify field values
	if logEntry["tran_id"] != "123456" {
		t.Errorf("tran_id = %v, want 123456", logEntry["tran_id"])
	}
	if logEntry["txid"] != "0xabc123def456" {
		t.Errorf("txid = %v, want 0xabc123def456", logEntry["txid"])
	}
	if logEntry["fee"] != "0.001" {
		t.Errorf("fee = %v, want 0.001", logEntry["fee"])
	}

	// Verify log level is WARN
	if level, ok := logEntry["level"].(string); !ok || !strings.Contains(level, "WARN") {
		t.Errorf("expected WARN level, got %v", logEntry["level"])
	}
}

func TestDefaultTimestamp(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	logger := slog.New(handler)

	audit := New(WithLogger(logger))

	// Truncate to second precision since RFC3339 format loses subsecond precision
	before := time.Now().UTC().Truncate(time.Second)

	// Call without setting Timestamp - should use current time
	audit.LogWithdrawalInitiated(WithdrawalInitiatedEvent{
		TranID:      "123456",
		Asset:       "BTC",
		Amount:      "1.0",
		Destination: "bc1qtest",
		Network:     "BTC",
		WithdrawID:  "w-001",
		// Timestamp not set
	})

	after := time.Now().UTC().Add(time.Second).Truncate(time.Second)

	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	timestamp, ok := logEntry["timestamp"].(string)
	if !ok {
		t.Fatalf("timestamp is not a string")
	}

	ts, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		t.Fatalf("failed to parse timestamp: %v", err)
	}

	// The timestamp should be between before and after (with second precision)
	if ts.Before(before) || ts.After(after) {
		t.Errorf("default timestamp %v not between %v and %v", ts, before, after)
	}
}
