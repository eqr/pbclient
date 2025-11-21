package migrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	pbclient "github.com/eqr/pbclient"
)

// Runner executes registered migrations against PocketBase and records progress.
type Runner struct {
	client         *pbclient.Client
	migrations     []Migration
	collectionName string
	byName         map[string]Migration
	autoCreate     bool
}

const ruleAuthenticated = "@request.auth.id != ''"

// Option configures the Runner.
type Option func(*Runner)

// WithCollectionName overrides the default migrations collection name.
func WithCollectionName(name string) Option {
	trimmed := strings.TrimSpace(name)
	return func(r *Runner) {
		if trimmed != "" {
			r.collectionName = trimmed
		}
	}
}

// WithAutoCreate controls whether the migrations collection is created automatically when missing.
// Defaults to true.
func WithAutoCreate(autoCreate bool) Option {
	return func(r *Runner) {
		r.autoCreate = autoCreate
	}
}

// NewRunner constructs a Runner with optional configuration.
func NewRunner(client *pbclient.Client, opts ...Option) *Runner {
	r := &Runner{
		client:         client,
		collectionName: defaultCollectionName,
		byName:         make(map[string]Migration),
		autoCreate:     true,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}

	if r.collectionName == "" {
		r.collectionName = defaultCollectionName
	}

	return r
}

// Register adds a single migration, ensuring unique names.
func (r *Runner) Register(m Migration) error {
	if m == nil {
		return errors.New("migration is nil")
	}

	name := strings.TrimSpace(m.Name())
	if name == "" {
		return errors.New("migration name is required")
	}

	if _, exists := r.byName[name]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateMigration, name)
	}

	r.byName[name] = m
	r.migrations = append(r.migrations, m)
	return nil
}

// RegisterAll adds multiple migrations in order.
func (r *Runner) RegisterAll(migrations ...Migration) error {
	for _, m := range migrations {
		if err := r.Register(m); err != nil {
			return err
		}
	}
	return nil
}

// Run executes pending migrations in name order.
func (r *Runner) Run(ctx context.Context) error {
	if err := r.ensureCollection(ctx); err != nil {
		return err
	}

	applied, err := r.fetchApplied(ctx)
	if err != nil {
		return err
	}

	appliedNames := make(map[string]struct{}, len(applied))
	for _, rec := range applied {
		appliedNames[rec.Name] = struct{}{}
	}

	for _, m := range r.sortedMigrations() {
		name := strings.TrimSpace(m.Name())
		if name == "" {
			continue
		}
		if _, ok := appliedNames[name]; ok {
			continue
		}

		if err := m.Up(r.client); err != nil {
			return fmt.Errorf("%v: %s: %w", ErrMigrationFailed, name, err)
		}
		if err := r.recordMigration(ctx, name); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}
	}

	return nil
}

// Pending returns registered migrations that have not been applied.
func (r *Runner) Pending(ctx context.Context) ([]Migration, error) {
	if err := r.ensureCollection(ctx); err != nil {
		return nil, err
	}

	applied, err := r.fetchApplied(ctx)
	if err != nil {
		return nil, err
	}

	appliedNames := make(map[string]struct{}, len(applied))
	for _, rec := range applied {
		appliedNames[rec.Name] = struct{}{}
	}

	pending := make([]Migration, 0)
	for _, m := range r.sortedMigrations() {
		name := strings.TrimSpace(m.Name())
		if name == "" {
			continue
		}
		if _, ok := appliedNames[name]; !ok {
			pending = append(pending, m)
		}
	}

	return pending, nil
}

// Applied returns the migration records stored in PocketBase.
func (r *Runner) Applied(ctx context.Context) ([]Record, error) {
	if err := r.ensureCollection(ctx); err != nil {
		return nil, err
	}
	return r.fetchApplied(ctx)
}

