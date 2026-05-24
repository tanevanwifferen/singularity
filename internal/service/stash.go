package service

import "context"

// StashService covers per-repo stash CRUD plus project-wide bulk ops.
// Bulk methods (StashAllRepos / ApplyStashAllRepos / ListAllStashes) take
// a ProjectHandle and move the per-repo goroutine fan-out (currently in
// project_stash.go) into the daemon — see CALL-SITES §3.2 recommendation (a).
type StashService interface {
	// List returns the stash entries of a single repo.
	List(ctx context.Context, repoPath string) ([]StashEntry, error)

	// Get returns one stash entry by index (with full diff).
	Get(ctx context.Context, repoPath string, index int) (*StashEntry, error)

	// Create creates a new stash entry. Returns the new stash index.
	Create(ctx context.Context, repoPath, message string, includeUntracked bool) (int, error)

	// Apply applies the stash at the given index. pop=true also drops it on success.
	Apply(ctx context.Context, repoPath string, index int, pop bool) error

	// Drop deletes the stash at the given index.
	Drop(ctx context.Context, repoPath string, index int) error

	// Clear removes all stash entries.
	Clear(ctx context.Context, repoPath string) error

	// ListAllRepos returns one RepoStashList per repo in the project.
	ListAllRepos(ctx context.Context, handle ProjectHandle) ([]RepoStashList, error)

	// StashAllRepos creates a stash with the same message in every repo
	// of the project. Result is per-repo success/failure.
	StashAllRepos(ctx context.Context, handle ProjectHandle, message string, includeUntracked bool) ([]RepoStashResult, error)

	// ApplyStashAllRepos applies (or pops) the matching stash entry in
	// every repo of the project. Result is per-repo success/failure.
	ApplyStashAllRepos(ctx context.Context, handle ProjectHandle, message string, pop bool) ([]RepoStashResult, error)
}
