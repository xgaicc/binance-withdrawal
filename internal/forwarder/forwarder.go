// Package forwarder implements idempotent forward processing for incoming transfers.
// It provides two-layer duplicate protection to ensure the same transfer is never forwarded twice.
package forwarder

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/binance-withdrawal/internal/binance"
	"github.com/binance-withdrawal/internal/config"
	"github.com/binance-withdrawal/internal/model"
	"github.com/binance-withdrawal/internal/retry"
	"github.com/binance-withdrawal/internal/sm"
	"github.com/binance-withdrawal/internal/store"
)

// Errors returned by the forwarder.
var (
	// ErrAlreadyForwarded indicates the transfer has already been forwarded.
	ErrAlreadyForwarded = errors.New("transfer already forwarded")

	// ErrDestinationNotConfigured indicates no destination is configured for the transfer.
	ErrDestinationNotConfigured = errors.New("destination not configured")
)

// Forwarder processes incoming transfers and initiates external withdrawals.
// It implements two-layer duplicate protection:
//   - Layer 1 (BoltDB): Fast-path local duplicate check
//   - Layer 2 (Binance): Source-of-truth verification via withdrawal history API
type Forwarder struct {
	client     *binance.Client
	store      *store.Store
	destCfg    *config.DestinationConfig
	sm         *sm.StateMachine
	logger     *slog.Logger
	retryCfg   *retry.Config
}

// ForwarderOption configures the Forwarder.
type ForwarderOption func(*Forwarder)

// WithForwarderLogger sets a custom logger.
func WithForwarderLogger(l *slog.Logger) ForwarderOption {
	return func(f *Forwarder) {
		f.logger = l
	}
}

// WithRetryConfig sets a custom retry configuration.
func WithRetryConfig(cfg *retry.Config) ForwarderOption {
	return func(f *Forwarder) {
		f.retryCfg = cfg
	}
}

