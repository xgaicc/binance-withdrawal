package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/binance-withdrawal/internal/model"
)

func TestStore_CreateAndGet(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	rec := &model.TransferRecord{
		TranID:       "tran123",
		SubAccount:   "test@example.com",
		Asset:        "SOL",
		Amount:       "100.5",
		Status:       string(model.StatusPending),
		TransferTime: time.Now().Add(-time.Hour),
		DetectedAt:   time.Now(),
	}

	// Create
	if err := s.Create(rec); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Get
	got, err := s.Get("tran123")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if got.TranID != rec.TranID {
		t.Errorf("TranID = %q, want %q", got.TranID, rec.TranID)
	}
	if got.SubAccount != rec.SubAccount {
		t.Errorf("SubAccount = %q, want %q", got.SubAccount, rec.SubAccount)
	}
	if got.Asset != rec.Asset {
		t.Errorf("Asset = %q, want %q", got.Asset, rec.Asset)
	}
	if got.Amount != rec.Amount {
		t.Errorf("Amount = %q, want %q", got.Amount, rec.Amount)
	}
	if got.Status != rec.Status {
		t.Errorf("Status = %q, want %q", got.Status, rec.Status)
	}
}

func TestStore_CreateDuplicate(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	rec := &model.TransferRecord{
		TranID:     "tran123",
		SubAccount: "test@example.com",
		Asset:      "SOL",
		Amount:     "100.5",
		Status:     string(model.StatusPending),
		DetectedAt: time.Now(),
	}

	if err := s.Create(rec); err != nil {
		t.Fatalf("First Create failed: %v", err)
	}

	// Second create should fail
	err := s.Create(rec)
	if err != ErrAlreadyExists {
		t.Errorf("Second Create error = %v, want ErrAlreadyExists", err)
	}
}

func TestStore_GetNotFound(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	_, err := s.Get("nonexistent")
	if err != ErrNotFound {
		t.Errorf("Get error = %v, want ErrNotFound", err)
	}
}

func TestStore_Exists(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	rec := &model.TransferRecord{
		TranID:     "tran123",
		SubAccount: "test@example.com",
		Asset:      "SOL",
		Amount:     "100.5",
		Status:     string(model.StatusPending),
		DetectedAt: time.Now(),
	}

	// Before create
	exists, err := s.Exists("tran123")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if exists {
		t.Error("Exists = true before Create, want false")
	}

	// After create
	if err := s.Create(rec); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	exists, err = s.Exists("tran123")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !exists {
		t.Error("Exists = false after Create, want true")
	}
}

func TestStore_Update(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	now := time.Now()
	rec := &model.TransferRecord{
		TranID:       "tran123",
		SubAccount:   "test@example.com",
		Asset:        "SOL",
		Amount:       "100.5",
		Status:       string(model.StatusPending),
		TransferTime: now.Add(-time.Hour),
		DetectedAt:   now,
	}

	if err := s.Create(rec); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Update record
	rec.Status = string(model.StatusForwarded)
	rec.WithdrawID = "withdraw456"
	rec.ForwardedAt = now.Add(time.Minute)

	if err := s.Update(rec); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Verify update
	got, err := s.Get("tran123")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if got.Status != string(model.StatusForwarded) {
		t.Errorf("Status = %q, want %q", got.Status, model.StatusForwarded)
	}
	if got.WithdrawID != "withdraw456" {
		t.Errorf("WithdrawID = %q, want %q", got.WithdrawID, "withdraw456")
	}
}

func TestStore_UpdateNotFound(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	rec := &model.TransferRecord{
		TranID:     "nonexistent",
		SubAccount: "test@example.com",
		Asset:      "SOL",
		Amount:     "100.5",
		Status:     string(model.StatusPending),
		DetectedAt: time.Now(),
	}

	err := s.Update(rec)
	if err != ErrNotFound {
		t.Errorf("Update error = %v, want ErrNotFound", err)
	}
}

