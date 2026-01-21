// Package metrics provides Prometheus metrics for the Binance withdrawal service.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const namespace = "binance_withdrawal"

var (
	// TransfersDetectedTotal counts the total number of transfers detected.
	// Labels: sub_account, asset
	TransfersDetectedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "transfers_detected_total",
			Help:      "Total number of transfers detected from sub-accounts",
		},
		[]string{"sub_account", "asset"},
	)

	// ForwardsTotal counts the total number of forward operations by outcome.
	// Labels: sub_account, asset, status (forwarded, skipped_duplicate, skipped_already_withdrawn, failed)
	ForwardsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "forwards_total",
			Help:      "Total number of forward operations by status",
		},
		[]string{"sub_account", "asset", "status"},
	)

	// ForwardLatencySeconds measures the time taken to complete a forward operation.
	// Labels: asset
	ForwardLatencySeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "forward_latency_seconds",
			Help:      "Time taken to complete forward operations in seconds",
			Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
		},
		[]string{"asset"},
	)

	// PendingCount is a gauge showing the current number of pending transfers.
	PendingCount = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "pending_count",
			Help:      "Current number of transfers in PENDING status",
		},
	)

	// ErrorsTotal counts errors by type.
	// Labels: error_type (destination_not_configured, withdrawal_failed, store_error, etc.)
	ErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "errors_total",
			Help:      "Total number of errors by type",
		},
		[]string{"error_type"},
	)

	// WebSocketConnected indicates whether the WebSocket is connected (1) or not (0).
	WebSocketConnected = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "websocket_connected",
			Help:      "Whether the WebSocket connection to Binance is active (1=connected, 0=disconnected)",
		},
	)

	// WebSocketReconnectsTotal counts the number of WebSocket reconnection attempts.
	WebSocketReconnectsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "websocket_reconnects_total",
			Help:      "Total number of WebSocket reconnection attempts",
		},
	)
)

// Error types for ErrorsTotal metric.
const (
	ErrorTypeDestinationNotConfigured = "destination_not_configured"
	ErrorTypeWithdrawalFailed         = "withdrawal_failed"
	ErrorTypeStoreError               = "store_error"
	ErrorTypeAPIError                 = "api_error"
	ErrorTypeWebSocketError           = "websocket_error"
)

// Forward status values for ForwardsTotal metric.
const (
	ForwardStatusForwarded             = "forwarded"
	ForwardStatusSkippedDuplicate      = "skipped_duplicate"
	ForwardStatusSkippedAlreadyWithdrawn = "skipped_already_withdrawn"
	ForwardStatusFailed                = "failed"
)

// RecordTransferDetected increments the transfers detected counter.
func RecordTransferDetected(subAccount, asset string) {
	TransfersDetectedTotal.WithLabelValues(subAccount, asset).Inc()
}

// RecordForward increments the forwards counter with the given status.
func RecordForward(subAccount, asset, status string) {
	ForwardsTotal.WithLabelValues(subAccount, asset, status).Inc()
}

// RecordForwardLatency records the latency of a forward operation.
func RecordForwardLatency(asset string, seconds float64) {
	ForwardLatencySeconds.WithLabelValues(asset).Observe(seconds)
}

// SetPendingCount sets the current pending transfer count.
func SetPendingCount(count int) {
	PendingCount.Set(float64(count))
}

// RecordError increments the error counter for the given error type.
func RecordError(errorType string) {
	ErrorsTotal.WithLabelValues(errorType).Inc()
}

// SetWebSocketConnected sets the WebSocket connection status.
func SetWebSocketConnected(connected bool) {
	if connected {
		WebSocketConnected.Set(1)
	} else {
		WebSocketConnected.Set(0)
	}
}

// RecordWebSocketReconnect increments the WebSocket reconnection counter.
func RecordWebSocketReconnect() {
	WebSocketReconnectsTotal.Inc()
}
