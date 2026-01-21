package retry

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/binance-withdrawal/internal/binance"
)

func TestIsTransient_BinanceAPIErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "unknown error -1000",
			err:      &binance.APIError{Code: -1000, Message: "unknown error"},
			expected: true,
		},
		{
			name:     "disconnected -1001",
			err:      &binance.APIError{Code: -1001, Message: "internal error"},
			expected: true,
		},
		{
			name:     "too many requests -1003",
			err:      &binance.APIError{Code: -1003, Message: "too many requests"},
			expected: true,
		},
		{
			name:     "too many orders -1015",
			err:      &binance.APIError{Code: -1015, Message: "too many orders"},
			expected: true,
		},
		{
			name:     "service shutting down -1016",
			err:      &binance.APIError{Code: -1016, Message: "service shutting down"},
			expected: false, // Not transient - service is going away
		},
		{
			name:     "invalid parameter -1102",
			err:      &binance.APIError{Code: -1102, Message: "invalid parameter"},
			expected: false, // Permanent error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTransient(tt.err); got != tt.expected {
				t.Errorf("IsTransient() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsTransient_HTTPStatusCodes(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "429 rate limit",
			err:      errors.New("API error: status 429, body: rate limited"),
			expected: true,
		},
		{
			name:     "503 service unavailable",
			err:      errors.New("API error: status 503, body: service unavailable"),
			expected: true,
		},
		{
			name:     "502 bad gateway",
			err:      errors.New("API error: status 502, body: bad gateway"),
			expected: true,
		},
		{
			name:     "504 gateway timeout",
			err:      errors.New("API error: status 504, body: timeout"),
			expected: true,
		},
		{
			name:     "400 bad request",
			err:      errors.New("API error: status 400, body: bad request"),
			expected: false,
		},
		{
			name:     "401 unauthorized",
			err:      errors.New("API error: status 401, body: unauthorized"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTransient(tt.err); got != tt.expected {
				t.Errorf("IsTransient() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsTransient_NetworkErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "connection timeout message",
			err:      errors.New("dial tcp: i/o timeout"),
			expected: true,
		},
		{
			name:     "connection refused",
			err:      errors.New("dial tcp 127.0.0.1:8080: connection refused"),
			expected: true,
		},
		{
			name:     "connection reset",
			err:      errors.New("read tcp: connection reset by peer"),
			expected: true,
		},
		{
			name:     "eof error",
			err:      errors.New("unexpected EOF"),
			expected: true,
		},
		{
			name:     "broken pipe",
			err:      errors.New("write: broken pipe"),
			expected: true,
		},
		{
			name:     "network unreachable",
			err:      errors.New("network is unreachable"),
			expected: true,
		},
		{
			name:     "tls handshake timeout",
			err:      errors.New("TLS handshake timeout"),
			expected: true,
		},
		{
			name:     "generic error",
			err:      errors.New("something went wrong"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTransient(tt.err); got != tt.expected {
				t.Errorf("IsTransient() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// timeoutError implements net.Error for testing
type timeoutError struct {
	timeout bool
}

func (e *timeoutError) Error() string   { return "timeout error" }
func (e *timeoutError) Timeout() bool   { return e.timeout }
func (e *timeoutError) Temporary() bool { return false }

func TestIsTransient_NetError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "timeout net.Error",
			err:      &timeoutError{timeout: true},
			expected: true,
		},
		{
			name:     "non-timeout net.Error",
			err:      &timeoutError{timeout: false},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify our test type implements net.Error
			var _ net.Error = tt.err.(net.Error)

			if got := IsTransient(tt.err); got != tt.expected {
				t.Errorf("IsTransient() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCalculateDelay(t *testing.T) {
	cfg := &Config{
		InitialDelay:   1 * time.Second,
		MaxDelay:       30 * time.Second,
		BackoffFactor:  2.0,
		JitterFraction: 0, // Disable jitter for predictable tests
	}

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{attempt: 0, expected: 0},
		{attempt: 1, expected: 1 * time.Second},
		{attempt: 2, expected: 2 * time.Second},
		{attempt: 3, expected: 4 * time.Second},
		{attempt: 4, expected: 8 * time.Second},
		{attempt: 5, expected: 16 * time.Second},
		{attempt: 6, expected: 30 * time.Second}, // Capped at MaxDelay
		{attempt: 10, expected: 30 * time.Second},
	}

	for _, tt := range tests {
		delay := cfg.CalculateDelay(tt.attempt)
		if delay != tt.expected {
			t.Errorf("CalculateDelay(%d) = %v, want %v", tt.attempt, delay, tt.expected)
		}
	}
}

func TestCalculateDelay_WithJitter(t *testing.T) {
	cfg := &Config{
		InitialDelay:   1 * time.Second,
		MaxDelay:       30 * time.Second,
		BackoffFactor:  2.0,
		JitterFraction: 0.1, // 10% jitter
	}

	// Run multiple times to verify jitter adds randomness
	delay1 := cfg.CalculateDelay(2)
	delay2 := cfg.CalculateDelay(2)
	delay3 := cfg.CalculateDelay(2)

	// Base delay is 2 seconds, with 10% jitter should be 2.0-2.2 seconds
	minExpected := 2 * time.Second
	maxExpected := 2200 * time.Millisecond

	for _, delay := range []time.Duration{delay1, delay2, delay3} {
		if delay < minExpected || delay > maxExpected {
			t.Errorf("CalculateDelay(2) with jitter = %v, expected between %v and %v",
				delay, minExpected, maxExpected)
		}
	}
}

func TestDo_Success(t *testing.T) {
	cfg := &Config{
		MaxAttempts:    5,
		InitialDelay:   10 * time.Millisecond,
		MaxDelay:       100 * time.Millisecond,
		BackoffFactor:  2.0,
		JitterFraction: 0,
	}

	callCount := 0
	result := Do(context.Background(), cfg, func() error {
		callCount++
		return nil // Success on first try
	})

	if result.Err != nil {
		t.Errorf("unexpected error: %v", result.Err)
	}
	if result.Attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", result.Attempts)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}
}

func TestDo_RetryTransientThenSuccess(t *testing.T) {
	cfg := &Config{
		MaxAttempts:    5,
		InitialDelay:   10 * time.Millisecond,
		MaxDelay:       100 * time.Millisecond,
		BackoffFactor:  2.0,
		JitterFraction: 0,
	}

	callCount := 0
	result := Do(context.Background(), cfg, func() error {
		callCount++
		if callCount < 3 {
			return errors.New("status 429 rate limited") // Transient
		}
		return nil // Success on third try
	})

	if result.Err != nil {
		t.Errorf("unexpected error: %v", result.Err)
	}
	if result.Attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", result.Attempts)
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls, got %d", callCount)
	}
}

func TestDo_PermanentErrorNoRetry(t *testing.T) {
	cfg := &Config{
		MaxAttempts:    5,
		InitialDelay:   10 * time.Millisecond,
		MaxDelay:       100 * time.Millisecond,
		BackoffFactor:  2.0,
		JitterFraction: 0,
	}

	callCount := 0
	permanentErr := errors.New("invalid parameter") // Not transient
	result := Do(context.Background(), cfg, func() error {
		callCount++
		return permanentErr
	})

	if result.Err != permanentErr {
		t.Errorf("expected permanent error, got: %v", result.Err)
	}
	if result.Attempts != 1 {
		t.Errorf("expected 1 attempt for permanent error, got %d", result.Attempts)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call for permanent error, got %d", callCount)
	}
}

func TestDo_MaxAttemptsExhausted(t *testing.T) {
	cfg := &Config{
		MaxAttempts:    3,
		InitialDelay:   10 * time.Millisecond,
		MaxDelay:       100 * time.Millisecond,
		BackoffFactor:  2.0,
		JitterFraction: 0,
	}

	callCount := 0
	transientErr := errors.New("status 503 service unavailable")
	result := Do(context.Background(), cfg, func() error {
		callCount++
		return transientErr // Always fail
	})

	if result.Err == nil {
		t.Error("expected error after exhausting retries")
	}
	if result.Attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", result.Attempts)
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls, got %d", callCount)
	}
}

func TestDo_ContextCancelled(t *testing.T) {
	cfg := &Config{
		MaxAttempts:    5,
		InitialDelay:   100 * time.Millisecond, // Long enough to cancel
		MaxDelay:       1 * time.Second,
		BackoffFactor:  2.0,
		JitterFraction: 0,
	}

	ctx, cancel := context.WithCancel(context.Background())

	callCount := 0
	go func() {
		time.Sleep(50 * time.Millisecond) // Cancel before first retry delay completes
		cancel()
	}()

	result := Do(ctx, cfg, func() error {
		callCount++
		return errors.New("status 429 rate limited")
	})

	if !errors.Is(result.Err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", result.Err)
	}
	if callCount < 1 {
		t.Errorf("expected at least 1 call, got %d", callCount)
	}
}

func TestDoWithResult_Success(t *testing.T) {
	cfg := &Config{
		MaxAttempts:    5,
		InitialDelay:   10 * time.Millisecond,
		MaxDelay:       100 * time.Millisecond,
		BackoffFactor:  2.0,
		JitterFraction: 0,
	}

	value, result := DoWithResult(context.Background(), cfg, func() (string, error) {
		return "success", nil
	})

	if result.Err != nil {
		t.Errorf("unexpected error: %v", result.Err)
	}
	if value != "success" {
		t.Errorf("expected value 'success', got %s", value)
	}
	if result.Attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", result.Attempts)
	}
}

func TestDoWithResult_RetryThenSuccess(t *testing.T) {
	cfg := &Config{
		MaxAttempts:    5,
		InitialDelay:   10 * time.Millisecond,
		MaxDelay:       100 * time.Millisecond,
		BackoffFactor:  2.0,
		JitterFraction: 0,
	}

	callCount := 0
	value, result := DoWithResult(context.Background(), cfg, func() (int, error) {
		callCount++
		if callCount < 3 {
			return 0, errors.New("status 503 unavailable")
		}
		return 42, nil
	})

	if result.Err != nil {
		t.Errorf("unexpected error: %v", result.Err)
	}
	if value != 42 {
		t.Errorf("expected value 42, got %d", value)
	}
	if result.Attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", result.Attempts)
	}
}

func TestIsPermanent_BinanceAPIErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "unauthorized -1002",
			err:      &binance.APIError{Code: -1002, Message: "unauthorized"},
			expected: true,
		},
		{
			name:     "min notional -1013",
			err:      &binance.APIError{Code: -1013, Message: "amount below minimum"},
			expected: true,
		},
		{
			name:     "service shutting down -1016",
			err:      &binance.APIError{Code: -1016, Message: "service shutting down"},
			expected: true,
		},
		{
			name:     "invalid timestamp -1021 (not permanent)",
			err:      &binance.APIError{Code: -1021, Message: "timestamp out of window"},
			expected: false,
		},
		{
			name:     "illegal chars -1100",
			err:      &binance.APIError{Code: -1100, Message: "illegal characters"},
			expected: true,
		},
		{
			name:     "mandatory param missing -1102",
			err:      &binance.APIError{Code: -1102, Message: "mandatory parameter missing"},
			expected: true,
		},
		{
			name:     "bad precision -1111",
			err:      &binance.APIError{Code: -1111, Message: "precision over maximum"},
			expected: true,
		},
		{
			name:     "bad symbol -1121",
			err:      &binance.APIError{Code: -1121, Message: "invalid symbol"},
			expected: true,
		},
		// Transient errors should NOT be permanent
		{
			name:     "unknown error -1000 (transient)",
			err:      &binance.APIError{Code: -1000, Message: "unknown error"},
			expected: false,
		},
		{
			name:     "disconnected -1001 (transient)",
			err:      &binance.APIError{Code: -1001, Message: "internal error"},
			expected: false,
		},
		{
			name:     "too many requests -1003 (transient)",
			err:      &binance.APIError{Code: -1003, Message: "too many requests"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPermanent(tt.err); got != tt.expected {
				t.Errorf("IsPermanent() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsPermanent_WithdrawalErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "address not whitelisted -4003",
			err:      &binance.APIError{Code: -4003, Message: "address not whitelisted"},
			expected: true,
		},
		{
			name:     "invalid address -4004",
			err:      &binance.APIError{Code: -4004, Message: "invalid address"},
			expected: true,
		},
		{
			name:     "negative amount -4005",
			err:      &binance.APIError{Code: -4005, Message: "withdrawal amount must be positive"},
			expected: true,
		},
		{
			name:     "insufficient balance -4006 (not permanent)",
			err:      &binance.APIError{Code: -4006, Message: "insufficient balance"},
			expected: false, // Could be temporary - funds might arrive
		},
		{
			name:     "network not supported -4015",
			err:      &binance.APIError{Code: -4015, Message: "network not supported for withdrawal"},
			expected: true,
		},
		{
			name:     "coin suspended -4019 (not permanent)",
			err:      &binance.APIError{Code: -4019, Message: "coin withdrawal suspended"},
			expected: false, // Could be temporary maintenance
		},
		{
			name:     "duplicate withdrawal -4026",
			err:      &binance.APIError{Code: -4026, Message: "duplicate withdrawal order id"},
			expected: true,
		},
		{
			name:     "below min amount -4029",
			err:      &binance.APIError{Code: -4029, Message: "below minimum withdrawal amount"},
			expected: true,
		},
		{
			name:     "exceeds max amount -4030",
			err:      &binance.APIError{Code: -4030, Message: "exceeds maximum withdrawal amount"},
			expected: true,
		},
		{
			name:     "address verification failed -4057",
			err:      &binance.APIError{Code: -4057, Message: "address verification failed"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPermanent(tt.err); got != tt.expected {
				t.Errorf("IsPermanent() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsPermanent_NonAPIErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "generic error",
			err:      errors.New("some random error"),
			expected: false,
		},
		{
			name:     "network timeout",
			err:      errors.New("dial tcp: i/o timeout"),
			expected: false,
		},
		{
			name:     "http 400 error",
			err:      errors.New("API error: status 400, body: bad request"),
			expected: false, // We don't classify HTTP errors as permanent without API code
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPermanent(tt.err); got != tt.expected {
				t.Errorf("IsPermanent() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.MaxAttempts != DefaultMaxAttempts {
		t.Errorf("MaxAttempts = %d, want %d", cfg.MaxAttempts, DefaultMaxAttempts)
	}
	if cfg.InitialDelay != DefaultInitialDelay {
		t.Errorf("InitialDelay = %v, want %v", cfg.InitialDelay, DefaultInitialDelay)
	}
	if cfg.MaxDelay != DefaultMaxDelay {
		t.Errorf("MaxDelay = %v, want %v", cfg.MaxDelay, DefaultMaxDelay)
	}
	if cfg.BackoffFactor != DefaultBackoffFactor {
		t.Errorf("BackoffFactor = %v, want %v", cfg.BackoffFactor, DefaultBackoffFactor)
	}
	if cfg.JitterFraction != DefaultJitterFraction {
		t.Errorf("JitterFraction = %v, want %v", cfg.JitterFraction, DefaultJitterFraction)
	}
}

func TestHTTPStatusFromError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected int
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: 0,
		},
		{
			name:     "429 rate limit",
			err:      errors.New("API error: status 429, body: rate limited"),
			expected: 429,
		},
		{
			name:     "503 service unavailable",
			err:      errors.New("API error: status 503"),
			expected: 503,
		},
		{
			name:     "502 bad gateway",
			err:      errors.New("status 502 bad gateway"),
			expected: 502,
		},
		{
			name:     "504 gateway timeout",
			err:      errors.New("status 504 timeout"),
			expected: 504,
		},
		{
			name:     "500 internal server error",
			err:      errors.New("status 500 server error"),
			expected: 500,
		},
		{
			name:     "unknown status",
			err:      errors.New("some other error"),
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HTTPStatusFromError(tt.err); got != tt.expected {
				t.Errorf("HTTPStatusFromError() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestDo_NilConfig(t *testing.T) {
	callCount := 0
	result := Do(context.Background(), nil, func() error {
		callCount++
		return nil
	})

	if result.Err != nil {
		t.Errorf("unexpected error: %v", result.Err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call with nil config, got %d", callCount)
	}
}
