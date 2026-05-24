package api

import "gitlab.com/tanevanwifferen1/singularity/internal/service"

// RepoInfo aliases the canonical repo descriptor so client code can refer to
// it without importing internal/git directly. The underlying type carries its
// own JSON tags (snake_case), so serialization is identical to the legacy
// payload.
type RepoInfo = service.RepoInfo

// RepoOpenRequest is the body for POST /api/repo/open. Path may be empty,
// in which case the daemon falls back to its cwd discovery rules.
type RepoOpenRequest struct {
	Path string `json:"path"`
}

// RepoRequest is the legacy alias kept for backward source-compat with the
// pre-split internal/api/types.go.
type RepoRequest = RepoOpenRequest

// RepoFindResponse is the body for GET /api/repo/find?path=.
type RepoFindResponse struct {
	Path string `json:"path"`
}

// RepoSubscribeRequest is the body for POST /api/repo/subscribe.
type RepoSubscribeRequest struct {
	RepoPath string `json:"repo_path"`
}
