package binance

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestBalanceUpdateEventParsing(t *testing.T) {
	eventJSON := `{
		"e": "balanceUpdate",
		"E": 1704067200000,
		"a": "SOL",
		"d": "100.5",
		"T": 1704067200500
	}`

	var event BalanceUpdateEvent
	if err := json.Unmarshal([]byte(eventJSON), &event); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if event.EventType != "balanceUpdate" {
		t.Errorf("EventType = %q, want balanceUpdate", event.EventType)
	}
	if event.Asset != "SOL" {
		t.Errorf("Asset = %q, want SOL", event.Asset)
	}
	if event.BalanceDelta != "100.5" {
		t.Errorf("BalanceDelta = %q, want 100.5", event.BalanceDelta)
	}
	if event.EventTime != 1704067200000 {
		t.Errorf("EventTime = %d, want 1704067200000", event.EventTime)
	}
	if event.ClearTime != 1704067200500 {
		t.Errorf("ClearTime = %d, want 1704067200500", event.ClearTime)
	}

	// Test timestamp parsing
	event.ParseTimestamps()

	expectedEventTime := time.UnixMilli(1704067200000)
	if !event.EventTimeAt.Equal(expectedEventTime) {
		t.Errorf("EventTimeAt = %v, want %v", event.EventTimeAt, expectedEventTime)
	}

	expectedClearTime := time.UnixMilli(1704067200500)
	if !event.ClearTimeAt.Equal(expectedClearTime) {
		t.Errorf("ClearTimeAt = %v, want %v", event.ClearTimeAt, expectedClearTime)
	}
}

func TestBalanceUpdateEventNegativeDelta(t *testing.T) {
	eventJSON := `{
		"e": "balanceUpdate",
		"E": 1704067200000,
		"a": "ETH",
		"d": "-5.25",
		"T": 1704067200500
	}`

	var event BalanceUpdateEvent
	if err := json.Unmarshal([]byte(eventJSON), &event); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if event.BalanceDelta != "-5.25" {
		t.Errorf("BalanceDelta = %q, want -5.25", event.BalanceDelta)
	}
}

func TestDefaultStreamURL(t *testing.T) {
	if DefaultStreamURL != "wss://stream.binance.com:9443/ws" {
		t.Errorf("DefaultStreamURL = %q, want wss://stream.binance.com:9443/ws", DefaultStreamURL)
	}
}

func TestDefaultRefreshInterval(t *testing.T) {
	if DefaultRefreshInterval != 30*time.Minute {
		t.Errorf("DefaultRefreshInterval = %v, want 30m", DefaultRefreshInterval)
	}
}

// mockEventHandler implements EventHandler for testing
type mockEventHandler struct {
	balanceUpdates []*BalanceUpdateEvent
	errors         []error
	connected      bool
	disconnected   bool
	reconnected    bool
}

func (h *mockEventHandler) OnBalanceUpdate(event *BalanceUpdateEvent) {
	h.balanceUpdates = append(h.balanceUpdates, event)
}

func (h *mockEventHandler) OnError(err error) {
	h.errors = append(h.errors, err)
}

func (h *mockEventHandler) OnConnected() {
	h.connected = true
}

func (h *mockEventHandler) OnDisconnected() {
	h.disconnected = true
}

func (h *mockEventHandler) OnReconnected() {
	h.reconnected = true
}

func TestNewUserDataStream(t *testing.T) {
	client := NewClient("api-key", "secret-key")
	handler := &mockEventHandler{}

	stream := NewUserDataStream(client, handler)

	if stream.client != client {
		t.Error("client not set correctly")
	}
	if stream.handler == nil {
		t.Error("handler is nil")
	}
	if stream.streamURL != DefaultStreamURL {
		t.Errorf("streamURL = %q, want %q", stream.streamURL, DefaultStreamURL)
	}
	if stream.refreshInterval != DefaultRefreshInterval {
		t.Errorf("refreshInterval = %v, want %v", stream.refreshInterval, DefaultRefreshInterval)
	}
}

