package pbclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type testRecord struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func newTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	client, err := NewClient(server.URL, "admin@example.com", "secret")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	client.tokenMutex.Lock()
	client.token = "test-token"
	client.tokenExpires = time.Now().Add(time.Hour)
	client.tokenMutex.Unlock()
	return client
}

func TestRepositoryGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/collections/test/records/123" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("missing auth header, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"123","name":"demo"}`))
	}))
	defer server.Close()

	client := newTestClient(t, server)
	repo := NewRepository[testRecord](client, "test")

	got, err := repo.Get(context.Background(), "123")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.ID != "123" || got.Name != "demo" {
		t.Fatalf("unexpected record: %+v", got)
	}
}

func TestRepositoryList_ComputesTotalPages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/collections/test/records" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"items":[{"id":"1","name":"a"},{"id":"2","name":"b"}],
			"page":2,
			"perPage":2,
			"totalItems":5
		}`))
	}))
	defer server.Close()

	client := newTestClient(t, server)
	repo := NewRepository[testRecord](client, "test")

	res, err := repo.List(context.Background(), ListOptions{Page: 2, PerPage: 2})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if res.Page != 2 || res.PerPage != 2 || res.TotalItems != 5 || res.TotalPages != 3 {
		t.Fatalf("unexpected pagination: %+v", res)
	}
	if len(res.Items) != 2 || res.Items[0].ID != "1" || res.Items[1].ID != "2" {
		t.Fatalf("unexpected items: %+v", res.Items)
	}
}

func TestRepositoryList_PropagatesNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	repo := NewRepository[testRecord](client, "test")

	_, err := repo.List(context.Background(), ListOptions{})
	if err == nil {
		t.Fatalf("expected error")
	}
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