// Down rolls back the latest n applied migrations.
func (r *Runner) Down(ctx context.Context, n int) error {
	if n <= 0 {
		return nil
	}

	if err := r.ensureCollection(ctx); err != nil {
		return err
	}

	applied, err := r.fetchApplied(ctx)
	if err != nil {
		return err
	}

	sort.Slice(applied, func(i, j int) bool {
		return applied[i].AppliedAt.After(applied[j].AppliedAt)
	})

	if n > len(applied) {
		n = len(applied)
	}

	for i := 0; i < n; i++ {
		rec := applied[i]
		mig := r.byName[rec.Name]
		if mig == nil {
			return fmt.Errorf("%w: %s", ErrMigrationNotFound, rec.Name)
		}

		if err := mig.Down(r.client); err != nil {
			return fmt.Errorf("%v: %s: %w", ErrMigrationFailed, rec.Name, err)
		}

		if err := r.deleteMigration(ctx, rec); err != nil {
			return fmt.Errorf("delete migration %s: %w", rec.Name, err)
		}
	}

	return nil
}

func (r *Runner) sortedMigrations() []Migration {
	copySlice := make([]Migration, len(r.migrations))
	copy(copySlice, r.migrations)

	sort.Slice(copySlice, func(i, j int) bool {
		return strings.TrimSpace(copySlice[i].Name()) < strings.TrimSpace(copySlice[j].Name())
	})

	return copySlice
}

func (r *Runner) ensureCollection(ctx context.Context) error {
	if r.client == nil {
		return errors.New("runner client is nil")
	}

	name := strings.TrimSpace(r.collectionName)
	if name == "" {
		return errors.New("collection name is required")
	}

	path := fmt.Sprintf("/api/collections/%s", url.PathEscape(name))
	resp, err := r.client.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return fmt.Errorf("read collection response: %w", readErr)
	}

	if resp.StatusCode == http.StatusNotFound {
		if !r.autoCreate {
			return ErrCollectionNotFound
		}
		return r.createCollection(ctx, name)
	}

	msg := strings.TrimSpace(string(body))
	return &pbclient.HTTPError{Status: resp.StatusCode, Message: msg}
}

func (r *Runner) createCollection(ctx context.Context, name string) error {
	payload := map[string]interface{}{
		"name":       name,
		"type":       "base",
		"listRule":   ruleAuthenticated,
		"viewRule":   ruleAuthenticated,
		"createRule": ruleAuthenticated,
		"updateRule": ruleAuthenticated,
		"deleteRule": ruleAuthenticated,
		"fields": []map[string]interface{}{
			{"name": "name", "type": "text", "required": true},
			{"name": "applied_at", "type": "date", "required": true},
		},
		"indexes": []string{fmt.Sprintf("CREATE UNIQUE INDEX idx_%s_name ON %s(name)", name, name)},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode collection payload: %w", err)
	}

	resp, err := r.client.Do(ctx, http.MethodPost, "/api/collections", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return fmt.Errorf("read collection response: %w", readErr)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(respBody))
		return &pbclient.HTTPError{Status: resp.StatusCode, Message: msg}
	}

	return nil
}

func (r *Runner) fetchApplied(ctx context.Context) ([]Record, error) {
	if r.client == nil {
		return nil, errors.New("runner client is nil")
	}

	repo := pbclient.NewRepository[Record](r.client, r.collectionName)
	all := make([]Record, 0)

	page := 1
	for {
		res, err := repo.List(ctx, pbclient.ListOptions{
			Page:    page,
			PerPage: 200,
			Fields:  []string{"id", "name", "applied_at"},
		})
		if err != nil {
			return nil, err
		}

		all = append(all, res.Items...)
		if res.TotalPages == 0 || res.Page >= res.TotalPages {
			break
		}
		page++
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].AppliedAt.Before(all[j].AppliedAt)
	})

	return all, nil
}

func (r *Runner) recordMigration(ctx context.Context, name string) error {
	repo := pbclient.NewRepository[Record](r.client, r.collectionName)
	_, err := repo.Create(ctx, Record{
		Name:      name,
		AppliedAt: time.Now().UTC(),
	})
	return err
}

func (r *Runner) deleteMigration(ctx context.Context, record Record) error {
	repo := pbclient.NewRepository[Record](r.client, r.collectionName)
	id := strings.TrimSpace(record.ID)
	if id == "" {
		return fmt.Errorf("%w: missing id for %s", ErrMigrationNotFound, record.Name)
	}
	return repo.Delete(ctx, id)
}
