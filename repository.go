package pbclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Repository exposes CRUD helpers for PocketBase collections.
type Repository[T any] struct {
	client     AuthenticatedClient
	collection string
}

// NewRepository creates a repository bound to a PocketBase collection.
func NewRepository[T any](client AuthenticatedClient, collection string) *Repository[T] {
	return &Repository[T]{
		client:     client,
		collection: strings.TrimSpace(collection),
	}
}

// ListOptions describes pagination and filtering options for list calls.
type ListOptions struct {
	Page    int
	PerPage int
	Filter  string
	Sort    string
	Fields  []string
}

// ListResult contains a page of items with pagination metadata.
type ListResult[T any] struct {
	Items      []T
	Page       int
	PerPage    int
	TotalItems int
	TotalPages int
}

// Get fetches a single record by ID.
func (r *Repository[T]) Get(ctx context.Context, id string) (*T, error) {
	if r.client == nil {
		return nil, errors.New("repository client is nil")
	}
	if r.collection == "" {
		return nil, errors.New("collection is required")
	}
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("id is required")
	}

	path := fmt.Sprintf("/api/collections/%s/records/%s", url.PathEscape(r.collection), url.PathEscape(id))
	resp, err := r.client.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out T
	if err := decodeJSONResponse(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// List returns a page of records using the provided options.
func (r *Repository[T]) List(ctx context.Context, opts ListOptions) (*ListResult[T], error) {
	if r.client == nil {
		return nil, errors.New("repository client is nil")
	}
	if r.collection == "" {
		return nil, errors.New("collection is required")
	}

	params := url.Values{}
	if opts.Page > 0 {
		params.Set("page", strconv.Itoa(opts.Page))
	}
	if opts.PerPage > 0 {
		params.Set("perPage", strconv.Itoa(opts.PerPage))
	}
	if opts.Filter != "" {
		params.Set("filter", opts.Filter)
	}
	if opts.Sort != "" {
		params.Set("sort", opts.Sort)
	}
	if len(opts.Fields) > 0 {
		params.Set("fields", strings.Join(opts.Fields, ","))
	}

	path := fmt.Sprintf("/api/collections/%s/records", url.PathEscape(r.collection))
	if encoded := params.Encode(); encoded != "" {
		path += "?" + encoded
	}

	resp, err := r.client.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var payload struct {
		Items      []T `json:"items"`
		Page       int `json:"page"`
		PerPage    int `json:"perPage"`
		TotalItems int `json:"totalItems"`
		TotalPages int `json:"totalPages"`
	}

	if err := decodeJSONResponse(resp, &payload); err != nil {
		return nil, err
	}

	totalPages := payload.TotalPages
	if totalPages == 0 && payload.PerPage > 0 {
		totalPages = (payload.TotalItems + payload.PerPage - 1) / payload.PerPage
	}

	return &ListResult[T]{
		Items:      payload.Items,
		Page:       payload.Page,
		PerPage:    payload.PerPage,
		TotalItems: payload.TotalItems,
		TotalPages: totalPages,
	}, nil
}

// Create inserts a new record.
func (r *Repository[T]) Create(ctx context.Context, record T) (*T, error) {
	if r.client == nil {
		return nil, errors.New("repository client is nil")
	}
	if r.collection == "" {
		return nil, errors.New("collection is required")
	}

	payload, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("marshal record: %w", err)
	}

	path := fmt.Sprintf("/api/collections/%s/records", url.PathEscape(r.collection))
	resp, err := r.client.Do(ctx, http.MethodPost, path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var created T
	if err := decodeJSONResponse(resp, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

// Update patches an existing record.
func (r *Repository[T]) Update(ctx context.Context, id string, record T) (*T, error) {
	if r.client == nil {
		return nil, errors.New("repository client is nil")
	}
	if r.collection == "" {
		return nil, errors.New("collection is required")
	}
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("id is required")
	}

	payload, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("marshal record: %w", err)
	}

	path := fmt.Sprintf("/api/collections/%s/records/%s", url.PathEscape(r.collection), url.PathEscape(id))
	resp, err := r.client.Do(ctx, http.MethodPatch, path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var updated T
	if err := decodeJSONResponse(resp, &updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

// Delete removes a record by ID.
func (r *Repository[T]) Delete(ctx context.Context, id string) error {
	if r.client == nil {
		return errors.New("repository client is nil")
	}
	if r.collection == "" {
		return errors.New("collection is required")
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("id is required")
	}

	path := fmt.Sprintf("/api/collections/%s/records/%s", url.PathEscape(r.collection), url.PathEscape(id))
	resp, err := r.client.Do(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return fmt.Errorf("delete failed: status %d: %w", resp.StatusCode, readErr)
	}

	return mapHTTPError(resp.StatusCode, body)
}

// decodeJSONResponse reads and decodes the response, mapping HTTP errors to sentinel values.
func decodeJSONResponse(resp *http.Response, dst any) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return mapHTTPError(resp.StatusCode, body)
	}

	if dst == nil || len(body) == 0 {
		return nil
	}

	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
