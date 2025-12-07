//go:build integration

package migrations

import (
	"context"
	"errors"
	"go/build"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"
	"time"

	pbclient "github.com/eqr/pbclient"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestRunnerIntegration_RunIdempotentAndAutoCreate(t *testing.T) {
	app, server := newPocketBaseServer(t)
	t.Cleanup(func() {
		server.Close()
		app.Cleanup()
	})

	client := newAuthedClient(t, server, app)
	runner := NewRunner(client)

	callCounts := map[string]int{}
	migrations := []Migration{
		stubMigration{name: "202401_alpha", up: func(pbclient.AuthenticatedClient) error {
			callCounts["202401_alpha"]++
			return nil
		}},
		stubMigration{name: "202402_beta", up: func(pbclient.AuthenticatedClient) error {
			callCounts["202402_beta"]++
			return nil
		}},
	}

	if err := runner.RegisterAll(migrations...); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run first pass: %v", err)
	}

	applied, err := runner.Applied(context.Background())
	if err != nil {
		t.Fatalf("Applied: %v", err)
	}
	if len(applied) != 2 {
		t.Fatalf("applied=%d want 2", len(applied))
	}

	// Idempotent second run should not call Up again or duplicate records.
	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run second pass: %v", err)
	}
	if callCounts["202401_alpha"] != 1 || callCounts["202402_beta"] != 1 {
		t.Fatalf("Up called more than once: %v", callCounts)
	}

	resp, err := client.Do(context.Background(), "GET", "/api/collections/"+defaultCollectionName, nil)
	if err != nil {
		t.Fatalf("check collection: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected collection 200, got %d", resp.StatusCode)
	}
}

func TestRunnerIntegration_RunStopsOnFailure(t *testing.T) {
	app, server := newPocketBaseServer(t)
	t.Cleanup(func() {
		server.Close()
		app.Cleanup()
	})

	client := newAuthedClient(t, server, app)
	runner := NewRunner(client)

	migrations := []Migration{
		stubMigration{name: "202401_ok"},
		stubMigration{name: "202402_fail", up: func(pbclient.AuthenticatedClient) error { return errors.New("boom") }},
		stubMigration{name: "202403_skip"},
	}

	if err := runner.RegisterAll(migrations...); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}

	err := runner.Run(context.Background())
	if err == nil {
		t.Fatalf("expected failure")
	}

	applied, errApplied := runner.Applied(context.Background())
	if errApplied != nil {
		t.Fatalf("Applied: %v", errApplied)
	}
	if len(applied) != 1 || applied[0].Name != "202401_ok" {
		t.Fatalf("unexpected applied after failure: %+v", applied)
	}
}

func TestRunnerIntegration_DownRemovesLatest(t *testing.T) {
	app, server := newPocketBaseServer(t)
	t.Cleanup(func() {
		server.Close()
		app.Cleanup()
	})

	client := newAuthedClient(t, server, app)
	runner := NewRunner(client)

	downCalls := make([]string, 0)
	migrations := []Migration{
		stubMigration{name: "202401_a", down: func(pbclient.AuthenticatedClient) error { downCalls = append(downCalls, "202401_a"); return nil }},
		stubMigration{name: "202402_b", down: func(pbclient.AuthenticatedClient) error { downCalls = append(downCalls, "202402_b"); return nil }},
		stubMigration{name: "202403_c", down: func(pbclient.AuthenticatedClient) error { downCalls = append(downCalls, "202403_c"); return nil }},
	}

	if err := runner.RegisterAll(migrations...); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if err := runner.Down(context.Background(), 2); err != nil {
		t.Fatalf("Down: %v", err)
	}

	applied, err := runner.Applied(context.Background())
	if err != nil {
		t.Fatalf("Applied: %v", err)
	}
	if len(applied) != 1 || applied[0].Name != "202401_a" {
		t.Fatalf("unexpected remaining applied: %+v", applied)
	}

	expect := []string{"202403_c", "202402_b"}
	if len(downCalls) != len(expect) {
		t.Fatalf("downCalls %v, want %v", downCalls, expect)
	}
	for i, name := range expect {
		if downCalls[i] != name {
			t.Fatalf("downCalls[%d]=%s want %s", i, downCalls[i], name)
		}
	}
}

// --- integration helpers ---

func newAuthedClient(t testing.TB, server *httptest.Server, app *tests.TestApp) pbclient.AuthenticatedClient {
	token := newAuthToken(t, app, core.CollectionNameSuperusers, "test@example.com")
	return tokenClientAdapter{
		baseURL: server.URL,
		token:   token,
		hc:      server.Client(),
	}
}

func newPocketBaseServer(t testing.TB) (*tests.TestApp, *httptest.Server) {
	dataDir := pocketBaseDataDir(t)
	app, err := tests.NewTestApp(dataDir)
	if err != nil {
		t.Fatalf("init test app: %v", err)
	}

	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}

	serveEvent := &core.ServeEvent{
		App:    app,
		Router: router,
	}

	if err := app.OnServe().Trigger(serveEvent, func(e *core.ServeEvent) error {
		return e.Next()
	}); err != nil {
		t.Fatalf("trigger serve event: %v", err)
	}

	mux, err := router.BuildMux()
	if err != nil {
		t.Fatalf("build mux: %v", err)
	}

	return app, httptest.NewServer(mux)
}

func newAuthToken(t testing.TB, app *tests.TestApp, collection, email string) string {
	record, err := app.FindAuthRecordByEmail(collection, email)
	if err != nil {
		t.Fatalf("find auth record: %v", err)
	}
	token, err := record.NewAuthToken()
	if err != nil {
		t.Fatalf("generate auth token: %v", err)
	}
	return token
}

func pocketBaseDataDir(t testing.TB) string {
	version := pocketBaseVersion()
	candidates := []string{
		filepath.Join("vendor", "github.com", "pocketbase", "pocketbase", "tests", "data"),
		filepath.Join(build.Default.GOPATH, "pkg", "mod", "github.com", "pocketbase", "pocketbase@"+version, "tests", "data"),
	}
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	t.Skipf("PocketBase test data not found (looked in %v)", candidates)
	return ""
}

type tokenClientAdapter struct {
	baseURL string
	token   string
	hc      *http.Client
}

func (c tokenClientAdapter) Do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.hc.Do(req)
}

func pocketBaseVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info != nil {
		for _, dep := range info.Deps {
			if dep.Path == "github.com/pocketbase/pocketbase" {
				return dep.Version
			}
		}
	}
	return "v0.31.0"
}
