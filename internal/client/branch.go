package client

import (
	"context"
	"net/url"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// BranchList calls Branch.List.
func (c *Client) BranchList(ctx context.Context, repoPath string) ([]api.BranchInfo, error) {
	var resp api.BranchListResponse
	if err := c.get(ctx, "/api/branch/list?repo_path="+url.QueryEscape(repoPath), &resp); err != nil {
		return nil, err
	}
	return resp.Branches, nil
}

// BranchCreate calls Branch.Create.
func (c *Client) BranchCreate(ctx context.Context, repoPath, name, from string) error {
	return c.post(ctx, "/api/branch/create", api.BranchCreateRequest{RepoPath: repoPath, Name: name, From: from}, nil)
}

// BranchCheckout calls Branch.Checkout.
func (c *Client) BranchCheckout(ctx context.Context, repoPath, branch string) error {
	return c.post(ctx, "/api/branch/checkout", api.BranchCheckoutRequest{RepoPath: repoPath, Branch: branch}, nil)
}

// BranchCheckoutDetached calls Branch.CheckoutDetached.
func (c *Client) BranchCheckoutDetached(ctx context.Context, repoPath string) error {
	return c.post(ctx, "/api/branch/checkout_detached", api.BranchCheckoutDetachedRequest{RepoPath: repoPath}, nil)
}

// BranchCheckoutDetachedAt calls Branch.CheckoutDetachedAt.
func (c *Client) BranchCheckoutDetachedAt(ctx context.Context, repoPath, commit string) error {
	return c.post(ctx, "/api/branch/checkout_detached_at", api.BranchCheckoutDetachedAtRequest{RepoPath: repoPath, Commit: commit}, nil)
}

// BranchDelete calls Branch.Delete.
func (c *Client) BranchDelete(ctx context.Context, repoPath, branch string, force bool) error {
	return c.post(ctx, "/api/branch/delete", api.BranchDeleteRequest{RepoPath: repoPath, Branch: branch, Force: force}, nil)
}

// BranchHEAD calls Branch.HEAD.
func (c *Client) BranchHEAD(ctx context.Context, repoPath string) (string, error) {
	var resp api.BranchHEADResponse
	if err := c.get(ctx, "/api/branch/head?repo_path="+url.QueryEscape(repoPath), &resp); err != nil {
		return "", err
	}
	return resp.HEAD, nil
}

// BranchResolveRef calls Branch.ResolveRef.
func (c *Client) BranchResolveRef(ctx context.Context, repoPath, ref string) (string, error) {
	var resp api.BranchResolveRefResponse
	q := "?repo_path=" + url.QueryEscape(repoPath) + "&ref=" + url.QueryEscape(ref)
	if err := c.get(ctx, "/api/branch/resolve"+q, &resp); err != nil {
		return "", err
	}
	return resp.SHA, nil
}

// BranchCompare calls Branch.Compare.
func (c *Client) BranchCompare(ctx context.Context, repoPath, a, b string) (*api.BranchComparison, error) {
	var cmp api.BranchComparison
	if err := c.post(ctx, "/api/branch/compare", api.BranchComparisonRequest{RepoPath: repoPath, BranchA: a, BranchB: b}, &cmp); err != nil {
		return nil, err
	}
	return &cmp, nil
}

// BranchCompareByTree calls Branch.CompareByTree.
func (c *Client) BranchCompareByTree(ctx context.Context, repoPath, a, b string) (*api.TreeComparison, error) {
	var cmp api.TreeComparison
	if err := c.post(ctx, "/api/branch/compare_tree", api.BranchComparisonRequest{RepoPath: repoPath, BranchA: a, BranchB: b}, &cmp); err != nil {
		return nil, err
	}
	return &cmp, nil
}
