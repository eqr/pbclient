# PocketBase Client

Go client library for PocketBase with automatic admin auth, generic repository helpers, and a simple KV store built on collections.

## Installation

```bash
go get github.com/eqr/pbclient
```

## Quick Start

```go
package main

import (
	"context"
	"log"

	"github.com/eqr/pbclient"
)

type Todo struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

func main() {
	client, err := pbclient.NewClient(
		"https://your-pocketbase-host",
		"admin@example.com",
		"super-secret",
	)
	if err != nil {
		log.Fatal(err)
	}

	repo := pbclient.NewRepository[Todo](client, "todos")

	ctx := context.Background()

	created, err := repo.Create(ctx, Todo{Title: "try pbclient"})
	if err != nil {
		log.Fatal(err)
	}

	item, err := repo.Get(ctx, created.ID)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("got todo: %+v", item)
}
```

## Client Options

- `WithHTTPClient(*http.Client)`: reuse your own transport (e.g., tracing, custom TLS).
- `WithTimeout(time.Duration)`: set HTTP timeout.
- `WithRetry(maxRetries, backoff)`: retry 429/network errors with exponential backoff.
- `WithLogger(*slog.Logger)`: structured logging for auth and retries.

## Repository Usage

```go
repo := pbclient.NewRepository[Todo](client, "todos")

// List with filters, sorting, and field selection
todos, err := repo.List(ctx, pbclient.ListOptions{
	Page:   1,
	PerPage: 20,
	Filter: pbclient.And(pbclient.Eq("done", "false"), pbclient.Gt("priority", "3")),
	Sort:   "-created",
	Fields: []string{"id", "title", "done"},
})

// Update an item
updated, err := repo.Update(ctx, created.ID, Todo{Title: "updated title", Done: true})
```

## KV Store Usage

```go
kv := pbclient.NewKVStore(client, "kv_store") // collection defaults to "kv_store"

_ = kv.Set(ctx, "feature_flag", map[string]any{"name": "beta", "enabled": true})

var flag map[string]any
_ = kv.Get(ctx, "feature_flag", &flag)

exists, _ := kv.Exists(ctx, "feature_flag")
keys, _ := kv.List(ctx, "feature_")
_ = kv.Delete(ctx, "feature_flag")
```

## Filters

Helpers for PocketBase filter strings:

- `Eq/Neq/Gt/Gte/Lt/Lte`
- `And/Or` to combine conditions

Example: `pbclient.And(pbclient.Eq("status", "active"), pbclient.Gt("score", "10"))`

## Error Handling

Common HTTP statuses map to sentinel errors (`ErrBadRequest`, `ErrUnauthorized`, `ErrForbidden`, `ErrNotFound`, `ErrConflict`, `ErrValidation`, `ErrRateLimited`, `ErrServer`). Other statuses return `*HTTPError` with status/message.

## Thread Safety

`Client` is safe for concurrent use; token access is locked and retries respect context cancellation. Repository and KV helpers share the same client and rely on PocketBase for atomicity.

## Integration Tests

An in-process PocketBase integration test is provided (`pocketbase_integration_test.go`, build tag `integration`). Run with:

```bash
go test -tags=integration ./...
```

It reuses PocketBase's bundled test data under GOPATH.
