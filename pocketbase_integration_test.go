//go:build integration

package pbclient

import (
	"context"
	"net/http/httptest"
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

	client, err := NewClient(server.URL, "test@example.com", "ignored", WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.token = token
	client.tokenExpires = time.Now().Add(time.Hour)

	type demoRecord struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}

	repo := NewRepository[demoRecord](client, "demo1")

	ctx := context.Background()

	list, err := repo.List(ctx, ListOptions{PerPage: 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list.Items) == 0 {
		t.Fatalf("expected seeded demo records")
	}

	created, err := repo.Create(ctx, demoRecord{Text: "pbclient integration"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" || created.Text != "pbclient integration" {
		t.Fatalf("Create returned unexpected record: %+v", created)
	}

	fetched, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if fetched.Text != created.Text {
		t.Fatalf("Get mismatch: got %q want %q", fetched.Text, created.Text)
	}

	updated, err := repo.Update(ctx, created.ID, demoRecord{Text: "pbclient updated"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Text != "pbclient updated" {
		t.Fatalf("Update mismatch: got %q", updated.Text)
	}

	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func newPocketBaseServer(t testing.TB) (*tests.TestApp, *httptest.Server) {
	app, err := tests.NewTestApp()
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
