// Package audit provides comprehensive audit logging for forwarding actions.
// All audit events are logged at WARN level with structured fields for
// compliance and debugging purposes.
package audit

import (
	"log/slog"
	"time"
)

// Event types for audit logging.
const (
	// EventWithdrawalInitiated is logged when a withdrawal is successfully initiated.
	EventWithdrawalInitiated = "WITHDRAWAL_INITIATED"

	// EventWithdrawalCompleted is logged when a withdrawal is confirmed on-chain.
	EventWithdrawalCompleted = "WITHDRAWAL_COMPLETED"
)

// Logger provides audit logging functionality.
type Logger struct {
	logger *slog.Logger
}

// Option configures the audit Logger.
type Option func(*Logger)

// WithLogger sets a custom slog.Logger for audit output.
func WithLogger(l *slog.Logger) Option {
	return func(al *Logger) {
		al.logger = l
	}
}

// New creates a new audit Logger.
func New(opts ...Option) *Logger {
	al := &Logger{
		logger: slog.Default(),
	}
	for _, opt := range opts {
		opt(al)
	}
	return al
}

// WithdrawalInitiatedEvent contains the details for a WITHDRAWAL_INITIATED audit event.
type WithdrawalInitiatedEvent struct {
	TranID      string    // Original transfer ID
	Asset       string    // Asset being withdrawn (e.g., BTC, ETH, USDT)
	Amount      string    // Withdrawal amount
	Destination string    // Destination address
	Network     string    // Withdrawal network (e.g., ETH, BSC, TRX)
	WithdrawID  string    // Binance withdrawal ID
	Timestamp   time.Time // When the withdrawal was initiated
}

// WithdrawalCompletedEvent contains the details for a WITHDRAWAL_COMPLETED audit event.
type WithdrawalCompletedEvent struct {
	TranID    string    // Original transfer ID
	WithdrawID string   // Binance withdrawal ID
	TxID      string    // On-chain transaction ID
	Fee       string    // Transaction fee charged
	Timestamp time.Time // When the withdrawal was confirmed
}

// LogWithdrawalInitiated logs a WITHDRAWAL_INITIATED audit event at WARN level.
// This should be called when a withdrawal is successfully submitted to Binance.
func (l *Logger) LogWithdrawalInitiated(event WithdrawalInitiatedEvent) {
	timestamp := event.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}

	l.logger.Warn(EventWithdrawalInitiated,
		"tran_id", event.TranID,
		"asset", event.Asset,
		"amount", event.Amount,
		"dest", event.Destination,
		"network", event.Network,
		"withdraw_id", event.WithdrawID,
		"timestamp", timestamp.Format(time.RFC3339),
	)
}

// LogWithdrawalCompleted logs a WITHDRAWAL_COMPLETED audit event at WARN level.
// This should be called when a withdrawal is confirmed on-chain.
func (l *Logger) LogWithdrawalCompleted(event WithdrawalCompletedEvent) {
	timestamp := event.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}

	l.logger.Warn(EventWithdrawalCompleted,
		"tran_id", event.TranID,
		"withdraw_id", event.WithdrawID,
		"txid", event.TxID,
		"fee", event.Fee,
		"timestamp", timestamp.Format(time.RFC3339),
	)
}
