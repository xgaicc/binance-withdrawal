package sm

import (
	"errors"
	"testing"
	"time"

	"github.com/binance-withdrawal/internal/model"
)

func TestStateMachine_ToPending(t *testing.T) {
	sm := New()
	now := time.Now()

	tests := []struct {
		name        string
		initial     string
		wantErr     error
		wantOldStat model.TransferStatus
	}{
		{
			name:        "empty status (unknown) to pending",
			initial:     "",
			wantErr:     nil,
			wantOldStat: model.StatusUnknown,
		},
		{
			name:    "pending to pending not allowed",
			initial: string(model.StatusPending),
			wantErr: ErrInvalidTransition,
		},
		{
			name:    "forwarded to pending not allowed",
			initial: string(model.StatusForwarded),
			wantErr: ErrInvalidTransition,
		},
		{
			name:    "completed to pending not allowed",
			initial: string(model.StatusCompleted),
			wantErr: ErrAlreadyTerminal,
		},
		{
			name:    "failed to pending not allowed",
			initial: string(model.StatusFailed),
			wantErr: ErrAlreadyTerminal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &model.TransferRecord{
				TranID: "test-123",
				Status: tt.initial,
			}

			result, err := sm.ToPending(rec, now)

			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error containing %v, got nil", tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result.OldStatus != tt.wantOldStat {
				t.Errorf("old status = %s, want %s", result.OldStatus, tt.wantOldStat)
			}
			if result.NewStatus != model.StatusPending {
				t.Errorf("new status = %s, want %s", result.NewStatus, model.StatusPending)
			}
			if rec.Status != string(model.StatusPending) {
				t.Errorf("record status = %s, want %s", rec.Status, model.StatusPending)
			}
			if rec.DetectedAt.IsZero() {
				t.Error("DetectedAt should be set")
			}
		})
	}
}

func TestStateMachine_ToPending_PreservesDetectedAt(t *testing.T) {
	sm := New()
	originalTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	now := time.Now()

	rec := &model.TransferRecord{
		TranID:     "test-123",
		Status:     "",
		DetectedAt: originalTime,
	}

	_, err := sm.ToPending(rec, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !rec.DetectedAt.Equal(originalTime) {
		t.Errorf("DetectedAt changed: got %v, want %v", rec.DetectedAt, originalTime)
	}
}

func TestStateMachine_ToForwarded(t *testing.T) {
	sm := New()
	now := time.Now()

	tests := []struct {
		name       string
		initial    string
		withdrawID string
		wantErr    error
	}{
		{
			name:       "pending to forwarded with withdraw ID",
			initial:    string(model.StatusPending),
			withdrawID: "wd-123",
			wantErr:    nil,
		},
		{
			name:       "pending to forwarded without withdraw ID",
			initial:    string(model.StatusPending),
			withdrawID: "",
			wantErr:    ErrMissingWithdrawID,
		},
		{
			name:       "unknown to forwarded not allowed",
			initial:    "",
			withdrawID: "wd-123",
			wantErr:    ErrInvalidTransition,
		},
		{
			name:       "forwarded to forwarded not allowed",
			initial:    string(model.StatusForwarded),
			withdrawID: "wd-123",
			wantErr:    ErrInvalidTransition,
		},
		{
			name:       "completed to forwarded not allowed",
			initial:    string(model.StatusCompleted),
			withdrawID: "wd-123",
			wantErr:    ErrAlreadyTerminal,
		},
		{
			name:       "failed to forwarded not allowed",
			initial:    string(model.StatusFailed),
			withdrawID: "wd-123",
			wantErr:    ErrAlreadyTerminal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &model.TransferRecord{
				TranID: "test-123",
				Status: tt.initial,
			}

			result, err := sm.ToForwarded(rec, tt.withdrawID, "addr-123", "SOL", now)

			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error containing %v, got nil", tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result.OldStatus != model.StatusPending {
				t.Errorf("old status = %s, want %s", result.OldStatus, model.StatusPending)
			}
			if result.NewStatus != model.StatusForwarded {
				t.Errorf("new status = %s, want %s", result.NewStatus, model.StatusForwarded)
			}
			if rec.Status != string(model.StatusForwarded) {
				t.Errorf("record status = %s, want %s", rec.Status, model.StatusForwarded)
			}
			if rec.WithdrawID != tt.withdrawID {
				t.Errorf("WithdrawID = %s, want %s", rec.WithdrawID, tt.withdrawID)
			}
			if rec.DestAddress != "addr-123" {
				t.Errorf("DestAddress = %s, want addr-123", rec.DestAddress)
			}
			if rec.DestNetwork != "SOL" {
				t.Errorf("DestNetwork = %s, want SOL", rec.DestNetwork)
			}
			if rec.ForwardedAt.IsZero() {
				t.Error("ForwardedAt should be set")
			}
		})
	}
}

