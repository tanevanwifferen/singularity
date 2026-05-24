package service

import "errors"

// Sentinel errors that cross the service boundary. The remote implementation
// MUST decode a server-sent error code (e.g. {"code":"MR_ALREADY_EXISTS"}) and
// return the matching sentinel here so views can rely on errors.Is.
//
// Each sentinel is wrapped, not replaced, by callers — implementations should
// use fmt.Errorf("...: %w", ErrFoo) to attach context while preserving identity.
var (
	// ErrMRAlreadyExists is returned by MRService.Create when a merge/pull
	// request already exists for the source branch. Mirrors git.ErrMRAlreadyExists.
	// Code: "MR_ALREADY_EXISTS".
	ErrMRAlreadyExists = errors.New("a merge request already exists for this branch")

	// ErrNotFound indicates the requested resource (repo, branch, agent,
	// stash entry, worktree, workflow, issue, ...) does not exist.
	// Code: "NOT_FOUND".
	ErrNotFound = errors.New("not found")

	// ErrConflict indicates a mutation could not complete because the
	// underlying state changed concurrently or git reported a merge
	// conflict (rebase, pull, cherry-pick). Use errors.Is to differentiate
	// from other failure modes. Code: "CONFLICT".
	ErrConflict = errors.New("conflict")

	// ErrAgentLimit indicates the engine has reached its MaxAgents cap and
	// the requested StartAgent/ResumeWithHistory was rejected. Code: "AGENT_LIMIT".
	ErrAgentLimit = errors.New("agent limit reached")

	// ErrNoForge indicates DetectForgeAuth found neither a GitHub nor a
	// GitLab credential — the caller cannot create MRs or fetch pipelines.
	// Code: "NO_FORGE". (Not strictly listed in the audit, but views need
	// to distinguish "no creds configured" from "creds rejected" — the
	// existing call sites at pr.go:93, branches.go:110, pipeline.go:202
	// only check err == nil today, which is ambiguous over the wire.)
	ErrNoForge = errors.New("no forge auth detected")

	// ErrRebaseInProgress indicates an operation was rejected because a
	// rebase is currently in progress on the repo. Code: "REBASE_IN_PROGRESS".
	// (Added because RebaseService.Continue/Skip/Abort need to differentiate
	// "no rebase active" from other failures; pure convenience sentinel.)
	ErrRebaseInProgress = errors.New("rebase already in progress")

	// ErrNoRebaseInProgress is the dual of ErrRebaseInProgress.
	// Code: "NO_REBASE_IN_PROGRESS".
	ErrNoRebaseInProgress = errors.New("no rebase in progress")

	// ErrPermissionDenied indicates the caller is not authorized (TCP mode
	// with missing/invalid bearer token, or filesystem-level EPERM from
	// git operations). Code: "PERMISSION_DENIED".
	ErrPermissionDenied = errors.New("permission denied")

	// ErrUnavailable indicates the daemon is reachable but the requested
	// subsystem is not ready (e.g. project loader not configured, jira
	// client not authenticated). Code: "UNAVAILABLE".
	ErrUnavailable = errors.New("service unavailable")

	// ErrCanceled indicates the operation was canceled via context. Code: "CANCELED".
	ErrCanceled = errors.New("canceled")
)
