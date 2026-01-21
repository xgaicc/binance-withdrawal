package binance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	c := NewClient("api-key", "secret-key")
	if c.apiKey != "api-key" {
		t.Errorf("apiKey = %q, want %q", c.apiKey, "api-key")
	}
	if c.secretKey != "secret-key" {
		t.Errorf("secretKey = %q, want %q", c.secretKey, "secret-key")
	}
	if c.baseURL != "https://api.binance.com" {
		t.Errorf("baseURL = %q, want %q", c.baseURL, "https://api.binance.com")
	}
}

func TestClientWithOptions(t *testing.T) {
	customClient := &http.Client{Timeout: 10 * time.Second}
	c := NewClient("api-key", "secret-key",
		WithBaseURL("https://testnet.binance.vision"),
		WithHTTPClient(customClient),
	)
	if c.baseURL != "https://testnet.binance.vision" {
		t.Errorf("baseURL = %q, want %q", c.baseURL, "https://testnet.binance.vision")
	}
	if c.httpClient != customClient {
		t.Error("httpClient not set correctly")
	}
}

func TestCreateListenKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/sapi/v1/userDataStream" {
			t.Errorf("Path = %q, want /sapi/v1/userDataStream", r.URL.Path)
		}
		if r.Header.Get("X-MBX-APIKEY") != "test-api-key" {
			t.Errorf("X-MBX-APIKEY = %q, want test-api-key", r.Header.Get("X-MBX-APIKEY"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"listenKey": "test-listen-key-12345",
		})
	}))
	defer server.Close()

	c := NewClient("test-api-key", "test-secret", WithBaseURL(server.URL))

	listenKey, err := c.CreateListenKey()
	if err != nil {
		t.Fatalf("CreateListenKey() error = %v", err)
	}
	if listenKey != "test-listen-key-12345" {
		t.Errorf("listenKey = %q, want %q", listenKey, "test-listen-key-12345")
	}
}

func TestRefreshListenKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("Method = %q, want PUT", r.Method)
		}
		if r.URL.Path != "/sapi/v1/userDataStream" {
			t.Errorf("Path = %q, want /sapi/v1/userDataStream", r.URL.Path)
		}
		if r.URL.Query().Get("listenKey") != "my-listen-key" {
			t.Errorf("listenKey param = %q, want my-listen-key", r.URL.Query().Get("listenKey"))
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	c := NewClient("test-api-key", "test-secret", WithBaseURL(server.URL))

	err := c.RefreshListenKey("my-listen-key")
	if err != nil {
		t.Fatalf("RefreshListenKey() error = %v", err)
	}
}

func TestCloseListenKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("Method = %q, want DELETE", r.Method)
		}
		if r.URL.Path != "/sapi/v1/userDataStream" {
			t.Errorf("Path = %q, want /sapi/v1/userDataStream", r.URL.Path)
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	c := NewClient("test-api-key", "test-secret", WithBaseURL(server.URL))

	err := c.CloseListenKey("my-listen-key")
	if err != nil {
		t.Fatalf("CloseListenKey() error = %v", err)
	}
}

