package api

import "gitlab.com/tanevanwifferen1/singularity/internal/service"

// BranchInfo, BranchComparison, TreeComparison are aliases of the canonical
// types from internal/git (re-exported via internal/service).
type (
	BranchInfo       = service.BranchInfo
	BranchComparison = service.BranchComparison
	TreeComparison   = service.TreeComparison
)

// BranchListResponse is the body for GET /api/branch/list?repo_path=.
type BranchListResponse struct {
	Branches []BranchInfo `json:"branches"`
}

// BranchCreateRequest is the body for POST /api/branch/create.
type BranchCreateRequest struct {
	RepoPath string `json:"repo_path"`
	Name     string `json:"name"`
	From     string `json:"from"`
}

// BranchCheckoutRequest is the body for POST /api/branch/checkout.
type BranchCheckoutRequest struct {
	RepoPath string `json:"repo_path"`
	Branch   string `json:"branch"`
}

// BranchCheckoutDetachedRequest is the body for POST /api/branch/checkout_detached.
type BranchCheckoutDetachedRequest struct {
	RepoPath string `json:"repo_path"`
}

// BranchCheckoutDetachedAtRequest is the body for POST /api/branch/checkout_detached_at.
type BranchCheckoutDetachedAtRequest struct {
	RepoPath string `json:"repo_path"`
	Commit   string `json:"commit"`
}

// BranchDeleteRequest is the body for POST /api/branch/delete.
type BranchDeleteRequest struct {
	RepoPath string `json:"repo_path"`
	Branch   string `json:"branch"`
	Force    bool   `json:"force"`
}

// BranchHEADResponse is the body for GET /api/branch/head?repo_path=.
type BranchHEADResponse struct {
	HEAD string `json:"head"`
}

// BranchResolveRefResponse is the body for GET /api/branch/resolve.
type BranchResolveRefResponse struct {
	SHA string `json:"sha"`
}

// BranchComparisonRequest is the body for POST /api/branch/compare and
// POST /api/branch/compare_tree. Reused from the legacy types.go shape.
type BranchComparisonRequest struct {
	RepoPath string `json:"repo_path"`
	BranchA  string `json:"branch_a"`
	BranchB  string `json:"branch_b"`
}

// BranchMergeRequest is the body for POST /api/branch/merge.
type BranchMergeRequest struct {
	RepoPath        string `json:"repo_path"`
	Branch          string `json:"branch"`
	FastForwardOnly bool   `json:"fast_forward_only,omitempty"`
	NoFastForward   bool   `json:"no_fast_forward,omitempty"`
	Squash          bool   `json:"squash,omitempty"`
	Message         string `json:"message,omitempty"`
}

// BranchMergeResponse is the body for POST /api/branch/merge response.
type BranchMergeResponse struct {
	Success     bool     `json:"success"`
	FastForward bool     `json:"fast_forward"`
	Conflicts   []string `json:"conflicts,omitempty"`
	Message     string   `json:"message,omitempty"`
}
