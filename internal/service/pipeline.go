package service

import "context"

// PipelineService covers CI pipeline reads + retry, with a subscription for
// pipeline_update WS events (already declared in internal/api, never
// broadcast today — COVERAGE.md §3c flags this gap).
//
// FormatPipelineStatus / status enum constants are pure rendering and stay
// in internal/git as free helpers; views call them directly.
type PipelineService interface {
	// Statuses returns the pipeline state for each given branch in one call.
	Statuses(ctx context.Context, repoPath string, branches []BranchInfo) (map[string]*PipelineInfo, error)

	// Retry retries the failed pipeline on the given branch.
	Retry(ctx context.Context, repoPath, branch string) error

	// Subscribe streams pipeline status changes for the repo. The daemon
	// polls upstream forges and emits PipelineEvent when state changes.
	Subscribe(ctx context.Context, repoPath string) (<-chan PipelineEvent, func(), error)
}
