package api

import "gitlab.com/tanevanwifferen1/singularity/internal/service"

// Project DTOs alias the canonical types from internal/project (re-exported
// through internal/service).
type (
	ProjectInfo     = service.ProjectInfo
	ProjectStatus   = service.ProjectStatus
	BranchExistence = service.BranchExistence
	FeatureWorkflow = service.FeatureWorkflow
)

// ProjectListResponse is the body for GET /api/project/list.
type ProjectListResponse struct {
	Projects []string `json:"projects"`
	Loaded   []string `json:"loaded"`
}

// ProjectLoadRequest is the body for POST /api/project/load.
type ProjectLoadRequest struct {
	Key string `json:"key"`
}

// ProjectBranchRequest is the body for POST /api/project/branch/check and
// the legacy /api/project/branch/compare. Carries either a project key or a
// handle (one of the two must be non-empty).
type ProjectBranchRequest struct {
	Handle service.ProjectHandle `json:"project_handle,omitempty"`
	Key    string                `json:"key,omitempty"`
	Branch string                `json:"branch"`
}

// ProjectContextResponse is the body for GET /api/project/context.
type ProjectContextResponse struct {
	Context string `json:"context"`
}

// ProjectConfigPathResponse is the body for GET /api/project/config_path.
type ProjectConfigPathResponse struct {
	Path string `json:"path"`
}

// WorkflowCreateRequest is the body for POST /api/project/workflow/create.
type WorkflowCreateRequest struct {
	Handle  service.ProjectHandle `json:"project_handle"`
	Branch  string                `json:"branch"`
	BaseDir string                `json:"base_dir"`
}

// WorkflowRemoveRequest is the body for POST /api/project/workflow/remove.
type WorkflowRemoveRequest struct {
	Handle service.ProjectHandle `json:"project_handle"`
	Branch string                `json:"branch"`
}

// WorkflowListResponse is the body for GET /api/project/workflow/list.
type WorkflowListResponse struct {
	Workflows []*FeatureWorkflow `json:"workflows"`
}

// WorkflowSaveRequest is the body for POST /api/project/workflow/save.
type WorkflowSaveRequest struct {
	Handle    service.ProjectHandle `json:"project_handle"`
	Workflows []*FeatureWorkflow    `json:"workflows"`
}

// WorkflowDiscoverRequest is the body for POST /api/project/workflow/discover.
type WorkflowDiscoverRequest struct {
	Handle service.ProjectHandle `json:"project_handle"`
	Skip   map[string]bool       `json:"skip,omitempty"`
}
