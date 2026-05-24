package service

import "context"

// RebaseService covers interactive-rebase planning + in-progress control
// (continue/skip/abort) plus the long-running RebaseOntoMain flow which
// streams progress per CALL-SITES gotcha #7.
type RebaseService interface {
	// Plan returns the interactive-rebase commit list between base and
	// current. The returned slice is mutable on the client side (reorder,
	// change op) and round-tripped via Start.
	Plan(ctx context.Context, repoPath, base, current string) ([]RebaseCommit, error)

	// Status reports whether a rebase is currently in progress and the
	// commit being processed.
	Status(ctx context.Context, repoPath string) (inProgress bool, commit string, err error)

	// GenerateTodo serializes a plan back into git's todo-list format.
	GenerateTodo(ctx context.Context, commits []RebaseCommit) (string, error)

	// Continue resumes a rebase after a conflict has been resolved.
	// Returns ErrNoRebaseInProgress if no rebase is active.
	Continue(ctx context.Context, repoPath string) error

	// Skip skips the current rebase step.
	Skip(ctx context.Context, repoPath string) error

	// Abort aborts the in-progress rebase and restores HEAD.
	Abort(ctx context.Context, repoPath string) error

	// OntoMain rebases the given worktree onto its main branch. Long-running:
	// returns a stream of SyncProgressEvent lines plus a cancel closure.
	// The final event has Done=true and may carry an Err for conflicts.
	OntoMain(ctx context.Context, repoPath string) (<-chan SyncProgressEvent, func(), error)

	// Context returns the LLM-friendly conflict-resolution context for a
	// rebase-onto-main run; consumed by the agent that resolves conflicts.
	Context(ctx context.Context, repoPath, mainBranch string, conflictFiles []string) (string, error)
}
