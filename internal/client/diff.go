package client

import (
	"context"
	"net/url"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// DiffBranch calls Diff.BranchDiff.
func (c *Client) DiffBranch(ctx context.Context, repoPath, a, b string) (*api.BranchDiff, error) {
	var d api.BranchDiff
	if err := c.post(ctx, "/api/diff/branch", api.BranchDiffRequest{RepoPath: repoPath, BranchA: a, BranchB: b}, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// DiffWorkdir calls Diff.WorkdirStatus.
func (c *Client) DiffWorkdir(ctx context.Context, repoPath string) (*api.WorkdirDiff, error) {
	var d api.WorkdirDiff
	if err := c.get(ctx, "/api/diff/workdir?repo_path="+url.QueryEscape(repoPath), &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// DiffFile calls Diff.FileDiff.
func (c *Client) DiffFile(ctx context.Context, repoPath, a, b, path string) (string, error) {
	var resp api.FileDiffResponse
	if err := c.post(ctx, "/api/diff/file", api.FileDiffRequest{RepoPath: repoPath, BranchA: a, BranchB: b, Path: path}, &resp); err != nil {
		return "", err
	}
	return resp.Diff, nil
}

// DiffStagedFile calls Diff.StagedFileDiff.
func (c *Client) DiffStagedFile(ctx context.Context, repoPath, path string) (string, error) {
	var resp api.FileDiffResponse
	if err := c.post(ctx, "/api/diff/file_staged", api.SingleFileDiffRequest{RepoPath: repoPath, Path: path}, &resp); err != nil {
		return "", err
	}
	return resp.Diff, nil
}

// DiffUnstagedFile calls Diff.UnstagedFileDiff.
func (c *Client) DiffUnstagedFile(ctx context.Context, repoPath, path string) (string, error) {
	var resp api.FileDiffResponse
	if err := c.post(ctx, "/api/diff/file_unstaged", api.SingleFileDiffRequest{RepoPath: repoPath, Path: path}, &resp); err != nil {
		return "", err
	}
	return resp.Diff, nil
}

// DiffDeepFile calls Diff.DeepFileDiff.
func (c *Client) DiffDeepFile(ctx context.Context, repoPath, mergeBase, branch, defaultBranch, path string) ([]api.FilteredDiffHunk, string, error) {
	var resp api.DeepFileDiffResponse
	req := api.DeepFileDiffRequest{RepoPath: repoPath, MergeBase: mergeBase, Branch: branch, DefaultBranch: defaultBranch, Path: path}
	if err := c.post(ctx, "/api/diff/file_deep", req, &resp); err != nil {
		return nil, "", err
	}
	return resp.Hunks, resp.RawDiff, nil
}

// DiffMergeBase calls Diff.MergeBase.
func (c *Client) DiffMergeBase(ctx context.Context, repoPath, a, b string) (string, error) {
	var resp api.MergeBaseResponse
	if err := c.post(ctx, "/api/diff/merge_base", api.MergeBaseRequest{RepoPath: repoPath, RefA: a, RefB: b}, &resp); err != nil {
		return "", err
	}
	return resp.SHA, nil
}

// DiffStageHunk calls Diff.StageHunk.
func (c *Client) DiffStageHunk(ctx context.Context, repoPath, filePath string, hunk api.DiffHunk) error {
	return c.post(ctx, "/api/diff/stage_hunk", api.HunkRequest{RepoPath: repoPath, Path: filePath, Hunk: hunk}, nil)
}

// DiffUnstageHunk calls Diff.UnstageHunk.
func (c *Client) DiffUnstageHunk(ctx context.Context, repoPath, filePath string, hunk api.DiffHunk) error {
	return c.post(ctx, "/api/diff/unstage_hunk", api.HunkRequest{RepoPath: repoPath, Path: filePath, Hunk: hunk}, nil)
}

// DiffStageLines calls Diff.StageLines.
func (c *Client) DiffStageLines(ctx context.Context, repoPath, filePath string, hunk api.DiffHunk, lines []int) error {
	return c.post(ctx, "/api/diff/stage_lines", api.HunkLinesRequest{RepoPath: repoPath, Path: filePath, Hunk: hunk, SelectedLineIndices: lines}, nil)
}

// DiffUnstageLines calls Diff.UnstageLines.
func (c *Client) DiffUnstageLines(ctx context.Context, repoPath, filePath string, hunk api.DiffHunk, lines []int) error {
	return c.post(ctx, "/api/diff/unstage_lines", api.HunkLinesRequest{RepoPath: repoPath, Path: filePath, Hunk: hunk, SelectedLineIndices: lines}, nil)
}

// DiffAllRepos calls Diff.DiffAllRepos.
func (c *Client) DiffAllRepos(ctx context.Context, handle service.ProjectHandle) (map[string]*api.WorkdirDiff, error) {
	var resp api.DiffAllReposResponse
	if err := c.post(ctx, "/api/diff/all_repos", api.ProjectHandleRequest{Handle: handle}, &resp); err != nil {
		return nil, err
	}
	return resp.Repos, nil
}
