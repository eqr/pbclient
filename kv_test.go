package pbclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestKVSetGetAndExists(t *testing.T) {
	server := newKVTestServer(t)
	client := server.client()
	defer server.close()

	store := NewTypedKVStore[string](client, "", "app")

	// Create new key
	if err := store.Set(context.Background(), "foo", "initial"); err != nil {
		t.Fatalf("Set create: %v", err)
	}

	// Update existing key
	if err := store.Set(context.Background(), "foo", "updated"); err != nil {
		t.Fatalf("Set update: %v", err)
	}

	value, err := store.Get(context.Background(), "foo")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if value != "updated" {
		t.Fatalf("Get returned %q, want %q", value, "updated")
	}

	exists, err := store.Exists(context.Background(), "foo")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		t.Fatalf("Expected key to exist")
	}
}

func TestKVDeleteAndIdempotency(t *testing.T) {
	server := newKVTestServer(t)
	client := server.client()
	defer server.close()

	store := NewKVStore(client, "", "app")

	if err := store.Set(context.Background(), "gone", "value"); err != nil {
		t.Fatalf("seed Set: %v", err)
	}

	if err := store.Delete(context.Background(), "gone"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Second delete should be a no-op
	if err := store.Delete(context.Background(), "gone"); err != nil {
		t.Fatalf("Delete idempotent: %v", err)
	}

	exists, err := store.Exists(context.Background(), "gone")
	if err != nil {
		t.Fatalf("Exists after delete: %v", err)
	}
	if exists {
		t.Fatalf("Expected key to be deleted")
	}
}

func TestKVListWithPrefix(t *testing.T) {
	server := newKVTestServer(t)
	client := server.client()
	defer server.close()

	store := NewKVStore(client, "", "app")

	for _, key := range []string{"apple", "apricot", "banana", "apartment"} {
        if err := store.Set(context.Background(), key, 1); err != nil {
            t.Fatalf("seed Set %s: %v", key, err)
        }
    }

	keys, err := store.List(context.Background(), "ap")
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(keys) != 3 {
		t.Fatalf("List returned %v, want 3 keys", keys)
	}
	expect := map[string]bool{"apple": true, "apricot": true, "apartment": true}
	for _, k := range keys {
		if !expect[k] {
			t.Fatalf("unexpected key %q in %v", k, keys)
		}
	}
}

func TestKVMarshalComplexValue(t *testing.T) {
	server := newKVTestServer(t)
	client := server.client()
	defer server.close()

	type data struct {
		Numbers []int `json:"numbers"`
		Flag    bool  `json:"flag"`
	}
	store := NewTypedKVStore[data](client, "", "app")

	want := data{Numbers: []int{1, 2, 3}, Flag: true}
	if err := store.Set(context.Background(), "complex", want); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := store.Get(context.Background(), "complex")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Numbers) != len(want.Numbers) || got.Flag != want.Flag {
		t.Fatalf("unexpected value: %+v", got)
	}
}

func TestKVListPagination(t *testing.T) {
	server := newKVTestServer(t)
	client := server.client()
	defer server.close()

	store := NewKVStore(client, "", "app")
	for i := 0; i < 250; i++ {
		key := "key" + strconv.Itoa(i)
		if err := store.Set(context.Background(), key, i); err != nil {
			t.Fatalf("seed Set %s: %v", key, err)
		}
	}

	keys, err := store.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 250 {
		t.Fatalf("expected 250 keys, got %d", len(keys))
	}
	if keys[0] != "key0" || keys[len(keys)-1] != "key99" {
		t.Fatalf("unexpected ordering, first %s last %s", keys[0], keys[len(keys)-1])
	}
}

// --- test helpers ---

type kvRecord struct {
	ID      string          `json:"id"`
	Key     string          `json:"key"`
	AppName string          `json:"appname"`
	Value   json.RawMessage `json:"value"`
}

type kvTestServer struct {
	t       *testing.T
	ts      *httptest.Server
	records map[string]kvRecord
	nextID  int
}

