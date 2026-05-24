package client

import (
	"context"
	"net/url"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// WorktreeList calls Worktree.List.
func (c *Client) WorktreeList(ctx context.Context, repoPath string) ([]api.Worktree, error) {
	var resp api.WorktreeListResponse
	if err := c.get(ctx, "/api/worktree/list?repo_path="+url.QueryEscape(repoPath), &resp); err != nil {
		return nil, err
	}
	return resp.Worktrees, nil
}

// WorktreeCreate calls Worktree.Create.
func (c *Client) WorktreeCreate(ctx context.Context, repoPath, path, branch string, createBranch bool, startPoint string) error {
	req := api.WorktreeCreateRequest{RepoPath: repoPath, Path: path, Branch: branch, CreateBranch: createBranch, StartPoint: startPoint}
	return c.post(ctx, "/api/worktree/create", req, nil)
}

// WorktreeRemove calls Worktree.Remove.
func (c *Client) WorktreeRemove(ctx context.Context, repoPath, path string, force bool) error {
	return c.post(ctx, "/api/worktree/remove", api.WorktreeRemoveRequest{RepoPath: repoPath, Path: path, Force: force}, nil)
}

// WorktreePrune calls Worktree.Prune.
func (c *Client) WorktreePrune(ctx context.Context, repoPath string) error {
	return c.post(ctx, "/api/worktree/prune", api.RepoPathRequest{RepoPath: repoPath}, nil)
}

// WorktreeLock calls Worktree.Lock.
func (c *Client) WorktreeLock(ctx context.Context, repoPath, path string) error {
	return c.post(ctx, "/api/worktree/lock", api.WorktreePathRequest{RepoPath: repoPath, Path: path}, nil)
}

// WorktreeUnlock calls Worktree.Unlock.
func (c *Client) WorktreeUnlock(ctx context.Context, repoPath, path string) error {
	return c.post(ctx, "/api/worktree/unlock", api.WorktreePathRequest{RepoPath: repoPath, Path: path}, nil)
}
