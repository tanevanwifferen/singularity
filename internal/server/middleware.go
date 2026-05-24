package server

import (
	"crypto/subtle"
	"net/http"
	"path/filepath"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// requireToken wraps an HTTP handler with bearer-token authentication. It
// reads the Authorization header and compares it in constant time against the
// configured token. On mismatch it writes a 401 PERMISSION_DENIED response and
// does not invoke h.
//
// If the Server has no auth token configured (the unix-socket default), the
// wrapper is never installed; see registerRoutes.
func (s *Server) requireToken(h http.HandlerFunc) http.HandlerFunc {
	expected := []byte(s.authToken)
	return func(w http.ResponseWriter, r *http.Request) {
		got := bearerToken(r.Header.Get("Authorization"))
		if got == "" || subtle.ConstantTimeCompare([]byte(got), expected) != 1 {
			s.writeCoded(w, api.ErrCodePermissionDenied, "unauthorized")
			return
		}
		h(w, r)
	}
}

// bearerToken extracts the token from an "Authorization: Bearer <token>"
// header. Returns the trimmed token, or "" if the header is missing or not a
// Bearer credential.
func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

// validateRepoPath cleans an inbound repo_path / project_path value and
// rejects obvious traversal attempts. The returned string is the cleaned
// absolute path the daemon should use downstream.
//
// Rules:
//   - empty input is allowed (callers decide whether to require a path)
//   - rejects any input containing "..", before or after cleaning, to defeat
//     traversal in TCP mode
//   - resolves to an absolute path via filepath.Abs
//   - returns the cleaned absolute path
//
// An optional allow-list (Server.allowedRoots) constrains the result to
// directories rooted at one of those entries; when empty (the default) any
// absolute path is accepted.
func (s *Server) validateRepoPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	// Reject the literal ".." segment before any cleaning so callers can't
	// smuggle it through encoded forms; filepath.Clean collapses A/.. → ".".
	if strings.Contains(p, "..") {
		return "", errInvalidPath
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if strings.Contains(abs, "..") {
		return "", errInvalidPath
	}
	if len(s.allowedRoots) > 0 {
		ok := false
		for _, root := range s.allowedRoots {
			r := filepath.Clean(root)
			if abs == r || strings.HasPrefix(abs, r+string(filepath.Separator)) {
				ok = true
				break
			}
		}
		if !ok {
			return "", errInvalidPath
		}
	}
	return abs, nil
}

// errInvalidPath is the sentinel returned by validateRepoPath for any input
// the daemon refuses to act on. It's intentionally a plain error so callers
// don't conflate it with service-layer sentinels.
type pathError struct{ msg string }

func (e *pathError) Error() string { return e.msg }

var errInvalidPath = &pathError{msg: "invalid path"}
