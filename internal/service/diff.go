package service

import "context"

// DiffService covers branch/workdir/file/deep diffs and the index-mutating
// stage/unstage hunk and line operations. ParseHunks is a pure parser with
// no I/O, so it stays a free helper in internal/git rather than appearing
// here (calling it RPC for byte-mashing would be wasteful).
type DiffService interface {
	// BranchDiff returns the full textual diff between two branches.
	BranchDiff(ctx context.Context, repoPath, a, b string) (*BranchDiff, error)

	// WorkdirStatus returns the staged+unstaged working-tree snapshot.
	WorkdirStatus(ctx context.Context, repoPath string) (*WorkdirDiff, error)

	// FileDiff returns the textual diff for a single file between two branches.
	FileDiff(ctx context.Context, repoPath, a, b, path string) (string, error)

	// StagedFileDiff returns the staged-vs-HEAD diff for one file.
	StagedFileDiff(ctx context.Context, repoPath, path string) (string, error)

	// UnstagedFileDiff returns the workdir-vs-staged diff for one file.
	UnstagedFileDiff(ctx context.Context, repoPath, path string) (string, error)

	// DeepFileDiff returns per-file hunks filtered to "new in this branch"
	// (excluding hunks already in the merge base), plus the raw diff. Used
	// by branch_diff and workflow_diff views.
	DeepFileDiff(ctx context.Context, repoPath, mergeBase, branch, defaultBranch, path string) ([]FilteredDiffHunk, string, error)

	// MergeBase returns the merge-base SHA of two refs.
	MergeBase(ctx context.Context, repoPath, a, b string) (string, error)

	// StageHunk stages exactly the given hunk into the index.
	StageHunk(ctx context.Context, repoPath, filePath string, hunk DiffHunk) error

	// UnstageHunk removes exactly the given hunk from the index.
	UnstageHunk(ctx context.Context, repoPath, filePath string, hunk DiffHunk) error

	// StageLines stages a subset of lines within a hunk (line-mode partial staging).
	StageLines(ctx context.Context, repoPath, filePath string, hunk DiffHunk, selectedLineIndices []int) error

	// UnstageLines removes a subset of lines within a hunk from the index.
	UnstageLines(ctx context.Context, repoPath, filePath string, hunk DiffHunk, selectedLineIndices []int) error

	// DiffAllRepos returns the per-repo WorkdirDiff for every repo in the
	// loaded project. Bulk variant of WorkdirStatus addressing the
	// per-repo goroutine fan-out in project_diff.go (CALL-SITES §3.2).
	DiffAllRepos(ctx context.Context, handle ProjectHandle) (map[string]*WorkdirDiff, error)
}
