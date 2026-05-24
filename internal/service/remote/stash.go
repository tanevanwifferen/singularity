package remote

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// remoteStashService implements service.StashService.
type remoteStashService struct {
	c *client.Client
}

// List returns the stash entries of a single repo.
func (s *remoteStashService) List(ctx context.Context, repoPath string) ([]service.StashEntry, error) {
	return s.c.StashList(ctx, repoPath)
}

// Get returns one stash entry by index.
func (s *remoteStashService) Get(ctx context.Context, repoPath string, index int) (*service.StashEntry, error) {
	return s.c.StashGet(ctx, repoPath, index)
}

// Create creates a new stash entry.
func (s *remoteStashService) Create(ctx context.Context, repoPath, message string, includeUntracked bool) (int, error) {
	return s.c.StashCreate(ctx, repoPath, message, includeUntracked)
}

// Apply applies the stash at the given index.
func (s *remoteStashService) Apply(ctx context.Context, repoPath string, index int, pop bool) error {
	return s.c.StashApply(ctx, repoPath, index, pop)
}

// Drop deletes the stash at the given index.
func (s *remoteStashService) Drop(ctx context.Context, repoPath string, index int) error {
	return s.c.StashDrop(ctx, repoPath, index)
}

// Clear removes all stash entries.
func (s *remoteStashService) Clear(ctx context.Context, repoPath string) error {
	return s.c.StashClear(ctx, repoPath)
}

// ListAllRepos returns one RepoStashList per repo in the project.
func (s *remoteStashService) ListAllRepos(ctx context.Context, handle service.ProjectHandle) ([]service.RepoStashList, error) {
	return s.c.StashListAllRepos(ctx, handle)
}

// StashAllRepos creates a stash in every repo of the project.
func (s *remoteStashService) StashAllRepos(ctx context.Context, handle service.ProjectHandle, message string, includeUntracked bool) ([]service.RepoStashResult, error) {
	return s.c.StashAllRepos(ctx, handle, message, includeUntracked)
}

// ApplyStashAllRepos applies the matching stash in every repo of the project.
func (s *remoteStashService) ApplyStashAllRepos(ctx context.Context, handle service.ProjectHandle, message string, pop bool) ([]service.RepoStashResult, error) {
	return s.c.StashApplyAllRepos(ctx, handle, message, pop)
}
