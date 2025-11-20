package pbclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientRetries429(t *testing.T) {
	var attempts int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client, err := NewClient(ts.URL, "admin@example.com", "password", WithHTTPClient(ts.Client()), WithRetry(2, 10*time.Millisecond))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.token = "token"

	resp, err := client.doRequest(context.Background(), http.MethodGet, "/test", nil)
	if err != nil {
		t.Fatalf("doRequest: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestClientRetriesNetworkErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	flaky := &flakyTransport{
		failFor: 1,
		base:    ts.Client().Transport,
	}

	httpClient := &http.Client{Transport: flaky}
	client, err := NewClient(ts.URL, "admin@example.com", "password", WithHTTPClient(httpClient), WithRetry(2, 5*time.Millisecond))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.token = "token"

	resp, err := client.doRequest(context.Background(), http.MethodGet, "/test", nil)
	if err != nil {
		t.Fatalf("doRequest: %v", err)
	}
	defer resp.Body.Close()

	if flaky.calls != 2 {
		t.Fatalf("expected 2 attempts, got %d", flaky.calls)
	}
}

func TestAuthenticateSuccessAndFailure(t *testing.T) {
	var authCalls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/collections/users/auth-with-password":
			authCalls++
			if r.Method != http.MethodPost {
				http.Error(w, "bad method", http.StatusBadRequest)
				return
			}
			if authCalls == 1 {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"token":"tok1"}`))
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"invalid"}`))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer ts.Close()

	client, err := NewClient(ts.URL, "admin@example.com", "password", WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	// success
	if err := client.authenticate(); err != nil {
		t.Fatalf("authenticate success: %v", err)
	}
	if client.readToken() != "tok1" {
		t.Fatalf("expected token tok1, got %q", client.readToken())
	}
	if client.tokenExpires.IsZero() {
		t.Fatalf("expected token expiry set")
	}

	// failure clears token
	if err := client.authenticate(); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
	if client.readToken() != "" {
		t.Fatalf("token should be cleared after failure")
	}
	if !client.tokenExpires.IsZero() {
		t.Fatalf("token expiry should be cleared after failure")
	}
}

func TestEnsureAuthenticatedRefreshOnExpiry(t *testing.T) {
	var authCalls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authCalls++
		_, _ = w.Write([]byte(`{"token":"fresh"}`))
	}))
	defer ts.Close()

	client, err := NewClient(ts.URL, "admin@example.com", "password", WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	client.token = "stale"
	client.tokenExpires = time.Now().Add(-time.Minute)

	if err := client.ensureAuthenticated(); err != nil {
		t.Fatalf("ensureAuthenticated: %v", err)
	}
	if authCalls != 1 {
		t.Fatalf("expected one auth call, got %d", authCalls)
	}
	if client.readToken() != "fresh" {
		t.Fatalf("expected refreshed token, got %q", client.readToken())
	}
}

func TestDoRequestClearsTokenOnUnauthorized(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/collections/users/auth-with-password" {
			_, _ = w.Write([]byte(`{"token":"ok"}`))
			return
		}
		http.Error(w, "denied", http.StatusUnauthorized)
	}))
	defer ts.Close()

	client, err := NewClient(ts.URL, "admin@example.com", "password", WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	resp, err := client.doRequest(context.Background(), http.MethodGet, "/anything", nil)
	if err != nil {
		t.Fatalf("doRequest: %v", err)
	}
	defer resp.Body.Close()

	if client.readToken() != "" {
		t.Fatalf("token should be cleared on 401")
	}
}

type flakyTransport struct {
	failFor int
	calls   int
	base    http.RoundTripper
}

func (t *flakyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls++
	if t.calls <= t.failFor {
		return nil, errors.New("temporary failure")
	}
	return t.base.RoundTrip(req)
}
