package local

import (
	"context"
	"errors"

	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// localMRService implements service.MRService. Maps git.ErrMRAlreadyExists
// to service.ErrMRAlreadyExists so views can rely on errors.Is.
type localMRService struct{}

// GenerateTitle produces an AI-suggested MR title.
func (s *localMRService) GenerateTitle(ctx context.Context, repoPath, source, target string) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	out, err := git.GenerateMRTitle(repoPath, source, target)
	if err != nil {
		return "", wrapErr(err)
	}
	return out, nil
}

// GenerateDescription produces an AI-suggested MR description (body).
func (s *localMRService) GenerateDescription(ctx context.Context, repoPath, source, target string) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	out, err := git.GenerateMRDescription(repoPath, source, target)
	if err != nil {
		return "", wrapErr(err)
	}
	return out, nil
}

// Create opens a merge/pull request on the detected forge.
func (s *localMRService) Create(ctx context.Context, repoPath, source, target, title, description string, reviewers []string) (*service.MergeRequest, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	mr, err := git.CreateMR(repoPath, source, target, title, description, reviewers)
	if err != nil {
		if errors.Is(err, git.ErrMRAlreadyExists) {
			return nil, service.ErrMRAlreadyExists
		}
		return nil, wrapErr(err)
	}
	return mr, nil
}

// CreateCLI falls back to gh/glab.
func (s *localMRService) CreateCLI(ctx context.Context, repoPath string, provider service.RemoteProvider, baseBranch string) (*service.MRResult, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	res, err := git.CreateMergeRequestCLI(repoPath, provider, baseBranch)
	if err != nil {
		return nil, wrapErr(err)
	}
	return res, nil
}
