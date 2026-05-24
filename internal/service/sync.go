package service

import (
	"context"
	"time"
)

// SyncService covers upstream status reads plus the long-running fetch /
// pull / push / pull-rebase / set-upstream-and-push operations. Mutating
// methods stream progress lines via SyncProgressEvent rather than buffering
// output into a single blob — see CALL-SITES §2.8 ("currently captures
// output into a single string; recommend WS streaming").
//
// Kept distinct from RepoService despite the brief's suggestion to merge:
// 27 call sites, all mutating-or-network-bound, justify a focused interface
// and let the daemon enforce per-repo serialization without leaking that
// concern into RepoService.
type SyncService interface {
	// UpstreamStatus returns ahead/behind vs the configured upstream.
	UpstreamStatus(ctx context.Context, repoPath string) (*UpstreamStatus, error)

	// LastFetchTime returns the timestamp of the last `git fetch` for the repo.
	LastFetchTime(ctx context.Context, repoPath string) (time.Time, error)

	// Fetch fetches the given remote (empty = origin). Streams progress.
	Fetch(ctx context.Context, repoPath, remote string) (<-chan SyncProgressEvent, func(), error)

	// Pull does a standard pull. Streams progress. Returns ErrConflict via
	// the stream's terminal Err field on merge conflicts.
	Pull(ctx context.Context, repoPath string) (<-chan SyncProgressEvent, func(), error)

	// Push pushes the current branch; force=true uses --force-with-lease.
	Push(ctx context.Context, repoPath string, force bool) (<-chan SyncProgressEvent, func(), error)

	// PullRebase does `git pull --rebase`. Streams progress.
	PullRebase(ctx context.Context, repoPath string) (<-chan SyncProgressEvent, func(), error)

	// SetUpstreamAndPush sets the upstream tracking remote and pushes.
	SetUpstreamAndPush(ctx context.Context, repoPath, remote string) (<-chan SyncProgressEvent, func(), error)

	// SyncAllRepos runs the user's smart-sync flow (fetch + pull-rebase +
	// push) across every repo in a project, in parallel daemon-side.
	// Bulk variant addressing the per-repo goroutine fan-out in
	// project_sync.go (CALL-SITES §3.2).
	SyncAllRepos(ctx context.Context, handle ProjectHandle, force bool) (<-chan SyncProgressEvent, func(), error)
}
