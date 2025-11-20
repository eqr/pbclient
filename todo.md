# PocketBase Client Library - Roadmap

## Overview
A Go client library for PocketBase with automatic token management, generic repository pattern, and simple key-value store.

## Phase 1: Project Setup
- [x] Initialize go.mod with module name `github.com/eqr/pbclient`
- [x] Set up basic project structure:
  ```
  pbclient/
  ├── client.go          # Core client with authentication
  ├── client_test.go
  ├── repository.go      # Generic repository pattern
  ├── repository_test.go
  ├── kv.go             # Key-value store
  ├── kv_test.go
  ├── filter.go         # Filter building helpers
  ├── errors.go         # Error types
  ├── go.mod
  ├── go.sum
  ├── README.md
  └── examples/
      ├── repository_example.go
      └── kv_example.go
  ```
- [x] Create .gitignore for Go projects

## Phase 2: Core Client (`client.go`)

### Authentication & Token Management
- [x] Implement `Client` struct with:
  - `baseURL` string
  - `token` string
  - `tokenExpires` time.Time
  - `adminEmail` string
  - `adminPassword` string
  - `httpClient` *http.Client
  - `tokenMutex` sync.RWMutex
  - `logger` *slog.Logger (optional, nil-safe)

- [x] Implement `NewClient(baseURL, adminEmail, adminPassword string, opts ...ClientOption) (*Client, error)`
  - Validate inputs
  - Set default HTTP client with 30s timeout
  - Optional logger configuration
  - Do NOT authenticate on creation (lazy auth)

- [x] Implement `authenticate() error`
  - POST to `/api/collections/users/auth-with-password`
  - Parse token from response
  - Set `tokenExpires` to 23 hours from now
  - Log successful auth (if logger present)
  - Handle auth failures with detailed errors

- [x] Implement `ensureAuthenticated() error`
  - Check if token exists and is not expired
  - Use `tokenExpires.IsZero()` check for backward compatibility
  - Auto re-authenticate if expired
  - Log re-authentication events
  - Thread-safe with RWMutex

- [x] Implement token clearing on 401/403 responses
  - Clear token and expiry on authentication failures
  - Force re-auth on next request

### HTTP Operations
- [x] Implement `doRequest(method, path string, body io.Reader) (*http.Response, error)`
  - Ensure authentication before request
  - Set Authorization header
  - Set Content-Type to application/json
  - Return response for caller to handle

- [ ] Implement retry logic for transient failures (optional)
  - Retry on network errors
  - Retry on 429 (rate limit)
  - Exponential backoff

### Client Options
- [x] `WithHTTPClient(client *http.Client) ClientOption`
- [x] `WithLogger(logger *slog.Logger) ClientOption`
- [x] `WithTimeout(timeout time.Duration) ClientOption`

## Phase 3: Generic Repository Pattern (`repository.go`)

### Core Repository
- [x] Implement `Repository[T any]` struct:
  - `client` *Client
  - `collection` string

- [x] Implement `NewRepository[T any](client *Client, collection string) *Repository[T]`

### CRUD Operations
- [x] `Get(ctx context.Context, id string) (*T, error)`
  - GET `/api/collections/{collection}/records/{id}`
  - Decode JSON response into T
  - Return ErrNotFound if 404

- [x] `List(ctx context.Context, opts ListOptions) ([]T, error)`
  - GET `/api/collections/{collection}/records`
  - Support pagination (page, perPage)
  - Support filtering (filter string)
  - Support sorting (sort string)
  - Support field selection (fields []string)
  - Return list of T and pagination metadata

- [x] `Create(ctx context.Context, record T) (*T, error)`
  - POST `/api/collections/{collection}/records`
  - Marshal T to JSON
  - Return created record with ID

- [x] `Update(ctx context.Context, id string, record T) (*T, error)`
  - PATCH `/api/collections/{collection}/records/{id}`
  - Marshal T to JSON
  - Return updated record

- [x] `Delete(ctx context.Context, id string) error`
  - DELETE `/api/collections/{collection}/records/{id}`
  - Return nil on success, error otherwise

### Query Building
- [x] Implement `ListOptions` struct:
  - `Page` int
  - `PerPage` int
  - `Filter` string
  - `Sort` string
  - `Fields` []string

- [x] Implement `ListResult[T any]` struct:
  - `Items` []T
  - `Page` int
  - `PerPage` int
  - `TotalItems` int
  - `TotalPages` int

### Filter Helpers (`filter.go`)
- [ ] `Eq(field, value string) string` - field='value'
- [ ] `Neq(field, value string) string` - field!='value'
- [ ] `Gt(field, value string) string` - field>value
- [ ] `Gte(field, value string) string` - field>=value
- [ ] `Lt(field, value string) string` - field<value
- [ ] `Lte(field, value string) string` - field<=value
- [ ] `And(filters ...string) string` - (filter1 && filter2)
- [ ] `Or(filters ...string) string` - (filter1 || filter2)

## Phase 4: Key-Value Store (`kv.go`)

### KV Store Implementation
- [ ] Implement `KVStore` struct:
  - `client` *Client
  - `collection` string (default: "kv_store" or configurable)

- [ ] Implement `NewKVStore(client *Client, collection string) *KVStore`