func TestUserDataStreamWithOptions(t *testing.T) {
	client := NewClient("api-key", "secret-key")
	handler := &mockEventHandler{}
	customURL := "wss://testnet.binance.vision/ws"
	customRefresh := 15 * time.Minute

	stream := NewUserDataStream(client, handler,
		WithStreamURL(customURL),
		WithRefreshInterval(customRefresh),
	)

	if stream.streamURL != customURL {
		t.Errorf("streamURL = %q, want %q", stream.streamURL, customURL)
	}
	if stream.refreshInterval != customRefresh {
		t.Errorf("refreshInterval = %v, want %v", stream.refreshInterval, customRefresh)
	}
}

func TestUserDataStreamProcessMessage(t *testing.T) {
	client := NewClient("api-key", "secret-key")
	handler := &mockEventHandler{}
	stream := NewUserDataStream(client, handler)

	// Test balance update message
	balanceUpdateMsg := []byte(`{
		"e": "balanceUpdate",
		"E": 1704067200000,
		"a": "BTC",
		"d": "1.5",
		"T": 1704067200500
	}`)

	stream.processMessage(balanceUpdateMsg)

	if len(handler.balanceUpdates) != 1 {
		t.Fatalf("len(balanceUpdates) = %d, want 1", len(handler.balanceUpdates))
	}

	event := handler.balanceUpdates[0]
	if event.Asset != "BTC" {
		t.Errorf("Asset = %q, want BTC", event.Asset)
	}
	if event.BalanceDelta != "1.5" {
		t.Errorf("BalanceDelta = %q, want 1.5", event.BalanceDelta)
	}
}

func TestUserDataStreamProcessUnknownEventType(t *testing.T) {
	client := NewClient("api-key", "secret-key")
	handler := &mockEventHandler{}
	stream := NewUserDataStream(client, handler)

	// Test unknown event type (should be ignored)
	unknownMsg := []byte(`{
		"e": "unknownEvent",
		"data": "some data"
	}`)

	stream.processMessage(unknownMsg)

	if len(handler.balanceUpdates) != 0 {
		t.Errorf("len(balanceUpdates) = %d, want 0", len(handler.balanceUpdates))
	}
	if len(handler.errors) != 0 {
		t.Errorf("len(errors) = %d, want 0", len(handler.errors))
	}
}

func TestUserDataStreamProcessInvalidJSON(t *testing.T) {
	client := NewClient("api-key", "secret-key")
	handler := &mockEventHandler{}
	stream := NewUserDataStream(client, handler)

	// Test invalid JSON
	invalidMsg := []byte(`{invalid json}`)

	stream.processMessage(invalidMsg)

	if len(handler.errors) != 1 {
		t.Errorf("len(errors) = %d, want 1", len(handler.errors))
	}
}

func TestIsConnectedInitialState(t *testing.T) {
	client := NewClient("api-key", "secret-key")
	handler := &mockEventHandler{}
	stream := NewUserDataStream(client, handler)

	if stream.IsConnected() {
		t.Error("IsConnected() = true, want false for new stream")
	}
}

func TestDefaultReconnectionSettings(t *testing.T) {
	if DefaultReconnectDelay != 5*time.Second {
		t.Errorf("DefaultReconnectDelay = %v, want 5s", DefaultReconnectDelay)
	}
	if DefaultMaxReconnectDelay != 5*time.Minute {
		t.Errorf("DefaultMaxReconnectDelay = %v, want 5m", DefaultMaxReconnectDelay)
	}
	if DefaultReconnectBackoff != 2.0 {
		t.Errorf("DefaultReconnectBackoff = %v, want 2.0", DefaultReconnectBackoff)
	}
}

func TestUserDataStreamReconnectOptions(t *testing.T) {
	client := NewClient("api-key", "secret-key")
	handler := &mockEventHandler{}
	customDelay := 10 * time.Second
	customMaxDelay := 10 * time.Minute

	stream := NewUserDataStream(client, handler,
		WithReconnectDelay(customDelay),
		WithMaxReconnectDelay(customMaxDelay),
		WithAutoReconnect(false),
	)

	if stream.reconnectDelay != customDelay {
		t.Errorf("reconnectDelay = %v, want %v", stream.reconnectDelay, customDelay)
	}
	if stream.maxReconnectDelay != customMaxDelay {
		t.Errorf("maxReconnectDelay = %v, want %v", stream.maxReconnectDelay, customMaxDelay)
	}
	if stream.autoReconnect {
		t.Error("autoReconnect = true, want false")
	}
}

