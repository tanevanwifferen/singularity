package api

import "git-frontend/internal/git"

// API types shared between server and client

// StatusResponse is the response for /api/status
type StatusResponse struct {
	Version   string `json:"version"`
	Server    string `json:"server"`
	RepoPath  string `json:"repo_path,omitempty"`
	RepoInfo  *git.RepoInfo `json:"repo_info,omitempty"`
	Error     string `json:"error,omitempty"`
}

// RepoRequest is the request for repo operations
type RepoRequest struct {
	Path string `json:"path"`
}

// BranchComparisonRequest is the request for branch comparison
type BranchComparisonRequest struct {
	RepoPath  string `json:"repo_path"`
	BranchA  string `json:"branch_a"`
	BranchB  string `json:"branch_b"`
}

// BranchDiffRequest is the request for branch diff
type BranchDiffRequest struct {
	RepoPath  string `json:"repo_path"`
	BranchA  string `json:"branch_a"`
	BranchB  string `json:"branch_b"`
}

// CommitMessageRequest is the request for commit message generation
type CommitMessageRequest struct {
	RepoPath string `json:"repo_path"`
}

// MRRequest is the request for creating a merge request
type MRRequest struct {
	RepoPath      string   `json:"repo_path"`
	SourceBranch  string   `json:"source_branch"`
	TargetBranch  string   `json:"target_branch"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Reviewers     []string `json:"reviewers"`
}

// WSMessage represents a WebSocket message
type WSMessage struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

// WSEvent types
const (
	WSEventBranchUpdate  = "branch_update"
	WSEventRepoUpdate    = "repo_update"
	WSEventPipelineUpdate = "pipeline_update"
	WSEventError         = "error"
)

// APIResponse is a generic API response
type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}
