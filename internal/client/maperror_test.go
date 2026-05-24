package client

import (
	"errors"
	"strings"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// TestMapErrorEveryCode walks the api.ErrCode* surface and verifies that
// mapError returns a value satisfying errors.Is against the matching
// service sentinel. Adding a new code requires extending both the wire
// constants and this table.
func TestMapErrorEveryCode(t *testing.T) {
	cases := []struct {
		code     string
		sentinel error
	}{
		{api.ErrCodeMRAlreadyExists, service.ErrMRAlreadyExists},
		{api.ErrCodeNotFound, service.ErrNotFound},
		{api.ErrCodeConflict, service.ErrConflict},
		{api.ErrCodeAgentLimit, service.ErrAgentLimit},
		{api.ErrCodeNoForge, service.ErrNoForge},
		{api.ErrCodeRebaseInProgress, service.ErrRebaseInProgress},
		{api.ErrCodeNoRebaseInProgress, service.ErrNoRebaseInProgress},
		{api.ErrCodePermissionDenied, service.ErrPermissionDenied},
		{api.ErrCodeUnavailable, service.ErrUnavailable},
		{api.ErrCodeCanceled, service.ErrCanceled},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			err := mapError(tc.code, "something went wrong")
			if err == nil {
				t.Fatalf("mapError(%q) returned nil", tc.code)
			}
			if !errors.Is(err, tc.sentinel) {
				t.Errorf("errors.Is(%v, %v) = false; want true", err, tc.sentinel)
			}
			// The human message should be preserved through %w wrapping.
			if !strings.Contains(err.Error(), "something went wrong") {
				t.Errorf("error message lost wrapping: %q", err.Error())
			}
		})
	}
}

// TestMapErrorBadRequestAndInternal verifies the non-sentinel codes return
// plain errors (no sentinel to wrap) but still preserve the message.
func TestMapErrorBadRequestAndInternal(t *testing.T) {
	for _, code := range []string{api.ErrCodeBadRequest, api.ErrCodeInternal, "TOTALLY_UNKNOWN"} {
		err := mapError(code, "boom")
		if err == nil {
			t.Errorf("mapError(%q) returned nil", code)
			continue
		}
		// Must not satisfy any sentinel.
		for _, s := range []error{
			service.ErrMRAlreadyExists, service.ErrNotFound, service.ErrConflict,
			service.ErrAgentLimit, service.ErrNoForge, service.ErrRebaseInProgress,
			service.ErrNoRebaseInProgress, service.ErrPermissionDenied,
			service.ErrUnavailable, service.ErrCanceled,
		} {
			if errors.Is(err, s) {
				t.Errorf("code=%q accidentally matched sentinel %v", code, s)
			}
		}
		if !strings.Contains(err.Error(), "boom") {
			t.Errorf("error message lost: %q", err.Error())
		}
	}
}

// TestMapErrorEmptyMessage exercises the message-defaulting branch: when
// msg is empty mapError uses code as the human text so users at least see
// the wire code.
func TestMapErrorEmptyMessage(t *testing.T) {
	err := mapError(api.ErrCodeNotFound, "")
	if err == nil {
		t.Fatal("nil error")
	}
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("sentinel mismatch: %v", err)
	}
	if !strings.Contains(err.Error(), api.ErrCodeNotFound) {
		t.Errorf("expected message to mention code: %q", err.Error())
	}
}
