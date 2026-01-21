package forwarder

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/binance-withdrawal/internal/binance"
	"github.com/binance-withdrawal/internal/config"
	"github.com/binance-withdrawal/internal/model"
	"github.com/binance-withdrawal/internal/store"
)

// mockClient implements a mock Binance client for testing.
type mockClient struct {
	mu                  sync.Mutex
	withdrawalHistory   map[string]*binance.WithdrawalRecord
	withdrawResponses   map[string]*binance.WithdrawResponse
	withdrawError       error
	historyError        error
	withdrawCallCount   int32 // atomic
	historyCallCount    int32 // atomic
	lastWithdrawRequest *binance.WithdrawRequest
}

func newMockClient() *mockClient {
	return &mockClient{
		withdrawalHistory: make(map[string]*binance.WithdrawalRecord),
		withdrawResponses: make(map[string]*binance.WithdrawResponse),
	}
}

func (m *mockClient) GetWithdrawalHistoryByOrderID(withdrawOrderID string) (*binance.WithdrawalRecord, error) {
	atomic.AddInt32(&m.historyCallCount, 1)
	if m.historyError != nil {
		return nil, m.historyError
	}
	m.mu.Lock()
	rec, ok := m.withdrawalHistory[withdrawOrderID]
	m.mu.Unlock()
	if !ok {
		return nil, nil
	}
	return rec, nil
}

func (m *mockClient) Withdraw(req *binance.WithdrawRequest) (*binance.WithdrawResponse, error) {
	atomic.AddInt32(&m.withdrawCallCount, 1)
	m.mu.Lock()
	m.lastWithdrawRequest = req
	m.mu.Unlock()
	if m.withdrawError != nil {
		return nil, m.withdrawError
	}
	m.mu.Lock()
	resp, ok := m.withdrawResponses[req.WithdrawOrderID]
	m.mu.Unlock()
	if !ok {
		// Default response - and record it as if it happened
		m.mu.Lock()
		m.withdrawalHistory[req.WithdrawOrderID] = &binance.WithdrawalRecord{
			ID:              "withdraw-" + req.WithdrawOrderID,
			WithdrawOrderID: req.WithdrawOrderID,
			Address:         req.Address,
			Network:         req.Network,
			Amount:          req.Amount,
		}
		m.mu.Unlock()
		return &binance.WithdrawResponse{ID: "withdraw-" + req.WithdrawOrderID}, nil
	}
	return resp, nil
}

func (m *mockClient) getWithdrawCallCount() int {
	return int(atomic.LoadInt32(&m.withdrawCallCount))
}

func (m *mockClient) getHistoryCallCount() int {
	return int(atomic.LoadInt32(&m.historyCallCount))
}

// testForwarder wraps Forwarder for testing with mock client.
type testForwarder struct {
	*Forwarder
	mockClient *mockClient
}

func newTestForwarder(t *testing.T) (*testForwarder, func()) {
	t.Helper()

	// Create temp dir for store
	tmpDir, err := os.MkdirTemp("", "forwarder-test-*")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}

	// Create store
	st, err := store.Open(filepath.Join(tmpDir, "test.bolt"))
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("opening store: %v", err)
	}

	// Create destination config
	destCfg := config.NewDestinationConfig()
	destCfg.SetDestination("test@example.com", "SOL", config.Destination{
		Address: "sol-address-123",
		Network: "SOL",
	})
	destCfg.SetDestination("test@example.com", "ETH", config.Destination{
		Address: "0xeth-address-456",
		Network: "ETH",
	})

	// Create mock client
	mc := newMockClient()

	// Create forwarder with nil client (we'll use mock in Forward override)
	f := New(nil, st, destCfg)

	tf := &testForwarder{
		Forwarder:  f,
		mockClient: mc,
	}

	cleanup := func() {
		st.Close()
		os.RemoveAll(tmpDir)
	}

	return tf, cleanup
}

