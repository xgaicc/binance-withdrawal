package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/binance-withdrawal/internal/model"
	"github.com/binance-withdrawal/internal/store"
)

// extractTransferID extracts the transfer ID from a path like /transfers/{id} or /transfers/{id}/retry.
func extractTransferID(path string) string {
	// Remove /transfers/ prefix
	path = strings.TrimPrefix(path, "/transfers/")
	// Remove any trailing path components (like /retry)
	if idx := strings.Index(path, "/"); idx != -1 {
		path = path[:idx]
	}
	return path
}

// HealthResponse is the response for the /health endpoint.
type HealthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

// handleHealth returns basic health status.
// GET /health
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	resp := HealthResponse{
		Status:    "ok",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// StatusResponse is the response for the /status endpoint.
type StatusResponse struct {
	Timestamp string        `json:"timestamp"`
	Uptime    string        `json:"uptime"`
	Detector  DetectorState `json:"detector"`
	Transfers TransferStats `json:"transfers"`
}

// DetectorState represents the WebSocket detector state.
type DetectorState struct {
	Connected         bool   `json:"connected"`
	LastProcessedTime string `json:"last_processed_time,omitempty"`
}

// TransferStats shows transfer counts by status.
type TransferStats struct {
	Total    int            `json:"total"`
	ByStatus map[string]int `json:"by_status"`
}

// handleStatus returns detailed service status.
// GET /status
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	now := time.Now()

	// Get detector state
	var detectorState DetectorState
	if s.deps.Detector != nil {
		detectorState.Connected = s.deps.Detector.IsConnected()
		lastProcessed := s.deps.Detector.GetLastProcessedTime()
		if !lastProcessed.IsZero() {
			detectorState.LastProcessedTime = lastProcessed.UTC().Format(time.RFC3339)
		}
	}

	// Get transfer counts
	var transferStats TransferStats
	if s.deps.Store != nil {
		counts, err := s.deps.Store.CountByStatus()
		if err != nil {
			s.logger.Error("failed to get status counts", "error", err)
			s.writeError(w, http.StatusInternalServerError, "failed to retrieve transfer statistics")
			return
		}
		transferStats.ByStatus = counts
		for _, count := range counts {
			transferStats.Total += count
		}
	}

	resp := StatusResponse{
		Timestamp: now.UTC().Format(time.RFC3339),
		Uptime:    now.Sub(s.startTime).Round(time.Second).String(),
		Detector:  detectorState,
		Transfers: transferStats,
	}

	s.writeJSON(w, http.StatusOK, resp)
}

// TransferListResponse is the response for listing transfers.
type TransferListResponse struct {
	Transfers []*model.TransferRecord `json:"transfers"`
	Count     int                     `json:"count"`
}

// handleTransfers handles requests to /transfers.
// GET /transfers - list transfers with optional filters
func (s *Server) handleTransfers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.handleListTransfers(w, r)
}

// handleListTransfers returns a list of transfer records.
// GET /transfers
// Query params:
//   - status: filter by status (PENDING, FORWARDED, COMPLETED, FAILED)
//   - limit: maximum number of records (default 100, max 1000)
func (s *Server) handleListTransfers(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		s.writeError(w, http.StatusServiceUnavailable, "store not available")
		return
	}

	// Parse query parameters
	status := r.URL.Query().Get("status")
	limitStr := r.URL.Query().Get("limit")

	limit := 100
	if limitStr != "" {
		parsed, err := strconv.Atoi(limitStr)
		if err != nil || parsed < 1 {
			s.writeError(w, http.StatusBadRequest, "invalid limit parameter")
			return
		}
		if parsed > 1000 {
			parsed = 1000
		}
		limit = parsed
	}

	var records []*model.TransferRecord
	var err error

	if status != "" {
		// Validate status
		if !model.TransferStatus(status).IsValid() || status == string(model.StatusUnknown) {
			s.writeError(w, http.StatusBadRequest, "invalid status: must be PENDING, FORWARDED, COMPLETED, or FAILED")
			return
		}
		records, err = s.deps.Store.GetByStatus(status)
	} else {
		records, err = s.deps.Store.ListByTime(limit)
	}

	if err != nil {
		s.logger.Error("failed to list transfers", "error", err)
		s.writeError(w, http.StatusInternalServerError, "failed to retrieve transfers")
		return
	}

	// Apply limit if filtering by status (which doesn't have built-in limit)
	if status != "" && len(records) > limit {
		records = records[:limit]
	}

	resp := TransferListResponse{
		Transfers: records,
		Count:     len(records),
	}

	s.writeJSON(w, http.StatusOK, resp)
}