### KV Operations
- [ ] `Set(ctx context.Context, key string, value interface{}) error`
  - Check if key exists (getRecordIDByKey)
  - If exists: PATCH `/api/collections/{collection}/records/{id}`
  - If not: POST `/api/collections/{collection}/records`
  - Marshal value to JSON string
  - Store as: `{"key": "...", "value": "..."}`

- [ ] `Get(ctx context.Context, key string, dest interface{}) error`
  - GET `/api/collections/{collection}/records?filter=(key='...')`
  - Parse JSON value into dest
  - Return ErrNotFound if key doesn't exist

- [ ] `Delete(ctx context.Context, key string) error`
  - Get record ID by key
  - DELETE `/api/collections/{collection}/records/{id}`
  - Return nil if key doesn't exist (idempotent)

- [ ] `Exists(ctx context.Context, key string) (bool, error)`
  - Check if key exists without fetching value
  - More efficient than Get for existence checks

- [ ] `List(ctx context.Context, prefix string) ([]string, error)`
  - List all keys with optional prefix filter
  - Return array of matching keys

### Helper Methods
- [ ] `getRecordIDByKey(key string) (string, error)`
  - Internal helper to get PocketBase record ID
  - Used by Set, Delete operations

## Phase 5: Error Handling (`errors.go`)

### Error Types
- [ ] `ErrNotFound` - Record/key not found
- [ ] `ErrUnauthorized` - Authentication failed
- [ ] `ErrBadRequest` - Invalid request
- [ ] `ErrConflict` - Unique constraint violation
- [ ] `ErrRateLimit` - Too many requests

### Error Handling
- [ ] Implement error wrapping with context
- [ ] Parse PocketBase error responses
- [ ] Map HTTP status codes to error types

## Phase 6: Testing

### Client Tests (`client_test.go`)
- [ ] Test authentication flow
- [ ] Test token expiration and refresh
- [ ] Test concurrent requests with token refresh
- [ ] Test auth failure handling
- [ ] Test 401/403 token clearing
- [ ] Mock HTTP responses with httptest

### Repository Tests (`repository_test.go`)
- [ ] Test CRUD operations
- [ ] Test List with pagination
- [ ] Test List with filters
- [ ] Test error handling (404, 401, etc.)
- [ ] Test type marshaling/unmarshaling
- [ ] Integration tests with real PocketBase (optional)

### KV Store Tests (`kv_test.go`)
- [ ] Test Set/Get operations
- [ ] Test upsert behavior (Set on existing key)
- [ ] Test Delete operations
- [ ] Test Exists operation
- [ ] Test List with prefix
- [ ] Test value marshaling for complex types
- [ ] Test concurrent access

### Test Coverage
- [ ] Aim for >80% code coverage
- [ ] Add table-driven tests
- [ ] Add benchmark tests for common operations

## Phase 7: Documentation

### README.md
- [ ] Overview and features
- [ ] Installation instructions
- [ ] Quick start guide
- [ ] Client configuration examples
- [ ] Repository pattern usage examples
- [ ] KV store usage examples
- [ ] Error handling guide
- [ ] Thread safety guarantees
- [ ] Comparison with existing libraries
- [ ] License (MIT or Apache 2.0)

### Code Documentation
- [ ] Add godoc comments to all public types
- [ ] Add godoc comments to all public methods
- [ ] Add usage examples in godoc
- [ ] Document thread safety

### Examples
- [ ] `examples/repository_example.go` - Full CRUD example
- [ ] `examples/kv_example.go` - KV store example
- [ ] Example with custom types
- [ ] Example with filters and pagination

## Phase 8: Release

### Pre-release Checklist
- [ ] All tests passing
- [ ] golangci-lint passing
- [ ] go fmt applied
- [ ] No TODOs in code
- [ ] README complete
- [ ] Examples working
- [ ] CHANGELOG.md created

### Release
- [ ] Tag v0.1.0
- [ ] Push to GitHub
- [ ] Create GitHub release with notes
- [ ] Announce in Go community (optional)

## Future Enhancements (Post v1.0)

### Advanced Features
- [ ] Real-time subscriptions support
- [ ] File upload/download helpers
- [ ] Batch operations
- [ ] Transaction support (if PocketBase adds it)
- [ ] Connection pooling optimization
- [ ] Metrics and observability hooks

### Performance
- [ ] Request caching layer
- [ ] Bulk operations optimization
- [ ] Connection keep-alive tuning

### Developer Experience
- [ ] CLI tool for schema generation
- [ ] Code generator from PocketBase schema
- [ ] Migration helpers
- [ ] Schema validation

## Design Decisions

### Token Expiration Strategy
- Set expiry to 23 hours (PocketBase tokens last 7 days)
- Proactive refresh before expiration
- Clear token on 401/403 for immediate re-auth

### Thread Safety
- Use sync.RWMutex for token access
- Client is safe for concurrent use
- KV operations are atomic at PocketBase level

### Error Handling
- Return sentinel errors for common cases
- Wrap errors with context
- Log errors if logger provided, never panic

### API Design Principles
- Simple, idiomatic Go
- Minimal dependencies (only stdlib + slog)
- Generic types for type safety
- Context-aware (accept context.Context)
- Composable (client -> repository/kv)

## Dependencies
- Standard library only
- `log/slog` for structured logging
- Consider: `github.com/google/go-cmp` for testing (optional)

## Testing Strategy
- Unit tests with mocked HTTP
- Integration tests with dockerized PocketBase
- Benchmarks for common operations
- Race detector in CI