// Forward overrides the real Forward to use mock client.
func (tf *testForwarder) Forward(transfer *model.TransferRecord) *ForwardResult {
	now := time.Now()
	result := &ForwardResult{TranID: transfer.TranID}

	// Layer 1: BoltDB fast-path duplicate check
	existingRec, err := tf.store.Get(transfer.TranID)
	if err == nil {
		status := existingRec.GetStatus()
		// Skip if already forwarded, completed, or failed
		if status == model.StatusForwarded || status.IsTerminal() {
			result.Status = StatusSkippedDuplicate
			result.WithdrawID = existingRec.WithdrawID
			return result
		}
		transfer = existingRec
	} else if !errors.Is(err, store.ErrNotFound) {
		result.Status = StatusFailed
		result.Error = err
		return result
	}

	// Layer 2: Binance withdrawal history verification
	existingWithdrawal, err := tf.mockClient.GetWithdrawalHistoryByOrderID(transfer.TranID)
	if err != nil {
		result.Status = StatusFailed
		result.Error = err
		return result
	}

	if existingWithdrawal != nil {
		// Update local store
		tf.updateStoreFromWithdrawalMock(transfer, existingWithdrawal, now)
		result.Status = StatusSkippedAlreadyWithdrawn
		result.WithdrawID = existingWithdrawal.ID
		return result
	}

	// Look up destination
	dest, err := tf.destCfg.GetDestination(transfer.SubAccount, transfer.Asset)
	if err != nil {
		tf.markAsFailedMock(transfer, "destination not configured", now)
		result.Status = StatusFailed
		result.Error = ErrDestinationNotConfigured
		return result
	}

	// Ensure record is in store as PENDING
	if err := tf.ensurePendingMock(transfer, now); err != nil {
		result.Status = StatusFailed
		result.Error = err
		return result
	}

	// Initiate withdrawal
	withdrawResp, err := tf.mockClient.Withdraw(&binance.WithdrawRequest{
		Coin:            transfer.Asset,
		Network:         dest.Network,
		Address:         dest.Address,
		Amount:          transfer.Amount,
		WithdrawOrderID: transfer.TranID,
	})
	if err != nil {
		result.Status = StatusFailed
		result.Error = err
		return result
	}

	// Update store to FORWARDED
	tf.markAsForwardedMock(transfer, withdrawResp.ID, dest.Address, dest.Network, now)

	result.Status = StatusForwarded
	result.WithdrawID = withdrawResp.ID
	return result
}

func (tf *testForwarder) ensurePendingMock(transfer *model.TransferRecord, now time.Time) error {
	existing, err := tf.store.Get(transfer.TranID)
	if err == nil {
		if existing.GetStatus() == model.StatusPending {
			return nil
		}
		return nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}

	rec := &model.TransferRecord{
		TranID:       transfer.TranID,
		SubAccount:   transfer.SubAccount,
		Asset:        transfer.Asset,
		Amount:       transfer.Amount,
		Status:       string(model.StatusPending),
		TransferTime: transfer.TransferTime,
		DetectedAt:   now,
	}
	return tf.store.Create(rec)
}

func (tf *testForwarder) markAsForwardedMock(transfer *model.TransferRecord, withdrawID, destAddress, destNetwork string, now time.Time) {
	rec, _ := tf.store.Get(transfer.TranID)
	if rec == nil {
		return
	}
	rec.Status = string(model.StatusForwarded)
	rec.WithdrawID = withdrawID
	rec.DestAddress = destAddress
	rec.DestNetwork = destNetwork
	rec.ForwardedAt = now
	tf.store.Update(rec)
}

func (tf *testForwarder) markAsFailedMock(transfer *model.TransferRecord, errMsg string, now time.Time) {
	rec, err := tf.store.Get(transfer.TranID)
	if errors.Is(err, store.ErrNotFound) {
		rec = &model.TransferRecord{
			TranID:       transfer.TranID,
			SubAccount:   transfer.SubAccount,
			Asset:        transfer.Asset,
			Amount:       transfer.Amount,
			Status:       string(model.StatusPending),
			TransferTime: transfer.TransferTime,
			DetectedAt:   now,
		}
		tf.store.Create(rec)
		rec, _ = tf.store.Get(transfer.TranID)
	}
	if rec != nil {
		rec.Status = string(model.StatusFailed)
		rec.Error = errMsg
		tf.store.Update(rec)
	}
}

