package binance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Default configuration values for WebSocket connections.
const (
	DefaultStreamURL          = "wss://stream.binance.com:9443/ws"
	DefaultRefreshInterval    = 30 * time.Minute
	DefaultPingInterval       = 3 * time.Minute
	DefaultPongTimeout        = 10 * time.Second
	DefaultReconnectDelay     = 5 * time.Second
	DefaultMaxReconnectDelay  = 5 * time.Minute
	DefaultReconnectBackoff   = 2.0 // Exponential backoff multiplier
)

// UserDataStream manages a WebSocket connection to Binance's User Data Stream.
type UserDataStream struct {
	client     *Client
	streamURL  string
	listenKey  string
	conn       *websocket.Conn
	handler    EventHandler
	errorChan  chan error
	doneChan   chan struct{}
	stopOnce   sync.Once
	mu         sync.RWMutex
	connected  bool

	// Configuration
	refreshInterval   time.Duration
	pingInterval      time.Duration
	pongTimeout       time.Duration
	reconnectDelay    time.Duration
	maxReconnectDelay time.Duration
	reconnectBackoff  float64
	autoReconnect     bool

	// Reconnection state
	ctx           context.Context
	wasConnected  bool // Tracks if we were previously connected (for OnReconnected vs OnConnected)
}

// EventHandler processes events from the User Data Stream.
type EventHandler interface {
	// OnBalanceUpdate is called when a balance update event is received.
	OnBalanceUpdate(event *BalanceUpdateEvent)
	// OnError is called when an error occurs.
	OnError(err error)
	// OnConnected is called when the WebSocket connection is established.
	OnConnected()
	// OnDisconnected is called when the WebSocket connection is lost.
	OnDisconnected()
	// OnReconnected is called when the WebSocket connection is re-established after a disconnection.
	// This is a good time to poll for any transfers that may have been missed.
	OnReconnected()
}

// StreamOption configures the UserDataStream.
type StreamOption func(*UserDataStream)

// WithStreamURL sets a custom WebSocket URL.
func WithStreamURL(url string) StreamOption {
	return func(s *UserDataStream) {
		s.streamURL = url
	}
}

// WithRefreshInterval sets the listen key refresh interval.
func WithRefreshInterval(d time.Duration) StreamOption {
	return func(s *UserDataStream) {
		s.refreshInterval = d
	}
}

// NewUserDataStream creates a new User Data Stream manager.
func NewUserDataStream(client *Client, handler EventHandler, opts ...StreamOption) *UserDataStream {
	s := &UserDataStream{
		client:            client,
		streamURL:         DefaultStreamURL,
		handler:           handler,
		errorChan:         make(chan error, 10),
		doneChan:          make(chan struct{}),
		refreshInterval:   DefaultRefreshInterval,
		pingInterval:      DefaultPingInterval,
		pongTimeout:       DefaultPongTimeout,
		reconnectDelay:    DefaultReconnectDelay,
		maxReconnectDelay: DefaultMaxReconnectDelay,
		reconnectBackoff:  DefaultReconnectBackoff,
		autoReconnect:     true, // Enable auto-reconnect by default
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithReconnectDelay sets the initial reconnection delay.
func WithReconnectDelay(d time.Duration) StreamOption {
	return func(s *UserDataStream) {
		s.reconnectDelay = d
	}
}

// WithMaxReconnectDelay sets the maximum reconnection delay.
func WithMaxReconnectDelay(d time.Duration) StreamOption {
	return func(s *UserDataStream) {
		s.maxReconnectDelay = d
	}
}

// WithAutoReconnect enables or disables automatic reconnection.
func WithAutoReconnect(enabled bool) StreamOption {
	return func(s *UserDataStream) {
		s.autoReconnect = enabled
	}
}

// Connect establishes the WebSocket connection.
// It obtains a listen key and connects to the User Data Stream.
func (s *UserDataStream) Connect(ctx context.Context) error {
	s.ctx = ctx
	return s.connect(false)
}

// connect is the internal connection method.
// isReconnect indicates whether this is a reconnection attempt.
func (s *UserDataStream) connect(isReconnect bool) error {
	// Obtain listen key
	listenKey, err := s.client.CreateListenKey()
	if err != nil {
		return fmt.Errorf("creating listen key: %w", err)
	}
	s.listenKey = listenKey

	// Connect to WebSocket
	wsURL := s.streamURL + "/" + listenKey
	conn, _, err := websocket.DefaultDialer.DialContext(s.ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("connecting to WebSocket: %w", err)
	}

	s.mu.Lock()
	s.conn = conn
	s.connected = true
	wasConnected := s.wasConnected
	s.wasConnected = true
	s.mu.Unlock()

	// Set up pong handler
	conn.SetPongHandler(func(appData string) error {
		return conn.SetReadDeadline(time.Now().Add(s.pongTimeout + s.pingInterval))
	})

	// Notify handler - use OnReconnected if this was a reconnection
	if isReconnect || wasConnected {
		s.handler.OnReconnected()
	} else {
		s.handler.OnConnected()
	}

	// Start background goroutines
	go s.readLoop()
	go s.keepAlive(s.ctx)

	return nil
}

// Close closes the WebSocket connection and cleans up resources.
func (s *UserDataStream) Close() error {
	var closeErr error
	s.stopOnce.Do(func() {
		close(s.doneChan)

		s.mu.Lock()
		if s.conn != nil {
			closeErr = s.conn.Close()
			s.connected = false
		}
		s.mu.Unlock()

		// Best effort to close the listen key
		if s.listenKey != "" {
			_ = s.client.CloseListenKey(s.listenKey)
		}

		s.handler.OnDisconnected()
	})
	return closeErr
}

// IsConnected returns whether the WebSocket is currently connected.
func (s *UserDataStream) IsConnected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connected
}

// readLoop reads messages from the WebSocket connection.
func (s *UserDataStream) readLoop() {
	defer func() {
		s.mu.Lock()
		s.connected = false
		s.mu.Unlock()
	}()

	for {
		select {
		case <-s.doneChan:
			return
		default:
		}

		s.mu.RLock()
		conn := s.conn
		s.mu.RUnlock()

		if conn == nil {
			return
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return
			}
			select {
			case <-s.doneChan:
				return
			default:
				s.handler.OnError(fmt.Errorf("reading WebSocket message: %w", err))
				s.handler.OnDisconnected()
				// Attempt reconnection if enabled
				if s.autoReconnect {
					go s.reconnectLoop()
				}
				return
			}
		}

		s.processMessage(message)
	}
}

