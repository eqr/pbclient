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

const defaultKVCollection = "kv_store"

// KVStore offers simple key-value helpers backed by PocketBase.
type KVStore struct {
	client     *Client
	collection string
}

// NewKVStore creates a key-value store backed by the provided collection.
// If collection is empty, a default "kv_store" collection is used.
func NewKVStore(client *Client, collection string) *KVStore {
	collection = strings.TrimSpace(collection)
	if collection == "" {
		collection = defaultKVCollection
	}
	return &KVStore{
		client:     client,
		collection: collection,
	}
}

// Set inserts or overwrites a value for the given key.
func (s *KVStore) Set(ctx context.Context, key string, value interface{}) error {
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

	payload := map[string]string{
		"key":   key,
		"value": string(valueBytes),
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

	resp, err := s.client.doRequest(ctx, method, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return decodeJSONResponse(resp, nil)
}

// Get fetches a value for the given key into dest.
func (s *KVStore) Get(ctx context.Context, key string, dest interface{}) error {
	if s.client == nil {
		return errors.New("kv client is nil")
	}

	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("key is required")
	}

	params := url.Values{}
	params.Set("filter", Eq("key", key))
	params.Set("perPage", "1")

	path := fmt.Sprintf("/api/collections/%s/records?%s", url.PathEscape(s.collection), params.Encode())
	resp, err := s.client.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var payload struct {
		Items []struct {
			Value string `json:"value"`
		} `json:"items"`
	}

	if err := decodeJSONResponse(resp, &payload); err != nil {
		return err
	}

	if len(payload.Items) == 0 {
		return ErrNotFound
	}

	if dest == nil {
		return fmt.Errorf("dest must be non-nil")
	}

	if err := json.Unmarshal([]byte(payload.Items[0].Value), dest); err != nil {
		return fmt.Errorf("decode value: %w", err)
	}
	return nil
}

// Delete removes a key. It is idempotent and returns nil if the key does not exist.
func (s *KVStore) Delete(ctx context.Context, key string) error {
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
	resp, err := s.client.doRequest(ctx, http.MethodDelete, path, nil)
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
func (s *KVStore) Exists(ctx context.Context, key string) (bool, error) {
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
func (s *KVStore) List(ctx context.Context, prefix string) ([]string, error) {
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
		if prefix != "" {
			params.Set("filter", fmt.Sprintf("key~'%s%%'", escapeFilterValue(prefix)))
		}

		path := fmt.Sprintf("/api/collections/%s/records?%s", url.PathEscape(s.collection), params.Encode())
		resp, err := s.client.doRequest(ctx, http.MethodGet, path, nil)
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
func (s *KVStore) getRecordIDByKey(ctx context.Context, key string) (string, error) {
	if s.client == nil {
		return "", errors.New("kv client is nil")
	}

	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("key is required")
	}

	params := url.Values{}
	params.Set("filter", Eq("key", key))
	params.Set("perPage", "1")
	params.Set("fields", "id")

	path := fmt.Sprintf("/api/collections/%s/records?%s", url.PathEscape(s.collection), params.Encode())
	resp, err := s.client.doRequest(ctx, http.MethodGet, path, nil)
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
