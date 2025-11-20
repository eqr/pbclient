package pbclient

import (
	"errors"
	"strings"
	"testing"
)

func TestMapHTTPErrorSentinels(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		sentinel error
		contains string
	}{
		{
			name:     "bad request with message",
			status:   400,
			body:     `{"message":"invalid input"}`,
			sentinel: ErrBadRequest,
			contains: "invalid input",
		},
		{
			name:     "validation data message",
			status:   422,
			body:     `{"code":422,"data":{"name":{"message":"is required"}}}`,
			sentinel: ErrValidation,
			contains: "name: is required",
		},
		{
			name:     "not found text",
			status:   404,
			body:     "missing",
			sentinel: ErrNotFound,
			contains: "missing",
		},
		{
			name:     "server error",
			status:   500,
			body:     "boom",
			sentinel: ErrServer,
			contains: "boom",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			err := mapHTTPError(tt.status, []byte(tt.body))
			if !errors.Is(err, tt.sentinel) {
				t.Fatalf("expected sentinel %v, got %v", tt.sentinel, err)
			}
			if tt.contains != "" && !strings.Contains(err.Error(), tt.contains) {
				t.Fatalf("error message %q missing %q", err.Error(), tt.contains)
			}
		})
	}
}

func TestMapHTTPErrorPassThrough(t *testing.T) {
	if err := mapHTTPError(200, nil); err != nil {
		t.Fatalf("expected nil for success, got %v", err)
	}

	err := mapHTTPError(418, []byte("teapot"))
	if err == nil {
		t.Fatalf("expected error for non-mapped status")
	}
	if errors.Is(err, ErrBadRequest) || errors.Is(err, ErrServer) {
		t.Fatalf("unexpected sentinel for unmapped status: %v", err)
	}
	if !strings.Contains(err.Error(), "teapot") {
		t.Fatalf("expected body in error message, got %q", err.Error())
	}
}
