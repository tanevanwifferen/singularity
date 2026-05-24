package service

import "context"

// BranchService covers branch listing, creation, checkout (including
// detached), HEAD inspection, ref resolution, and cross-branch comparison.
// CompareBranches and CompareBranchesByTree fold in here from the audit's
// §2.12 because they are pure branch metadata operations.
type BranchService interface {
	// List returns the BranchInfo set for the repo (local + tracked).
	// This is the same data already populated by Open in RepoInfo, but
	// exposed as a focused call for refresh paths.
	List(ctx context.Context, repoPath string) ([]BranchInfo, error)

	// Create creates a new branch named `name` starting from `from`
	// (default branch if empty). Does not checkout.
	Create(ctx context.Context, repoPath, name, from string) error

	// Checkout switches the working tree to the named branch.
	Checkout(ctx context.Context, repoPath, branch string) error

	// CheckoutDetached detaches HEAD at the current commit.
	CheckoutDetached(ctx context.Context, repoPath string) error

	// CheckoutDetachedAt detaches HEAD at the given commit/ref.
	CheckoutDetachedAt(ctx context.Context, repoPath, commit string) error

	// Delete deletes a local branch; force=true allows deleting unmerged branches.
	Delete(ctx context.Context, repoPath, branch string, force bool) error

	// HEAD returns the current HEAD ref of the repo (commit SHA).
	HEAD(ctx context.Context, repoPath string) (string, error)

	// ResolveRef resolves any ref expression (branch name, tag, SHA prefix)
	// to a full commit SHA, returning empty string when the ref is unknown.
	ResolveRef(ctx context.Context, repoPath, ref string) (string, error)

	// Compare returns ahead/behind/divergence counts between two branches.
	Compare(ctx context.Context, repoPath, a, b string) (*BranchComparison, error)

	// CompareByTree returns a per-path tree-level comparison between two branches.
	CompareByTree(ctx context.Context, repoPath, a, b string) (*TreeComparison, error)
}
