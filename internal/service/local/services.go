// Package local implements service.Services by calling the in-process
// internal/git, internal/engine, internal/project, and internal/jira packages
// directly. It is the daemon-side implementation; the TUI uses service/remote
// to reach this code over HTTP+WS.
//
// One *engine.Engine is shared across every service that needs it (agent,
// jira AI workflows, worktree-isolated agents). The project loader and the
// jira config are likewise passed in once and shared. Construct via New.
package local

import (
	"context"
	"errors"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/config"
	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/project"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// New wires every local service implementation into a single *service.Services
// value. eng is the shared agent engine (must be non-nil — the daemon always
// owns one). projectLoader may be nil; in that case ProjectService and the
// project-handle-aware bulk methods on Stash/Diff/Sync return
// service.ErrUnavailable. jiraCfg may be a zero value when Jira is disabled;
// the resulting JiraService returns ErrUnavailable for every call.
func New(eng *engine.Engine, projectLoader *project.Loader, jiraCfg config.JiraConfig) *service.Services {
	projSvc := newProjectService(projectLoader)
	return &service.Services{
		Repo:     &localRepoService{},
		Branch:   &localBranchService{},
		Diff:     &localDiffService{proj: projSvc},
		Commit:   &localCommitService{},
		Stash:    &localStashService{proj: projSvc},
		Rebase:   &localRebaseService{},
		Worktree: &localWorktreeService{},
		Sync:     &localSyncService{proj: projSvc},
		Pipeline: &localPipelineService{},
		MR:       &localMRService{},
		Forge:    &localForgeService{},
		Project:  projSvc,
		Agent:    &localAgentService{eng: eng},
		Jira:     newJiraService(eng, jiraCfg),
	}
}

// checkCtx returns service.ErrCanceled if ctx is already canceled.
// Service methods call this at the top so callers get a stable sentinel
// instead of context.Canceled / DeadlineExceeded leaking through.
func checkCtx(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return service.ErrCanceled
	}
	return nil
}

// mapErr translates errors coming out of internal/git, internal/engine, and
// internal/project into the service-package sentinels. The underlying packages
// largely return stringly-typed errors; we match on substrings as well as the
// few real sentinels (git.ErrMRAlreadyExists).
//
// If err already wraps a service sentinel it's returned unchanged.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	// Already a service sentinel — leave as-is so errors.Is keeps working.
	for _, s := range knownSentinels {
		if errors.Is(err, s) {
			return err
		}
	}
	if errors.Is(err, git.ErrMRAlreadyExists) {
		return service.ErrMRAlreadyExists
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "already exists for this branch"),
		strings.Contains(msg, "merge request already exists"),
		strings.Contains(msg, "pull request already exists"):
		return service.ErrMRAlreadyExists
	case strings.Contains(msg, "agent limit reached"):
		return service.ErrAgentLimit
	case strings.Contains(msg, "rebase already in progress"),
		strings.Contains(msg, "rebase in progress"):
		return service.ErrRebaseInProgress
	case strings.Contains(msg, "no rebase in progress"):
		return service.ErrNoRebaseInProgress
	case strings.Contains(msg, "conflict"):
		return service.ErrConflict
	case strings.Contains(msg, "agent not found"),
		strings.Contains(msg, "not a git repository"),
		strings.Contains(msg, "no such file"),
		strings.Contains(msg, "does not exist"),
		strings.Contains(msg, "not found"),
		strings.Contains(msg, "not loaded"),
		strings.Contains(msg, "no git repository found"):
		return service.ErrNotFound
	case strings.Contains(msg, "exit status 128"):
		// git's "fatal: not a git repository" / missing-ref failures surface
		// as ExitError("exit status 128") from os/exec without the underlying
		// stderr text reaching this layer. Treat the well-known git "no
		// repo / missing ref" exit code as NotFound; downstream callers
		// already use ErrNotFound for both shapes.
		return service.ErrNotFound
	case strings.Contains(msg, "permission denied"):
		return service.ErrPermissionDenied
	case strings.Contains(msg, "canceled"), strings.Contains(msg, "context canceled"):
		return service.ErrCanceled
	}
	return err
}

// wrapErr is mapErr that preserves the original message via %w so callers can
// errors.Unwrap if they need diagnostic context while still matching the
// sentinel via errors.Is.
func wrapErr(err error) error {
	mapped := mapErr(err)
	if mapped == nil || mapped == err {
		return err
	}
	// Re-wrap so both the sentinel and original text survive.
	return errFromSentinel{sentinel: mapped, original: err}
}

type errFromSentinel struct {
	sentinel error
	original error
}

func (e errFromSentinel) Error() string {
	if e.original == nil {
		return e.sentinel.Error()
	}
	return e.original.Error()
}

func (e errFromSentinel) Is(target error) bool {
	return errors.Is(e.sentinel, target)
}

func (e errFromSentinel) Unwrap() error { return e.original }

var knownSentinels = []error{
	service.ErrMRAlreadyExists,
	service.ErrNotFound,
	service.ErrConflict,
	service.ErrAgentLimit,
	service.ErrNoForge,
	service.ErrRebaseInProgress,
	service.ErrNoRebaseInProgress,
	service.ErrPermissionDenied,
	service.ErrUnavailable,
	service.ErrCanceled,
}

// agentSnapshotsFromEngine adapts engine.ListAgents (which returns []*Agent)
// to the []AgentSnapshot shape AgentService.List requires. Kept here so both
// the agent service and any future consumer share the same adapter.
func agentSnapshotsFromEngine(agents []*engine.Agent) []engine.AgentSnapshot {
	out := make([]engine.AgentSnapshot, len(agents))
	for i, a := range agents {
		out[i] = a.Snapshot()
	}
	return out
}
