package migrations

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	pbclient "github.com/eqr/pbclient"
)

func TestRunExecutesInNameOrder(t *testing.T) {
	server := newMigrationTestServer(t)
	client := server.client()
	t.Cleanup(server.close)

	runner := NewRunner(client)

	calls := make([]string, 0)
	migrations := []Migration{
		stubMigration{name: "202503_add_c", up: func(*pbclient.Client) error {
			calls = append(calls, "202503_add_c")
			return nil
		}},
		stubMigration{name: "202401_add_a", up: func(*pbclient.Client) error {
			calls = append(calls, "202401_add_a")
			return nil
		}},
		stubMigration{name: "202502_add_b", up: func(*pbclient.Client) error {
			calls = append(calls, "202502_add_b")
			return nil
		}},
	}

	if err := runner.RegisterAll(migrations...); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	expect := []string{"202401_add_a", "202502_add_b", "202503_add_c"}
	if len(calls) != len(expect) {
		t.Fatalf("unexpected calls %v", calls)
	}
	for i, name := range expect {
		if calls[i] != name {
			t.Fatalf("call %d = %s want %s", i, calls[i], name)
		}
	}

	if len(server.records) != len(expect) {
		t.Fatalf("expected %d recorded migrations, got %d", len(expect), len(server.records))
	}
}

func TestRegisterDuplicate(t *testing.T) {
	runner := NewRunner(nil)

	m := stubMigration{name: "dup"}
	if err := runner.Register(m); err != nil {
		t.Fatalf("first Register: %v", err)
	}

	err := runner.Register(m)
	if !errors.Is(err, ErrDuplicateMigration) {
		t.Fatalf("expected ErrDuplicateMigration, got %v", err)
	}
}

func TestPendingFiltersApplied(t *testing.T) {
	server := newMigrationTestServer(t)
	server.collectionExists = true
	t.Cleanup(server.close)

	server.addRecord("202401_add_a", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	server.addRecord("202501_add_c", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	client := server.client()
	runner := NewRunner(client)

	if err := runner.RegisterAll(
		stubMigration{name: "202401_add_a"},
		stubMigration{name: "202402_add_b"},
		stubMigration{name: "202501_add_c"},
		stubMigration{name: "202601_add_d"},
	); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}

	pending, err := runner.Pending(context.Background())
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}

	names := make([]string, 0, len(pending))
	for _, m := range pending {
		names = append(names, m.Name())
	}

	expect := []string{"202402_add_b", "202601_add_d"}
	if len(names) != len(expect) {
		t.Fatalf("pending names %v, want %v", names, expect)
	}
	for i, name := range expect {
		if names[i] != name {
			t.Fatalf("pending[%d]=%s want %s", i, names[i], name)
		}
	}
}

func TestDownRollsBackLatestMigrations(t *testing.T) {
	server := newMigrationTestServer(t)
	server.collectionExists = true
	server.addRecord("202401_add_a", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	server.addRecord("202502_add_b", time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC))
	server.addRecord("202603_add_c", time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	t.Cleanup(server.close)

	client := server.client()
	runner := NewRunner(client)

	downCalls := make([]string, 0)
	migrations := []Migration{
		stubMigration{name: "202401_add_a", down: func(*pbclient.Client) error { downCalls = append(downCalls, "202401_add_a"); return nil }},
		stubMigration{name: "202502_add_b", down: func(*pbclient.Client) error { downCalls = append(downCalls, "202502_add_b"); return nil }},
		stubMigration{name: "202603_add_c", down: func(*pbclient.Client) error { downCalls = append(downCalls, "202603_add_c"); return nil }},
	}

	if err := runner.RegisterAll(migrations...); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}

	if err := runner.Down(context.Background(), 2); err != nil {
		t.Fatalf("Down: %v", err)
	}

	expectCalls := []string{"202603_add_c", "202502_add_b"}
	if len(downCalls) != len(expectCalls) {
		t.Fatalf("downCalls %v, want %v", downCalls, expectCalls)
	}
	for i, name := range expectCalls {
		if downCalls[i] != name {
			t.Fatalf("downCalls[%d]=%s want %s", i, downCalls[i], name)
		}
	}

	if len(server.records) != 1 {
		t.Fatalf("expected 1 remaining record, got %d", len(server.records))
	}
	if server.records[0].Name != "202401_add_a" {
		t.Fatalf("remaining record %s, want 202401_add_a", server.records[0].Name)
	}
}

