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

type pbError struct {
	Message string
	Fields  []string
}

// mapHTTPError maps an HTTP status and optional body to meaningful errors.
// It returns sentinel errors when possible, wrapping them with parsed messages for context.
func mapHTTPError(status int, body []byte) error {
	errInfo := parsePBError(body)
	msg := errInfo.Message
	if msg == "" && len(body) > 0 {
		msg = strings.TrimSpace(string(body))
	}
	if msg == "" && len(errInfo.Fields) > 0 {
		msg = strings.Join(errInfo.Fields, "; ")
	} else if msg != "" && len(errInfo.Fields) > 0 {
		msg = msg + ": " + strings.Join(errInfo.Fields, "; ")
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

func parsePBError(body []byte) pbError {
	if len(body) == 0 {
		return pbError{}
	}

	var pbErr pocketBaseError
	if err := json.Unmarshal(body, &pbErr); err != nil {
		return pbError{}
	}

	var fields []string
	if len(pbErr.Data) > 0 {
		fieldNames := make([]string, 0, len(pbErr.Data))
		for field := range pbErr.Data {
			fieldNames = append(fieldNames, field)
		}
		sort.Strings(fieldNames)
		for _, field := range fieldNames {
			detail := pbErr.Data[field]
			if msg := strings.TrimSpace(detail.Message); msg != "" {
				fields = append(fields, fmt.Sprintf("%s: %s", field, msg))
			} else if detail.Code != "" {
				fields = append(fields, fmt.Sprintf("%s: %s", field, detail.Code))
			}
		}
	}

	return pbError{
		Message: strings.TrimSpace(pbErr.Message),
		Fields:  fields,
	}
}
