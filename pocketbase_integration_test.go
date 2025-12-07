//go:build integration

package pbclient

import (
	"context"
	"go/build"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

// This integration test spins up an in-process PocketBase instance using the
// upstream test harness to exercise basic repository CRUD against real routes.
func TestRepositoryAgainstPocketBase(t *testing.T) {
	app, server := newPocketBaseServer(t)
	t.Cleanup(func() {
		server.Close()
		app.Cleanup()
	})

	token := newAuthToken(t, app, core.CollectionNameSuperusers, "test@example.com")

	raw, err := NewClient(server.URL, WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client := &authenticatedClient{
		client:       raw.(*client),
		token:        token,
		tokenExpires: time.Now().Add(time.Hour),
	}

	repo := NewRepository[map[string]any](client, "demo2")

	ctx := context.Background()

	list, err := repo.List(ctx, ListOptions{PerPage: 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list.Items) == 0 {
		t.Fatalf("expected seeded demo records")
	}

	created, err := repo.Create(ctx, map[string]any{"title": "pbclient integration"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	createdMap := *created
	id, _ := createdMap["id"].(string)
	title, _ := createdMap["title"].(string)
	if id == "" || title != "pbclient integration" {
		t.Fatalf("Create returned unexpected record: %+v", createdMap)
	}

	fetched, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	fetchedMap := *fetched
	if fetchedMap["title"] != title {
		t.Fatalf("Get mismatch: got %v want %v", fetchedMap["title"], title)
	}

	updated, err := repo.Update(ctx, id, map[string]any{"title": "pbclient updated"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	updatedMap := *updated
	if updatedMap["title"] != "pbclient updated" {
		t.Fatalf("Update mismatch: got %v", updatedMap["title"])
	}

	if err := repo.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
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

func pocketBaseVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info != nil {
		for _, dep := range info.Deps {
			if dep.Path == "github.com/pocketbase/pocketbase" {
				return dep.Version
			}
		}
	}
	// fallback to known version in go.mod
	return "v0.31.0"
}
