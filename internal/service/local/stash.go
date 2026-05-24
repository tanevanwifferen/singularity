package local

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/project"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// localStashService implements service.StashService. Bulk methods delegate
// to project.StashAllRepos / project.ListAllStashes / project.ApplyStashAllRepos
// which already implement the goroutine fan-out — we just resolve the handle.
type localStashService struct {
	proj *localProjectService
}

// List returns the stash entries of a single repo.
func (s *localStashService) List(ctx context.Context, repoPath string) ([]service.StashEntry, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	entries, err := git.GetStashList(repoPath)
	if err != nil {
		return nil, wrapErr(err)
	}
	return entries, nil
}

// Get returns one stash entry by index (with full diff).
func (s *localStashService) Get(ctx context.Context, repoPath string, index int) (*service.StashEntry, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	entry, err := git.GetStash(repoPath, index)
	if err != nil {
		return nil, wrapErr(err)
	}
	return entry, nil
}

// Create creates a new stash entry.
func (s *localStashService) Create(ctx context.Context, repoPath, message string, includeUntracked bool) (int, error) {
	if err := checkCtx(ctx); err != nil {
		return 0, err
	}
	idx, err := git.CreateStash(repoPath, message, includeUntracked)
	if err != nil {
		return idx, wrapErr(err)
	}
	return idx, nil
}

// Apply applies the stash at the given index. pop=true also drops on success.
func (s *localStashService) Apply(ctx context.Context, repoPath string, index int, pop bool) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.ApplyStash(repoPath, index, pop))
}

// Drop deletes the stash at the given index.
func (s *localStashService) Drop(ctx context.Context, repoPath string, index int) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.DropStash(repoPath, index))
}

// Clear removes all stash entries.
func (s *localStashService) Clear(ctx context.Context, repoPath string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.ClearStash(repoPath))
}

// ListAllRepos returns one RepoStashList per repo in the project.
func (s *localStashService) ListAllRepos(ctx context.Context, handle service.ProjectHandle) ([]service.RepoStashList, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	proj, err := s.proj.resolve(handle)
	if err != nil {
		return nil, err
	}
	return project.ListAllStashes(proj), nil
}

// StashAllRepos creates a stash with the same message in every repo of the project.
func (s *localStashService) StashAllRepos(ctx context.Context, handle service.ProjectHandle, message string, includeUntracked bool) ([]service.RepoStashResult, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	proj, err := s.proj.resolve(handle)
	if err != nil {
		return nil, err
	}
	return project.StashAllRepos(proj, message, includeUntracked), nil
}

// ApplyStashAllRepos applies (or pops) the matching stash in every repo.
func (s *localStashService) ApplyStashAllRepos(ctx context.Context, handle service.ProjectHandle, message string, pop bool) ([]service.RepoStashResult, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	proj, err := s.proj.resolve(handle)
	if err != nil {
		return nil, err
	}
	return project.ApplyStashAllRepos(proj, message, pop), nil
}
