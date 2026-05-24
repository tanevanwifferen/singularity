package service

import "context"

// RepoService is the read-only capability for opening and inspecting
// individual git repositories. Mutations live on the more specific services
// (BranchService, CommitService, SyncService, ...).
//
// Kept separate from SyncService because the audit (CALL-SITES.md §2.1 vs
// §2.8) shows distinct usage patterns: Open/Find is invoked at view init
// from many surfaces while sync ops are concentrated in sync.go /
// project_sync.go and are long-running. Merging them would inflate one
// interface for no callsite benefit.
type RepoService interface {
	// Open returns the RepoInfo for the given repo path, populating
	// branches, remotes, HEAD, and dirty state in one shot.
	Open(ctx context.Context, path string) (*RepoInfo, error)

	// Find walks upward from path looking for a git repository root and
	// returns its absolute path (the .git-containing directory's parent).
	Find(ctx context.Context, path string) (string, error)

	// Subscribe streams RepoInfo updates whenever the daemon detects a
	// change to the given repo (HEAD move, branch ref update, dirty-bit
	// flip). The channel closes when the context is canceled, the cancel
	// closure is invoked, or the daemon stops watching the path.
	Subscribe(ctx context.Context, path string) (<-chan *RepoInfo, func(), error)
}
