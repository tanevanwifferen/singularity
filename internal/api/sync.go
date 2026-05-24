package api

import (
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// UpstreamStatus aliases the canonical type from internal/git.
type UpstreamStatus = service.UpstreamStatus

// LastFetchResponse is the body for GET /api/sync/last_fetch.
type LastFetchResponse struct {
	Time time.Time `json:"time"`
}

// SyncFetchRequest is the body for POST /api/sync/fetch.
type SyncFetchRequest struct {
	RepoPath string `json:"repo_path"`
	Remote   string `json:"remote"`
}

// SyncPushRequest is the body for POST /api/sync/push.
type SyncPushRequest struct {
	RepoPath string `json:"repo_path"`
	Force    bool   `json:"force"`
}

// SyncSetUpstreamRequest is the body for POST /api/sync/set_upstream.
type SyncSetUpstreamRequest struct {
	RepoPath string `json:"repo_path"`
	Remote   string `json:"remote"`
}

// SyncAllRequest is the body for POST /api/sync/all.
type SyncAllRequest struct {
	Handle service.ProjectHandle `json:"project_handle"`
	Force  bool                  `json:"force"`
}
