package api

import "gitlab.com/tanevanwifferen1/singularity/internal/service"

// RebaseCommit and RebaseOperation alias the canonical types from internal/git.
type (
	RebaseCommit    = service.RebaseCommit
	RebaseOperation = service.RebaseOperation
)

// RebasePlanRequest is the body for POST /api/rebase/plan.
type RebasePlanRequest struct {
	RepoPath string `json:"repo_path"`
	Base     string `json:"base"`
	Current  string `json:"current"`
}

// RebasePlanResponse is the body for POST /api/rebase/plan.
type RebasePlanResponse struct {
	Commits []RebaseCommit `json:"commits"`
}

// RebaseStatusResponse is the body for GET /api/rebase/status.
type RebaseStatusResponse struct {
	InProgress bool   `json:"in_progress"`
	Commit     string `json:"commit,omitempty"`
}

// RebaseTodoRequest is the body for POST /api/rebase/todo.
type RebaseTodoRequest struct {
	Commits []RebaseCommit `json:"commits"`
}

// RebaseTodoResponse is the body for POST /api/rebase/todo.
type RebaseTodoResponse struct {
	Todo string `json:"todo"`
}

// RebaseContextRequest is the body for POST /api/rebase/context.
type RebaseContextRequest struct {
	RepoPath      string   `json:"repo_path"`
	MainBranch    string   `json:"main_branch"`
	ConflictFiles []string `json:"conflict_files"`
}

// RebaseContextResponse is the body for POST /api/rebase/context.
type RebaseContextResponse struct {
	Context string `json:"context"`
}
