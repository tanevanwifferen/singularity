package local

import (
	"context"
	"sync"

	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// localDiffService implements service.DiffService. The bulk DiffAllRepos
// walks the loaded project and calls GetWorkdirStatus concurrently with a
// small bounded worker pool (see CALL-SITES §3.2).
type localDiffService struct {
	proj *localProjectService // for ProjectHandle → *project.Project resolution
}

// BranchDiff returns the full textual diff between two branches.
func (s *localDiffService) BranchDiff(ctx context.Context, repoPath, a, b string) (*service.BranchDiff, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	d, err := git.GetBranchDiff(repoPath, a, b)
	if err != nil {
		return nil, wrapErr(err)
	}
	return d, nil
}

// WorkdirStatus returns the staged+unstaged working-tree snapshot.
func (s *localDiffService) WorkdirStatus(ctx context.Context, repoPath string) (*service.WorkdirDiff, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	d, err := git.GetWorkdirStatus(repoPath)
	if err != nil {
		return nil, wrapErr(err)
	}
	return d, nil
}

// FileDiff returns the textual diff for a single file between two branches.
func (s *localDiffService) FileDiff(ctx context.Context, repoPath, a, b, path string) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	out, err := git.GetFileDiff(repoPath, a, b, path)
	if err != nil {
		return "", wrapErr(err)
	}
	return out, nil
}

// StagedFileDiff returns the staged-vs-HEAD diff for one file.
func (s *localDiffService) StagedFileDiff(ctx context.Context, repoPath, path string) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	out, err := git.GetStagedFileDiff(repoPath, path)
	if err != nil {
		return "", wrapErr(err)
	}
	return out, nil
}

// UnstagedFileDiff returns the workdir-vs-staged diff for one file.
func (s *localDiffService) UnstagedFileDiff(ctx context.Context, repoPath, path string) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	out, err := git.GetUnstagedFileDiff(repoPath, path)
	if err != nil {
		return "", wrapErr(err)
	}
	return out, nil
}

// DeepFileDiff returns per-file hunks filtered to "new in this branch".
func (s *localDiffService) DeepFileDiff(ctx context.Context, repoPath, mergeBase, branch, defaultBranch, path string) ([]service.FilteredDiffHunk, string, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, "", err
	}
	hunks, raw, err := git.GetDeepFileDiff(repoPath, mergeBase, branch, defaultBranch, path)
	if err != nil {
		return nil, "", wrapErr(err)
	}
	return hunks, raw, nil
}

// MergeBase returns the merge-base SHA of two refs.
func (s *localDiffService) MergeBase(ctx context.Context, repoPath, a, b string) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	out, err := git.GetMergeBase(repoPath, a, b)
	if err != nil {
		return "", wrapErr(err)
	}
	return out, nil
}

// StageHunk stages exactly the given hunk into the index.
func (s *localDiffService) StageHunk(ctx context.Context, repoPath, filePath string, hunk service.DiffHunk) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.StageHunk(repoPath, filePath, hunk))
}

// UnstageHunk removes exactly the given hunk from the index.
func (s *localDiffService) UnstageHunk(ctx context.Context, repoPath, filePath string, hunk service.DiffHunk) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.UnstageHunk(repoPath, filePath, hunk))
}

// StageLines stages a subset of lines within a hunk.
func (s *localDiffService) StageLines(ctx context.Context, repoPath, filePath string, hunk service.DiffHunk, selectedLineIndices []int) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.StageLines(repoPath, filePath, hunk, selectedLineIndices))
}

// UnstageLines removes a subset of lines within a hunk from the index.
func (s *localDiffService) UnstageLines(ctx context.Context, repoPath, filePath string, hunk service.DiffHunk, selectedLineIndices []int) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.UnstageLines(repoPath, filePath, hunk, selectedLineIndices))
}

// DiffAllRepos returns the per-repo WorkdirDiff for every repo in the
// loaded project. Runs per-repo lookups concurrently with a bounded worker
// pool (semaphore=4) so we don't fork-bomb on projects with many repos.
func (s *localDiffService) DiffAllRepos(ctx context.Context, handle service.ProjectHandle) (map[string]*service.WorkdirDiff, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	proj, err := s.proj.resolve(handle)
	if err != nil {
		return nil, err
	}
	repos := proj.Repos
	out := make(map[string]*service.WorkdirDiff, len(repos))
	var mu sync.Mutex
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, r := range repos {
		wg.Add(1)
		go func(name, path string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			d, derr := git.GetWorkdirStatus(path)
			mu.Lock()
			if derr == nil {
				out[name] = d
			} else {
				out[name] = nil
			}
			mu.Unlock()
		}(r.Name, r.Path)
	}
	wg.Wait()
	return out, nil
}