// reconnectLoop attempts to reconnect with exponential backoff.
func (s *UserDataStream) reconnectLoop() {
	delay := s.reconnectDelay

	for {
		select {
		case <-s.doneChan:
			return
		case <-s.ctx.Done():
			return
		default:
		}

		// Wait before attempting reconnection
		select {
		case <-s.doneChan:
			return
		case <-s.ctx.Done():
			return
		case <-time.After(delay):
		}

		// Close old listen key if it exists
		if s.listenKey != "" {
			_ = s.client.CloseListenKey(s.listenKey)
			s.listenKey = ""
		}

		// Attempt to reconnect
		err := s.connect(true)
		if err == nil {
			// Successfully reconnected
			return
		}

		s.handler.OnError(fmt.Errorf("reconnection failed: %w", err))

		// Increase delay with exponential backoff
		delay = time.Duration(float64(delay) * s.reconnectBackoff)
		if delay > s.maxReconnectDelay {
			delay = s.maxReconnectDelay
		}
	}
}

// keepAlive refreshes the listen key and sends ping frames.
func (s *UserDataStream) keepAlive(ctx context.Context) {
	refreshTicker := time.NewTicker(s.refreshInterval)
	pingTicker := time.NewTicker(s.pingInterval)
	defer refreshTicker.Stop()
	defer pingTicker.Stop()

	for {
		select {
		case <-s.doneChan:
			return
		case <-ctx.Done():
			return
		case <-refreshTicker.C:
			if err := s.client.RefreshListenKey(s.listenKey); err != nil {
				s.handler.OnError(fmt.Errorf("refreshing listen key: %w", err))
			}
		case <-pingTicker.C:
			s.mu.RLock()
			conn := s.conn
			s.mu.RUnlock()

			if conn != nil {
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					s.handler.OnError(fmt.Errorf("sending ping: %w", err))
				}
			}
		}
	}
}

// processMessage parses and dispatches a WebSocket message.
func (s *UserDataStream) processMessage(data []byte) {
	// First, determine the event type
	// Note: We include both "e" (event type) and "E" (event time) fields
	// to prevent Go 1.24's case-insensitive JSON matching from conflicting
	var baseEvent struct {
		EventType string `json:"e"`
		EventTime int64  `json:"E"`
	}
	if err := json.Unmarshal(data, &baseEvent); err != nil {
		s.handler.OnError(fmt.Errorf("parsing event type: %w", err))
		return
	}

	switch baseEvent.EventType {
	case "balanceUpdate":
		var event BalanceUpdateEvent
		if err := json.Unmarshal(data, &event); err != nil {
			s.handler.OnError(fmt.Errorf("parsing balanceUpdate: %w", err))
			return
		}
		event.ParseTimestamps()
		s.handler.OnBalanceUpdate(&event)

	// Future: handle other event types like outboundAccountPosition, etc.
	default:
		// Unknown event type - ignore for now
	}
}

// BalanceUpdateEvent represents a balance change on the account.
// This is triggered when funds are deposited, withdrawn, or transferred.
type BalanceUpdateEvent struct {
	EventType     string `json:"e"` // "balanceUpdate"
	EventTime     int64  `json:"E"` // Event time (milliseconds)
	Asset         string `json:"a"` // Asset
	BalanceDelta  string `json:"d"` // Balance delta (can be negative)
	ClearTime     int64  `json:"T"` // Clear time (milliseconds)
	EventTimeAt   time.Time
	ClearTimeAt   time.Time
}

// ParseTimestamps converts millisecond timestamps to time.Time.
func (e *BalanceUpdateEvent) ParseTimestamps() {
	e.EventTimeAt = time.UnixMilli(e.EventTime)
	e.ClearTimeAt = time.UnixMilli(e.ClearTime)
}

// ErrNotConnected is returned when attempting operations on a disconnected stream.
var ErrNotConnected = errors.New("WebSocket not connected")
