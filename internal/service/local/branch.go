package local

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// localBranchService implements service.BranchService by calling
// internal/git's branch-level helpers directly. List re-opens the repo
// and returns the populated branch slice (matching the existing pattern
// in the views).
type localBranchService struct{}

// List re-opens the repo and returns its branch slice.
func (s *localBranchService) List(ctx context.Context, repoPath string) ([]service.BranchInfo, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	info, err := git.OpenRepo(repoPath)
	if err != nil {
		return nil, wrapErr(err)
	}
	return info.Branches, nil
}

// Create creates a new branch. When from is empty, git resolves it relative
// to HEAD (the historical behavior of git.CreateBranch).
func (s *localBranchService) Create(ctx context.Context, repoPath, name, from string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.CreateBranch(repoPath, name, from))
}

// Checkout switches the working tree to the named branch.
func (s *localBranchService) Checkout(ctx context.Context, repoPath, branch string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.Checkout(repoPath, branch))
}

// CheckoutDetached detaches HEAD at the current commit.
func (s *localBranchService) CheckoutDetached(ctx context.Context, repoPath string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.CheckoutDetached(repoPath))
}

// CheckoutDetachedAt detaches HEAD at the given commit/ref.
func (s *localBranchService) CheckoutDetachedAt(ctx context.Context, repoPath, commit string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.CheckoutDetachedAt(repoPath, commit))
}

// Delete deletes a local branch.
func (s *localBranchService) Delete(ctx context.Context, repoPath, branch string, force bool) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.DeleteBranch(repoPath, branch, force))
}

// HEAD returns the current HEAD ref of the repo (commit SHA).
func (s *localBranchService) HEAD(ctx context.Context, repoPath string) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	head, err := git.GetHEAD(repoPath)
	if err != nil {
		return "", wrapErr(err)
	}
	return head, nil
}

// ResolveRef resolves any ref expression to a full commit SHA. Returns the
// empty string (and a nil error) when the ref is unknown — git.ResolveRef's
// historical contract.
func (s *localBranchService) ResolveRef(ctx context.Context, repoPath, ref string) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	return git.ResolveRef(repoPath, ref), nil
}

// Compare returns ahead/behind/divergence between two branches.
func (s *localBranchService) Compare(ctx context.Context, repoPath, a, b string) (*service.BranchComparison, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	cmp, err := git.CompareBranches(repoPath, a, b)
	if err != nil {
		return nil, wrapErr(err)
	}
	return cmp, nil
}

// CompareByTree returns a tree-level comparison between two branches.
func (s *localBranchService) CompareByTree(ctx context.Context, repoPath, a, b string) (*service.TreeComparison, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	cmp, err := git.CompareBranchesByTree(repoPath, a, b)
	if err != nil {
		return nil, wrapErr(err)
	}
	return cmp, nil
}