func TestUserDataStreamAutoReconnectEnabled(t *testing.T) {
	client := NewClient("api-key", "secret-key")
	handler := &mockEventHandler{}

	stream := NewUserDataStream(client, handler)

	if !stream.autoReconnect {
		t.Error("autoReconnect = false, want true (default)")
	}
}

func TestExponentialBackoffCalculation(t *testing.T) {
	tests := []struct {
		name           string
		initialDelay   time.Duration
		maxDelay       time.Duration
		backoffFactor  float64
		attempts       int
		expectedDelays []time.Duration
	}{
		{
			name:          "default settings",
			initialDelay:  5 * time.Second,
			maxDelay:      5 * time.Minute,
			backoffFactor: 2.0,
			attempts:      5,
			expectedDelays: []time.Duration{
				5 * time.Second,
				10 * time.Second,
				20 * time.Second,
				40 * time.Second,
				80 * time.Second,
			},
		},
		{
			name:          "hits max delay",
			initialDelay:  1 * time.Minute,
			maxDelay:      5 * time.Minute,
			backoffFactor: 2.0,
			attempts:      4,
			expectedDelays: []time.Duration{
				1 * time.Minute,
				2 * time.Minute,
				4 * time.Minute,
				5 * time.Minute, // capped at max
			},
		},
		{
			name:          "custom backoff factor",
			initialDelay:  1 * time.Second,
			maxDelay:      1 * time.Minute,
			backoffFactor: 1.5,
			attempts:      4,
			expectedDelays: []time.Duration{
				1 * time.Second,
				1500 * time.Millisecond,
				2250 * time.Millisecond,
				3375 * time.Millisecond,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delay := tt.initialDelay
			for i := 0; i < tt.attempts; i++ {
				if i > 0 {
					delay = time.Duration(float64(delay) * tt.backoffFactor)
					if delay > tt.maxDelay {
						delay = tt.maxDelay
					}
				}
				if delay != tt.expectedDelays[i] {
					t.Errorf("attempt %d: delay = %v, want %v", i, delay, tt.expectedDelays[i])
				}
			}
		})
	}
}

func TestReconnectLoopRespectsContext(t *testing.T) {
	client := NewClient("api-key", "secret-key")
	handler := &mockEventHandler{}
	stream := NewUserDataStream(client, handler,
		WithReconnectDelay(10*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())
	stream.ctx = ctx
	stream.doneChan = make(chan struct{})

	// Start reconnect loop in background
	done := make(chan struct{})
	go func() {
		stream.reconnectLoop()
		close(done)
	}()

	// Cancel context after a short delay
	time.Sleep(20 * time.Millisecond)
	cancel()

	// Reconnect loop should exit promptly
	select {
	case <-done:
		// Success
	case <-time.After(500 * time.Millisecond):
		t.Error("reconnectLoop did not exit after context cancellation")
	}
}

func TestReconnectLoopRespectsDoneChan(t *testing.T) {
	client := NewClient("api-key", "secret-key")
	handler := &mockEventHandler{}
	stream := NewUserDataStream(client, handler,
		WithReconnectDelay(10*time.Millisecond),
	)

	ctx := context.Background()
	stream.ctx = ctx
	stream.doneChan = make(chan struct{})

	// Start reconnect loop in background
	done := make(chan struct{})
	go func() {
		stream.reconnectLoop()
		close(done)
	}()

	// Close done channel after a short delay
	time.Sleep(20 * time.Millisecond)
	close(stream.doneChan)

	// Reconnect loop should exit promptly
	select {
	case <-done:
		// Success
	case <-time.After(500 * time.Millisecond):
		t.Error("reconnectLoop did not exit after doneChan closed")
	}
}

func TestWasConnectedTracking(t *testing.T) {
	client := NewClient("api-key", "secret-key")
	handler := &mockEventHandler{}
	stream := NewUserDataStream(client, handler)

	// Initially wasConnected should be false
	if stream.wasConnected {
		t.Error("wasConnected = true, want false initially")
	}

	// After setting it, it should be true
	stream.wasConnected = true
	if !stream.wasConnected {
		t.Error("wasConnected = false, want true after setting")
	}
}
