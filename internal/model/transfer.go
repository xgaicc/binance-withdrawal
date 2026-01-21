// Package model defines the core domain types for the withdrawal service.
package model

import (
	"slices"
	"time"
)

// TransferStatus represents the lifecycle state of a transfer.
//
// State machine lifecycle:
//
//	UNKNOWN → PENDING → FORWARDED → COMPLETED
//	              ↓          ↓
//	           FAILED ←──────┘
//
// UNKNOWN: Transfer not yet recorded in store (implicit state)
// PENDING: Recorded, withdrawal not yet initiated
// FORWARDED: Withdrawal API succeeded, have Binance refId
// COMPLETED: External withdrawal confirmed on-chain
// FAILED: Permanent failure (e.g., invalid address, unconfigured destination)
type TransferStatus string

const (
	// StatusUnknown represents a transfer not yet in the store.
	// This is an implicit state - transfers don't have this status stored.
	StatusUnknown TransferStatus = "UNKNOWN"

	// StatusPending indicates the transfer has been recorded but withdrawal not yet initiated.
	StatusPending TransferStatus = "PENDING"

	// StatusForwarded indicates the withdrawal API call succeeded and we have a Binance refId.
	StatusForwarded TransferStatus = "FORWARDED"

	// StatusCompleted indicates the withdrawal has been confirmed on-chain.
	StatusCompleted TransferStatus = "COMPLETED"

	// StatusFailed indicates a permanent failure (e.g., invalid address, unconfigured destination).
	StatusFailed TransferStatus = "FAILED"
)

// String returns the string representation of the status.
func (s TransferStatus) String() string {
	return string(s)
}

// IsTerminal returns true if the status is a final state (COMPLETED or FAILED).
func (s TransferStatus) IsTerminal() bool {
	return s == StatusCompleted || s == StatusFailed
}

// IsValid returns true if the status is a recognized value.
func (s TransferStatus) IsValid() bool {
	switch s {
	case StatusUnknown, StatusPending, StatusForwarded, StatusCompleted, StatusFailed:
		return true
	default:
		return false
	}
}

// ValidTransitions defines the allowed state transitions.
// Key is the current state, value is the list of valid next states.
var ValidTransitions = map[TransferStatus][]TransferStatus{
	StatusUnknown:   {StatusPending},
	StatusPending:   {StatusForwarded, StatusFailed},
	StatusForwarded: {StatusCompleted, StatusFailed},
	StatusCompleted: {}, // Terminal state
	StatusFailed:    {}, // Terminal state
}

// CanTransitionTo returns true if transitioning from this status to the target is allowed.
func (s TransferStatus) CanTransitionTo(target TransferStatus) bool {
	allowed, ok := ValidTransitions[s]
	if !ok {
		return false
	}
	return slices.Contains(allowed, target)
}

// TransferRecord represents a sub-account transfer that needs to be forwarded
// to an external wallet.
type TransferRecord struct {
	TranID     string    `json:"tran_id"`     // Binance internal transfer ID (primary key)
	SubAccount string    `json:"sub_account"` // Source sub-account email
	Asset      string    `json:"asset"`       // e.g., "SOL", "ETH"
	Amount     string    `json:"amount"`      // Decimal string for precision
	Status     string    `json:"status"`      // PENDING, FORWARDED, COMPLETED, FAILED

	// Destination info (captured at forward time)
	DestAddress string `json:"dest_address,omitempty"`
	DestNetwork string `json:"dest_network,omitempty"`

	// Binance withdrawal tracking
	WithdrawID   string `json:"withdraw_id,omitempty"`   // Binance withdrawal ID
	WithdrawTxID string `json:"withdraw_txid,omitempty"` // Blockchain transaction hash
	WithdrawFee  string `json:"withdraw_fee,omitempty"`  // Fee deducted by Binance

	// Timestamps
	TransferTime time.Time `json:"transfer_time"`           // When Binance recorded the internal transfer
	DetectedAt   time.Time `json:"detected_at"`             // When we detected it
	ForwardedAt  time.Time `json:"forwarded_at,omitempty"`  // When withdrawal was initiated
	CompletedAt  time.Time `json:"completed_at,omitempty"`  // When withdrawal was confirmed

	// Error tracking
	Error      string `json:"error,omitempty"` // Error message if FAILED
	RetryCount int    `json:"retry_count"`     // Number of retry attempts
}

// GetStatus returns the transfer's status as a TransferStatus type.
func (r *TransferRecord) GetStatus() TransferStatus {
	return TransferStatus(r.Status)
}

// IsTerminal returns true if the transfer is in a terminal state (COMPLETED or FAILED).
func (r *TransferRecord) IsTerminal() bool {
	return r.GetStatus().IsTerminal()
}

// IsPending returns true if the transfer is in PENDING state.
func (r *TransferRecord) IsPending() bool {
	return r.GetStatus() == StatusPending
}

// IsForwarded returns true if the transfer is in FORWARDED state.
func (r *TransferRecord) IsForwarded() bool {
	return r.GetStatus() == StatusForwarded
}

// IsCompleted returns true if the transfer is in COMPLETED state.
func (r *TransferRecord) IsCompleted() bool {
	return r.GetStatus() == StatusCompleted
}

// IsFailed returns true if the transfer is in FAILED state.
func (r *TransferRecord) IsFailed() bool {
	return r.GetStatus() == StatusFailed
}

// CanTransitionTo returns true if transitioning to the target status is allowed.
func (r *TransferRecord) CanTransitionTo(target TransferStatus) bool {
	return r.GetStatus().CanTransitionTo(target)
}
