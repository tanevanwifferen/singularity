package api

import "gitlab.com/tanevanwifferen1/singularity/internal/service"

// Diff DTOs alias the canonical types from internal/git via internal/service.
type (
	BranchDiff       = service.BranchDiff
	WorkdirDiff      = service.WorkdirDiff
	WorkdirStatus    = service.WorkdirStatus
	FileChange       = service.FileChange
	DiffHunk         = service.DiffHunk
	FilteredDiffHunk = service.FilteredDiffHunk
)

// BranchDiffRequest is the body for POST /api/diff/branch (and the legacy
// /api/branch/diff alias).
type BranchDiffRequest struct {
	RepoPath string `json:"repo_path"`
	BranchA  string `json:"branch_a"`
	BranchB  string `json:"branch_b"`
}

// FileDiffRequest is the body for POST /api/diff/file.
type FileDiffRequest struct {
	RepoPath string `json:"repo_path"`
	BranchA  string `json:"branch_a"`
	BranchB  string `json:"branch_b"`
	Path     string `json:"path"`
}

// SingleFileDiffRequest covers staged / unstaged single-file diffs.
type SingleFileDiffRequest struct {
	RepoPath string `json:"repo_path"`
	Path     string `json:"path"`
}

// FileDiffResponse is the body for any endpoint returning a single textual
// file-level diff (FileDiff / StagedFileDiff / UnstagedFileDiff /
// Commit.FileDiff / Commit.FullDiff).
type FileDiffResponse struct {
	Diff string `json:"diff"`
}

// DeepFileDiffRequest is the body for POST /api/diff/file_deep.
type DeepFileDiffRequest struct {
	RepoPath      string `json:"repo_path"`
	MergeBase     string `json:"merge_base"`
	Branch        string `json:"branch"`
	DefaultBranch string `json:"default_branch"`
	Path          string `json:"path"`
}

// DeepFileDiffResponse is the body for POST /api/diff/file_deep.
type DeepFileDiffResponse struct {
	Hunks   []FilteredDiffHunk `json:"hunks"`
	RawDiff string             `json:"raw_diff"`
}

// MergeBaseRequest is the body for POST /api/diff/merge_base.
type MergeBaseRequest struct {
	RepoPath string `json:"repo_path"`
	RefA     string `json:"ref_a"`
	RefB     string `json:"ref_b"`
}

// MergeBaseResponse is the body for POST /api/diff/merge_base.
type MergeBaseResponse struct {
	SHA string `json:"sha"`
}

// HunkRequest is the body for stage/unstage-hunk endpoints.
type HunkRequest struct {
	RepoPath string   `json:"repo_path"`
	Path     string   `json:"path"`
	Hunk     DiffHunk `json:"hunk"`
}

// HunkLinesRequest is the body for stage/unstage-lines endpoints.
type HunkLinesRequest struct {
	RepoPath            string   `json:"repo_path"`
	Path                string   `json:"path"`
	Hunk                DiffHunk `json:"hunk"`
	SelectedLineIndices []int    `json:"selected_line_indices"`
}

// DiffAllReposResponse is the body for POST /api/diff/all_repos.
type DiffAllReposResponse struct {
	Repos map[string]*WorkdirDiff `json:"repos"`
}