func TestStateMachine_ToCompleted(t *testing.T) {
	sm := New()
	now := time.Now()

	tests := []struct {
		name    string
		initial string
		txID    string
		wantErr error
	}{
		{
			name:    "forwarded to completed with txID",
			initial: string(model.StatusForwarded),
			txID:    "tx-hash-123",
			wantErr: nil,
		},
		{
			name:    "forwarded to completed without txID",
			initial: string(model.StatusForwarded),
			txID:    "",
			wantErr: nil,
		},
		{
			name:    "pending to completed not allowed",
			initial: string(model.StatusPending),
			txID:    "tx-hash-123",
			wantErr: ErrInvalidTransition,
		},
		{
			name:    "unknown to completed not allowed",
			initial: "",
			txID:    "tx-hash-123",
			wantErr: ErrInvalidTransition,
		},
		{
			name:    "completed to completed not allowed",
			initial: string(model.StatusCompleted),
			txID:    "tx-hash-123",
			wantErr: ErrAlreadyTerminal,
		},
		{
			name:    "failed to completed not allowed",
			initial: string(model.StatusFailed),
			txID:    "tx-hash-123",
			wantErr: ErrAlreadyTerminal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &model.TransferRecord{
				TranID: "test-123",
				Status: tt.initial,
			}

			result, err := sm.ToCompleted(rec, tt.txID, now)

			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error containing %v, got nil", tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result.OldStatus != model.StatusForwarded {
				t.Errorf("old status = %s, want %s", result.OldStatus, model.StatusForwarded)
			}
			if result.NewStatus != model.StatusCompleted {
				t.Errorf("new status = %s, want %s", result.NewStatus, model.StatusCompleted)
			}
			if rec.Status != string(model.StatusCompleted) {
				t.Errorf("record status = %s, want %s", rec.Status, model.StatusCompleted)
			}
			if rec.CompletedAt.IsZero() {
				t.Error("CompletedAt should be set")
			}
			if tt.txID != "" && rec.WithdrawTxID != tt.txID {
				t.Errorf("WithdrawTxID = %s, want %s", rec.WithdrawTxID, tt.txID)
			}
		})
	}
}

func TestStateMachine_ToFailed(t *testing.T) {
	sm := New()

	tests := []struct {
		name    string
		initial string
		errMsg  string
		wantErr error
	}{
		{
			name:    "pending to failed",
			initial: string(model.StatusPending),
			errMsg:  "invalid destination address",
			wantErr: nil,
		},
		{
			name:    "forwarded to failed",
			initial: string(model.StatusForwarded),
			errMsg:  "withdrawal rejected by Binance",
			wantErr: nil,
		},
		{
			name:    "unknown to failed not allowed",
			initial: "",
			errMsg:  "error message",
			wantErr: ErrInvalidTransition,
		},
		{
			name:    "completed to failed not allowed",
			initial: string(model.StatusCompleted),
			errMsg:  "error message",
			wantErr: ErrAlreadyTerminal,
		},
		{
			name:    "failed to failed not allowed",
			initial: string(model.StatusFailed),
			errMsg:  "error message",
			wantErr: ErrAlreadyTerminal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &model.TransferRecord{
				TranID: "test-123",
				Status: tt.initial,
			}

			result, err := sm.ToFailed(rec, tt.errMsg)

			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error containing %v, got nil", tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result.NewStatus != model.StatusFailed {
				t.Errorf("new status = %s, want %s", result.NewStatus, model.StatusFailed)
			}
			if rec.Status != string(model.StatusFailed) {
				t.Errorf("record status = %s, want %s", rec.Status, model.StatusFailed)
			}
			if rec.Error != tt.errMsg {
				t.Errorf("Error = %s, want %s", rec.Error, tt.errMsg)
			}
		})
	}
}

