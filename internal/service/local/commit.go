package local

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// localCommitService implements service.CommitService.
type localCommitService struct{}

// SuggestMessage generates a one-shot conventional commit message.
func (s *localCommitService) SuggestMessage(ctx context.Context, repoPath string) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	msg, err := git.SuggestCommitMessage(repoPath)
	if err != nil {
		return "", wrapErr(err)
	}
	return msg, nil
}

// Files returns the list of files touched by a commit.
func (s *localCommitService) Files(ctx context.Context, repoPath, hash string) ([]service.FileChange, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	files, err := git.GetCommitFiles(repoPath, hash)
	if err != nil {
		return nil, wrapErr(err)
	}
	return files, nil
}

// FileDiff returns the diff for one file in one commit.
func (s *localCommitService) FileDiff(ctx context.Context, repoPath, hash, filePath string) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	out, err := git.GetCommitFileDiff(repoPath, hash, filePath)
	if err != nil {
		return "", wrapErr(err)
	}
	return out, nil
}

// FullDiff returns the full diff of one commit.
func (s *localCommitService) FullDiff(ctx context.Context, repoPath, hash string) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	out, err := git.GetCommitFullDiff(repoPath, hash)
	if err != nil {
		return "", wrapErr(err)
	}
	return out, nil
}

// CherryPick cherry-picks the given commit onto the current branch.
func (s *localCommitService) CherryPick(ctx context.Context, repoPath, hash string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.CherryPick(repoPath, hash))
}

// Reset moves HEAD to the given commit using the requested mode.
func (s *localCommitService) Reset(ctx context.Context, repoPath, hash, mode string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.ResetToCommit(repoPath, hash, mode))
}

// AmendMessage rewrites the message of the most recent commit.
func (s *localCommitService) AmendMessage(ctx context.Context, repoPath, message string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.AmendCommitMessage(repoPath, message))
}

// GenerateMessage is the structured variant of SuggestMessage.
func (s *localCommitService) GenerateMessage(ctx context.Context, repoPath string) (*service.CommitMessage, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	msg, err := git.GenerateCommitMessage(repoPath)
	if err != nil {
		return nil, wrapErr(err)
	}
	return msg, nil
}
