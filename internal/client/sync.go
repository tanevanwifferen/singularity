package client

import (
	"context"
	"net/url"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// SyncUpstreamStatus calls Sync.UpstreamStatus.
func (c *Client) SyncUpstreamStatus(ctx context.Context, repoPath string) (*api.UpstreamStatus, error) {
	var st api.UpstreamStatus
	if err := c.get(ctx, "/api/sync/upstream?repo_path="+url.QueryEscape(repoPath), &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// SyncLastFetchTime calls Sync.LastFetchTime.
func (c *Client) SyncLastFetchTime(ctx context.Context, repoPath string) (time.Time, error) {
	var resp api.LastFetchResponse
	if err := c.get(ctx, "/api/sync/last_fetch?repo_path="+url.QueryEscape(repoPath), &resp); err != nil {
		return time.Time{}, err
	}
	return resp.Time, nil
}

// SyncFetch calls Sync.Fetch — stream.
func (c *Client) SyncFetch(ctx context.Context, repoPath, remote string) (<-chan service.SyncProgressEvent, func(), error) {
	return startStream(ctx, c, "/api/sync/fetch", api.SyncFetchRequest{RepoPath: repoPath, Remote: remote}, reEncode[service.SyncProgressEvent])
}

// SyncPull calls Sync.Pull — stream.
func (c *Client) SyncPull(ctx context.Context, repoPath string) (<-chan service.SyncProgressEvent, func(), error) {
	return startStream(ctx, c, "/api/sync/pull", api.RepoPathRequest{RepoPath: repoPath}, reEncode[service.SyncProgressEvent])
}

// SyncPush calls Sync.Push — stream.
func (c *Client) SyncPush(ctx context.Context, repoPath string, force bool) (<-chan service.SyncProgressEvent, func(), error) {
	return startStream(ctx, c, "/api/sync/push", api.SyncPushRequest{RepoPath: repoPath, Force: force}, reEncode[service.SyncProgressEvent])
}

// SyncPullRebase calls Sync.PullRebase — stream.
func (c *Client) SyncPullRebase(ctx context.Context, repoPath string) (<-chan service.SyncProgressEvent, func(), error) {
	return startStream(ctx, c, "/api/sync/pull_rebase", api.RepoPathRequest{RepoPath: repoPath}, reEncode[service.SyncProgressEvent])
}

// SyncSetUpstreamAndPush calls Sync.SetUpstreamAndPush — stream.
func (c *Client) SyncSetUpstreamAndPush(ctx context.Context, repoPath, remote string) (<-chan service.SyncProgressEvent, func(), error) {
	return startStream(ctx, c, "/api/sync/set_upstream", api.SyncSetUpstreamRequest{RepoPath: repoPath, Remote: remote}, reEncode[service.SyncProgressEvent])
}

// SyncAllRepos calls Sync.SyncAllRepos — stream.
func (c *Client) SyncAllRepos(ctx context.Context, handle service.ProjectHandle, force bool) (<-chan service.SyncProgressEvent, func(), error) {
	return startStream(ctx, c, "/api/sync/all", api.SyncAllRequest{Handle: handle, Force: force}, reEncode[service.SyncProgressEvent])
}