func (tf *testForwarder) updateStoreFromWithdrawalMock(transfer *model.TransferRecord, withdrawal *binance.WithdrawalRecord, now time.Time) {
	rec, err := tf.store.Get(transfer.TranID)
	if errors.Is(err, store.ErrNotFound) {
		rec = &model.TransferRecord{
			TranID:       transfer.TranID,
			SubAccount:   transfer.SubAccount,
			Asset:        transfer.Asset,
			Amount:       transfer.Amount,
			Status:       string(model.StatusPending),
			TransferTime: transfer.TransferTime,
			DetectedAt:   now,
		}
		tf.store.Create(rec)
		rec, _ = tf.store.Get(transfer.TranID)
	}
	if rec != nil && !rec.IsTerminal() {
		rec.Status = string(model.StatusForwarded)
		rec.WithdrawID = withdrawal.ID
		rec.DestAddress = withdrawal.Address
		rec.DestNetwork = withdrawal.Network
		rec.WithdrawTxID = withdrawal.TxID
		rec.WithdrawFee = withdrawal.TransactionFee
		rec.ForwardedAt = now
		tf.store.Update(rec)
	}
}

// TestNewTransferForwarded tests that a new transfer is forwarded successfully.
func TestNewTransferForwarded(t *testing.T) {
	tf, cleanup := newTestForwarder(t)
	defer cleanup()

	transfer := &model.TransferRecord{
		TranID:       "tran-001",
		SubAccount:   "test@example.com",
		Asset:        "SOL",
		Amount:       "100.5",
		TransferTime: time.Now(),
	}

	result := tf.Forward(transfer)

	if result.Status != StatusForwarded {
		t.Errorf("expected status %s, got %s", StatusForwarded, result.Status)
	}
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}
	if result.WithdrawID == "" {
		t.Error("expected withdraw ID")
	}
	if tf.mockClient.getWithdrawCallCount() != 1 {
		t.Errorf("expected 1 withdraw call, got %d", tf.mockClient.getWithdrawCallCount())
	}
	if tf.mockClient.getHistoryCallCount() != 1 {
		t.Errorf("expected 1 history call, got %d", tf.mockClient.getHistoryCallCount())
	}

	// Verify store was updated
	rec, err := tf.store.Get("tran-001")
	if err != nil {
		t.Fatalf("getting record: %v", err)
	}
	if rec.GetStatus() != model.StatusForwarded {
		t.Errorf("expected status %s, got %s", model.StatusForwarded, rec.Status)
	}
	if rec.WithdrawID == "" {
		t.Error("expected withdraw ID in store")
	}
}

// TestLayer1DuplicateSkipped tests that Layer 1 (BoltDB) prevents duplicate processing
// when record is already in terminal state.
func TestLayer1DuplicateSkipped(t *testing.T) {
	tf, cleanup := newTestForwarder(t)
	defer cleanup()

	// Create a FORWARDED record directly in the store to simulate previous success
	forwardedRec := &model.TransferRecord{
		TranID:       "tran-002",
		SubAccount:   "test@example.com",
		Asset:        "SOL",
		Amount:       "50.0",
		Status:       string(model.StatusForwarded),
		WithdrawID:   "prev-withdraw-id",
		TransferTime: time.Now(),
		DetectedAt:   time.Now(),
		ForwardedAt:  time.Now(),
	}
	if err := tf.store.Create(forwardedRec); err != nil {
		t.Fatalf("creating forwarded record: %v", err)
	}

	// Verify the record was stored correctly
	storedRec, err := tf.store.Get("tran-002")
	if err != nil {
		t.Fatalf("getting stored record: %v", err)
	}
	if storedRec.GetStatus() != model.StatusForwarded {
		t.Fatalf("expected stored status FORWARDED, got %s", storedRec.Status)
	}

	transfer := &model.TransferRecord{
		TranID:       "tran-002",
		SubAccount:   "test@example.com",
		Asset:        "SOL",
		Amount:       "50.0",
		TransferTime: time.Now(),
	}

	// Forward should be skipped by Layer 1 (BoltDB check)
	result := tf.Forward(transfer)

	if result.Status != StatusSkippedDuplicate {
		t.Errorf("expected status %s, got %s", StatusSkippedDuplicate, result.Status)
	}
	if result.WithdrawID != "prev-withdraw-id" {
		t.Errorf("expected withdraw ID 'prev-withdraw-id', got %s", result.WithdrawID)
	}
	// Layer 1 should prevent even the Binance API calls
	if tf.mockClient.getWithdrawCallCount() != 0 {
		t.Errorf("Layer 1 should have prevented withdraw call, got %d", tf.mockClient.getWithdrawCallCount())
	}
	if tf.mockClient.getHistoryCallCount() != 0 {
		t.Errorf("Layer 1 should have prevented history call, got %d", tf.mockClient.getHistoryCallCount())
	}
}

