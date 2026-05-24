// Package api defines the wire types shared between the singularity daemon
// (internal/server) and the TUI client SDK (internal/client). Every request,
// response, and WS payload that crosses the HTTP+WS boundary is declared
// here as a plain struct with snake_case JSON tags.
//
// Capability-specific types live in a per-capability file (repo.go, branch.go,
// diff.go, ...). This file holds only the cross-cutting envelope types and a
// handful of shared building blocks (RepoPathRequest, ProjectHandleRequest,
// StreamStartResponse) that are referenced across capabilities.
package api

import (
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// APIResponse is the canonical JSON envelope for every HTTP response. On
// success Data carries the operation's payload (whose Go type is documented
// in docs/design/WIRE-CONTRACT.md). On failure Error is a human-readable
// message and Code is the stable string constant from errors.go that the
// client maps back into a service sentinel.
type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	Code    string      `json:"code,omitempty"`
}

// RepoPathRequest is the common shape for endpoints that take only a repo
// path. Every endpoint that needs a repo path uses this struct (or embeds it)
// so the client SDK can share one helper.
type RepoPathRequest struct {
	RepoPath string `json:"repo_path"`
}

// ProjectHandleRequest is the common shape for endpoints that operate on a
// loaded project identified by its opaque ProjectHandle.
type ProjectHandleRequest struct {
	Handle service.ProjectHandle `json:"project_handle"`
}

// StreamStartResponse is the body returned (inside APIResponse.Data) from
// every endpoint marked **stream** in WIRE-CONTRACT.md. The client opens a
// WebSocket, sends SubscribeStreamPayload{StreamID}, and receives frames
// typed "stream:<id>" until a terminal frame with Done=true.
type StreamStartResponse struct {
	StreamID string `json:"stream_id"`
}

// StatusResponse is the response for /api/status. Kept as a thin re-export
// of legacy fields plus the now-explicit version + server name.
type StatusResponse struct {
	Version  string            `json:"version"`
	Server   string            `json:"server"`
	RepoPath string            `json:"repo_path,omitempty"`
	RepoInfo *service.RepoInfo `json:"repo_info,omitempty"`
	Error    string            `json:"error,omitempty"`
}
