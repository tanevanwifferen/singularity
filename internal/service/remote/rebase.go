package remote

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// remoteRebaseService implements service.RebaseService.
type remoteRebaseService struct {
	c *client.Client
}

// Plan returns the interactive-rebase commit list.
func (s *remoteRebaseService) Plan(ctx context.Context, repoPath, base, current string) ([]service.RebaseCommit, error) {
	return s.c.RebasePlan(ctx, repoPath, base, current)
}

// Status reports whether a rebase is currently in progress.
func (s *remoteRebaseService) Status(ctx context.Context, repoPath string) (bool, string, error) {
	return s.c.RebaseStatus(ctx, repoPath)
}

// GenerateTodo serializes a plan back into git's todo-list format.
func (s *remoteRebaseService) GenerateTodo(ctx context.Context, commits []service.RebaseCommit) (string, error) {
	return s.c.RebaseGenerateTodo(ctx, commits)
}

// Continue resumes a rebase after a conflict has been resolved.
func (s *remoteRebaseService) Continue(ctx context.Context, repoPath string) error {
	return s.c.RebaseContinue(ctx, repoPath)
}

// Skip skips the current rebase step.
func (s *remoteRebaseService) Skip(ctx context.Context, repoPath string) error {
	return s.c.RebaseSkip(ctx, repoPath)
}

// Abort aborts the in-progress rebase and restores HEAD.
func (s *remoteRebaseService) Abort(ctx context.Context, repoPath string) error {
	return s.c.RebaseAbort(ctx, repoPath)
}

// OntoMain rebases the given worktree onto its main branch.
func (s *remoteRebaseService) OntoMain(ctx context.Context, repoPath string) (<-chan service.SyncProgressEvent, func(), error) {
	return s.c.RebaseOntoMain(ctx, repoPath)
}

// Context returns the LLM-friendly conflict-resolution context.
func (s *remoteRebaseService) Context(ctx context.Context, repoPath, mainBranch string, conflictFiles []string) (string, error) {
	return s.c.RebaseContext(ctx, repoPath, mainBranch, conflictFiles)
}
