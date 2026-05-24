package local

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// localWorktreeService implements service.WorktreeService.
type localWorktreeService struct{}

// List returns all worktrees of a repo.
func (s *localWorktreeService) List(ctx context.Context, repoPath string) ([]service.Worktree, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	wts, err := git.GetWorktrees(repoPath)
	if err != nil {
		return nil, wrapErr(err)
	}
	return wts, nil
}

// Create creates a new worktree.
func (s *localWorktreeService) Create(ctx context.Context, repoPath, path, branch string, createBranch bool, startPoint string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.CreateWorktree(repoPath, path, branch, createBranch, startPoint))
}

// Remove removes a worktree.
func (s *localWorktreeService) Remove(ctx context.Context, repoPath, path string, force bool) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.RemoveWorktree(repoPath, path, force))
}

// Prune removes stale worktree administrative files.
func (s *localWorktreeService) Prune(ctx context.Context, repoPath string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.PruneWorktrees(repoPath))
}

// Lock marks a worktree as locked.
func (s *localWorktreeService) Lock(ctx context.Context, repoPath, path string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.LockWorktree(repoPath, path))
}

// Unlock removes the lock marker.
func (s *localWorktreeService) Unlock(ctx context.Context, repoPath, path string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.UnlockWorktree(repoPath, path))
}
