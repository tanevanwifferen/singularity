package api

import "gitlab.com/tanevanwifferen1/singularity/internal/service"

// CommitMessage aliases the canonical commit-message DTO from internal/git.
type CommitMessage = service.CommitMessage

// CommitMessageRequest is the body for POST /api/commit/message (legacy path
// used by Commit.GenerateMessage).
type CommitMessageRequest struct {
	RepoPath string `json:"repo_path"`
}

// CommitSuggestResponse is the body for POST /api/commit/suggest.
type CommitSuggestResponse struct {
	Message string `json:"message"`
}

// CommitHashRequest is the body for POST /api/commit/cherry_pick.
type CommitHashRequest struct {
	RepoPath string `json:"repo_path"`
	Hash     string `json:"hash"`
}

// CommitResetRequest is the body for POST /api/commit/reset.
type CommitResetRequest struct {
	RepoPath string `json:"repo_path"`
	Hash     string `json:"hash"`
	Mode     string `json:"mode"` // "soft" | "mixed" | "hard"
}

// CommitAmendRequest is the body for POST /api/commit/amend.
type CommitAmendRequest struct {
	RepoPath string `json:"repo_path"`
	Message  string `json:"message"`
}

// CommitFilesResponse is the body for GET /api/commit/files.
type CommitFilesResponse struct {
	Files []FileChange `json:"files"`
}

// CommitFileDiffRequest is the body for POST /api/commit/file_diff.
type CommitFileDiffRequest struct {
	RepoPath string `json:"repo_path"`
	Hash     string `json:"hash"`
	Path     string `json:"path"`
}

// CommitFullDiffRequest is the body for POST /api/commit/full_diff.
type CommitFullDiffRequest struct {
	RepoPath string `json:"repo_path"`
	Hash     string `json:"hash"`
}
