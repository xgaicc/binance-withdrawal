// Package api provides HTTP endpoints for monitoring and manual intervention.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/binance-withdrawal/internal/model"
)

// Server provides HTTP endpoints for monitoring and manual intervention.
type Server struct {
	httpServer *http.Server
	mux        *http.ServeMux
	logger     *slog.Logger
	startTime  time.Time

	// Dependencies
	deps *Dependencies
}

// Dependencies holds the service dependencies needed by the API handlers.
type Dependencies struct {
	Store     TransferStore
	Forwarder TransferForwarder
	Detector  ConnectionStatus
}

// TransferStore provides access to transfer records.
type TransferStore interface {
	Get(tranID string) (*model.TransferRecord, error)
	GetByStatus(status string) ([]*model.TransferRecord, error)
	ListByTime(limit int) ([]*model.TransferRecord, error)
	CountByStatus() (map[string]int, error)
}

// ForwardResult represents the result of a forward operation.
type ForwardResult struct {
	TranID     string
	Status     string
	WithdrawID string
	Error      error
}

// TransferForwarder provides the ability to retry transfers.
type TransferForwarder interface {
	Forward(ctx context.Context, transfer *model.TransferRecord) *ForwardResult
}

// ConnectionStatus provides WebSocket connection state.
type ConnectionStatus interface {
	IsConnected() bool
	GetLastProcessedTime() time.Time
}

// ServerOption configures the Server.
type ServerOption func(*Server)

// WithLogger sets a custom logger.
func WithLogger(l *slog.Logger) ServerOption {
	return func(s *Server) {
		s.logger = l
	}
}

// New creates a new API server.
func New(addr string, deps *Dependencies, opts ...ServerOption) *Server {
	mux := http.NewServeMux()

	s := &Server{
		mux:       mux,
		logger:    slog.Default(),
		startTime: time.Now(),
		deps:      deps,
	}

	for _, opt := range opts {
		opt(s)
	}

	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      s.withMiddleware(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	s.registerRoutes()

	return s
}

// Start begins serving HTTP requests.
// Returns immediately; use Shutdown to stop.
func (s *Server) Start() error {
	s.logger.Info("starting HTTP server", "addr", s.httpServer.Addr)
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("HTTP server error", "error", err)
		}
	}()
	return nil
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down HTTP server")
	return s.httpServer.Shutdown(ctx)
}

// registerRoutes sets up the HTTP routes.
func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/status", s.handleStatus)
	s.mux.HandleFunc("/transfers", s.handleTransfers)
	s.mux.HandleFunc("/transfers/", s.handleTransferByID)
}

// withMiddleware wraps the handler with common middleware.
func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Set common headers
		w.Header().Set("Content-Type", "application/json")

		// Log the request
		defer func() {
			s.logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"duration", time.Since(start),
			)
		}()

		next.ServeHTTP(w, r)
	})
}

// writeJSON writes a JSON response.
func (s *Server) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		s.logger.Error("failed to encode JSON response", "error", err)
	}
}

// writeError writes an error response.
func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, map[string]string{"error": message})
}
