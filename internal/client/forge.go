package client

import (
	"context"
	"net/url"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// ForgeDetectAuth calls Forge.DetectAuth. Prefer ForgeDetect for client code
// that doesn't need the credential payload.
func (c *Client) ForgeDetectAuth(ctx context.Context) (*api.ForgeAuth, error) {
	var auth api.ForgeAuth
	if err := c.get(ctx, "/api/forge/auth", &auth); err != nil {
		return nil, err
	}
	return &auth, nil
}

// ForgeDetect calls Forge.Detect.
func (c *Client) ForgeDetect(ctx context.Context) (*api.ForgeInfo, error) {
	var info api.ForgeInfo
	if err := c.get(ctx, "/api/forge/info", &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// ForgeDetectProvider calls Forge.DetectProvider.
func (c *Client) ForgeDetectProvider(ctx context.Context, repoPath string) (api.RemoteProvider, error) {
	resp, err := c.forgeProvider(ctx, repoPath)
	if err != nil {
		return "", err
	}
	return resp.Provider, nil
}

// ForgeProviderInfo calls Forge.ProviderInfo — the provider plus the state of
// the CLI that drives it (gh / glab / tea).
func (c *Client) ForgeProviderInfo(ctx context.Context, repoPath string) (*api.ForgeProviderInfo, error) {
	resp, err := c.forgeProvider(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	return &api.ForgeProviderInfo{
		Provider:     resp.Provider,
		CLI:          resp.CLI,
		CLIInstalled: resp.CLIInstalled,
		HasLogin:     resp.HasLogin,
		Host:         resp.Host,
		User:         resp.User,
		Hint:         resp.Hint,
	}, nil
}

// forgeProvider performs the single GET both provider accessors share.
func (c *Client) forgeProvider(ctx context.Context, repoPath string) (*api.ForgeProviderResponse, error) {
	var resp api.ForgeProviderResponse
	if err := c.get(ctx, "/api/forge/provider?repo_path="+url.QueryEscape(repoPath), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
