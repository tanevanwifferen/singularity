package remote

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// remoteBranchService implements service.BranchService.
type remoteBranchService struct {
	c *client.Client
}

// List returns the BranchInfo set for the repo.
func (s *remoteBranchService) List(ctx context.Context, repoPath string) ([]service.BranchInfo, error) {
	return s.c.BranchList(ctx, repoPath)
}

// Create creates a new branch named `name` starting from `from`.
func (s *remoteBranchService) Create(ctx context.Context, repoPath, name, from string) error {
	return s.c.BranchCreate(ctx, repoPath, name, from)
}

// Checkout switches the working tree to the named branch.
func (s *remoteBranchService) Checkout(ctx context.Context, repoPath, branch string) error {
	return s.c.BranchCheckout(ctx, repoPath, branch)
}

// CheckoutDetached detaches HEAD at the current commit.
func (s *remoteBranchService) CheckoutDetached(ctx context.Context, repoPath string) error {
	return s.c.BranchCheckoutDetached(ctx, repoPath)
}

// CheckoutDetachedAt detaches HEAD at the given commit/ref.
func (s *remoteBranchService) CheckoutDetachedAt(ctx context.Context, repoPath, commit string) error {
	return s.c.BranchCheckoutDetachedAt(ctx, repoPath, commit)
}

// Delete deletes a local branch.
func (s *remoteBranchService) Delete(ctx context.Context, repoPath, branch string, force bool) error {
	return s.c.BranchDelete(ctx, repoPath, branch, force)
}

// HEAD returns the current HEAD ref of the repo.
func (s *remoteBranchService) HEAD(ctx context.Context, repoPath string) (string, error) {
	return s.c.BranchHEAD(ctx, repoPath)
}

// ResolveRef resolves any ref expression to a full commit SHA.
func (s *remoteBranchService) ResolveRef(ctx context.Context, repoPath, ref string) (string, error) {
	return s.c.BranchResolveRef(ctx, repoPath, ref)
}

// Compare returns ahead/behind/divergence counts between two branches.
func (s *remoteBranchService) Compare(ctx context.Context, repoPath, a, b string) (*service.BranchComparison, error) {
	return s.c.BranchCompare(ctx, repoPath, a, b)
}

// CompareByTree returns a per-path tree-level comparison.
func (s *remoteBranchService) CompareByTree(ctx context.Context, repoPath, a, b string) (*service.TreeComparison, error) {
	return s.c.BranchCompareByTree(ctx, repoPath, a, b)
}

// Merge merges the given branch into the current HEAD.
func (s *remoteBranchService) Merge(ctx context.Context, repoPath, branch string, opts service.MergeOptions) (*service.MergeResult, error) {
	resp, err := s.c.BranchMerge(ctx, repoPath, branch, opts.FastForwardOnly, opts.NoFastForward, opts.Squash, opts.Message)
	if err != nil {
		// Return partial result on conflict
		if resp != nil {
			return &service.MergeResult{
				Success:     resp.Success,
				FastForward: resp.FastForward,
				Conflicts:   resp.Conflicts,
				Message:     resp.Message,
			}, err
		}
		return nil, err
	}
	return &service.MergeResult{
		Success:     resp.Success,
		FastForward: resp.FastForward,
		Conflicts:   resp.Conflicts,
		Message:     resp.Message,
	}, nil
}
