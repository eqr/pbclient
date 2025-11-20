package pbclient

import (
    "errors"
    "fmt"
)

// Sentinel errors returned for common HTTP statuses.
var (
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

// classifyHTTPError maps an HTTP status code to a sentinel error when possible.
func classifyHTTPError(status int) error {
    switch status {
    case 400:
        return ErrValidation
    case 401:
        return ErrUnauthorized
    case 403:
        return ErrForbidden
    case 404:
        return ErrNotFound
    case 409:
        return ErrConflict
    case 429:
        return ErrRateLimited
    }
    if status >= 500 {
        return ErrServer
    }
    return nil
}
