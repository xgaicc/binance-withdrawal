package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/binance-withdrawal/internal/model"
	"github.com/binance-withdrawal/internal/store"
)

// mockStore is a test double for TransferStore.
type mockStore struct {
	records      map[string]*model.TransferRecord
	statusCounts map[string]int
	getErr       error
	listErr      error
	countErr     error
}

func newMockStore() *mockStore {
	return &mockStore{
		records:      make(map[string]*model.TransferRecord),
		statusCounts: make(map[string]int),
	}
}

func (m *mockStore) Get(tranID string) (*model.TransferRecord, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	rec, ok := m.records[tranID]
	if !ok {
		return nil, store.ErrNotFound
	}
	return rec, nil
}

func (m *mockStore) GetByStatus(status string) ([]*model.TransferRecord, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	var records []*model.TransferRecord
	for _, rec := range m.records {
		if rec.Status == status {
			records = append(records, rec)
		}
	}
	return records, nil
}

func (m *mockStore) ListByTime(limit int) ([]*model.TransferRecord, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	var records []*model.TransferRecord
	for _, rec := range m.records {
		records = append(records, rec)
		if limit > 0 && len(records) >= limit {
			break
		}
	}
	return records, nil
}

func (m *mockStore) CountByStatus() (map[string]int, error) {
	if m.countErr != nil {
		return nil, m.countErr
	}
	return m.statusCounts, nil
}

// mockForwarder is a test double for TransferForwarder.
type mockForwarder struct {
	result *ForwardResult
}

func (m *mockForwarder) Forward(ctx context.Context, transfer *model.TransferRecord) *ForwardResult {
	if m.result != nil {
		return m.result
	}
	return &ForwardResult{
		TranID: transfer.TranID,
		Status: "forwarded",
	}
}

// mockDetector is a test double for ConnectionStatus.
type mockDetector struct {
	connected         bool
	lastProcessedTime time.Time
}

func (m *mockDetector) IsConnected() bool {
	return m.connected
}

func (m *mockDetector) GetLastProcessedTime() time.Time {
	return m.lastProcessedTime
}

func TestHandleHealth(t *testing.T) {
	srv := New(":0", &Dependencies{})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", resp.Status)
	}

	if resp.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}
}

func TestHandleHealth_MethodNotAllowed(t *testing.T) {
	srv := New(":0", &Dependencies{})

	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	w := httptest.NewRecorder()

	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, w.Code)
	}
}

func TestHandleStatus(t *testing.T) {
	mockSt := newMockStore()
	mockSt.statusCounts = map[string]int{
		"PENDING":   2,
		"FORWARDED": 3,
		"COMPLETED": 10,
		"FAILED":    1,
	}

	mockDet := &mockDetector{
		connected:         true,
		lastProcessedTime: time.Date(2026, 1, 21, 10, 0, 0, 0, time.UTC),
	}

	srv := New(":0", &Dependencies{
		Store:    mockSt,
		Detector: mockDet,
	})

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	w := httptest.NewRecorder()

	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp StatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Detector.Connected {
		t.Error("expected detector to be connected")
	}

	if resp.Detector.LastProcessedTime == "" {
		t.Error("expected non-empty last processed time")
	}

	if resp.Transfers.Total != 16 {
		t.Errorf("expected total 16, got %d", resp.Transfers.Total)
	}

	if resp.Transfers.ByStatus["PENDING"] != 2 {
		t.Errorf("expected 2 PENDING, got %d", resp.Transfers.ByStatus["PENDING"])
	}
}

func TestHandleListTransfers(t *testing.T) {
	mockSt := newMockStore()
	mockSt.records["123"] = &model.TransferRecord{
		TranID:     "123",
		SubAccount: "test@example.com",
		Asset:      "SOL",
		Amount:     "10.5",
		Status:     "PENDING",
		DetectedAt: time.Now(),
	}
	mockSt.records["456"] = &model.TransferRecord{
		TranID:     "456",
		SubAccount: "test@example.com",
		Asset:      "ETH",
		Amount:     "1.0",
		Status:     "COMPLETED",
		DetectedAt: time.Now(),
	}

	srv := New(":0", &Dependencies{Store: mockSt})

	t.Run("list all", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/transfers", nil)
		w := httptest.NewRecorder()

		srv.mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}

		var resp TransferListResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if resp.Count != 2 {
			t.Errorf("expected 2 transfers, got %d", resp.Count)
		}
	})

	t.Run("filter by status", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/transfers?status=PENDING", nil)
		w := httptest.NewRecorder()

		srv.mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}

		var resp TransferListResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if resp.Count != 1 {
			t.Errorf("expected 1 transfer, got %d", resp.Count)
		}
	})

	t.Run("invalid status", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/transfers?status=INVALID", nil)
		w := httptest.NewRecorder()

		srv.mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
		}
	})

	t.Run("invalid limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/transfers?limit=abc", nil)
		w := httptest.NewRecorder()

		srv.mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
		}
	})
}

