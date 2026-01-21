// Package sm provides a state machine for managing transfer lifecycle transitions.
package sm

import (
	"errors"
	"fmt"
	"time"

	"github.com/binance-withdrawal/internal/model"
)

// Errors returned by the state machine.
var (
	// ErrInvalidTransition is returned when a state transition is not allowed.
	ErrInvalidTransition = errors.New("invalid state transition")

	// ErrAlreadyTerminal is returned when attempting to transition from a terminal state.
	ErrAlreadyTerminal = errors.New("transfer is in terminal state")

	// ErrMissingWithdrawID is returned when transitioning to FORWARDED without a withdraw ID.
	ErrMissingWithdrawID = errors.New("withdraw_id is required for FORWARDED status")
)

// StateMachine manages transfer state transitions with validation and side effects.
type StateMachine struct{}

// New creates a new StateMachine instance.
func New() *StateMachine {
	return &StateMachine{}
}

// TransitionResult contains the outcome of a state transition.
type TransitionResult struct {
	// OldStatus is the status before the transition.
	OldStatus model.TransferStatus
	// NewStatus is the status after the transition.
	NewStatus model.TransferStatus
	// Record is the updated transfer record.
	Record *model.TransferRecord
}

// ToPending transitions a transfer to PENDING state.
// This is used when first detecting a transfer.
// Sets DetectedAt to the current time if not already set.
func (sm *StateMachine) ToPending(rec *model.TransferRecord, now time.Time) (*TransitionResult, error) {
	oldStatus := rec.GetStatus()

	// For new records, the status might be empty (effectively UNKNOWN)
	if rec.Status == "" {
		oldStatus = model.StatusUnknown
	}

	if !oldStatus.CanTransitionTo(model.StatusPending) {
		if oldStatus.IsTerminal() {
			return nil, ErrAlreadyTerminal
		}
		return nil, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, oldStatus, model.StatusPending)
	}

	rec.Status = string(model.StatusPending)
	if rec.DetectedAt.IsZero() {
		rec.DetectedAt = now
	}

	return &TransitionResult{
		OldStatus: oldStatus,
		NewStatus: model.StatusPending,
		Record:    rec,
	}, nil
}

// ToForwarded transitions a transfer to FORWARDED state.
// This is used when the withdrawal API call succeeds.
// Requires withdrawID and destination info. Sets ForwardedAt to the current time.
func (sm *StateMachine) ToForwarded(rec *model.TransferRecord, withdrawID, destAddress, destNetwork string, now time.Time) (*TransitionResult, error) {
	oldStatus := rec.GetStatus()

	if !oldStatus.CanTransitionTo(model.StatusForwarded) {
		if oldStatus.IsTerminal() {
			return nil, ErrAlreadyTerminal
		}
		return nil, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, oldStatus, model.StatusForwarded)
	}

	if withdrawID == "" {
		return nil, ErrMissingWithdrawID
	}

	rec.Status = string(model.StatusForwarded)
	rec.WithdrawID = withdrawID
	rec.DestAddress = destAddress
	rec.DestNetwork = destNetwork
	rec.ForwardedAt = now

	return &TransitionResult{
		OldStatus: oldStatus,
		NewStatus: model.StatusForwarded,
		Record:    rec,
	}, nil
}

// ToCompleted transitions a transfer to COMPLETED state.
// This is used when the withdrawal is confirmed on-chain.
// Sets CompletedAt to the current time. Optionally updates the transaction hash.
func (sm *StateMachine) ToCompleted(rec *model.TransferRecord, txID string, now time.Time) (*TransitionResult, error) {
	oldStatus := rec.GetStatus()

	if !oldStatus.CanTransitionTo(model.StatusCompleted) {
		if oldStatus.IsTerminal() {
			return nil, ErrAlreadyTerminal
		}
		return nil, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, oldStatus, model.StatusCompleted)
	}

	rec.Status = string(model.StatusCompleted)
	rec.CompletedAt = now
	if txID != "" {
		rec.WithdrawTxID = txID
	}

	return &TransitionResult{
		OldStatus: oldStatus,
		NewStatus: model.StatusCompleted,
		Record:    rec,
	}, nil
}

// ToFailed transitions a transfer to FAILED state.
// This is used when a permanent failure occurs.
// Requires an error message describing the failure.
func (sm *StateMachine) ToFailed(rec *model.TransferRecord, errMsg string) (*TransitionResult, error) {
	oldStatus := rec.GetStatus()

	if !oldStatus.CanTransitionTo(model.StatusFailed) {
		if oldStatus.IsTerminal() {
			return nil, ErrAlreadyTerminal
		}
		return nil, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, oldStatus, model.StatusFailed)
	}

	rec.Status = string(model.StatusFailed)
	rec.Error = errMsg

	return &TransitionResult{
		OldStatus: oldStatus,
		NewStatus: model.StatusFailed,
		Record:    rec,
	}, nil
}

// Validate checks if a transition from the current status to the target is valid.
func (sm *StateMachine) Validate(current, target model.TransferStatus) error {
	if current.IsTerminal() {
		return ErrAlreadyTerminal
	}
	if !current.CanTransitionTo(target) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current, target)
	}
	return nil
}
