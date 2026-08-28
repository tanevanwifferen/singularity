package service

import "context"

// ProjectService covers project loading, status, branch existence checks,
// per-repo bulk metadata, feature-workflow CRUD, and the project-status
// subscription (replacing the workflow polling loop per CALL-SITES §3.3).
//
// Per CALL-SITES §5.4 the client never instantiates a *project.Project —
// the daemon owns it and identifies it via ProjectHandle. View code holds
// ProjectInfo and asks for the heavier ProjectStatus on demand.
type ProjectService interface {
	// List returns the configured + currently-loaded project keys.
	List(ctx context.Context) ([]string, error)

	// Load loads a project by key and returns its lean Info + handle.
	Load(ctx context.Context, key string) (*ProjectInfo, error)

	// Info returns the cached lean Info for a loaded project.
	Info(ctx context.Context, handle ProjectHandle) (*ProjectInfo, error)

	// Status returns the aggregated multi-repo status snapshot.
	Status(ctx context.Context, handle ProjectHandle) (*ProjectStatus, error)

	// Refresh re-scans the project's repos and returns the fresh status.
	// Also fires a ProjectEvent on any active subscription.
	Refresh(ctx context.Context, handle ProjectHandle) (*ProjectStatus, error)

	// BranchExists checks which repos in the project carry the named branch.
	BranchExists(ctx context.Context, handle ProjectHandle, branch string) (*BranchExistence, error)

	// ContextSummary returns the text summary handed to Claude Code as
	// agent context (project name + per-repo paths/branches).
	ContextSummary(ctx context.Context, handle ProjectHandle) (string, error)

	// DefaultConfigPath returns the daemon's path to its default project
	// config file. Returned for diagnostic / CLI surfacing; the TUI does
	// not need to parse it.
	DefaultConfigPath(ctx context.Context) (string, error)

	// Subscribe streams project status updates whenever any repo in the
	// project changes (file watcher / external git op detected by daemon).
	Subscribe(ctx context.Context, handle ProjectHandle) (<-chan ProjectEvent, func(), error)

	// --- workflow operations (project/workflow.go) ---

	// CreateWorkflow creates a new multi-repo feature workflow for `branch`.
	// baseDir overrides the per-project default if non-empty.
	CreateWorkflow(ctx context.Context, handle ProjectHandle, branch, baseDir string) (*FeatureWorkflow, error)

	// RemoveWorkflow tears down the workflow for `branch`: removes every
	// repo's worktree, deletes the local and remote feature branches, and
	// drops the workflow from persistence once fully clean. Per-repo
	// failures are reported on the returned workflow's repos; a partially
	// failed workflow stays persisted so a retry can finish the job.
	RemoveWorkflow(ctx context.Context, handle ProjectHandle, branch string) (*FeatureWorkflow, error)

	// LoadWorkflows reads persisted workflows for the project from disk.
	LoadWorkflows(ctx context.Context, handle ProjectHandle) ([]*FeatureWorkflow, error)

	// SaveWorkflows persists the given workflow set to disk.
	SaveWorkflows(ctx context.Context, handle ProjectHandle, workflows []*FeatureWorkflow) error

	// DiscoverWorkflowsAllRepos scans every repo in the project for
	// existing worktrees that look like in-flight workflows. Long-running
	// (CALL-SITES gotcha #7) so streams progress. The terminal event has
	// Done=true; collected workflows are obtained via LoadWorkflows after
	// completion.
	DiscoverWorkflowsAllRepos(ctx context.Context, handle ProjectHandle, skip map[string]bool) (<-chan DiscoveryProgressEvent, func(), error)

	// SubscribeWorkflows streams workflow_updated events, replacing the
	// current workflowTickCmd polling loop in workflows.go.
	SubscribeWorkflows(ctx context.Context, handle ProjectHandle) (<-chan WorkflowEvent, func(), error)
}
