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

	rawClient, err := NewClient(ts.URL, WithHTTPClient(ts.Client()), WithRetry(2, 10*time.Millisecond))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c := rawClient.(*client)
	client := &authenticatedClient{
		client:       c,
		token:        "token",
		tokenExpires: time.Now().Add(time.Hour),
	}

	resp, err := client.Do(context.Background(), http.MethodGet, "/test", nil)
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
	rawClient, err := NewClient(ts.URL, WithHTTPClient(httpClient), WithRetry(2, 5*time.Millisecond))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c := rawClient.(*client)
	client := &authenticatedClient{
		client:       c,
		token:        "token",
		tokenExpires: time.Now().Add(time.Hour),
	}

	resp, err := client.Do(context.Background(), http.MethodGet, "/test", nil)
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

	rawClient, err := NewClient(ts.URL, WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c := rawClient.(*client)
	ac := &authenticatedClient{
		client:       c,
		creds:        Credentials{Email: "admin@example.com", Password: "password"},
		authEndpoint: userAuthEndpoint,
	}

	// success
	if err := ac.reauthenticate(); err != nil {
		t.Fatalf("authenticate success: %v", err)
	}
	if ac.readToken() != "tok1" {
		t.Fatalf("expected token tok1, got %q", ac.readToken())
	}
	if ac.tokenExpires.IsZero() {
		t.Fatalf("expected token expiry set")
	}

	// failure clears token
	if err := ac.reauthenticate(); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
	if ac.readToken() != "" {
		t.Fatalf("token should be cleared after failure")
	}
	if !ac.tokenExpires.IsZero() {
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

	rawClient, err := NewClient(ts.URL, WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c := rawClient.(*client)
	client := &authenticatedClient{
		client:       c,
		creds:        Credentials{Email: "admin@example.com", Password: "password"},
		authEndpoint: userAuthEndpoint,
		token:        "stale",
		tokenExpires: time.Now().Add(-time.Minute),
	}

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

	rawClient, err := NewClient(ts.URL, WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c := rawClient.(*client)
	client := &authenticatedClient{
		client:       c,
		creds:        Credentials{Email: "admin@example.com", Password: "password"},
		authEndpoint: userAuthEndpoint,
		token:        "token",
		tokenExpires: time.Now().Add(time.Hour),
	}

	resp, err := client.Do(context.Background(), http.MethodGet, "/anything", nil)
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

func TestEnsureAuthenticatedSingleFlight(t *testing.T) {
	var authCalls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/collections/users/auth-with-password" {
			http.NotFound(w, r)
			return
		}
		authCalls++
		_, _ = w.Write([]byte(`{"token":"once"}`))
	}))
	defer ts.Close()

	rawClient, err := NewClient(ts.URL, WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client := &authenticatedClient{
		client:       rawClient.(*client),
		creds:        Credentials{Email: "admin@example.com", Password: "password"},
		authEndpoint: userAuthEndpoint,
		tokenExpires: time.Now().Add(-time.Hour),
	}

	start := make(chan struct{})
	done := make(chan struct{}, 2)

	go func() {
		<-start
		_ = client.ensureAuthenticated()
		done <- struct{}{}
	}()
	go func() {
		<-start
		_ = client.ensureAuthenticated()
		done <- struct{}{}
	}()

	close(start)
	<-done
	<-done

	if authCalls != 1 {
		t.Fatalf("expected single authentication call, got %d", authCalls)
	}
	if client.readToken() != "once" {
		t.Fatalf("token not set correctly, got %q", client.readToken())
	}
}