// New creates a new Forwarder.
func New(client *binance.Client, st *store.Store, destCfg *config.DestinationConfig, opts ...ForwarderOption) *Forwarder {
	f := &Forwarder{
		client:   client,
		store:    st,
		destCfg:  destCfg,
		sm:       sm.New(),
		logger:   slog.Default(),
		retryCfg: retry.DefaultConfig(),
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// ForwardResult contains the outcome of a forward operation.
type ForwardResult struct {
	// TranID is the original transfer ID.
	TranID string

	// Status indicates what happened during processing.
	Status ForwardStatus

	// WithdrawID is the Binance withdrawal ID (if forwarded).
	WithdrawID string

	// Error contains any error that occurred.
	Error error
}

// ForwardStatus indicates the outcome of a forward attempt.
type ForwardStatus string

const (
	// StatusForwarded indicates the withdrawal was successfully initiated.
	StatusForwarded ForwardStatus = "forwarded"

	// StatusSkippedDuplicate indicates the transfer was already processed (Layer 1).
	StatusSkippedDuplicate ForwardStatus = "skipped_duplicate"

	// StatusSkippedAlreadyWithdrawn indicates a withdrawal already exists (Layer 2).
	StatusSkippedAlreadyWithdrawn ForwardStatus = "skipped_already_withdrawn"

	// StatusFailed indicates a permanent failure occurred.
	StatusFailed ForwardStatus = "failed"
)

// Forward processes an incoming transfer with two-layer idempotency protection.
//
// Processing flow:
//  1. Layer 1 (BoltDB): Check if transfer is already in store with terminal status
//  2. Layer 2 (Binance): Query withdrawal history by withdrawOrderId (which equals tranId)
//  3. If no withdrawal exists, initiate the withdrawal with retry on transient errors
//  4. Update store with result
//
// Transient errors (network timeouts, rate limits, service unavailable) are automatically
// retried with exponential backoff up to MaxAttempts. The retry count is tracked in
// the TransferRecord.
//
// This ensures a transfer is never forwarded twice, even if BoltDB state is lost.
func (f *Forwarder) Forward(ctx context.Context, transfer *model.TransferRecord) *ForwardResult {
	now := time.Now()
	result := &ForwardResult{TranID: transfer.TranID}

	f.logger.Info("processing transfer for forwarding",
		"tran_id", transfer.TranID,
		"sub_account", transfer.SubAccount,
		"asset", transfer.Asset,
		"amount", transfer.Amount,
	)

	// === Layer 1: BoltDB fast-path duplicate check ===
	existingRec, err := f.store.Get(transfer.TranID)
	if err == nil {
		// Record exists - check its status
		// Skip if already forwarded, completed, or failed (any non-pending state)
		status := existingRec.GetStatus()
		if status == model.StatusForwarded || status.IsTerminal() {
			f.logger.Info("skipping transfer: already processed (Layer 1)",
				"tran_id", transfer.TranID,
				"status", existingRec.Status,
			)
			result.Status = StatusSkippedDuplicate
			result.WithdrawID = existingRec.WithdrawID
			return result
		}

		// Record exists in PENDING state - could be from a crash before withdrawal completed
		// Continue to Layer 2 verification to check if withdrawal actually exists
		f.logger.Debug("found PENDING record, continuing to Layer 2",
			"tran_id", transfer.TranID,
			"status", existingRec.Status,
		)
		transfer = existingRec // Use the existing record
	} else if !errors.Is(err, store.ErrNotFound) {
		// Unexpected error
		f.logger.Error("failed to check store", "error", err)
		result.Status = StatusFailed
		result.Error = fmt.Errorf("checking store: %w", err)
		return result
	}

	// === Layer 2: Binance withdrawal history verification ===
	// Query Binance to see if a withdrawal with this tranId already exists
	existingWithdrawal, err := f.client.GetWithdrawalHistoryByOrderID(transfer.TranID)
	if err != nil {
		f.logger.Error("failed to check withdrawal history", "error", err)
		result.Status = StatusFailed
		result.Error = fmt.Errorf("checking withdrawal history: %w", err)
		return result
	}

	if existingWithdrawal != nil {
		// Withdrawal already exists - update our store and return
		f.logger.Info("skipping transfer: withdrawal already exists (Layer 2)",
			"tran_id", transfer.TranID,
			"withdraw_id", existingWithdrawal.ID,
		)

		// Update local store to reflect the forwarded state
		if err := f.updateStoreFromWithdrawal(transfer, existingWithdrawal, now); err != nil {
			f.logger.Warn("failed to update store from existing withdrawal", "error", err)
		}

		result.Status = StatusSkippedAlreadyWithdrawn
		result.WithdrawID = existingWithdrawal.ID
		return result
	}

	// === No existing withdrawal - proceed with forwarding ===

	// Look up destination
	dest, err := f.destCfg.GetDestination(transfer.SubAccount, transfer.Asset)
	if err != nil {
		f.logger.Error("destination not configured",
			"sub_account", transfer.SubAccount,
			"asset", transfer.Asset,
			"error", err,
		)
		// Mark as failed in store
		f.markAsFailed(transfer, fmt.Sprintf("destination not configured: %v", err), now)
		result.Status = StatusFailed
		result.Error = ErrDestinationNotConfigured
		return result
	}

	// Ensure record is in store as PENDING before initiating withdrawal
	if err := f.ensurePending(transfer, now); err != nil {
		f.logger.Error("failed to ensure pending state", "error", err)
		result.Status = StatusFailed
		result.Error = err
		return result
	}

	// Initiate withdrawal with withdrawOrderId set to tranId for idempotency
	// Uses retry logic for transient errors (network timeouts, rate limits, 503s)
	f.logger.Info("initiating withdrawal",
		"tran_id", transfer.TranID,
		"asset", transfer.Asset,
		"amount", transfer.Amount,
		"dest_address", dest.Address,
		"dest_network", dest.Network,
	)

	withdrawReq := &binance.WithdrawRequest{
		Coin:            transfer.Asset,
		Network:         dest.Network,
		Address:         dest.Address,
		Amount:          transfer.Amount,
		WithdrawOrderID: transfer.TranID, // Key for idempotency!
	}

	withdrawResp, retryResult := retry.DoWithResult(ctx, f.retryCfg, func() (*binance.WithdrawResponse, error) {
		return f.client.Withdraw(withdrawReq)
	})

	// Update retry count in transfer record
	if retryResult.Attempts > 1 {
		transfer.RetryCount = retryResult.Attempts - 1 // Attempts includes initial try
		f.logger.Info("withdrawal completed after retries",
			"tran_id", transfer.TranID,
			"attempts", retryResult.Attempts,
			"retry_count", transfer.RetryCount,
		)
		// Persist the retry count
		if err := f.updateRetryCount(transfer); err != nil {
			f.logger.Warn("failed to update retry count", "error", err)
		}
	}

	if retryResult.Err != nil {
		isPermanent := retry.IsPermanent(retryResult.Err)
		isTransient := retry.IsTransient(retryResult.Err)

		f.logger.Error("withdrawal failed after retries",
			"error", retryResult.Err,
			"attempts", retryResult.Attempts,
			"is_transient", isTransient,
			"is_permanent", isPermanent,
		)

		// Mark as FAILED if it's a permanent error (requires human intervention)
		// Permanent errors include:
		// - Invalid destination address
		// - Asset not supported for withdrawal
		// - Amount below minimum withdrawal
		// - Address not whitelisted
		// - Network not supported
		if isPermanent || !isTransient {
			// Permanent error - mark as failed with descriptive message
			errMsg := classifyWithdrawalError(retryResult.Err)
			f.markAsFailed(transfer, errMsg, now)
		}
		// If transient and exhausted retries, leave as PENDING for later retry

		result.Status = StatusFailed
		result.Error = fmt.Errorf("withdrawal API call: %w", retryResult.Err)
		return result
	}

	// Success! Update store to FORWARDED
	f.logger.Info("withdrawal initiated successfully",
		"tran_id", transfer.TranID,
		"withdraw_id", withdrawResp.ID,
	)

	if err := f.markAsForwarded(transfer, withdrawResp.ID, dest.Address, dest.Network, now); err != nil {
		f.logger.Error("failed to update store after forwarding", "error", err)
		// The withdrawal was initiated - don't fail the result
	}

	result.Status = StatusForwarded
	result.WithdrawID = withdrawResp.ID
	return result
}

// ensurePending ensures the transfer record exists in the store with PENDING status.
func (f *Forwarder) ensurePending(transfer *model.TransferRecord, now time.Time) error {
	// Check if record already exists
	existing, err := f.store.Get(transfer.TranID)
	if err == nil {
		// Already exists
		if existing.GetStatus() == model.StatusPending {
			return nil // Already pending
		}
		// Shouldn't happen but handle it
		return fmt.Errorf("unexpected status %s for record %s", existing.Status, transfer.TranID)
	}
	if !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("checking store: %w", err)
	}

	// Create new record as PENDING
	rec := &model.TransferRecord{
		TranID:       transfer.TranID,
		SubAccount:   transfer.SubAccount,
		Asset:        transfer.Asset,
		Amount:       transfer.Amount,
		Status:       string(model.StatusPending),
		TransferTime: transfer.TransferTime,
		DetectedAt:   now,
	}

	if err := f.store.Create(rec); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			// Race condition - another goroutine created it
			return nil
		}
		return fmt.Errorf("creating record: %w", err)
	}

	return nil
}