func TestHandleGetTransfer(t *testing.T) {
	mockSt := newMockStore()
	mockSt.records["123"] = &model.TransferRecord{
		TranID:     "123",
		SubAccount: "test@example.com",
		Asset:      "SOL",
		Amount:     "10.5",
		Status:     "PENDING",
	}

	srv := New(":0", &Dependencies{Store: mockSt})

	t.Run("found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/transfers/123", nil)
		w := httptest.NewRecorder()

		srv.mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}

		var resp model.TransferRecord
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if resp.TranID != "123" {
			t.Errorf("expected tran_id '123', got %q", resp.TranID)
		}
	})

	t.Run("not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/transfers/999", nil)
		w := httptest.NewRecorder()

		srv.mux.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("expected status %d, got %d", http.StatusNotFound, w.Code)
		}
	})
}

func TestHandleRetryTransfer(t *testing.T) {
	t.Run("successful retry", func(t *testing.T) {
		mockSt := newMockStore()
		mockSt.records["123"] = &model.TransferRecord{
			TranID:     "123",
			SubAccount: "test@example.com",
			Asset:      "SOL",
			Amount:     "10.5",
			Status:     "FAILED",
		}

		mockFwd := &mockForwarder{
			result: &ForwardResult{
				TranID:     "123",
				Status:     "forwarded",
				WithdrawID: "W123",
			},
		}

		srv := New(":0", &Dependencies{
			Store:     mockSt,
			Forwarder: mockFwd,
		})

		req := httptest.NewRequest(http.MethodPost, "/transfers/123/retry", nil)
		w := httptest.NewRecorder()

		srv.mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}

		var resp RetryResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if resp.Status != "forwarded" {
			t.Errorf("expected status 'forwarded', got %q", resp.Status)
		}

		if resp.WithdrawID != "W123" {
			t.Errorf("expected withdraw_id 'W123', got %q", resp.WithdrawID)
		}
	})

	t.Run("skip completed transfer", func(t *testing.T) {
		mockSt := newMockStore()
		mockSt.records["123"] = &model.TransferRecord{
			TranID: "123",
			Status: "COMPLETED",
		}

		srv := New(":0", &Dependencies{
			Store:     mockSt,
			Forwarder: &mockForwarder{},
		})

		req := httptest.NewRequest(http.MethodPost, "/transfers/123/retry", nil)
		w := httptest.NewRecorder()

		srv.mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}

		var resp RetryResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if resp.Status != "skipped" {
			t.Errorf("expected status 'skipped', got %q", resp.Status)
		}

		if resp.Message != "transfer already completed" {
			t.Errorf("unexpected message: %q", resp.Message)
		}
	})

	t.Run("skip forwarded transfer", func(t *testing.T) {
		mockSt := newMockStore()
		mockSt.records["123"] = &model.TransferRecord{
			TranID:     "123",
			Status:     "FORWARDED",
			WithdrawID: "W999",
		}

		srv := New(":0", &Dependencies{
			Store:     mockSt,
			Forwarder: &mockForwarder{},
		})

		req := httptest.NewRequest(http.MethodPost, "/transfers/123/retry", nil)
		w := httptest.NewRecorder()

		srv.mux.ServeHTTP(w, req)

		var resp RetryResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if resp.Status != "skipped" {
			t.Errorf("expected status 'skipped', got %q", resp.Status)
		}

		if resp.WithdrawID != "W999" {
			t.Errorf("expected withdraw_id 'W999', got %q", resp.WithdrawID)
		}
	})

	t.Run("not found", func(t *testing.T) {
		mockSt := newMockStore()

		srv := New(":0", &Dependencies{
			Store:     mockSt,
			Forwarder: &mockForwarder{},
		})

		req := httptest.NewRequest(http.MethodPost, "/transfers/999/retry", nil)
		w := httptest.NewRecorder()

		srv.mux.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("expected status %d, got %d", http.StatusNotFound, w.Code)
		}
	})

	t.Run("forward error", func(t *testing.T) {
		mockSt := newMockStore()
		mockSt.records["123"] = &model.TransferRecord{
			TranID: "123",
			Status: "FAILED",
		}

		mockFwd := &mockForwarder{
			result: &ForwardResult{
				TranID: "123",
				Status: "failed",
				Error:  errors.New("withdrawal API error"),
			},
		}

		srv := New(":0", &Dependencies{
			Store:     mockSt,
			Forwarder: mockFwd,
		})

		req := httptest.NewRequest(http.MethodPost, "/transfers/123/retry", nil)
		w := httptest.NewRecorder()

		srv.mux.ServeHTTP(w, req)

		if w.Code != http.StatusUnprocessableEntity {
			t.Errorf("expected status %d, got %d", http.StatusUnprocessableEntity, w.Code)
		}

		var resp RetryResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if resp.Error != "withdrawal API error" {
			t.Errorf("expected error 'withdrawal API error', got %q", resp.Error)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		srv := New(":0", &Dependencies{
			Store:     newMockStore(),
			Forwarder: &mockForwarder{},
		})

		req := httptest.NewRequest(http.MethodGet, "/transfers/123/retry", nil)
		w := httptest.NewRecorder()

		srv.mux.ServeHTTP(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, w.Code)
		}
	})
}

func TestExtractTransferID(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/transfers/123", "123"},
		{"/transfers/123/retry", "123"},
		{"/transfers/abc-456", "abc-456"},
		{"/transfers/", ""},
		{"/transfers", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := extractTransferID(tt.path)
			if result != tt.expected {
				t.Errorf("extractTransferID(%q) = %q, expected %q", tt.path, result, tt.expected)
			}
		})
	}
}
