package api

import "net/http"

// Error code constants. These are the stable string values that travel in the
// `code` field of an APIResponse on every non-2xx HTTP response, and on every
// WS "error" frame. The remote client SDK maps each code back to the matching
// sentinel in internal/service so views can use errors.Is end-to-end.
//
// Whenever a new sentinel is added in internal/service/errors.go, add the code
// here and extend ErrCodeStatus / both decoder helpers.
const (
	ErrCodeMRAlreadyExists    = "MR_ALREADY_EXISTS"
	ErrCodeNotFound           = "NOT_FOUND"
	ErrCodeConflict           = "CONFLICT"
	ErrCodeAgentLimit         = "AGENT_LIMIT"
	ErrCodeNoForge            = "NO_FORGE"
	ErrCodeRebaseInProgress   = "REBASE_IN_PROGRESS"
	ErrCodeNoRebaseInProgress = "NO_REBASE_IN_PROGRESS"
	ErrCodePermissionDenied   = "PERMISSION_DENIED"
	ErrCodeUnavailable        = "UNAVAILABLE"
	ErrCodeCanceled           = "CANCELED"
	ErrCodeBadRequest         = "BAD_REQUEST"
	ErrCodeInternal           = "INTERNAL"
)

// StatusClosedRequest is the non-standard 499 "Client Closed Request" status
// used for context-cancellation errors. Matches nginx convention and avoids
// overlap with 408 Request Timeout.
const StatusClosedRequest = 499

// ErrCodeStatus maps a stable error code to the HTTP status it travels on.
// Unknown codes fall through to 500. Use HTTPStatusForCode for lookup; this
// map is exposed only for tests and tooling.
var ErrCodeStatus = map[string]int{
	ErrCodeMRAlreadyExists:    http.StatusConflict,
	ErrCodeNotFound:           http.StatusNotFound,
	ErrCodeConflict:           http.StatusConflict,
	ErrCodeAgentLimit:         http.StatusTooManyRequests,
	ErrCodeNoForge:            http.StatusNotFound,
	ErrCodeRebaseInProgress:   http.StatusConflict,
	ErrCodeNoRebaseInProgress: http.StatusConflict,
	ErrCodePermissionDenied:   http.StatusUnauthorized,
	ErrCodeUnavailable:        http.StatusServiceUnavailable,
	ErrCodeCanceled:           StatusClosedRequest,
	ErrCodeBadRequest:         http.StatusBadRequest,
	ErrCodeInternal:           http.StatusInternalServerError,
}

// HTTPStatusForCode returns the HTTP status that should accompany a given
// error code on the wire. Unknown codes default to 500 Internal Server Error.
func HTTPStatusForCode(code string) int {
	if status, ok := ErrCodeStatus[code]; ok {
		return status
	}
	return http.StatusInternalServerError
}