func TestStateMachine_Validate(t *testing.T) {
	sm := New()

	tests := []struct {
		name    string
		current model.TransferStatus
		target  model.TransferStatus
		wantErr error
	}{
		{
			name:    "unknown to pending",
			current: model.StatusUnknown,
			target:  model.StatusPending,
			wantErr: nil,
		},
		{
			name:    "pending to forwarded",
			current: model.StatusPending,
			target:  model.StatusForwarded,
			wantErr: nil,
		},
		{
			name:    "pending to failed",
			current: model.StatusPending,
			target:  model.StatusFailed,
			wantErr: nil,
		},
		{
			name:    "forwarded to completed",
			current: model.StatusForwarded,
			target:  model.StatusCompleted,
			wantErr: nil,
		},
		{
			name:    "forwarded to failed",
			current: model.StatusForwarded,
			target:  model.StatusFailed,
			wantErr: nil,
		},
		{
			name:    "completed is terminal",
			current: model.StatusCompleted,
			target:  model.StatusFailed,
			wantErr: ErrAlreadyTerminal,
		},
		{
			name:    "failed is terminal",
			current: model.StatusFailed,
			target:  model.StatusCompleted,
			wantErr: ErrAlreadyTerminal,
		},
		{
			name:    "unknown to forwarded not allowed",
			current: model.StatusUnknown,
			target:  model.StatusForwarded,
			wantErr: ErrInvalidTransition,
		},
		{
			name:    "unknown to completed not allowed",
			current: model.StatusUnknown,
			target:  model.StatusCompleted,
			wantErr: ErrInvalidTransition,
		},
		{
			name:    "pending to completed not allowed",
			current: model.StatusPending,
			target:  model.StatusCompleted,
			wantErr: ErrInvalidTransition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := sm.Validate(tt.current, tt.target)

			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error containing %v, got nil", tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestStateMachine_FullLifecycle(t *testing.T) {
	sm := New()
	now := time.Now()

	// Start with a new record (UNKNOWN state)
	rec := &model.TransferRecord{
		TranID:       "transfer-001",
		SubAccount:   "sub@example.com",
		Asset:        "SOL",
		Amount:       "100.5",
		TransferTime: now.Add(-time.Hour),
	}

	// UNKNOWN -> PENDING
	result, err := sm.ToPending(rec, now)
	if err != nil {
		t.Fatalf("ToPending failed: %v", err)
	}
	if result.OldStatus != model.StatusUnknown {
		t.Errorf("expected old status UNKNOWN, got %s", result.OldStatus)
	}
	if !rec.IsPending() {
		t.Error("expected record to be pending")
	}

	// PENDING -> FORWARDED
	result, err = sm.ToForwarded(rec, "wd-001", "dest-addr", "SOL", now)
	if err != nil {
		t.Fatalf("ToForwarded failed: %v", err)
	}
	if result.OldStatus != model.StatusPending {
		t.Errorf("expected old status PENDING, got %s", result.OldStatus)
	}
	if !rec.IsForwarded() {
		t.Error("expected record to be forwarded")
	}

	// FORWARDED -> COMPLETED
	result, err = sm.ToCompleted(rec, "tx-hash-001", now)
	if err != nil {
		t.Fatalf("ToCompleted failed: %v", err)
	}
	if result.OldStatus != model.StatusForwarded {
		t.Errorf("expected old status FORWARDED, got %s", result.OldStatus)
	}
	if !rec.IsCompleted() {
		t.Error("expected record to be completed")
	}
	if !rec.IsTerminal() {
		t.Error("completed should be terminal")
	}

	// Verify terminal state can't transition
	_, err = sm.ToPending(rec, now)
	if !errors.Is(err, ErrAlreadyTerminal) {
		t.Errorf("expected ErrAlreadyTerminal, got %v", err)
	}
}

func TestStateMachine_FailureLifecycle(t *testing.T) {
	sm := New()
	now := time.Now()

	// Test failure from PENDING
	rec1 := &model.TransferRecord{
		TranID: "transfer-001",
		Status: "",
	}

	_, err := sm.ToPending(rec1, now)
	if err != nil {
		t.Fatalf("ToPending failed: %v", err)
	}

	_, err = sm.ToFailed(rec1, "destination not configured")
	if err != nil {
		t.Fatalf("ToFailed from pending failed: %v", err)
	}
	if !rec1.IsFailed() {
		t.Error("expected record to be failed")
	}

	// Test failure from FORWARDED
	rec2 := &model.TransferRecord{
		TranID: "transfer-002",
		Status: string(model.StatusPending),
	}

	_, err = sm.ToForwarded(rec2, "wd-002", "addr", "ETH", now)
	if err != nil {
		t.Fatalf("ToForwarded failed: %v", err)
	}

	_, err = sm.ToFailed(rec2, "withdrawal rejected")
	if err != nil {
		t.Fatalf("ToFailed from forwarded failed: %v", err)
	}
	if !rec2.IsFailed() {
		t.Error("expected record to be failed")
	}
	if !rec2.IsTerminal() {
		t.Error("failed should be terminal")
	}
}
