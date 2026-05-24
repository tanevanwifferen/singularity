package client

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// MRGenerateTitle calls MR.GenerateTitle.
func (c *Client) MRGenerateTitle(ctx context.Context, repoPath, source, target string) (string, error) {
	var resp api.MRTextResponse
	req := api.MRGenerateRequest{RepoPath: repoPath, SourceBranch: source, TargetBranch: target}
	if err := c.post(ctx, "/api/mr/title", req, &resp); err != nil {
		return "", err
	}
	return resp.Text, nil
}

// MRGenerateDescription calls MR.GenerateDescription.
func (c *Client) MRGenerateDescription(ctx context.Context, repoPath, source, target string) (string, error) {
	var resp api.MRTextResponse
	req := api.MRGenerateRequest{RepoPath: repoPath, SourceBranch: source, TargetBranch: target}
	if err := c.post(ctx, "/api/mr/description", req, &resp); err != nil {
		return "", err
	}
	return resp.Text, nil
}

// MRCreate calls MR.Create. Returns ErrMRAlreadyExists (via errors.Is) when
// the daemon detects an existing MR for the source branch.
func (c *Client) MRCreate(ctx context.Context, repoPath, source, target, title, description string, reviewers []string) (*api.MergeRequest, error) {
	var mr api.MergeRequest
	req := api.MRRequest{
		RepoPath:     repoPath,
		SourceBranch: source,
		TargetBranch: target,
		Title:        title,
		Description:  description,
		Reviewers:    reviewers,
	}
	if err := c.post(ctx, "/api/mr/create", req, &mr); err != nil {
		return nil, err
	}
	return &mr, nil
}

// MRCreateCLI calls MR.CreateCLI.
func (c *Client) MRCreateCLI(ctx context.Context, repoPath string, provider api.RemoteProvider, baseBranch string) (*api.MRResult, error) {
	var res api.MRResult
	req := api.MRCreateCLIRequest{RepoPath: repoPath, Provider: provider, BaseBranch: baseBranch}
	if err := c.post(ctx, "/api/mr/create_cli", req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}