func TestStore_UpdateStatus(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	rec := &model.TransferRecord{
		TranID:     "tran123",
		SubAccount: "test@example.com",
		Asset:      "SOL",
		Amount:     "100.5",
		Status:     string(model.StatusPending),
		DetectedAt: time.Now(),
	}

	if err := s.Create(rec); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Update status
	if err := s.UpdateStatus("tran123", string(model.StatusForwarded)); err != nil {
		t.Fatalf("UpdateStatus failed: %v", err)
	}

	// Verify
	got, err := s.Get("tran123")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Status != string(model.StatusForwarded) {
		t.Errorf("Status = %q, want %q", got.Status, model.StatusForwarded)
	}

	// Verify index is updated
	pending, err := s.GetByStatus(string(model.StatusPending))
	if err != nil {
		t.Fatalf("GetByStatus PENDING failed: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("GetByStatus PENDING = %d records, want 0", len(pending))
	}

	forwarded, err := s.GetByStatus(string(model.StatusForwarded))
	if err != nil {
		t.Fatalf("GetByStatus FORWARDED failed: %v", err)
	}
	if len(forwarded) != 1 {
		t.Errorf("GetByStatus FORWARDED = %d records, want 1", len(forwarded))
	}
}

func TestStore_GetByStatus(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	now := time.Now()

	// Create records with different statuses
	records := []*model.TransferRecord{
		{TranID: "tran1", SubAccount: "test@example.com", Asset: "SOL", Amount: "10", Status: string(model.StatusPending), DetectedAt: now},
		{TranID: "tran2", SubAccount: "test@example.com", Asset: "ETH", Amount: "20", Status: string(model.StatusPending), DetectedAt: now.Add(time.Second)},
		{TranID: "tran3", SubAccount: "test@example.com", Asset: "BTC", Amount: "30", Status: string(model.StatusForwarded), DetectedAt: now.Add(2 * time.Second)},
		{TranID: "tran4", SubAccount: "test@example.com", Asset: "USDC", Amount: "40", Status: string(model.StatusCompleted), DetectedAt: now.Add(3 * time.Second)},
		{TranID: "tran5", SubAccount: "test@example.com", Asset: "SOL", Amount: "50", Status: string(model.StatusFailed), DetectedAt: now.Add(4 * time.Second)},
	}

	for _, rec := range records {
		if err := s.Create(rec); err != nil {
			t.Fatalf("Create %s failed: %v", rec.TranID, err)
		}
	}

	// Test each status
	tests := []struct {
		status string
		want   int
	}{
		{string(model.StatusPending), 2},
		{string(model.StatusForwarded), 1},
		{string(model.StatusCompleted), 1},
		{string(model.StatusFailed), 1},
	}

	for _, tt := range tests {
		got, err := s.GetByStatus(tt.status)
		if err != nil {
			t.Errorf("GetByStatus(%s) failed: %v", tt.status, err)
			continue
		}
		if len(got) != tt.want {
			t.Errorf("GetByStatus(%s) = %d records, want %d", tt.status, len(got), tt.want)
		}
	}
}

func TestStore_ListByTime(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	now := time.Now()

	// Create records at different times
	records := []*model.TransferRecord{
		{TranID: "tran1", SubAccount: "test@example.com", Asset: "SOL", Amount: "10", Status: string(model.StatusPending), DetectedAt: now},
		{TranID: "tran2", SubAccount: "test@example.com", Asset: "ETH", Amount: "20", Status: string(model.StatusPending), DetectedAt: now.Add(time.Second)},
		{TranID: "tran3", SubAccount: "test@example.com", Asset: "BTC", Amount: "30", Status: string(model.StatusForwarded), DetectedAt: now.Add(2 * time.Second)},
	}

	for _, rec := range records {
		if err := s.Create(rec); err != nil {
			t.Fatalf("Create %s failed: %v", rec.TranID, err)
		}
	}

	// List all
	got, err := s.ListByTime(0)
	if err != nil {
		t.Fatalf("ListByTime(0) failed: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("ListByTime(0) = %d records, want 3", len(got))
	}

	// Verify order (should be by time)
	if got[0].TranID != "tran1" {
		t.Errorf("First record TranID = %q, want tran1", got[0].TranID)
	}
	if got[2].TranID != "tran3" {
		t.Errorf("Last record TranID = %q, want tran3", got[2].TranID)
	}

	// List with limit
	got, err = s.ListByTime(2)
	if err != nil {
		t.Fatalf("ListByTime(2) failed: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("ListByTime(2) = %d records, want 2", len(got))
	}
}

func TestStore_ListByTimeRange(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	now := time.Now()

	// Create records at different times
	records := []*model.TransferRecord{
		{TranID: "tran1", SubAccount: "test@example.com", Asset: "SOL", Amount: "10", Status: string(model.StatusPending), DetectedAt: now},
		{TranID: "tran2", SubAccount: "test@example.com", Asset: "ETH", Amount: "20", Status: string(model.StatusPending), DetectedAt: now.Add(time.Hour)},
		{TranID: "tran3", SubAccount: "test@example.com", Asset: "BTC", Amount: "30", Status: string(model.StatusForwarded), DetectedAt: now.Add(2 * time.Hour)},
	}

	for _, rec := range records {
		if err := s.Create(rec); err != nil {
			t.Fatalf("Create %s failed: %v", rec.TranID, err)
		}
	}

	// Query range that includes only middle record
	got, err := s.ListByTimeRange(now.Add(30*time.Minute), now.Add(90*time.Minute))
	if err != nil {
		t.Fatalf("ListByTimeRange failed: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("ListByTimeRange = %d records, want 1", len(got))
	}
	if len(got) > 0 && got[0].TranID != "tran2" {
		t.Errorf("ListByTimeRange record TranID = %q, want tran2", got[0].TranID)
	}
}

func TestStore_Delete(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	rec := &model.TransferRecord{
		TranID:     "tran123",
		SubAccount: "test@example.com",
		Asset:      "SOL",
		Amount:     "100.5",
		Status:     string(model.StatusPending),
		DetectedAt: time.Now(),
	}

	if err := s.Create(rec); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Delete
	if err := s.Delete("tran123"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify record is gone
	_, err := s.Get("tran123")
	if err != ErrNotFound {
		t.Errorf("Get after Delete error = %v, want ErrNotFound", err)
	}

	// Verify indexes are cleaned
	pending, err := s.GetByStatus(string(model.StatusPending))
	if err != nil {
		t.Fatalf("GetByStatus failed: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("GetByStatus PENDING = %d records after delete, want 0", len(pending))
	}
}

func TestStore_DeleteNotFound(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	err := s.Delete("nonexistent")
	if err != ErrNotFound {
		t.Errorf("Delete error = %v, want ErrNotFound", err)
	}
}

func TestStore_Count(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	now := time.Now()

	// Initially empty
	count, err := s.Count()
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Initial Count = %d, want 0", count)
	}

	// Add some records
	for i := 0; i < 5; i++ {
		rec := &model.TransferRecord{
			TranID:     "tran" + string(rune('0'+i)),
			SubAccount: "test@example.com",
			Asset:      "SOL",
			Amount:     "10",
			Status:     string(model.StatusPending),
			DetectedAt: now.Add(time.Duration(i) * time.Second),
		}
		if err := s.Create(rec); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	count, err = s.Count()
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 5 {
		t.Errorf("Count = %d, want 5", count)
	}
}

func TestStore_CountByStatus(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	now := time.Now()

	// Create records with different statuses
	records := []*model.TransferRecord{
		{TranID: "tran1", SubAccount: "test@example.com", Asset: "SOL", Amount: "10", Status: string(model.StatusPending), DetectedAt: now},
		{TranID: "tran2", SubAccount: "test@example.com", Asset: "ETH", Amount: "20", Status: string(model.StatusPending), DetectedAt: now.Add(time.Second)},
		{TranID: "tran3", SubAccount: "test@example.com", Asset: "BTC", Amount: "30", Status: string(model.StatusForwarded), DetectedAt: now.Add(2 * time.Second)},
		{TranID: "tran4", SubAccount: "test@example.com", Asset: "USDC", Amount: "40", Status: string(model.StatusCompleted), DetectedAt: now.Add(3 * time.Second)},
	}

	for _, rec := range records {
		if err := s.Create(rec); err != nil {
			t.Fatalf("Create %s failed: %v", rec.TranID, err)
		}
	}

	counts, err := s.CountByStatus()
	if err != nil {
		t.Fatalf("CountByStatus failed: %v", err)
	}

	if counts[string(model.StatusPending)] != 2 {
		t.Errorf("PENDING count = %d, want 2", counts[string(model.StatusPending)])
	}
	if counts[string(model.StatusForwarded)] != 1 {
		t.Errorf("FORWARDED count = %d, want 1", counts[string(model.StatusForwarded)])
	}
	if counts[string(model.StatusCompleted)] != 1 {
		t.Errorf("COMPLETED count = %d, want 1", counts[string(model.StatusCompleted)])
	}
}

func TestStore_StatusIndexConsistency(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	rec := &model.TransferRecord{
		TranID:     "tran123",
		SubAccount: "test@example.com",
		Asset:      "SOL",
		Amount:     "100.5",
		Status:     string(model.StatusPending),
		DetectedAt: time.Now(),
	}

	if err := s.Create(rec); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Transition through all statuses
	transitions := []string{
		string(model.StatusForwarded),
		string(model.StatusCompleted),
	}

	for _, newStatus := range transitions {
		if err := s.UpdateStatus("tran123", newStatus); err != nil {
			t.Fatalf("UpdateStatus to %s failed: %v", newStatus, err)
		}

		// Check only one status has this record
		for _, status := range []string{
			string(model.StatusPending),
			string(model.StatusForwarded),
			string(model.StatusCompleted),
			string(model.StatusFailed),
		} {
			recs, err := s.GetByStatus(status)
			if err != nil {
				t.Fatalf("GetByStatus(%s) failed: %v", status, err)
			}

			expected := 0
			if status == newStatus {
				expected = 1
			}
			if len(recs) != expected {
				t.Errorf("After transition to %s: GetByStatus(%s) = %d, want %d",
					newStatus, status, len(recs), expected)
			}
		}
	}
}

// newTestStore creates a new store for testing with a temporary database file.
func newTestStore(t *testing.T) *Store {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.bolt")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Failed to open test store: %v", err)
	}

	t.Cleanup(func() {
		s.Close()
		os.Remove(path)
	})

	return s
}
