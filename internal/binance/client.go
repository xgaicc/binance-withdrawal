// Package binance provides a client for interacting with Binance APIs.
package binance

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Client provides authenticated access to Binance REST APIs.
type Client struct {
	apiKey     string
	secretKey  string
	baseURL    string
	httpClient *http.Client
}

// ClientOption is a function that configures the Client.
type ClientOption func(*Client)

// WithBaseURL sets a custom base URL (useful for testnet).
func WithBaseURL(url string) ClientOption {
	return func(c *Client) {
		c.baseURL = strings.TrimSuffix(url, "/")
	}
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(hc *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = hc
	}
}

// NewClient creates a new Binance API client.
func NewClient(apiKey, secretKey string, opts ...ClientOption) *Client {
	c := &Client{
		apiKey:    apiKey,
		secretKey: secretKey,
		baseURL:   "https://api.binance.com",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// sign creates an HMAC-SHA256 signature for the query string.
func (c *Client) sign(queryString string) string {
	mac := hmac.New(sha256.New, []byte(c.secretKey))
	mac.Write([]byte(queryString))
	return hex.EncodeToString(mac.Sum(nil))
}

// doRequest executes an HTTP request with optional signing.
func (c *Client) doRequest(method, endpoint string, params url.Values, signed bool) ([]byte, error) {
	u := c.baseURL + endpoint

	if signed {
		if params == nil {
			params = url.Values{}
		}
		params.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
		queryString := sortedEncode(params)
		signature := c.sign(queryString)
		u = u + "?" + queryString + "&signature=" + signature
	} else if len(params) > 0 {
		u = u + "?" + params.Encode()
	}

	req, err := http.NewRequest(method, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("X-MBX-APIKEY", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr APIError
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Code != 0 {
			return nil, &apiErr
		}
		return nil, fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// sortedEncode encodes URL values with sorted keys for consistent signatures.
func sortedEncode(v url.Values) string {
	if v == nil {
		return ""
	}
	var keys []string
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		vs := v[k]
		for _, val := range vs {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(val))
		}
	}
	return strings.Join(parts, "&")
}

// APIError represents an error response from Binance.
type APIError struct {
	Code    int    `json:"code"`
	Message string `json:"msg"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("binance API error %d: %s", e.Code, e.Message)
}

// CreateListenKey creates a new listen key for the User Data Stream.
// POST /sapi/v1/userDataStream
func (c *Client) CreateListenKey() (string, error) {
	body, err := c.doRequest(http.MethodPost, "/sapi/v1/userDataStream", nil, false)
	if err != nil {
		return "", fmt.Errorf("creating listen key: %w", err)
	}

	var resp struct {
		ListenKey string `json:"listenKey"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parsing listen key response: %w", err)
	}

	return resp.ListenKey, nil
}

// RefreshListenKey keeps the listen key alive.
// PUT /sapi/v1/userDataStream
func (c *Client) RefreshListenKey(listenKey string) error {
	params := url.Values{}
	params.Set("listenKey", listenKey)

	_, err := c.doRequest(http.MethodPut, "/sapi/v1/userDataStream", params, false)
	if err != nil {
		return fmt.Errorf("refreshing listen key: %w", err)
	}

	return nil
}

// CloseListenKey closes the listen key.
// DELETE /sapi/v1/userDataStream
func (c *Client) CloseListenKey(listenKey string) error {
	params := url.Values{}
	params.Set("listenKey", listenKey)

	_, err := c.doRequest(http.MethodDelete, "/sapi/v1/userDataStream", params, false)
	if err != nil {
		return fmt.Errorf("closing listen key: %w", err)
	}

	return nil
}

// SubAccountTransfer represents an incoming transfer from a sub-account.
type SubAccountTransfer struct {
	TranID       int64     `json:"tranId"`
	FromEmail    string    `json:"fromEmail"`
	ToEmail      string    `json:"toEmail"`
	Asset        string    `json:"asset"`
	Qty          string    `json:"qty"`
	Status       string    `json:"status"`
	CreateTime   int64     `json:"createTimeStamp"`
	CreateTimeAt time.Time `json:"-"` // Parsed from CreateTime
}

// GetSubAccountTransferHistory retrieves transfer history from sub-accounts.
// GET /sapi/v1/sub-account/transfer/subUserHistory
// type=1 means transfers TO master account
func (c *Client) GetSubAccountTransferHistory(startTime, endTime time.Time, limit int) ([]SubAccountTransfer, error) {
	params := url.Values{}
	params.Set("type", "1") // Transfers TO master

	if !startTime.IsZero() {
		params.Set("startTime", strconv.FormatInt(startTime.UnixMilli(), 10))
	}
	if !endTime.IsZero() {
		params.Set("endTime", strconv.FormatInt(endTime.UnixMilli(), 10))
	}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}

	body, err := c.doRequest(http.MethodGet, "/sapi/v1/sub-account/transfer/subUserHistory", params, true)
	if err != nil {
		return nil, fmt.Errorf("getting transfer history: %w", err)
	}

	var transfers []SubAccountTransfer
	if err := json.Unmarshal(body, &transfers); err != nil {
		return nil, fmt.Errorf("parsing transfer history: %w", err)
	}

	// Parse timestamps
	for i := range transfers {
		transfers[i].CreateTimeAt = time.UnixMilli(transfers[i].CreateTime)
	}

	return transfers, nil
}

// GetSubAccountTransferByID retrieves a specific transfer by ID.
// Uses the history endpoint with returnTranId filter.
func (c *Client) GetSubAccountTransferByID(tranID int64) (*SubAccountTransfer, error) {
	// Query with a wide time range - the tranID is the filter we care about
	// Since we don't know when the transfer occurred, we need to search
	params := url.Values{}
	params.Set("type", "1")
	params.Set("limit", "500") // Max allowed

	body, err := c.doRequest(http.MethodGet, "/sapi/v1/sub-account/transfer/subUserHistory", params, true)
	if err != nil {
		return nil, fmt.Errorf("getting transfer history: %w", err)
	}

	var transfers []SubAccountTransfer
	if err := json.Unmarshal(body, &transfers); err != nil {
		return nil, fmt.Errorf("parsing transfer history: %w", err)
	}

	for i := range transfers {
		if transfers[i].TranID == tranID {
			transfers[i].CreateTimeAt = time.UnixMilli(transfers[i].CreateTime)
			return &transfers[i], nil
		}
	}

	return nil, fmt.Errorf("transfer %d not found", tranID)
}
