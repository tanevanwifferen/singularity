package service

import "context"

// MRService covers merge/pull request creation via forge APIs plus the
// CLI-fallback path (gh / glab). Detection-of-existing-MR semantics
// surface as ErrMRAlreadyExists (round-tripped across the wire).
type MRService interface {
	// GenerateTitle produces an AI-suggested MR title.
	GenerateTitle(ctx context.Context, repoPath, source, target string) (string, error)

	// GenerateDescription produces an AI-suggested MR description (body).
	GenerateDescription(ctx context.Context, repoPath, source, target string) (string, error)

	// Create opens a merge/pull request on the detected forge. Returns
	// ErrMRAlreadyExists (errors.Is) when one already exists for `source`.
	Create(ctx context.Context, repoPath, source, target, title, description string, reviewers []string) (*MergeRequest, error)

	// CreateCLI falls back to `gh pr create` / `glab mr create` for forges
	// where direct API auth is unavailable.
	CreateCLI(ctx context.Context, repoPath string, provider RemoteProvider, baseBranch string) (*MRResult, error)
}
