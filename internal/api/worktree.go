package api

import "gitlab.com/tanevanwifferen1/singularity/internal/service"

// Worktree aliases the canonical type from internal/git.
type Worktree = service.Worktree

// WorktreeListResponse is the body for GET /api/worktree/list.
type WorktreeListResponse struct {
	Worktrees []Worktree `json:"worktrees"`
}

// WorktreeCreateRequest is the body for POST /api/worktree/create.
type WorktreeCreateRequest struct {
	RepoPath     string `json:"repo_path"`
	Path         string `json:"path"`
	Branch       string `json:"branch"`
	CreateBranch bool   `json:"create_branch"`
	StartPoint   string `json:"start_point"`
}

// WorktreeRemoveRequest is the body for POST /api/worktree/remove.
type WorktreeRemoveRequest struct {
	RepoPath string `json:"repo_path"`
	Path     string `json:"path"`
	Force    bool   `json:"force"`
}

// WorktreePathRequest is the body for POST /api/worktree/lock and unlock.
type WorktreePathRequest struct {
	RepoPath string `json:"repo_path"`
	Path     string `json:"path"`
}
