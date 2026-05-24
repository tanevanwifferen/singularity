package client

import (
	"context"
	"net/url"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// RepoOpen calls Repo.Open via POST /api/repo/open.
func (c *Client) RepoOpen(ctx context.Context, path string) (*api.RepoInfo, error) {
	var info api.RepoInfo
	if err := c.post(ctx, "/api/repo/open", api.RepoOpenRequest{Path: path}, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// RepoInfo calls Repo.Open via GET /api/repo/info?path=. Kept as a separate
// method (vs. RepoOpen) to mirror the legacy SDK shape.
func (c *Client) RepoInfo(ctx context.Context, path string) (*api.RepoInfo, error) {
	var info api.RepoInfo
	if err := c.get(ctx, "/api/repo/info?path="+url.QueryEscape(path), &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// RepoFind calls Repo.Find via GET /api/repo/find?path=.
func (c *Client) RepoFind(ctx context.Context, path string) (string, error) {
	var resp api.RepoFindResponse
	if err := c.get(ctx, "/api/repo/find?path="+url.QueryEscape(path), &resp); err != nil {
		return "", err
	}
	return resp.Path, nil
}

// RepoSubscribe calls Repo.Subscribe — long-lived stream.
func (c *Client) RepoSubscribe(ctx context.Context, repoPath string) (<-chan *api.RepoInfo, func(), error) {
	return startStream(ctx, c, "/api/repo/subscribe", api.RepoSubscribeRequest{RepoPath: repoPath}, reEncode[*api.RepoInfo])
}
