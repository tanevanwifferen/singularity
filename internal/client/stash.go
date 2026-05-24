package client

import (
	"context"
	"net/url"
	"strconv"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// StashList calls Stash.List.
func (c *Client) StashList(ctx context.Context, repoPath string) ([]api.StashEntry, error) {
	var resp api.StashListResponse
	if err := c.get(ctx, "/api/stash/list?repo_path="+url.QueryEscape(repoPath), &resp); err != nil {
		return nil, err
	}
	return resp.Entries, nil
}

// StashGet calls Stash.Get.
func (c *Client) StashGet(ctx context.Context, repoPath string, index int) (*api.StashEntry, error) {
	var entry api.StashEntry
	q := "?repo_path=" + url.QueryEscape(repoPath) + "&index=" + strconv.Itoa(index)
	if err := c.get(ctx, "/api/stash/get"+q, &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

// StashCreate calls Stash.Create.
func (c *Client) StashCreate(ctx context.Context, repoPath, message string, includeUntracked bool) (int, error) {
	var resp api.StashCreateResponse
	req := api.StashCreateRequest{RepoPath: repoPath, Message: message, IncludeUntracked: includeUntracked}
	if err := c.post(ctx, "/api/stash/create", req, &resp); err != nil {
		return 0, err
	}
	return resp.Index, nil
}

// StashApply calls Stash.Apply.
func (c *Client) StashApply(ctx context.Context, repoPath string, index int, pop bool) error {
	return c.post(ctx, "/api/stash/apply", api.StashApplyRequest{RepoPath: repoPath, Index: index, Pop: pop}, nil)
}

// StashDrop calls Stash.Drop.
func (c *Client) StashDrop(ctx context.Context, repoPath string, index int) error {
	return c.post(ctx, "/api/stash/drop", api.StashDropRequest{RepoPath: repoPath, Index: index}, nil)
}

// StashClear calls Stash.Clear.
func (c *Client) StashClear(ctx context.Context, repoPath string) error {
	return c.post(ctx, "/api/stash/clear", api.RepoPathRequest{RepoPath: repoPath}, nil)
}

// StashListAllRepos calls Stash.ListAllRepos.
func (c *Client) StashListAllRepos(ctx context.Context, handle service.ProjectHandle) ([]api.RepoStashList, error) {
	var resp api.StashListAllResponse
	if err := c.post(ctx, "/api/stash/list_all", api.ProjectHandleRequest{Handle: handle}, &resp); err != nil {
		return nil, err
	}
	return resp.Repos, nil
}

// StashAllRepos calls Stash.StashAllRepos.
func (c *Client) StashAllRepos(ctx context.Context, handle service.ProjectHandle, message string, includeUntracked bool) ([]api.RepoStashResult, error) {
	var resp api.StashBulkResponse
	req := api.StashAllRequest{Handle: handle, Message: message, IncludeUntracked: includeUntracked}
	if err := c.post(ctx, "/api/stash/all", req, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// StashApplyAllRepos calls Stash.ApplyStashAllRepos.
func (c *Client) StashApplyAllRepos(ctx context.Context, handle service.ProjectHandle, message string, pop bool) ([]api.RepoStashResult, error) {
	var resp api.StashBulkResponse
	req := api.StashApplyAllRequest{Handle: handle, Message: message, Pop: pop}
	if err := c.post(ctx, "/api/stash/apply_all", req, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}