func TestGetSubAccountTransferHistory(t *testing.T) {
	now := time.Now()
	transfers := []SubAccountTransfer{
		{
			TranID:     123456,
			FromEmail:  "sub@example.com",
			ToEmail:    "master@example.com",
			Asset:      "SOL",
			Qty:        "100.5",
			Status:     "SUCCESS",
			CreateTime: now.UnixMilli(),
		},
		{
			TranID:     123457,
			FromEmail:  "sub2@example.com",
			ToEmail:    "master@example.com",
			Asset:      "ETH",
			Qty:        "2.0",
			Status:     "SUCCESS",
			CreateTime: now.Add(-1 * time.Hour).UnixMilli(),
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Method = %q, want GET", r.Method)
		}
		if r.URL.Path != "/sapi/v1/sub-account/transfer/subUserHistory" {
			t.Errorf("Path = %q, want /sapi/v1/sub-account/transfer/subUserHistory", r.URL.Path)
		}

		// Check required params
		if r.URL.Query().Get("type") != "1" {
			t.Errorf("type param = %q, want 1", r.URL.Query().Get("type"))
		}
		if r.URL.Query().Get("signature") == "" {
			t.Error("signature param missing")
		}
		if r.URL.Query().Get("timestamp") == "" {
			t.Error("timestamp param missing")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(transfers)
	}))
	defer server.Close()

	c := NewClient("test-api-key", "test-secret", WithBaseURL(server.URL))

	startTime := now.Add(-2 * time.Hour)
	endTime := now
	result, err := c.GetSubAccountTransferHistory(startTime, endTime, 100)
	if err != nil {
		t.Fatalf("GetSubAccountTransferHistory() error = %v", err)
	}

	if len(result) != 2 {
		t.Errorf("len(result) = %d, want 2", len(result))
	}

	if result[0].TranID != 123456 {
		t.Errorf("result[0].TranID = %d, want 123456", result[0].TranID)
	}
	if result[0].FromEmail != "sub@example.com" {
		t.Errorf("result[0].FromEmail = %q, want sub@example.com", result[0].FromEmail)
	}
	if result[0].Asset != "SOL" {
		t.Errorf("result[0].Asset = %q, want SOL", result[0].Asset)
	}
	if result[0].Qty != "100.5" {
		t.Errorf("result[0].Qty = %q, want 100.5", result[0].Qty)
	}

	// Check that timestamps are parsed
	if result[0].CreateTimeAt.IsZero() {
		t.Error("result[0].CreateTimeAt not parsed")
	}
}

func TestAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"code": -1121,
			"msg":  "Invalid symbol.",
		})
	}))
	defer server.Close()

	c := NewClient("test-api-key", "test-secret", WithBaseURL(server.URL))

	_, err := c.CreateListenKey()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// The error is wrapped, so we need to check the error message
	errStr := err.Error()
	if !strings.Contains(errStr, "-1121") {
		t.Errorf("error should contain code -1121, got: %v", err)
	}
	if !strings.Contains(errStr, "Invalid symbol") {
		t.Errorf("error should contain message, got: %v", err)
	}
}

func TestSign(t *testing.T) {
	c := NewClient("api-key", "NhqPtmdSJYdKjVHjA7PZj4Mge3R5YNiP1e3UZjInClVN65XAbvqqM6A7H5fATj0j")

	// Test vector from Binance documentation
	// Note: This is a simplified test - real signature includes timestamp
	queryString := "symbol=LTCBTC&side=BUY&type=LIMIT&timeInForce=GTC&quantity=1&price=0.1&recvWindow=5000&timestamp=1499827319559"
	signature := c.sign(queryString)

	// This is the expected signature from Binance docs for this query string with this secret
	expected := "c8db56825ae71d6d79447849e617115f4a920fa2acdcab2b053c4b2838bd6b71"
	if signature != expected {
		t.Errorf("signature = %q, want %q", signature, expected)
	}
}

func TestSortedEncode(t *testing.T) {
	tests := []struct {
		name   string
		values map[string][]string
		want   string
	}{
		{
			name:   "empty",
			values: nil,
			want:   "",
		},
		{
			name: "single value",
			values: map[string][]string{
				"key": {"value"},
			},
			want: "key=value",
		},
		{
			name: "multiple values sorted",
			values: map[string][]string{
				"z": {"3"},
				"a": {"1"},
				"m": {"2"},
			},
			want: "a=1&m=2&z=3",
		},
		{
			name: "special characters",
			values: map[string][]string{
				"email": {"test@example.com"},
			},
			want: "email=test%40example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sortedEncode(tt.values)
			if got != tt.want {
				t.Errorf("sortedEncode() = %q, want %q", got, tt.want)
			}
		})
	}
}
