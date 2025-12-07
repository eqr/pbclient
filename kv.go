package pbclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const defaultKVCollection = "kv"

// KVStore offers simple key-value helpers backed by PocketBase.
type KVStore struct {
	client     AuthenticatedClient
	collection string
	appName    string
}

// NewKVStore creates a key-value store backed by the provided collection.
// If collection is empty, a default "kv" collection is used.
// appName scopes keys when the backing collection includes an "appname" field.
func NewKVStore(client AuthenticatedClient, collection string, appName string) KVStore {
	collection = strings.TrimSpace(collection)
	if collection == "" {
		collection = defaultKVCollection
	}
	return KVStore{
		client:     client,
		collection: collection,
		appName:    strings.TrimSpace(appName),
	}
}

// Set inserts or overwrites a value for the given key.
func (s KVStore) Set(ctx context.Context, key string, value interface{}) error {
	if s.client == nil {
		return errors.New("kv client is nil")
	}

	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("key is required")
	}

	valueBytes, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal value: %w", err)
	}

	id, err := s.getRecordIDByKey(ctx, key)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}

	// Use interface{} for value to support both text and JSON field types
	payload := map[string]interface{}{
		"key":     key,
		"value":   json.RawMessage(valueBytes),
		"appname": s.appName,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	method := http.MethodPost
	path := fmt.Sprintf("/api/collections/%s/records", url.PathEscape(s.collection))
	if id != "" {
		method = http.MethodPatch
		path += "/" + url.PathEscape(id)
	}

	resp, err := s.client.Do(ctx, method, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return decodeJSONResponse(resp, nil)
}

// Get fetches a value for the given key as raw JSON bytes.
// For collections using a text field, the returned bytes contain the decoded JSON string.
func (s KVStore) Get(ctx context.Context, key string) (json.RawMessage, error) {
	if s.client == nil {
		return nil, errors.New("kv client is nil")
	}

	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("key is required")
	}

	params := url.Values{}
	params.Set("filter", s.filterByKey(key))
	params.Set("perPage", "1")

	path := fmt.Sprintf("/api/collections/%s/records?%s", url.PathEscape(s.collection), params.Encode())
	resp, err := s.client.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var payload struct {
		Items []struct {
			Value json.RawMessage `json:"value"`
		} `json:"items"`
	}

	if err := decodeJSONResponse(resp, &payload); err != nil {
		return nil, err
	}

	if len(payload.Items) == 0 {
		return nil, ErrNotFound
	}

	// Try to unmarshal as direct JSON first (for JSON field type)
	var raw json.RawMessage
	if err := json.Unmarshal(payload.Items[0].Value, &raw); err == nil {
		return raw, nil
	}

	// Fall back to treating it as a JSON-encoded string (for text field type)
	var str string
	if err := json.Unmarshal(payload.Items[0].Value, &str); err != nil {
		return nil, fmt.Errorf("decode value: %w", err)
	}
	return json.RawMessage(str), nil
}

// GetInto fetches a value for the given key and unmarshals it into dest.
func (s KVStore) GetInto(ctx context.Context, key string, dest interface{}) error {
	if dest == nil {
		return fmt.Errorf("dest must be non-nil")
	}

	data, err := s.Get(ctx, key)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("decode value: %w", err)
	}

	return nil
}

// TypedKVStore provides typed helpers built on top of KVStore.
type TypedKVStore[T any] struct {
	store KVStore
}

// NewTypedKVStore creates a typed KV store bound to a PocketBase collection.
func NewTypedKVStore[T any](client AuthenticatedClient, collection string, appName string) TypedKVStore[T] {
	return TypedKVStore[T]{store: NewKVStore(client, collection, appName)}
}

// Set inserts or overwrites a value for the given key.
func (s TypedKVStore[T]) Set(ctx context.Context, key string, value T) error {
	return s.store.Set(ctx, key, value)
}

