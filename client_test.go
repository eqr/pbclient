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
