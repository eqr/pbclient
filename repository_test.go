package pbclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type testRecord struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func newTestClient(t *testing.T, server *httptest.Server) AuthenticatedClient {
	t.Helper()
	raw, err := NewClient(server.URL, WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	c := raw.(*client)
	return &authenticatedClient{
		client:       c,
		token:        "test-token",
		tokenExpires: time.Now().Add(time.Hour),
	}
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
		if r.URL.RawQuery != "page=2&perPage=2" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
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

func TestRepositoryCreateUpdateDelete(t *testing.T) {
	var createCalled, updateCalled, deleteCalled bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			createCalled = true
			body := readBody(t, r)
			if !strings.Contains(string(body), `"name":"demo"`) {
				t.Fatalf("unexpected create body: %s", string(body))
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"abc","name":"demo"}`))
		case http.MethodPatch:
			updateCalled = true
			if !strings.Contains(r.URL.Path, "/abc") {
				t.Fatalf("unexpected update path: %s", r.URL.Path)
			}
			body := readBody(t, r)
			if !strings.Contains(string(body), `"name":"changed"`) {
				t.Fatalf("unexpected update body: %s", string(body))
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"abc","name":"changed"}`))
		case http.MethodDelete:
			deleteCalled = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server)
	repo := NewRepository[testRecord](client, "test")

	created, err := repo.Create(context.Background(), testRecord{Name: "demo"})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if created.ID != "abc" || created.Name != "demo" {
		t.Fatalf("Create result mismatch: %+v", created)
	}

	updated, err := repo.Update(context.Background(), "abc", testRecord{Name: "changed"})
	if err != nil {
		t.Fatalf("Update error: %v", err)
	}
	if updated.Name != "changed" {
		t.Fatalf("Update result mismatch: %+v", updated)
	}

	if err := repo.Delete(context.Background(), "abc"); err != nil {
		t.Fatalf("Delete error: %v", err)
	}

	if !createCalled || !updateCalled || !deleteCalled {
		t.Fatalf("expected all CRUD handlers called (create:%v update:%v delete:%v)", createCalled, updateCalled, deleteCalled)
	}
}

func TestRepositoryList_FiltersAndFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("filter") != "name='john'" {
			t.Fatalf("unexpected filter: %s", q.Get("filter"))
		}
		if q.Get("fields") != "id,name" {
			t.Fatalf("unexpected fields: %s", q.Get("fields"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[],"page":1,"perPage":10,"totalItems":0,"totalPages":0}`))
	}))
	defer server.Close()

	client := newTestClient(t, server)
	repo := NewRepository[testRecord](client, "test")

	opts := ListOptions{
		Page:    1,
		PerPage: 10,
		Filter:  Eq("name", "john"),
		Fields:  []string{"id", "name"},
	}
	if _, err := repo.List(context.Background(), opts); err != nil {
		t.Fatalf("List error: %v", err)
	}
}

func TestRepositoryDeleteHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadRequest)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	repo := NewRepository[testRecord](client, "test")

	err := repo.Delete(context.Background(), "abc")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("expected ErrBadRequest, got %v", err)
	}
}

func readBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return data
}