func TestEnsureCollectionNotCreatedWhenAutoCreateDisabled(t *testing.T) {
	server := newMigrationTestServer(t)
	t.Cleanup(server.close)

	client := server.client()
	runner := NewRunner(client, WithAutoCreate(false))

	err := runner.Run(context.Background())
	if !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("expected ErrCollectionNotFound, got %v", err)
	}
	if server.collectionExists {
		t.Fatalf("collection should not have been created when autoCreate is false")
	}
}

// --- test helpers ---

type stubMigration struct {
	name string
	up   func(*pbclient.Client) error
	down func(*pbclient.Client) error
}

func (m stubMigration) Name() string { return m.name }
func (m stubMigration) Up(c *pbclient.Client) error {
	if m.up != nil {
		return m.up(c)
	}
	return nil
}
func (m stubMigration) Down(c *pbclient.Client) error {
	if m.down != nil {
		return m.down(c)
	}
	return nil
}

type migrationTestServer struct {
	t                *testing.T
	ts               *httptest.Server
	records          []Record
	nextID           int
	collectionExists bool
}

func newMigrationTestServer(t *testing.T) *migrationTestServer {
	s := &migrationTestServer{
		t:       t,
		records: make([]Record, 0),
		nextID:  1,
	}
	s.ts = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

func (s *migrationTestServer) client() *pbclient.Client {
	client, err := pbclient.NewClient(s.ts.URL, "admin@example.com", "password", pbclient.WithHTTPClient(s.ts.Client()))
	if err != nil {
		s.t.Fatalf("build client: %v", err)
	}
	return client
}

func (s *migrationTestServer) close() {
	s.ts.Close()
}

func (s *migrationTestServer) addRecord(name string, appliedAt time.Time) {
	s.records = append(s.records, Record{
		ID:        strconv.Itoa(s.nextID),
		Name:      name,
		AppliedAt: appliedAt,
	})
	s.nextID++
}

func (s *migrationTestServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/collections/users/auth-with-password" && r.Method == http.MethodPost {
		writeJSON(w, http.StatusOK, map[string]string{"token": "test-token"})
		return
	}

	if r.URL.Path == "/api/collections" && r.Method == http.MethodPost {
		s.handleCreateCollection(w, r)
		return
	}

	collectionPath := "/api/collections/" + defaultCollectionName
	if r.URL.Path == collectionPath {
		s.handleGetCollection(w, r)
		return
	}

	if strings.HasPrefix(r.URL.Path, collectionPath+"/records") {
		s.handleRecords(w, r)
		return
	}

	http.NotFound(w, r)
}

func (s *migrationTestServer) handleCreateCollection(w http.ResponseWriter, r *http.Request) {
	s.collectionExists = true
	writeJSON(w, http.StatusOK, map[string]any{"name": defaultCollectionName})
}

func (s *migrationTestServer) handleGetCollection(w http.ResponseWriter, _ *http.Request) {
	if !s.collectionExists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": defaultCollectionName})
}

func (s *migrationTestServer) handleRecords(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleList(w, r)
	case http.MethodPost:
		s.handleCreateRecord(w, r)
	case http.MethodDelete:
		s.handleDeleteRecord(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *migrationTestServer) handleList(w http.ResponseWriter, r *http.Request) {
	perPage := parseIntDefault(r.URL.Query().Get("perPage"), 30)
	page := parseIntDefault(r.URL.Query().Get("page"), 1)

	sorted := make([]Record, len(s.records))
	copy(sorted, s.records)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].AppliedAt.Before(sorted[j].AppliedAt)
	})

	totalItems := len(sorted)
	start := (page - 1) * perPage
	if start > totalItems {
		start = totalItems
	}
	end := start + perPage
	if end > totalItems {
		end = totalItems
	}
	items := sorted[start:end]

	totalPages := 0
	if perPage > 0 {
		totalPages = (totalItems + perPage - 1) / perPage
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items":      items,
		"page":       page,
		"perPage":    perPage,
		"totalItems": totalItems,
		"totalPages": totalPages,
	})
}

func (s *migrationTestServer) handleCreateRecord(w http.ResponseWriter, r *http.Request) {
	var rec Record
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	rec.ID = strconv.Itoa(s.nextID)
	s.nextID++
	if rec.AppliedAt.IsZero() {
		rec.AppliedAt = time.Now().UTC()
	}
	s.records = append(s.records, rec)

	writeJSON(w, http.StatusOK, rec)
}

func (s *migrationTestServer) handleDeleteRecord(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/collections/"+defaultCollectionName+"/records/")
	for idx, rec := range s.records {
		if rec.ID == id {
			s.records = append(s.records[:idx], s.records[idx+1:]...)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
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
