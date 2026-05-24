package service

import "context"

// WorktreeService covers git worktree CRUD.
type WorktreeService interface {
	// List returns all worktrees of a repo (including the main one).
	List(ctx context.Context, repoPath string) ([]Worktree, error)

	// Create creates a worktree at `path` checked out to `branch`. If
	// createBranch=true the branch is created from `startPoint` (default
	// branch when empty).
	Create(ctx context.Context, repoPath, path, branch string, createBranch bool, startPoint string) error

	// Remove removes a worktree; force=true removes even with uncommitted changes.
	Remove(ctx context.Context, repoPath, path string, force bool) error

	// Prune removes stale worktree administrative files (worktree dirs
	// that disappeared from disk).
	Prune(ctx context.Context, repoPath string) error

	// Lock marks a worktree as locked (refuse removal/prune).
	Lock(ctx context.Context, repoPath, path string) error

	// Unlock removes the lock marker.
	Unlock(ctx context.Context, repoPath, path string) error
}
