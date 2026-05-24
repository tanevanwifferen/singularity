package remote

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// remoteCommitService implements service.CommitService.
type remoteCommitService struct {
	c *client.Client
}

// SuggestMessage generates a conventional-commit message for staged changes.
func (s *remoteCommitService) SuggestMessage(ctx context.Context, repoPath string) (string, error) {
	return s.c.CommitSuggestMessage(ctx, repoPath)
}

// Files returns the list of files touched by a commit.
func (s *remoteCommitService) Files(ctx context.Context, repoPath, hash string) ([]service.FileChange, error) {
	return s.c.CommitFiles(ctx, repoPath, hash)
}

// FileDiff returns the diff for one file in one commit.
func (s *remoteCommitService) FileDiff(ctx context.Context, repoPath, hash, filePath string) (string, error) {
	return s.c.CommitFileDiff(ctx, repoPath, hash, filePath)
}

// FullDiff returns the full diff of one commit.
func (s *remoteCommitService) FullDiff(ctx context.Context, repoPath, hash string) (string, error) {
	return s.c.CommitFullDiff(ctx, repoPath, hash)
}

// CherryPick cherry-picks the given commit onto the current branch.
func (s *remoteCommitService) CherryPick(ctx context.Context, repoPath, hash string) error {
	return s.c.CommitCherryPick(ctx, repoPath, hash)
}

// Reset moves HEAD to the given commit.
func (s *remoteCommitService) Reset(ctx context.Context, repoPath, hash, mode string) error {
	return s.c.CommitReset(ctx, repoPath, hash, mode)
}

// AmendMessage rewrites the message of the most recent commit.
func (s *remoteCommitService) AmendMessage(ctx context.Context, repoPath, message string) error {
	return s.c.CommitAmend(ctx, repoPath, message)
}

// GenerateMessage is the structured variant of SuggestMessage.
func (s *remoteCommitService) GenerateMessage(ctx context.Context, repoPath string) (*service.CommitMessage, error) {
	return s.c.CommitGenerateMessage(ctx, repoPath)
}
