package remote

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// remoteProjectService implements service.ProjectService.
type remoteProjectService struct {
	c *client.Client
}

// List returns the configured + currently-loaded project keys.
func (s *remoteProjectService) List(ctx context.Context) ([]string, error) {
	return s.c.ProjectList(ctx)
}

// Load loads a project by key.
func (s *remoteProjectService) Load(ctx context.Context, key string) (*service.ProjectInfo, error) {
	return s.c.ProjectLoad(ctx, key)
}

// Info returns the cached lean Info for a loaded project.
func (s *remoteProjectService) Info(ctx context.Context, handle service.ProjectHandle) (*service.ProjectInfo, error) {
	return s.c.ProjectInfo(ctx, handle)
}

// Status returns the aggregated multi-repo status snapshot.
func (s *remoteProjectService) Status(ctx context.Context, handle service.ProjectHandle) (*service.ProjectStatus, error) {
	return s.c.ProjectStatus(ctx, handle)
}

// Refresh re-scans the project's repos and returns the fresh status.
func (s *remoteProjectService) Refresh(ctx context.Context, handle service.ProjectHandle) (*service.ProjectStatus, error) {
	return s.c.ProjectRefresh(ctx, handle)
}

// BranchExists checks which repos in the project carry the named branch.
func (s *remoteProjectService) BranchExists(ctx context.Context, handle service.ProjectHandle, branch string) (*service.BranchExistence, error) {
	return s.c.ProjectBranchExists(ctx, handle, branch)
}

// ContextSummary returns the text summary handed to agents as context.
func (s *remoteProjectService) ContextSummary(ctx context.Context, handle service.ProjectHandle) (string, error) {
	return s.c.ProjectContextSummary(ctx, handle)
}

// DefaultConfigPath returns the daemon's path to its default project config.
func (s *remoteProjectService) DefaultConfigPath(ctx context.Context) (string, error) {
	return s.c.ProjectDefaultConfigPath(ctx)
}

// Subscribe streams project status updates.
func (s *remoteProjectService) Subscribe(ctx context.Context, handle service.ProjectHandle) (<-chan service.ProjectEvent, func(), error) {
	return s.c.ProjectSubscribe(ctx, handle)
}

// CreateWorkflow creates a new multi-repo feature workflow.
func (s *remoteProjectService) CreateWorkflow(ctx context.Context, handle service.ProjectHandle, branch, baseDir string) (*service.FeatureWorkflow, error) {
	return s.c.ProjectCreateWorkflow(ctx, handle, branch, baseDir)
}

// RemoveWorkflow tears down the workflow for `branch`.
func (s *remoteProjectService) RemoveWorkflow(ctx context.Context, handle service.ProjectHandle, branch string) (*service.FeatureWorkflow, error) {
	return s.c.ProjectRemoveWorkflow(ctx, handle, branch)
}

// LoadWorkflows reads persisted workflows for the project from disk.
func (s *remoteProjectService) LoadWorkflows(ctx context.Context, handle service.ProjectHandle) ([]*service.FeatureWorkflow, error) {
	return s.c.ProjectLoadWorkflows(ctx, handle)
}

// SaveWorkflows persists the given workflow set to disk.
func (s *remoteProjectService) SaveWorkflows(ctx context.Context, handle service.ProjectHandle, workflows []*service.FeatureWorkflow) error {
	return s.c.ProjectSaveWorkflows(ctx, handle, workflows)
}

// DiscoverWorkflowsAllRepos scans every repo for existing worktree workflows.
func (s *remoteProjectService) DiscoverWorkflowsAllRepos(ctx context.Context, handle service.ProjectHandle, skip map[string]bool) (<-chan service.DiscoveryProgressEvent, func(), error) {
	return s.c.ProjectDiscoverWorkflowsAllRepos(ctx, handle, skip)
}

// SubscribeWorkflows streams workflow_updated events.
func (s *remoteProjectService) SubscribeWorkflows(ctx context.Context, handle service.ProjectHandle) (<-chan service.WorkflowEvent, func(), error) {
	return s.c.ProjectSubscribeWorkflows(ctx, handle)
}
