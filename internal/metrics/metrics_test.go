package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordTransferDetected(t *testing.T) {
	// Reset the counter for testing
	TransfersDetectedTotal.Reset()

	RecordTransferDetected("sub1@example.com", "BTC")
	RecordTransferDetected("sub1@example.com", "BTC")
	RecordTransferDetected("sub2@example.com", "ETH")

	// Verify counts
	count := testutil.ToFloat64(TransfersDetectedTotal.WithLabelValues("sub1@example.com", "BTC"))
	if count != 2 {
		t.Errorf("expected 2 for sub1 BTC, got %f", count)
	}

	count = testutil.ToFloat64(TransfersDetectedTotal.WithLabelValues("sub2@example.com", "ETH"))
	if count != 1 {
		t.Errorf("expected 1 for sub2 ETH, got %f", count)
	}
}

func TestRecordForward(t *testing.T) {
	// Reset the counter for testing
	ForwardsTotal.Reset()

	RecordForward("sub1@example.com", "BTC", ForwardStatusForwarded)
	RecordForward("sub1@example.com", "BTC", ForwardStatusFailed)
	RecordForward("sub1@example.com", "ETH", ForwardStatusSkippedDuplicate)

	// Verify counts
	count := testutil.ToFloat64(ForwardsTotal.WithLabelValues("sub1@example.com", "BTC", ForwardStatusForwarded))
	if count != 1 {
		t.Errorf("expected 1 for forwarded, got %f", count)
	}

	count = testutil.ToFloat64(ForwardsTotal.WithLabelValues("sub1@example.com", "BTC", ForwardStatusFailed))
	if count != 1 {
		t.Errorf("expected 1 for failed, got %f", count)
	}
}

func TestRecordForwardLatency(t *testing.T) {
	// Reset the histogram for testing
	ForwardLatencySeconds.Reset()

	RecordForwardLatency("BTC", 0.5)
	RecordForwardLatency("BTC", 1.5)
	RecordForwardLatency("ETH", 0.1)

	// Verify histogram has observations
	count := testutil.CollectAndCount(ForwardLatencySeconds)
	if count != 2 { // 2 label combinations
		t.Errorf("expected 2 metric series, got %d", count)
	}
}

func TestSetPendingCount(t *testing.T) {
	SetPendingCount(5)
	count := testutil.ToFloat64(PendingCount)
	if count != 5 {
		t.Errorf("expected 5, got %f", count)
	}

	SetPendingCount(10)
	count = testutil.ToFloat64(PendingCount)
	if count != 10 {
		t.Errorf("expected 10, got %f", count)
	}

	SetPendingCount(0)
	count = testutil.ToFloat64(PendingCount)
	if count != 0 {
		t.Errorf("expected 0, got %f", count)
	}
}

func TestRecordError(t *testing.T) {
	// Reset the counter for testing
	ErrorsTotal.Reset()

	RecordError(ErrorTypeDestinationNotConfigured)
	RecordError(ErrorTypeWithdrawalFailed)
	RecordError(ErrorTypeWithdrawalFailed)

	count := testutil.ToFloat64(ErrorsTotal.WithLabelValues(ErrorTypeDestinationNotConfigured))
	if count != 1 {
		t.Errorf("expected 1 for destination_not_configured, got %f", count)
	}

	count = testutil.ToFloat64(ErrorsTotal.WithLabelValues(ErrorTypeWithdrawalFailed))
	if count != 2 {
		t.Errorf("expected 2 for withdrawal_failed, got %f", count)
	}
}

func TestSetWebSocketConnected(t *testing.T) {
	SetWebSocketConnected(true)
	val := testutil.ToFloat64(WebSocketConnected)
	if val != 1 {
		t.Errorf("expected 1 for connected, got %f", val)
	}

	SetWebSocketConnected(false)
	val = testutil.ToFloat64(WebSocketConnected)
	if val != 0 {
		t.Errorf("expected 0 for disconnected, got %f", val)
	}
}

func TestRecordWebSocketReconnect(t *testing.T) {
	// Get initial count
	initial := testutil.ToFloat64(WebSocketReconnectsTotal)

	RecordWebSocketReconnect()
	RecordWebSocketReconnect()

	count := testutil.ToFloat64(WebSocketReconnectsTotal)
	if count != initial+2 {
		t.Errorf("expected %f, got %f", initial+2, count)
	}
}

func TestMetricsRegistered(t *testing.T) {
	// Verify all metrics are registered with the default registry
	metricFamilies, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	expectedMetrics := []string{
		"binance_withdrawal_transfers_detected_total",
		"binance_withdrawal_forwards_total",
		"binance_withdrawal_forward_latency_seconds",
		"binance_withdrawal_pending_count",
		"binance_withdrawal_errors_total",
		"binance_withdrawal_websocket_connected",
		"binance_withdrawal_websocket_reconnects_total",
	}

	foundMetrics := make(map[string]bool)
	for _, mf := range metricFamilies {
		foundMetrics[*mf.Name] = true
	}

	for _, expected := range expectedMetrics {
		if !foundMetrics[expected] {
			t.Errorf("metric %s not found in registry", expected)
		}
	}
}

func TestMetricLabels(t *testing.T) {
	// Reset metrics
	TransfersDetectedTotal.Reset()
	ForwardsTotal.Reset()

	// Record some metrics to populate labels
	RecordTransferDetected("test@example.com", "USDT")
	RecordForward("test@example.com", "USDT", ForwardStatusForwarded)

	// Collect and verify labels exist
	expected := `
		# HELP binance_withdrawal_transfers_detected_total Total number of transfers detected from sub-accounts
		# TYPE binance_withdrawal_transfers_detected_total counter
		binance_withdrawal_transfers_detected_total{asset="USDT",sub_account="test@example.com"} 1
	`

	if err := testutil.CollectAndCompare(TransfersDetectedTotal, strings.NewReader(expected)); err != nil {
		t.Errorf("unexpected collecting result:\n%s", err)
	}
}

func TestErrorTypeConstants(t *testing.T) {
	// Verify error type constants are defined
	errorTypes := []string{
		ErrorTypeDestinationNotConfigured,
		ErrorTypeWithdrawalFailed,
		ErrorTypeStoreError,
		ErrorTypeAPIError,
		ErrorTypeWebSocketError,
	}

	for _, et := range errorTypes {
		if et == "" {
			t.Error("error type constant is empty")
		}
	}
}

func TestForwardStatusConstants(t *testing.T) {
	// Verify forward status constants are defined
	statuses := []string{
		ForwardStatusForwarded,
		ForwardStatusSkippedDuplicate,
		ForwardStatusSkippedAlreadyWithdrawn,
		ForwardStatusFailed,
	}

	for _, s := range statuses {
		if s == "" {
			t.Error("forward status constant is empty")
		}
	}
}