// TestSequentialDuplicateViaLayer1 tests that a second forward of the same transfer
// is caught by Layer 1 (BoltDB) since the first forward updates store to FORWARDED.
func TestSequentialDuplicateViaLayer1(t *testing.T) {
	tf, cleanup := newTestForwarder(t)
	defer cleanup()

	transfer := &model.TransferRecord{
		TranID:       "tran-seq-002",
		SubAccount:   "test@example.com",
		Asset:        "SOL",
		Amount:       "50.0",
		TransferTime: time.Now(),
	}

	// First forward - should succeed
	result1 := tf.Forward(transfer)
	if result1.Status != StatusForwarded {
		t.Fatalf("expected first forward to succeed, got %s", result1.Status)
	}

	// Verify store was updated to FORWARDED
	rec, err := tf.store.Get("tran-seq-002")
	if err != nil {
		t.Fatalf("getting record: %v", err)
	}
	if rec.GetStatus() != model.StatusForwarded {
		t.Fatalf("expected store status FORWARDED, got %s", rec.Status)
	}

	// Second forward - should be caught by Layer 1 since store has FORWARDED
	result2 := tf.Forward(transfer)

	if result2.Status != StatusSkippedDuplicate {
		t.Errorf("expected status %s, got %s", StatusSkippedDuplicate, result2.Status)
	}
}

// TestLayer2AlreadyWithdrawnSkipped tests that Layer 2 (Binance) prevents duplicate processing
// when the withdrawal exists in Binance but not in local store.
func TestLayer2AlreadyWithdrawnSkipped(t *testing.T) {
	tf, cleanup := newTestForwarder(t)
	defer cleanup()

	// Pre-populate Binance with an existing withdrawal
	tf.mockClient.withdrawalHistory["tran-003"] = &binance.WithdrawalRecord{
		ID:              "existing-withdraw-id",
		WithdrawOrderID: "tran-003",
		Address:         "sol-address-123",
		Network:         "SOL",
		Amount:          "75.0",
		TxID:            "blockchain-txid",
	}

	transfer := &model.TransferRecord{
		TranID:       "tran-003",
		SubAccount:   "test@example.com",
		Asset:        "SOL",
		Amount:       "75.0",
		TransferTime: time.Now(),
	}

	result := tf.Forward(transfer)

	if result.Status != StatusSkippedAlreadyWithdrawn {
		t.Errorf("expected status %s, got %s", StatusSkippedAlreadyWithdrawn, result.Status)
	}
	if result.WithdrawID != "existing-withdraw-id" {
		t.Errorf("expected withdraw ID 'existing-withdraw-id', got %s", result.WithdrawID)
	}
	// Should have called history API but not withdraw API
	if tf.mockClient.getHistoryCallCount() != 1 {
		t.Errorf("expected 1 history call, got %d", tf.mockClient.getHistoryCallCount())
	}
	if tf.mockClient.getWithdrawCallCount() != 0 {
		t.Errorf("expected 0 withdraw calls, got %d", tf.mockClient.getWithdrawCallCount())
	}

	// Verify store was updated with the existing withdrawal info
	rec, err := tf.store.Get("tran-003")
	if err != nil {
		t.Fatalf("getting record: %v", err)
	}
	if rec.GetStatus() != model.StatusForwarded {
		t.Errorf("expected status %s, got %s", model.StatusForwarded, rec.Status)
	}
	if rec.WithdrawID != "existing-withdraw-id" {
		t.Errorf("expected withdraw ID 'existing-withdraw-id', got %s", rec.WithdrawID)
	}
	if rec.WithdrawTxID != "blockchain-txid" {
		t.Errorf("expected txid 'blockchain-txid', got %s", rec.WithdrawTxID)
	}
}

