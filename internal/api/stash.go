package api

import "gitlab.com/tanevanwifferen1/singularity/internal/service"

// Stash DTOs alias canonical types.
type (
	StashEntry      = service.StashEntry
	RepoStashList   = service.RepoStashList
	RepoStashResult = service.RepoStashResult
)

// StashListResponse is the body for GET /api/stash/list.
type StashListResponse struct {
	Entries []StashEntry `json:"entries"`
}

// StashCreateRequest is the body for POST /api/stash/create.
type StashCreateRequest struct {
	RepoPath         string `json:"repo_path"`
	Message          string `json:"message"`
	IncludeUntracked bool   `json:"include_untracked"`
}

// StashCreateResponse is the body for POST /api/stash/create.
type StashCreateResponse struct {
	Index int `json:"index"`
}

// StashApplyRequest is the body for POST /api/stash/apply.
type StashApplyRequest struct {
	RepoPath string `json:"repo_path"`
	Index    int    `json:"index"`
	Pop      bool   `json:"pop"`
}

// StashDropRequest is the body for POST /api/stash/drop.
type StashDropRequest struct {
	RepoPath string `json:"repo_path"`
	Index    int    `json:"index"`
}

// StashListAllResponse is the body for POST /api/stash/list_all.
type StashListAllResponse struct {
	Repos []RepoStashList `json:"repos"`
}

// StashAllRequest is the body for POST /api/stash/all.
type StashAllRequest struct {
	Handle           service.ProjectHandle `json:"project_handle"`
	Message          string                `json:"message"`
	IncludeUntracked bool                  `json:"include_untracked"`
}

// StashApplyAllRequest is the body for POST /api/stash/apply_all.
type StashApplyAllRequest struct {
	Handle  service.ProjectHandle `json:"project_handle"`
	Message string                `json:"message"`
	Pop     bool                  `json:"pop"`
}

// StashBulkResponse is the body for the per-repo bulk stash endpoints.
type StashBulkResponse struct {
	Results []RepoStashResult `json:"results"`
}
