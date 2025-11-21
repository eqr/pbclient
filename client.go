package pbclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"log/slog"
)

// Client provides authenticated HTTP access to PocketBase.
// It is safe for concurrent use.
type Client struct {
	baseURL       string
	token         string
	tokenExpires  time.Time
	adminEmail    string
	adminPassword string
	httpClient    *http.Client
	maxRetries    int
	backoff       time.Duration
	authMutex     sync.Mutex
	tokenMutex    sync.RWMutex
	logger        *slog.Logger
}

// ClientOption configures optional Client settings.
type ClientOption func(*Client)

// WithHTTPClient overrides the default http.Client.
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

// WithLogger attaches a logger used for debug information.
func WithLogger(logger *slog.Logger) ClientOption {
	return func(c *Client) {
		c.logger = logger
	}
}

// WithTimeout sets the HTTP client timeout.
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) {
		if c.httpClient == nil {
			c.httpClient = defaultHTTPClient()
		}
		c.httpClient.Timeout = timeout
	}
}

// WithRetry sets the maximum number of retries for transient errors and the base backoff.
// Retries apply to network errors and HTTP 429 responses.
func WithRetry(maxRetries int, backoff time.Duration) ClientOption {
	return func(c *Client) {
		if maxRetries < 0 {
			maxRetries = 0
		}
		c.maxRetries = maxRetries
		if backoff > 0 {
			c.backoff = backoff
		}
	}
}

// NewClient constructs a PocketBase client without performing authentication.
// Use ClientOptions to set timeouts, retries, logging, or custom transports.
func NewClient(baseURL, adminEmail, adminPassword string, opts ...ClientOption) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	adminEmail = strings.TrimSpace(adminEmail)

	if baseURL == "" {
		return nil, errors.New("baseURL is required")
	}
	if adminEmail == "" {
		return nil, errors.New("admin email is required")
	}
	if adminPassword == "" {
		return nil, errors.New("admin password is required")
	}

	client := &Client{
		baseURL:       strings.TrimRight(baseURL, "/"),
		adminEmail:    adminEmail,
		adminPassword: adminPassword,
		httpClient:    defaultHTTPClient(),
	}

	for _, opt := range opts {
		if opt != nil {
			opt(client)
		}
	}
	if client.httpClient == nil {
		client.httpClient = defaultHTTPClient()
	}

	return client, nil
}

// authenticate logs in and stores the access token.
func (c *Client) authenticate() error {
	payload := map[string]string{
		"identity": c.adminEmail,
		"password": c.adminPassword,
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return fmt.Errorf("encode auth payload: %w", err)
	}

	url := c.baseURL + "/api/collections/users/auth-with-password"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, &buf)
	if err != nil {
		return fmt.Errorf("build auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("authentication request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read auth response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		c.clearToken()
		return mapHTTPError(resp.StatusCode, body)
	}

	var authResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &authResp); err != nil {
		return fmt.Errorf("parse auth response: %w", err)
	}
	if authResp.Token == "" {
		return errors.New("authentication succeeded but token missing")
	}

	expiry := time.Now().Add(23 * time.Hour)
	c.tokenMutex.Lock()
	c.token = authResp.Token
	c.tokenExpires = expiry
	c.tokenMutex.Unlock()

	if c.logger != nil {
		c.logger.Info("authenticated with PocketBase", "expires", expiry)
	}
	return nil
}

// ensureAuthenticated lazily obtains or refreshes a token when needed.
func (c *Client) ensureAuthenticated() error {
	if c.tokenValid() {
		return nil
	}
	c.authMutex.Lock()
	defer c.authMutex.Unlock()

	if c.tokenValid() {
		return nil
	}
	return c.authenticate()
}

// doRequest executes an authenticated HTTP request to PocketBase with optional retries.
// It handles token refresh, 401/403 clearing, and exponential backoff on 429/network failures.
func (c *Client) doRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	var bodyBytes []byte
	if body != nil {
		data, err := io.ReadAll(body)
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
		bodyBytes = data
	}

	baseURL := c.baseURL + "/" + strings.TrimLeft(path, "/")
	attempts := c.maxRetries

	for attempt := 0; attempt <= attempts; attempt++ {
		if err := c.ensureAuthenticated(); err != nil {
			return nil, err
		}

		token := c.readToken()
		var reqBody io.Reader
		if bodyBytes != nil {
			reqBody = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, baseURL, reqBody)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}

		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if bodyBytes != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if attempt == attempts {
				return nil, err
			}
			if waitErr := c.wait(ctx, attempt); waitErr != nil {
				return nil, waitErr
			}
			continue
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			c.clearToken()
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < attempts {
			resp.Body.Close()
			if waitErr := c.wait(ctx, attempt); waitErr != nil {
				return nil, waitErr
			}
			continue
		}

		return resp, nil
	}

	return nil, errors.New("request failed after retries")
}

// Do executes an authenticated HTTP request with retries.
// Callers must close the returned response body.
func (c *Client) Do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	return c.doRequest(ctx, method, path, body)
}

func (c *Client) wait(ctx context.Context, attempt int) error {
	backoff := c.backoff
	if backoff <= 0 {
		backoff = 200 * time.Millisecond
	}
	delay := backoff << attempt

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) tokenValid() bool {
	c.tokenMutex.RLock()
	defer c.tokenMutex.RUnlock()

	if c.token == "" {
		return false
	}
	if c.tokenExpires.IsZero() {
		return true
	}
	return time.Now().Before(c.tokenExpires)
}

func (c *Client) readToken() string {
	c.tokenMutex.RLock()
	defer c.tokenMutex.RUnlock()
	return c.token
}

func (c *Client) clearToken() {
	c.tokenMutex.Lock()
	defer c.tokenMutex.Unlock()
	c.token = ""
	c.tokenExpires = time.Time{}
}

func defaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}