// handleTransferByID handles requests to /transfers/{id} and /transfers/{id}/retry.
func (s *Server) handleTransferByID(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	tranID := extractTransferID(path)

	if tranID == "" {
		s.writeError(w, http.StatusBadRequest, "transfer ID required")
		return
	}

	// Check if this is a retry request
	if strings.HasSuffix(path, "/retry") {
		if r.Method != http.MethodPost {
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.handleRetryTransfer(w, r, tranID)
		return
	}

	// Otherwise it's a GET for a specific transfer
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.handleGetTransfer(w, r, tranID)
}

// handleGetTransfer returns a single transfer by ID.
// GET /transfers/{id}
func (s *Server) handleGetTransfer(w http.ResponseWriter, r *http.Request, tranID string) {
	if s.deps.Store == nil {
		s.writeError(w, http.StatusServiceUnavailable, "store not available")
		return
	}

	record, err := s.deps.Store.Get(tranID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "transfer not found")
			return
		}
		s.logger.Error("failed to get transfer", "tran_id", tranID, "error", err)
		s.writeError(w, http.StatusInternalServerError, "failed to retrieve transfer")
		return
	}

	s.writeJSON(w, http.StatusOK, record)
}

// RetryResponse is the response for the retry endpoint.
type RetryResponse struct {
	TranID     string `json:"tran_id"`
	Status     string `json:"status"`
	WithdrawID string `json:"withdraw_id,omitempty"`
	Message    string `json:"message,omitempty"`
	Error      string `json:"error,omitempty"`
}

// handleRetryTransfer initiates a retry for a failed transfer.
// POST /transfers/{id}/retry
func (s *Server) handleRetryTransfer(w http.ResponseWriter, r *http.Request, tranID string) {
	if s.deps.Store == nil || s.deps.Forwarder == nil {
		s.writeError(w, http.StatusServiceUnavailable, "service not available")
		return
	}

	// Get the transfer record
	record, err := s.deps.Store.Get(tranID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "transfer not found")
			return
		}
		s.logger.Error("failed to get transfer for retry", "tran_id", tranID, "error", err)
		s.writeError(w, http.StatusInternalServerError, "failed to retrieve transfer")
		return
	}

	// Check if transfer can be retried
	status := record.GetStatus()
	if status == model.StatusCompleted {
		s.writeJSON(w, http.StatusOK, RetryResponse{
			TranID:  tranID,
			Status:  "skipped",
			Message: "transfer already completed",
		})
		return
	}
	if status == model.StatusForwarded {
		s.writeJSON(w, http.StatusOK, RetryResponse{
			TranID:     tranID,
			Status:     "skipped",
			WithdrawID: record.WithdrawID,
			Message:    "transfer already forwarded, awaiting confirmation",
		})
		return
	}

	// Attempt the retry
	s.logger.Info("initiating manual retry", "tran_id", tranID)
	result := s.deps.Forwarder.Forward(r.Context(), record)

	// Build response
	resp := RetryResponse{
		TranID:     tranID,
		Status:     result.Status,
		WithdrawID: result.WithdrawID,
	}
	if result.Error != nil {
		resp.Error = result.Error.Error()
	}

	statusCode := http.StatusOK
	if resp.Error != "" {
		statusCode = http.StatusUnprocessableEntity
	}

	s.writeJSON(w, statusCode, resp)
}
