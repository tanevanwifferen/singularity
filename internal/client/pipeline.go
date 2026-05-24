package client

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// PipelineStatuses calls Pipeline.Statuses.
func (c *Client) PipelineStatuses(ctx context.Context, repoPath string, branches []api.BranchInfo) (map[string]*api.PipelineInfo, error) {
	var resp api.PipelineStatusesResponse
	if err := c.post(ctx, "/api/pipeline/statuses", api.PipelineStatusesRequest{RepoPath: repoPath, Branches: branches}, &resp); err != nil {
		return nil, err
	}
	return resp.Pipelines, nil
}

// PipelineRetry calls Pipeline.Retry.
func (c *Client) PipelineRetry(ctx context.Context, repoPath, branch string) error {
	return c.post(ctx, "/api/pipeline/retry", api.PipelineRetryRequest{RepoPath: repoPath, Branch: branch}, nil)
}

// PipelineSubscribe calls Pipeline.Subscribe — stream.
func (c *Client) PipelineSubscribe(ctx context.Context, repoPath string) (<-chan service.PipelineEvent, func(), error) {
	return startStream(ctx, c, "/api/pipeline/subscribe", api.RepoPathRequest{RepoPath: repoPath}, reEncode[service.PipelineEvent])
}