// markAsForwarded updates the transfer record to FORWARDED status.
func (f *Forwarder) markAsForwarded(transfer *model.TransferRecord, withdrawID, destAddress, destNetwork string, now time.Time) error {
	rec, err := f.store.Get(transfer.TranID)
	if err != nil {
		return fmt.Errorf("getting record: %w", err)
	}

	_, err = f.sm.ToForwarded(rec, withdrawID, destAddress, destNetwork, now)
	if err != nil {
		return fmt.Errorf("transitioning to forwarded: %w", err)
	}

	if err := f.store.Update(rec); err != nil {
		return fmt.Errorf("updating record: %w", err)
	}

	return nil
}

// markAsFailed updates the transfer record to FAILED status.
func (f *Forwarder) markAsFailed(transfer *model.TransferRecord, errMsg string, now time.Time) error {
	// Check if record exists
	rec, err := f.store.Get(transfer.TranID)
	if errors.Is(err, store.ErrNotFound) {
		// Create as PENDING first, then transition to FAILED
		rec = &model.TransferRecord{
			TranID:       transfer.TranID,
			SubAccount:   transfer.SubAccount,
			Asset:        transfer.Asset,
			Amount:       transfer.Amount,
			Status:       string(model.StatusPending),
			TransferTime: transfer.TransferTime,
			DetectedAt:   now,
		}
		if err := f.store.Create(rec); err != nil && !errors.Is(err, store.ErrAlreadyExists) {
			return fmt.Errorf("creating record: %w", err)
		}
		// Re-fetch to get the correct record
		rec, err = f.store.Get(transfer.TranID)
		if err != nil {
			return fmt.Errorf("re-fetching record: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("getting record: %w", err)
	}

	_, err = f.sm.ToFailed(rec, errMsg)
	if err != nil {
		return fmt.Errorf("transitioning to failed: %w", err)
	}

	if err := f.store.Update(rec); err != nil {
		return fmt.Errorf("updating record: %w", err)
	}

	return nil
}

// updateStoreFromWithdrawal updates the store based on an existing withdrawal found during Layer 2 check.
func (f *Forwarder) updateStoreFromWithdrawal(transfer *model.TransferRecord, withdrawal *binance.WithdrawalRecord, now time.Time) error {
	// Check if record exists
	rec, err := f.store.Get(transfer.TranID)
	if errors.Is(err, store.ErrNotFound) {
		// Create as PENDING first
		rec = &model.TransferRecord{
			TranID:       transfer.TranID,
			SubAccount:   transfer.SubAccount,
			Asset:        transfer.Asset,
			Amount:       transfer.Amount,
			Status:       string(model.StatusPending),
			TransferTime: transfer.TransferTime,
			DetectedAt:   now,
		}
		if err := f.store.Create(rec); err != nil && !errors.Is(err, store.ErrAlreadyExists) {
			return fmt.Errorf("creating record: %w", err)
		}
		// Re-fetch
		rec, err = f.store.Get(transfer.TranID)
		if err != nil {
			return fmt.Errorf("re-fetching record: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("getting record: %w", err)
	}

	// If already in terminal state, no need to update
	if rec.IsTerminal() {
		return nil
	}

	// Transition to FORWARDED
	_, err = f.sm.ToForwarded(rec, withdrawal.ID, withdrawal.Address, withdrawal.Network, now)
	if err != nil {
		return fmt.Errorf("transitioning to forwarded: %w", err)
	}

	// Set additional withdrawal details
	rec.WithdrawTxID = withdrawal.TxID
	rec.WithdrawFee = withdrawal.TransactionFee

	if err := f.store.Update(rec); err != nil {
		return fmt.Errorf("updating record: %w", err)
	}

	return nil
}

// classifyWithdrawalError returns a human-readable error message for withdrawal failures.
// This helps operators understand why a transfer failed and what action is needed.
func classifyWithdrawalError(err error) string {
	if err == nil {
		return "unknown error"
	}

	// Check for Binance API errors with specific codes
	var apiErr *binance.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case -4003:
			return "permanent: destination address not whitelisted in Binance - add address to whitelist"
		case -4004:
			return "permanent: destination address is invalid - verify address format"
		case -4005:
			return "permanent: withdrawal amount must be positive"
		case -4015:
			return "permanent: network not supported for withdrawal - check network configuration"
		case -4026:
			return "permanent: duplicate withdrawal - transfer already processed"
		case -4029:
			return "permanent: amount below minimum withdrawal - increase amount"
		case -4030:
			return "permanent: amount exceeds maximum withdrawal - decrease amount"
		case -4057:
			return "permanent: address verification failed - verify address is correct"
		case -1002:
			return "permanent: unauthorized - check API key permissions"
		case -1013:
			return "permanent: amount below minimum notional"
		case -1100:
			return "permanent: illegal characters in parameter"
		case -1102:
			return "permanent: mandatory parameter missing"
		case -1111:
			return "permanent: precision over maximum allowed"
		case -1121:
			return "permanent: invalid asset symbol"
		}
	}

	// Generic fallback
	return fmt.Sprintf("withdrawal failed: %v", err)
}

// updateRetryCount updates the retry count for a transfer record in the store.
func (f *Forwarder) updateRetryCount(transfer *model.TransferRecord) error {
	rec, err := f.store.Get(transfer.TranID)
	if err != nil {
		return fmt.Errorf("getting record: %w", err)
	}

	rec.RetryCount = transfer.RetryCount
	if err := f.store.Update(rec); err != nil {
		return fmt.Errorf("updating record: %w", err)
	}

	return nil
}

// RecoverPending processes any PENDING records on startup.
// This handles the case where the service crashed after recording a transfer
// but before completing the withdrawal.
func (f *Forwarder) RecoverPending(ctx context.Context) error {
	pendingRecords, err := f.store.GetByStatus(string(model.StatusPending))
	if err != nil {
		return fmt.Errorf("getting pending records: %w", err)
	}

	f.logger.Info("recovering pending records", "count", len(pendingRecords))

	for _, rec := range pendingRecords {
		result := f.Forward(ctx, rec)
		f.logger.Info("recovery result",
			"tran_id", rec.TranID,
			"status", result.Status,
			"error", result.Error,
		)
	}

	return nil
}
