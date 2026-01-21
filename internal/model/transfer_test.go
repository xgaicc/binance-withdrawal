package model

import (
	"testing"
)

func TestTransferStatus_String(t *testing.T) {
	tests := []struct {
		status TransferStatus
		want   string
	}{
		{StatusUnknown, "UNKNOWN"},
		{StatusPending, "PENDING"},
		{StatusForwarded, "FORWARDED"},
		{StatusCompleted, "COMPLETED"},
		{StatusFailed, "FAILED"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.status.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTransferStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		status   TransferStatus
		terminal bool
	}{
		{StatusUnknown, false},
		{StatusPending, false},
		{StatusForwarded, false},
		{StatusCompleted, true},
		{StatusFailed, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsTerminal(); got != tt.terminal {
				t.Errorf("IsTerminal() = %v, want %v", got, tt.terminal)
			}
		})
	}
}

func TestTransferStatus_IsValid(t *testing.T) {
	tests := []struct {
		status TransferStatus
		valid  bool
	}{
		{StatusUnknown, true},
		{StatusPending, true},
		{StatusForwarded, true},
		{StatusCompleted, true},
		{StatusFailed, true},
		{"INVALID", false},
		{"", false},
		{"pending", false}, // Case sensitive
	}

	for _, tt := range tests {
		name := string(tt.status)
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			if got := tt.status.IsValid(); got != tt.valid {
				t.Errorf("IsValid() = %v, want %v", got, tt.valid)
			}
		})
	}
}

func TestTransferStatus_CanTransitionTo(t *testing.T) {
	tests := []struct {
		from    TransferStatus
		to      TransferStatus
		allowed bool
	}{
		// Valid transitions
		{StatusUnknown, StatusPending, true},
		{StatusPending, StatusForwarded, true},
		{StatusPending, StatusFailed, true},
		{StatusForwarded, StatusCompleted, true},
		{StatusForwarded, StatusFailed, true},

		// Invalid transitions
		{StatusUnknown, StatusForwarded, false},
		{StatusUnknown, StatusCompleted, false},
		{StatusUnknown, StatusFailed, false},
		{StatusPending, StatusCompleted, false},
		{StatusPending, StatusPending, false},
		{StatusForwarded, StatusPending, false},
		{StatusForwarded, StatusForwarded, false},
		{StatusCompleted, StatusPending, false},
		{StatusCompleted, StatusForwarded, false},
		{StatusCompleted, StatusFailed, false},
		{StatusFailed, StatusPending, false},
		{StatusFailed, StatusForwarded, false},
		{StatusFailed, StatusCompleted, false},

		// Invalid source status
		{"INVALID", StatusPending, false},
	}

	for _, tt := range tests {
		name := string(tt.from) + "_to_" + string(tt.to)
		t.Run(name, func(t *testing.T) {
			if got := tt.from.CanTransitionTo(tt.to); got != tt.allowed {
				t.Errorf("CanTransitionTo() = %v, want %v", got, tt.allowed)
			}
		})
	}
}

func TestTransferRecord_GetStatus(t *testing.T) {
	rec := &TransferRecord{Status: "PENDING"}
	if got := rec.GetStatus(); got != StatusPending {
		t.Errorf("GetStatus() = %v, want %v", got, StatusPending)
	}
}

func TestTransferRecord_StatusHelpers(t *testing.T) {
	tests := []struct {
		status      string
		isPending   bool
		isForwarded bool
		isCompleted bool
		isFailed    bool
		isTerminal  bool
	}{
		{"PENDING", true, false, false, false, false},
		{"FORWARDED", false, true, false, false, false},
		{"COMPLETED", false, false, true, false, true},
		{"FAILED", false, false, false, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			rec := &TransferRecord{Status: tt.status}

			if got := rec.IsPending(); got != tt.isPending {
				t.Errorf("IsPending() = %v, want %v", got, tt.isPending)
			}
			if got := rec.IsForwarded(); got != tt.isForwarded {
				t.Errorf("IsForwarded() = %v, want %v", got, tt.isForwarded)
			}
			if got := rec.IsCompleted(); got != tt.isCompleted {
				t.Errorf("IsCompleted() = %v, want %v", got, tt.isCompleted)
			}
			if got := rec.IsFailed(); got != tt.isFailed {
				t.Errorf("IsFailed() = %v, want %v", got, tt.isFailed)
			}
			if got := rec.IsTerminal(); got != tt.isTerminal {
				t.Errorf("IsTerminal() = %v, want %v", got, tt.isTerminal)
			}
		})
	}
}

func TestTransferRecord_CanTransitionTo(t *testing.T) {
	rec := &TransferRecord{Status: "PENDING"}

	if !rec.CanTransitionTo(StatusForwarded) {
		t.Error("PENDING should be able to transition to FORWARDED")
	}
	if !rec.CanTransitionTo(StatusFailed) {
		t.Error("PENDING should be able to transition to FAILED")
	}
	if rec.CanTransitionTo(StatusCompleted) {
		t.Error("PENDING should not be able to transition to COMPLETED")
	}
}

func TestValidTransitions_AllStatusesCovered(t *testing.T) {
	statuses := []TransferStatus{
		StatusUnknown,
		StatusPending,
		StatusForwarded,
		StatusCompleted,
		StatusFailed,
	}

	for _, s := range statuses {
		if _, ok := ValidTransitions[s]; !ok {
			t.Errorf("status %q not in ValidTransitions map", s)
		}
	}
}