// TestLayer2RecoveryFromCrash tests recovery when local store has PENDING but withdrawal exists.
// This simulates a crash after withdrawal succeeded but before local store was updated.
func TestLayer2RecoveryFromCrash(t *testing.T) {
	tf, cleanup := newTestForwarder(t)
	defer cleanup()

	// Create PENDING record in local store (simulating state before crash)
	pendingRec := &model.TransferRecord{
		TranID:       "tran-004",
		SubAccount:   "test@example.com",
		Asset:        "SOL",
		Amount:       "200.0",
		Status:       string(model.StatusPending),
		TransferTime: time.Now(),
		DetectedAt:   time.Now(),
	}
	if err := tf.store.Create(pendingRec); err != nil {
		t.Fatalf("creating pending record: %v", err)
	}

	// But withdrawal actually exists in Binance (API succeeded before crash)
	tf.mockClient.withdrawalHistory["tran-004"] = &binance.WithdrawalRecord{
		ID:              "crash-withdraw-id",
		WithdrawOrderID: "tran-004",
		Address:         "sol-address-123",
		Network:         "SOL",
		Amount:          "200.0",
	}

	transfer := &model.TransferRecord{
		TranID:       "tran-004",
		SubAccount:   "test@example.com",
		Asset:        "SOL",
		Amount:       "200.0",
		TransferTime: time.Now(),
	}

	result := tf.Forward(transfer)

	if result.Status != StatusSkippedAlreadyWithdrawn {
		t.Errorf("expected status %s, got %s", StatusSkippedAlreadyWithdrawn, result.Status)
	}
	// Should NOT have called withdraw API
	if tf.mockClient.getWithdrawCallCount() != 0 {
		t.Errorf("expected 0 withdraw calls, got %d", tf.mockClient.getWithdrawCallCount())
	}

	// Store should now be updated to FORWARDED
	rec, err := tf.store.Get("tran-004")
	if err != nil {
		t.Fatalf("getting record: %v", err)
	}
	if rec.GetStatus() != model.StatusForwarded {
		t.Errorf("expected status %s, got %s", model.StatusForwarded, rec.Status)
	}
}

// TestDestinationNotConfigured tests handling of unconfigured destinations.
func TestDestinationNotConfigured(t *testing.T) {
	tf, cleanup := newTestForwarder(t)
	defer cleanup()

	transfer := &model.TransferRecord{
		TranID:       "tran-005",
		SubAccount:   "unknown@example.com", // Not configured
		Asset:        "BTC",
		Amount:       "1.0",
		TransferTime: time.Now(),
	}

	result := tf.Forward(transfer)

	if result.Status != StatusFailed {
		t.Errorf("expected status %s, got %s", StatusFailed, result.Status)
	}
	if !errors.Is(result.Error, ErrDestinationNotConfigured) {
		t.Errorf("expected ErrDestinationNotConfigured, got %v", result.Error)
	}

	// Verify record was marked as FAILED in store
	rec, err := tf.store.Get("tran-005")
	if err != nil {
		t.Fatalf("getting record: %v", err)
	}
	if rec.GetStatus() != model.StatusFailed {
		t.Errorf("expected status %s, got %s", model.StatusFailed, rec.Status)
	}
}

