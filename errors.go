package pbclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Sentinel errors returned for common HTTP statuses.
var (
	ErrBadRequest   = errors.New("bad request")
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrValidation   = errors.New("validation failed")
	ErrRateLimited  = errors.New("rate limited")
	ErrServer       = errors.New("server error")
)

// HTTPError captures the status and response message for non-2xx responses.
type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("http error: status %d", e.Status)
	}
	return fmt.Sprintf("http error: status %d: %s", e.Status, e.Message)
}

// pocketBaseError models the PocketBase error response structure.
type pocketBaseError struct {
	Code    int                `json:"code"`
	Message string             `json:"message"`
	Data    map[string]pbField `json:"data"`
}

type pbField struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

// mapHTTPError maps an HTTP status and optional body to meaningful errors.
// It returns sentinel errors when possible, wrapping them with parsed messages for context.
func mapHTTPError(status int, body []byte) error {
	msg := extractPBErrorMessage(body)
	if msg == "" && len(body) > 0 {
		msg = strings.TrimSpace(string(body))
	}

	switch status {
	case 400:
		return wrapIfMessage(ErrBadRequest, msg)
	case 401:
		return wrapIfMessage(ErrUnauthorized, msg)
	case 403:
		return wrapIfMessage(ErrForbidden, msg)
	case 404:
		return wrapIfMessage(ErrNotFound, msg)
	case 409:
		return wrapIfMessage(ErrConflict, msg)
	case 422:
		return wrapIfMessage(ErrValidation, msg)
	case 429:
		return wrapIfMessage(ErrRateLimited, msg)
	}

	if status >= 500 {
		return wrapIfMessage(ErrServer, msg)
	}

	if status >= 200 && status < 300 {
		return nil
	}

	return &HTTPError{Status: status, Message: msg}
}

func wrapIfMessage(sentinel error, msg string) error {
	if msg == "" {
		return sentinel
	}
	return fmt.Errorf("%w: %s", sentinel, msg)
}

func extractPBErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	var pbErr pocketBaseError
	if err := json.Unmarshal(body, &pbErr); err != nil {
		return ""
	}

	if m := strings.TrimSpace(pbErr.Message); m != "" {
		return m
	}

	if len(pbErr.Data) == 0 {
		return ""
	}

	fields := make([]string, 0, len(pbErr.Data))
	for field := range pbErr.Data {
		fields = append(fields, field)
	}
	sort.Strings(fields)

	for _, field := range fields {
		detail := pbErr.Data[field]
		if msg := strings.TrimSpace(detail.Message); msg != "" {
			return fmt.Sprintf("%s: %s", field, msg)
		}
		if detail.Code != "" {
			return fmt.Sprintf("%s: %s", field, detail.Code)
		}
	}

	return ""
}
