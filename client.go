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

// Credentials holds authentication credentials.
type Credentials struct {
	Email    string
	Password string
}

// Client provides unauthenticated access to PocketBase and can create authenticated clients.
type Client interface {
	AuthenticateUser(creds Credentials) (AuthenticatedClient, error)
	AuthenticateSuperuser(creds Credentials) (AuthenticatedClient, error)
}

// AuthenticatedClient provides authenticated HTTP access to PocketBase.
type AuthenticatedClient interface {
	Do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error)
}

// ClientOption configures optional Client settings.
type ClientOption func(*client)

// WithHTTPClient overrides the default http.Client.
func WithHTTPClient(hc *http.Client) ClientOption {
	return func(c *client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// WithLogger attaches a logger used for debug information.
func WithLogger(logger *slog.Logger) ClientOption {
	return func(c *client) {
		c.logger = logger
	}
}

// WithTimeout sets the HTTP client timeout.
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *client) {
		if c.httpClient == nil {
			c.httpClient = defaultHTTPClient()
		}
		c.httpClient.Timeout = timeout
	}
}

// WithRetry sets the maximum number of retries for transient errors and the base backoff.
func WithRetry(maxRetries int, backoff time.Duration) ClientOption {
	return func(c *client) {
		if maxRetries < 0 {
			maxRetries = 0
		}
		c.maxRetries = maxRetries
		if backoff > 0 {
			c.backoff = backoff
		}
	}
}

// client is the implementation of Client.
type client struct {
	baseURL    string
	httpClient *http.Client
	maxRetries int
	backoff    time.Duration
	logger     *slog.Logger
}

// NewClient constructs a PocketBase client.
func NewClient(baseURL string, opts ...ClientOption) (Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, errors.New("baseURL is required")
	}

	c := &client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: defaultHTTPClient(),
	}

	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	if c.httpClient == nil {
		c.httpClient = defaultHTTPClient()
	}

	return c, nil
}

const (
	userAuthEndpoint      = "/api/collections/users/auth-with-password"
	superuserAuthEndpoint = "/api/collections/_superusers/auth-with-password"
)

// AuthenticateUser authenticates using the users collection endpoint.
func (c *client) AuthenticateUser(creds Credentials) (AuthenticatedClient, error) {
	return c.authenticate(creds, userAuthEndpoint)
}

// AuthenticateSuperuser authenticates using the superuser endpoint.
func (c *client) AuthenticateSuperuser(creds Credentials) (AuthenticatedClient, error) {
	return c.authenticate(creds, superuserAuthEndpoint)
}

func (c *client) authenticate(creds Credentials, endpoint string) (AuthenticatedClient, error) {
	if strings.TrimSpace(creds.Email) == "" {
		return nil, errors.New("email is required")
	}
	if creds.Password == "" {
		return nil, errors.New("password is required")
	}

	payload := map[string]string{
		"identity": creds.Email,
		"password": creds.Password,
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return nil, fmt.Errorf("encode auth payload: %w", err)
	}

	url := c.baseURL + endpoint
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, &buf)
	if err != nil {
		return nil, fmt.Errorf("build auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("authentication request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read auth response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, mapHTTPError(resp.StatusCode, body)
	}

	var authResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &authResp); err != nil {
		return nil, fmt.Errorf("parse auth response: %w", err)
	}
	if authResp.Token == "" {
		return nil, errors.New("authentication succeeded but token missing")
	}

	expiry := time.Now().Add(23 * time.Hour)
	if c.logger != nil {
		c.logger.Info("authenticated with PocketBase", "expires", expiry)
	}

	return &authenticatedClient{
		client:       c,
		token:        authResp.Token,
		tokenExpires: expiry,
		creds:        creds,
		authEndpoint: endpoint,
	}, nil
}

// authenticatedClient is the implementation of AuthenticatedClient.
type authenticatedClient struct {
	client       *client
	token        string
	tokenExpires time.Time
	creds        Credentials
	authEndpoint string
	authMutex    sync.Mutex
	tokenMutex   sync.RWMutex
}

// Do executes an authenticated HTTP request with retries.
func (ac *authenticatedClient) Do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
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

	url := ac.client.baseURL + "/" + strings.TrimLeft(path, "/")
	attempts := ac.client.maxRetries

	for attempt := 0; attempt <= attempts; attempt++ {
		if err := ac.ensureAuthenticated(); err != nil {
			return nil, err
		}

		token := ac.readToken()
		var reqBody io.Reader
		if bodyBytes != nil {
			reqBody = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}

		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if bodyBytes != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := ac.client.httpClient.Do(req)
		if err != nil {
			if attempt == attempts {
				return nil, err
			}
			if waitErr := ac.wait(ctx, attempt); waitErr != nil {
				return nil, waitErr
			}
			continue
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			ac.clearToken()
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < attempts {
			resp.Body.Close()
			if waitErr := ac.wait(ctx, attempt); waitErr != nil {
				return nil, waitErr
			}
			continue
		}

		return resp, nil
	}

	return nil, errors.New("request failed after retries")
}

func (ac *authenticatedClient) ensureAuthenticated() error {
	if ac.tokenValid() {
		return nil
	}
	ac.authMutex.Lock()
	defer ac.authMutex.Unlock()

	if ac.tokenValid() {
		return nil
	}
	return ac.reauthenticate()
}

func (ac *authenticatedClient) reauthenticate() error {
	payload := map[string]string{
		"identity": ac.creds.Email,
		"password": ac.creds.Password,
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return fmt.Errorf("encode auth payload: %w", err)
	}

	url := ac.client.baseURL + ac.authEndpoint
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, &buf)
	if err != nil {
		return fmt.Errorf("build auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ac.client.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("authentication request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read auth response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		ac.clearToken()
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
	ac.tokenMutex.Lock()
	ac.token = authResp.Token
	ac.tokenExpires = expiry
	ac.tokenMutex.Unlock()

	if ac.client.logger != nil {
		ac.client.logger.Info("re-authenticated with PocketBase", "expires", expiry)
	}
	return nil
}

func (ac *authenticatedClient) wait(ctx context.Context, attempt int) error {
	backoff := ac.client.backoff
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

func (ac *authenticatedClient) tokenValid() bool {
	ac.tokenMutex.RLock()
	defer ac.tokenMutex.RUnlock()

	if ac.token == "" {
		return false
	}
	if ac.tokenExpires.IsZero() {
		return true
	}
	return time.Now().Before(ac.tokenExpires)
}

func (ac *authenticatedClient) readToken() string {
	ac.tokenMutex.RLock()
	defer ac.tokenMutex.RUnlock()
	return ac.token
}

func (ac *authenticatedClient) clearToken() {
	ac.tokenMutex.Lock()
	defer ac.tokenMutex.Unlock()
	ac.token = ""
	ac.tokenExpires = time.Time{}
}

func defaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}