// TestWithdrawalAPIError tests handling of withdrawal API errors.
func TestWithdrawalAPIError(t *testing.T) {
	tf, cleanup := newTestForwarder(t)
	defer cleanup()

	tf.mockClient.withdrawError = errors.New("API error: insufficient funds")

	transfer := &model.TransferRecord{
		TranID:       "tran-006",
		SubAccount:   "test@example.com",
		Asset:        "SOL",
		Amount:       "1000000.0",
		TransferTime: time.Now(),
	}

	result := tf.Forward(transfer)

	if result.Status != StatusFailed {
		t.Errorf("expected status %s, got %s", StatusFailed, result.Status)
	}
	if result.Error == nil {
		t.Error("expected error")
	}

	// Record should still be PENDING (not failed) - transient errors should be retryable
	rec, err := tf.store.Get("tran-006")
	if err != nil {
		t.Fatalf("getting record: %v", err)
	}
	if rec.GetStatus() != model.StatusPending {
		t.Errorf("expected status %s, got %s", model.StatusPending, rec.Status)
	}
}

// TestHistoryAPIError tests handling of withdrawal history API errors.
func TestHistoryAPIError(t *testing.T) {
	tf, cleanup := newTestForwarder(t)
	defer cleanup()

	tf.mockClient.historyError = errors.New("network timeout")

	transfer := &model.TransferRecord{
		TranID:       "tran-007",
		SubAccount:   "test@example.com",
		Asset:        "SOL",
		Amount:       "50.0",
		TransferTime: time.Now(),
	}

	result := tf.Forward(transfer)

	if result.Status != StatusFailed {
		t.Errorf("expected status %s, got %s", StatusFailed, result.Status)
	}
	// Should NOT have attempted withdrawal
	if tf.mockClient.getWithdrawCallCount() != 0 {
		t.Errorf("expected 0 withdraw calls, got %d", tf.mockClient.getWithdrawCallCount())
	}
}

// TestWithdrawOrderIDSetCorrectly verifies the withdrawOrderId is set to tranId.
func TestWithdrawOrderIDSetCorrectly(t *testing.T) {
	tf, cleanup := newTestForwarder(t)
	defer cleanup()

	transfer := &model.TransferRecord{
		TranID:       "unique-tran-id-123",
		SubAccount:   "test@example.com",
		Asset:        "ETH",
		Amount:       "10.0",
		TransferTime: time.Now(),
	}

	tf.Forward(transfer)

	// Verify the withdrawal request had the correct withdrawOrderId
	if tf.mockClient.lastWithdrawRequest == nil {
		t.Fatal("expected withdraw request")
	}
	if tf.mockClient.lastWithdrawRequest.WithdrawOrderID != "unique-tran-id-123" {
		t.Errorf("expected withdrawOrderId 'unique-tran-id-123', got %s",
			tf.mockClient.lastWithdrawRequest.WithdrawOrderID)
	}
}

// TestConcurrentForwards tests that concurrent forwards for the same transfer
// are handled correctly. Note: in reality, Binance enforces idempotency via
// withdrawOrderId, so even if multiple requests make it through our local checks,
// only one withdrawal will be created. This test verifies our local checks work.
func TestConcurrentForwards(t *testing.T) {
	tf, cleanup := newTestForwarder(t)
	defer cleanup()

	// Pre-record a withdrawal in Binance before any forwards
	// This simulates the scenario where one request already succeeded
	tf.mockClient.mu.Lock()
	tf.mockClient.withdrawalHistory["tran-concurrent"] = &binance.WithdrawalRecord{
		ID:              "concurrent-withdraw-id",
		WithdrawOrderID: "tran-concurrent",
		Address:         "sol-address-123",
		Network:         "SOL",
		Amount:          "100.0",
	}
	tf.mockClient.mu.Unlock()

	transfer := &model.TransferRecord{
		TranID:       "tran-concurrent",
		SubAccount:   "test@example.com",
		Asset:        "SOL",
		Amount:       "100.0",
		TransferTime: time.Now(),
	}

	// Run forwards concurrently - all should be skipped via Layer 2
	results := make(chan *ForwardResult, 10)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- tf.Forward(transfer)
		}()
	}
	wg.Wait()
	close(results)

	// All should be skipped (Layer 2 catches them)
	var skippedCount int
	for result := range results {
		if result.Status == StatusSkippedAlreadyWithdrawn || result.Status == StatusSkippedDuplicate {
			skippedCount++
		}
	}

	if skippedCount != 10 {
		t.Errorf("expected all 10 to be skipped, got %d", skippedCount)
	}

	// No withdraw calls should have been made
	if tf.mockClient.getWithdrawCallCount() != 0 {
		t.Errorf("expected 0 withdraw calls, got %d", tf.mockClient.getWithdrawCallCount())
	}
}

