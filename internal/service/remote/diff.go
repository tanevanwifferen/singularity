package remote

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// remoteDiffService implements service.DiffService.
type remoteDiffService struct {
	c *client.Client
}

// BranchDiff returns the full textual diff between two branches.
func (s *remoteDiffService) BranchDiff(ctx context.Context, repoPath, a, b string) (*service.BranchDiff, error) {
	return s.c.DiffBranch(ctx, repoPath, a, b)
}

// WorkdirStatus returns the staged+unstaged working-tree snapshot.
func (s *remoteDiffService) WorkdirStatus(ctx context.Context, repoPath string) (*service.WorkdirDiff, error) {
	return s.c.DiffWorkdir(ctx, repoPath)
}

// FileDiff returns the textual diff for a single file between two branches.
func (s *remoteDiffService) FileDiff(ctx context.Context, repoPath, a, b, path string) (string, error) {
	return s.c.DiffFile(ctx, repoPath, a, b, path)
}

// StagedFileDiff returns the staged-vs-HEAD diff for one file.
func (s *remoteDiffService) StagedFileDiff(ctx context.Context, repoPath, path string) (string, error) {
	return s.c.DiffStagedFile(ctx, repoPath, path)
}

// UnstagedFileDiff returns the workdir-vs-staged diff for one file.
func (s *remoteDiffService) UnstagedFileDiff(ctx context.Context, repoPath, path string) (string, error) {
	return s.c.DiffUnstagedFile(ctx, repoPath, path)
}

// DeepFileDiff returns per-file hunks filtered to "new in this branch".
func (s *remoteDiffService) DeepFileDiff(ctx context.Context, repoPath, mergeBase, branch, defaultBranch, path string) ([]service.FilteredDiffHunk, string, error) {
	return s.c.DiffDeepFile(ctx, repoPath, mergeBase, branch, defaultBranch, path)
}

// MergeBase returns the merge-base SHA of two refs.
func (s *remoteDiffService) MergeBase(ctx context.Context, repoPath, a, b string) (string, error) {
	return s.c.DiffMergeBase(ctx, repoPath, a, b)
}

// StageHunk stages exactly the given hunk into the index.
func (s *remoteDiffService) StageHunk(ctx context.Context, repoPath, filePath string, hunk service.DiffHunk) error {
	return s.c.DiffStageHunk(ctx, repoPath, filePath, hunk)
}

// UnstageHunk removes exactly the given hunk from the index.
func (s *remoteDiffService) UnstageHunk(ctx context.Context, repoPath, filePath string, hunk service.DiffHunk) error {
	return s.c.DiffUnstageHunk(ctx, repoPath, filePath, hunk)
}

// StageLines stages a subset of lines within a hunk.
func (s *remoteDiffService) StageLines(ctx context.Context, repoPath, filePath string, hunk service.DiffHunk, selectedLineIndices []int) error {
	return s.c.DiffStageLines(ctx, repoPath, filePath, hunk, selectedLineIndices)
}

// UnstageLines removes a subset of lines within a hunk from the index.
func (s *remoteDiffService) UnstageLines(ctx context.Context, repoPath, filePath string, hunk service.DiffHunk, selectedLineIndices []int) error {
	return s.c.DiffUnstageLines(ctx, repoPath, filePath, hunk, selectedLineIndices)
}

// DiffAllRepos returns the per-repo WorkdirDiff for every repo in the project.
func (s *remoteDiffService) DiffAllRepos(ctx context.Context, handle service.ProjectHandle) (map[string]*service.WorkdirDiff, error) {
	return s.c.DiffAllRepos(ctx, handle)
}