func newKVTestServer(t *testing.T) *kvTestServer {
	s := &kvTestServer{
		t:       t,
		records: make(map[string]kvRecord),
		nextID:  1,
	}
	s.ts = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

func (s *kvTestServer) storeKey(appName, key string) string {
	return strings.TrimSpace(appName) + "|" + key
}

func (s *kvTestServer) client() AuthenticatedClient {
	raw, err := NewClient(s.ts.URL, WithHTTPClient(s.ts.Client()))
	if err != nil {
		s.t.Fatalf("build client: %v", err)
	}
	return &authenticatedClient{
		client:       raw.(*client),
		token:        "test-token",
		tokenExpires: time.Now().Add(time.Hour),
	}
}

func (s *kvTestServer) close() {
	s.ts.Close()
}

func (s *kvTestServer) handle(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleList(w, r)
	case http.MethodPost:
		s.handleCreate(w, r)
	case http.MethodPatch:
		s.handleUpdate(w, r)
	case http.MethodDelete:
		s.handleDelete(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *kvTestServer) handleList(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("filter")
	perPage := parseIntDefault(r.URL.Query().Get("perPage"), 30)
	page := parseIntDefault(r.URL.Query().Get("page"), 1)

	appNameFilter := extractFieldValue(filter, "appname")
	var match func(kvRecord) bool
	if strings.Contains(filter, "key~") {
		prefix := extractPrefixFilter(filter)
		match = func(rec kvRecord) bool {
			return strings.HasPrefix(rec.Key, prefix)
		}
	} else if strings.Contains(filter, "key=") {
		expect := extractFieldValue(filter, "key")
		match = func(rec kvRecord) bool {
			return rec.Key == expect
		}
	} else {
		match = func(kvRecord) bool { return true }
	}

	keys := make([]string, 0, len(s.records))
	for key := range s.records {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	filtered := make([]kvRecord, 0, len(keys))
	for _, key := range keys {
		rec := s.records[key]
		if appNameFilter != "" && rec.AppName != appNameFilter {
			continue
		}
		if match(rec) {
			filtered = append(filtered, rec)
		}
	}

	totalItems := len(filtered)
	start := (page - 1) * perPage
	end := start + perPage
	if start > totalItems {
		start = totalItems
	}
	if end > totalItems {
		end = totalItems
	}

	items := filtered[start:end]
	totalPages := 0
	if perPage > 0 {
		totalPages = (totalItems + perPage - 1) / perPage
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items":      items,
		"page":       page,
		"perPage":    perPage,
		"totalItems": totalItems,
		"totalPages": totalPages,
	})
}

func (s *kvTestServer) handleCreate(w http.ResponseWriter, r *http.Request) {
	var payload kvRecord
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	payload.ID = strconv.Itoa(s.nextID)
	s.nextID++
	s.records[s.storeKey(payload.AppName, payload.Key)] = payload
	writeJSON(w, http.StatusOK, payload)
}

func (s *kvTestServer) handleUpdate(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	id := parts[len(parts)-1]
	var payload kvRecord
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	for key, rec := range s.records {
		if rec.ID == id {
			rec.Value = payload.Value
			if payload.AppName != "" {
				rec.AppName = payload.AppName
			}
			delete(s.records, key)
			s.records[s.storeKey(rec.AppName, rec.Key)] = rec
			writeJSON(w, http.StatusOK, rec)
			return
		}
	}
	http.NotFound(w, r)
}

func (s *kvTestServer) handleDelete(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	id := parts[len(parts)-1]
	for key, rec := range s.records {
		if rec.ID == id {
			delete(s.records, key)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func extractQuoted(filter string) string {
	start := strings.Index(filter, "'")
	end := strings.LastIndex(filter, "'")
	if start == -1 || end == -1 || end <= start+1 {
		return ""
	}
	return strings.ReplaceAll(filter[start+1:end], "\\'", "'")
}

func extractFieldValue(filter, field string) string {
	needle := field + "='"
	start := strings.Index(filter, needle)
	if start == -1 {
		return ""
	}
	start += len(needle)
	rest := filter[start:]
	end := strings.Index(rest, "'")
	if end == -1 {
		return ""
	}
	return strings.ReplaceAll(rest[:end], "\\'", "'")
}

func extractPrefixFilter(filter string) string {
	needle := "key~'"
	start := strings.Index(filter, needle)
	if start == -1 {
		return ""
	}
	rest := filter[start+len(needle):]
	end := strings.Index(rest, "%")
	if end == -1 {
		return strings.TrimSuffix(rest, "'")
	}
	return rest[:end]
}

func parseIntDefault(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	val, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return val
}