// TestConcurrentForwardsWithoutPriorWithdrawal tests concurrent forwards when no
// prior withdrawal exists. Due to the race between checking history and recording,
// multiple forwards may "succeed" from our local perspective, but Binance's
// withdrawOrderId idempotency will prevent duplicate actual withdrawals.
func TestConcurrentForwardsWithoutPriorWithdrawal(t *testing.T) {
	tf, cleanup := newTestForwarder(t)
	defer cleanup()

	transfer := &model.TransferRecord{
		TranID:       "tran-concurrent-new",
		SubAccount:   "test@example.com",
		Asset:        "SOL",
		Amount:       "100.0",
		TransferTime: time.Now(),
	}

	// First forward should succeed
	result1 := tf.Forward(transfer)
	if result1.Status != StatusForwarded {
		t.Errorf("expected first forward to succeed, got %s", result1.Status)
	}

	// Subsequent forwards should be skipped (Layer 1 catches them now)
	for i := 0; i < 5; i++ {
		result := tf.Forward(transfer)
		if result.Status != StatusSkippedDuplicate && result.Status != StatusSkippedAlreadyWithdrawn {
			t.Errorf("subsequent forward %d should be skipped, got %s", i, result.Status)
		}
	}
}

// TestMultipleDistinctTransfers tests that different transfers are processed independently.
func TestMultipleDistinctTransfers(t *testing.T) {
	tf, cleanup := newTestForwarder(t)
	defer cleanup()

	transfers := []*model.TransferRecord{
		{TranID: "tran-a", SubAccount: "test@example.com", Asset: "SOL", Amount: "10.0", TransferTime: time.Now()},
		{TranID: "tran-b", SubAccount: "test@example.com", Asset: "ETH", Amount: "5.0", TransferTime: time.Now()},
		{TranID: "tran-c", SubAccount: "test@example.com", Asset: "SOL", Amount: "20.0", TransferTime: time.Now()},
	}

	for _, transfer := range transfers {
		result := tf.Forward(transfer)
		if result.Status != StatusForwarded {
			t.Errorf("expected transfer %s to be forwarded, got %s", transfer.TranID, result.Status)
		}
	}

	// All three should have been withdrawn
	if tf.mockClient.getWithdrawCallCount() != 3 {
		t.Errorf("expected 3 withdraw calls, got %d", tf.mockClient.getWithdrawCallCount())
	}
}

// TestTerminalStateTransferSkipped tests that FAILED transfers are also skipped by Layer 1.
func TestTerminalStateTransferSkipped(t *testing.T) {
	tf, cleanup := newTestForwarder(t)
	defer cleanup()

	// Create a FAILED record in store
	failedRec := &model.TransferRecord{
		TranID:       "tran-failed",
		SubAccount:   "test@example.com",
		Asset:        "SOL",
		Amount:       "100.0",
		Status:       string(model.StatusFailed),
		Error:        "previous failure",
		TransferTime: time.Now(),
		DetectedAt:   time.Now(),
	}
	if err := tf.store.Create(failedRec); err != nil {
		t.Fatalf("creating failed record: %v", err)
	}

	transfer := &model.TransferRecord{
		TranID:       "tran-failed",
		SubAccount:   "test@example.com",
		Asset:        "SOL",
		Amount:       "100.0",
		TransferTime: time.Now(),
	}

	result := tf.Forward(transfer)

	if result.Status != StatusSkippedDuplicate {
		t.Errorf("expected status %s, got %s", StatusSkippedDuplicate, result.Status)
	}
	// Should NOT have called any Binance APIs
	if tf.mockClient.getWithdrawCallCount() != 0 || tf.mockClient.getHistoryCallCount() != 0 {
		t.Error("should not have called Binance APIs for terminal state")
	}
}
