// Package retry provides utilities for retrying operations with exponential backoff.
package retry

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/binance-withdrawal/internal/binance"
)

// Default retry configuration values.
const (
	DefaultMaxAttempts    = 5
	DefaultInitialDelay   = 1 * time.Second
	DefaultMaxDelay       = 30 * time.Second
	DefaultBackoffFactor  = 2.0
	DefaultJitterFraction = 0.1 // 10% jitter
)

// Config holds retry configuration.
type Config struct {
	MaxAttempts    int           // Maximum number of attempts (including initial)
	InitialDelay   time.Duration // Initial delay before first retry
	MaxDelay       time.Duration // Maximum delay cap
	BackoffFactor  float64       // Multiplier for exponential backoff
	JitterFraction float64       // Fraction of delay to add as random jitter (0.0-1.0)
}

// DefaultConfig returns the default retry configuration.
func DefaultConfig() *Config {
	return &Config{
		MaxAttempts:    DefaultMaxAttempts,
		InitialDelay:   DefaultInitialDelay,
		MaxDelay:       DefaultMaxDelay,
		BackoffFactor:  DefaultBackoffFactor,
		JitterFraction: DefaultJitterFraction,
	}
}

// Result contains the outcome of a retry operation.
type Result struct {
	Attempts int   // Total number of attempts made
	Err      error // Final error (nil if successful)
}

// IsTransient determines if an error is transient and should be retried.
// Transient errors include:
// - Network timeouts
// - Rate limits (HTTP 429)
// - Service unavailable (HTTP 503)
// - Temporary network errors
func IsTransient(err error) bool {
	if err == nil {
		return false
	}

	// Check for Binance API errors with specific codes
	var apiErr *binance.APIError
	if errors.As(err, &apiErr) {
		return isTransientAPIError(apiErr)
	}

	// Check for network errors
	if isNetworkError(err) {
		return true
	}

	// Check error message for HTTP status codes
	errMsg := err.Error()
	if strings.Contains(errMsg, "status 429") ||
		strings.Contains(errMsg, "status 503") ||
		strings.Contains(errMsg, "status 502") ||
		strings.Contains(errMsg, "status 504") {
		return true
	}

	return false
}

// isTransientAPIError checks if a Binance API error is transient.
// Reference: https://binance-docs.github.io/apidocs/spot/en/#error-codes
func isTransientAPIError(err *binance.APIError) bool {
	switch err.Code {
	case -1000: // UNKNOWN - An unknown error occurred
		return true
	case -1001: // DISCONNECTED - Internal error; please try again
		return true
	case -1003: // TOO_MANY_REQUESTS - Too many requests; please slow down
		return true
	case -1015: // TOO_MANY_ORDERS - Too many orders; please slow down
		return true
	case -1016: // SERVICE_SHUTTING_DOWN - This service is no longer available
		return false // Not transient - service is going away
	}
	return false
}

// isNetworkError checks if the error is a network-related error.
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}

	// Check for timeout
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}

	// Check for connection refused, reset, etc.
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	// Check for DNS errors
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.Temporary()
	}

	// Check error message for common network error patterns
	errMsg := strings.ToLower(err.Error())
	networkPatterns := []string{
		"timeout",
		"connection refused",
		"connection reset",
		"no such host",
		"network is unreachable",
		"i/o timeout",
		"tls handshake timeout",
		"eof",
		"broken pipe",
	}

	for _, pattern := range networkPatterns {
		if strings.Contains(errMsg, pattern) {
			return true
		}
	}

	return false
}

// CalculateDelay computes the delay for a given attempt using exponential backoff with jitter.
func (c *Config) CalculateDelay(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}

	// Calculate exponential delay: initialDelay * backoffFactor^(attempt-1)
	delay := float64(c.InitialDelay) * math.Pow(c.BackoffFactor, float64(attempt-1))

	// Cap at max delay
	if delay > float64(c.MaxDelay) {
		delay = float64(c.MaxDelay)
	}

	// Add jitter to prevent thundering herd
	if c.JitterFraction > 0 {
		jitter := delay * c.JitterFraction * rand.Float64()
		delay += jitter
	}

	return time.Duration(delay)
}

// Do executes the operation with retry logic.
// The operation function should return an error if it fails.
// Returns the result of the final attempt.
func Do(ctx context.Context, cfg *Config, operation func() error) *Result {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	result := &Result{}

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		result.Attempts = attempt

		// Execute the operation
		err := operation()
		if err == nil {
			// Success
			result.Err = nil
			return result
		}

		result.Err = err

		// Check if this is the last attempt
		if attempt >= cfg.MaxAttempts {
			break
		}

		// Check if error is transient
		if !IsTransient(err) {
			// Permanent error - don't retry
			break
		}

		// Calculate delay and wait
		delay := cfg.CalculateDelay(attempt)

		select {
		case <-ctx.Done():
			result.Err = ctx.Err()
			return result
		case <-time.After(delay):
			// Continue to next attempt
		}
	}

	return result
}

// DoWithResult executes an operation that returns a value along with an error.
// Returns the value from the successful attempt, or nil and the final error.
func DoWithResult[T any](ctx context.Context, cfg *Config, operation func() (T, error)) (T, *Result) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	var result T
	res := &Result{}

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		res.Attempts = attempt

		// Execute the operation
		val, err := operation()
		if err == nil {
			// Success
			res.Err = nil
			return val, res
		}

		result = val // Keep last result even on error
		res.Err = err

		// Check if this is the last attempt
		if attempt >= cfg.MaxAttempts {
			break
		}

		// Check if error is transient
		if !IsTransient(err) {
			// Permanent error - don't retry
			break
		}

		// Calculate delay and wait
		delay := cfg.CalculateDelay(attempt)

		select {
		case <-ctx.Done():
			res.Err = ctx.Err()
			return result, res
		case <-time.After(delay):
			// Continue to next attempt
		}
	}

	return result, res
}

// HTTPStatusFromError extracts HTTP status code from an error if possible.
// Returns 0 if no status code can be determined.
func HTTPStatusFromError(err error) int {
	if err == nil {
		return 0
	}

	errMsg := err.Error()

	// Common status patterns in error messages
	statusPatterns := map[string]int{
		"status 429": http.StatusTooManyRequests,
		"status 500": http.StatusInternalServerError,
		"status 502": http.StatusBadGateway,
		"status 503": http.StatusServiceUnavailable,
		"status 504": http.StatusGatewayTimeout,
	}

	for pattern, code := range statusPatterns {
		if strings.Contains(errMsg, pattern) {
			return code
		}
	}

	return 0
}
