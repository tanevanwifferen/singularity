package service

import "context"

// CommitService covers history reads, commit-level diffs, AI-suggested
// commit messages, and the mutating cherry-pick / reset / amend ops.
//
// Note: clipboard is NOT in this interface (CALL-SITES gotcha #3); copying
// a commit hash is OS-local and must run client-side via a local helper.
type CommitService interface {
	// SuggestMessage generates a conventional-commit message for the
	// staged changes, calling out to Claude if available.
	SuggestMessage(ctx context.Context, repoPath string) (string, error)

	// Files returns the list of files touched by a commit.
	Files(ctx context.Context, repoPath, hash string) ([]FileChange, error)

	// FileDiff returns the diff for one file in one commit.
	FileDiff(ctx context.Context, repoPath, hash, filePath string) (string, error)

	// FullDiff returns the full diff of one commit (all files concatenated).
	FullDiff(ctx context.Context, repoPath, hash string) (string, error)

	// CherryPick cherry-picks the given commit onto the current branch.
	// Returns ErrConflict on cherry-pick conflicts.
	CherryPick(ctx context.Context, repoPath, hash string) error

	// Reset moves HEAD to the given commit. mode is "soft" | "mixed" | "hard".
	Reset(ctx context.Context, repoPath, hash, mode string) error

	// AmendMessage rewrites the message of the most recent commit.
	AmendMessage(ctx context.Context, repoPath, message string) error

	// GenerateMessage is the structured variant of SuggestMessage that
	// also returns the parsed type/scope/subject/body. Mirrors the
	// existing /api/commit/message endpoint.
	GenerateMessage(ctx context.Context, repoPath string) (*CommitMessage, error)
}
