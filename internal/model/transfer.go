// Package model defines the core domain types for the withdrawal service.
package model

import "time"

// TransferStatus represents the lifecycle state of a transfer.
type TransferStatus string

const (
	StatusPending   TransferStatus = "PENDING"
	StatusForwarded TransferStatus = "FORWARDED"
	StatusCompleted TransferStatus = "COMPLETED"
	StatusFailed    TransferStatus = "FAILED"
)

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
