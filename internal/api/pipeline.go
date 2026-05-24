package api

import "gitlab.com/tanevanwifferen1/singularity/internal/service"

// Pipeline DTOs alias the canonical types from internal/git.
type (
	PipelineInfo   = service.PipelineInfo
	PipelineStatus = service.PipelineStatus
)

// PipelineStatusesRequest is the body for POST /api/pipeline/statuses.
type PipelineStatusesRequest struct {
	RepoPath string       `json:"repo_path"`
	Branches []BranchInfo `json:"branches"`
}

// PipelineStatusesResponse is the body for POST /api/pipeline/statuses.
type PipelineStatusesResponse struct {
	Pipelines map[string]*PipelineInfo `json:"pipelines"`
}

// PipelineRetryRequest is the body for POST /api/pipeline/retry.
type PipelineRetryRequest struct {
	RepoPath string `json:"repo_path"`
	Branch   string `json:"branch"`
}
