package api

import "gitlab.com/tanevanwifferen1/singularity/internal/service"

// MR DTOs alias the canonical types.
type (
	MergeRequest   = service.MergeRequest
	MRResult       = service.MRResult
	RemoteProvider = service.RemoteProvider
)

// MRRequest is the body for POST /api/mr/create (legacy path).
type MRRequest struct {
	RepoPath     string   `json:"repo_path"`
	SourceBranch string   `json:"source_branch"`
	TargetBranch string   `json:"target_branch"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Reviewers    []string `json:"reviewers"`
}

// MRGenerateRequest is the body for POST /api/mr/title and /api/mr/description.
type MRGenerateRequest struct {
	RepoPath     string `json:"repo_path"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
}

// MRTextResponse is the body for the title / description generators.
type MRTextResponse struct {
	Text string `json:"text"`
}

// MRCreateCLIRequest is the body for POST /api/mr/create_cli.
type MRCreateCLIRequest struct {
	RepoPath   string         `json:"repo_path"`
	Provider   RemoteProvider `json:"provider"`
	BaseBranch string         `json:"base_branch"`
}
