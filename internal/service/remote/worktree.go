package remote

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// remoteWorktreeService implements service.WorktreeService.
type remoteWorktreeService struct {
	c *client.Client
}

// List returns all worktrees of a repo.
func (s *remoteWorktreeService) List(ctx context.Context, repoPath string) ([]service.Worktree, error) {
	return s.c.WorktreeList(ctx, repoPath)
}

// Create creates a worktree at `path` checked out to `branch`.
func (s *remoteWorktreeService) Create(ctx context.Context, repoPath, path, branch string, createBranch bool, startPoint string) error {
	return s.c.WorktreeCreate(ctx, repoPath, path, branch, createBranch, startPoint)
}

// Remove removes a worktree.
func (s *remoteWorktreeService) Remove(ctx context.Context, repoPath, path string, force bool) error {
	return s.c.WorktreeRemove(ctx, repoPath, path, force)
}

// Prune removes stale worktree administrative files.
func (s *remoteWorktreeService) Prune(ctx context.Context, repoPath string) error {
	return s.c.WorktreePrune(ctx, repoPath)
}

// Lock marks a worktree as locked.
func (s *remoteWorktreeService) Lock(ctx context.Context, repoPath, path string) error {
	return s.c.WorktreeLock(ctx, repoPath, path)
}

// Unlock removes the lock marker.
func (s *remoteWorktreeService) Unlock(ctx context.Context, repoPath, path string) error {
	return s.c.WorktreeUnlock(ctx, repoPath, path)
}