// Get fetches a value for the given key.
func (s TypedKVStore[T]) Get(ctx context.Context, key string) (T, error) {
	var zero T

	var out T
	if err := s.store.GetInto(ctx, key, &out); err != nil {
		return zero, err
	}
	return out, nil
}

// Delete removes a key. It is idempotent and returns nil if the key does not exist.
func (s TypedKVStore[T]) Delete(ctx context.Context, key string) error {
	return s.store.Delete(ctx, key)
}

// Exists returns true if a key exists.
func (s TypedKVStore[T]) Exists(ctx context.Context, key string) (bool, error) {
	return s.store.Exists(ctx, key)
}

// List returns all keys, optionally filtered by prefix.
func (s TypedKVStore[T]) List(ctx context.Context, prefix string) ([]string, error) {
	return s.store.List(ctx, prefix)
}

// Delete removes a key. It is idempotent and returns nil if the key does not exist.
func (s KVStore) Delete(ctx context.Context, key string) error {
	if s.client == nil {
		return errors.New("kv client is nil")
	}

	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("key is required")
	}

	id, err := s.getRecordIDByKey(ctx, key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}

	path := fmt.Sprintf("/api/collections/%s/records/%s", url.PathEscape(s.collection), url.PathEscape(id))
	resp, err := s.client.Do(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}

	return decodeJSONResponse(resp, nil)
}

// Exists returns true if a key exists.
func (s KVStore) Exists(ctx context.Context, key string) (bool, error) {
	id, err := s.getRecordIDByKey(ctx, key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return id != "", nil
}

// List returns all keys, optionally filtered by prefix.
func (s KVStore) List(ctx context.Context, prefix string) ([]string, error) {
	if s.client == nil {
		return nil, errors.New("kv client is nil")
	}

	keys := make([]string, 0)
	prefix = strings.TrimSpace(prefix)

	page := 1
	for {
		params := url.Values{}
		params.Set("page", strconv.Itoa(page))
		params.Set("perPage", "200")
		params.Set("fields", "id,key")
		filter := s.appNameFilter()
		if prefix != "" {
			prefixFilter := fmt.Sprintf("key~'%s%%'", escapeFilterValue(prefix))
			filter = And(filter, prefixFilter)
		}
		if filter != "" {
			params.Set("filter", filter)
		}

		path := fmt.Sprintf("/api/collections/%s/records?%s", url.PathEscape(s.collection), params.Encode())
		resp, err := s.client.Do(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		var payload struct {
			Items []struct {
				Key string `json:"key"`
			} `json:"items"`
			Page       int `json:"page"`
			TotalPages int `json:"totalPages"`
		}

		if err := decodeJSONResponse(resp, &payload); err != nil {
			return nil, err
		}

		for _, item := range payload.Items {
			keys = append(keys, item.Key)
		}

		if payload.TotalPages == 0 || payload.Page >= payload.TotalPages {
			break
		}
		page++
	}

	return keys, nil
}

// getRecordIDByKey returns the record ID for a key or ErrNotFound.
func (s KVStore) getRecordIDByKey(ctx context.Context, key string) (string, error) {
	if s.client == nil {
		return "", errors.New("kv client is nil")
	}

	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("key is required")
	}

	params := url.Values{}
	params.Set("filter", s.filterByKey(key))
	params.Set("perPage", "1")
	params.Set("fields", "id")

	path := fmt.Sprintf("/api/collections/%s/records?%s", url.PathEscape(s.collection), params.Encode())
	resp, err := s.client.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var payload struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}

	if err := decodeJSONResponse(resp, &payload); err != nil {
		return "", err
	}

	if len(payload.Items) == 0 {
		return "", ErrNotFound
	}

	return payload.Items[0].ID, nil
}

func (s KVStore) appNameFilter() string {
	if s.appName == "" {
		return ""
	}
	return Eq("appname", s.appName)
}

func (s KVStore) filterByKey(key string) string {
	return And(Eq("key", key), s.appNameFilter())
}
