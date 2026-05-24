package remote

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// remoteMRService implements service.MRService.
type remoteMRService struct {
	c *client.Client
}

// GenerateTitle produces an AI-suggested MR title.
func (s *remoteMRService) GenerateTitle(ctx context.Context, repoPath, source, target string) (string, error) {
	return s.c.MRGenerateTitle(ctx, repoPath, source, target)
}

// GenerateDescription produces an AI-suggested MR description (body).
func (s *remoteMRService) GenerateDescription(ctx context.Context, repoPath, source, target string) (string, error) {
	return s.c.MRGenerateDescription(ctx, repoPath, source, target)
}

// Create opens a merge/pull request on the detected forge.
func (s *remoteMRService) Create(ctx context.Context, repoPath, source, target, title, description string, reviewers []string) (*service.MergeRequest, error) {
	return s.c.MRCreate(ctx, repoPath, source, target, title, description, reviewers)
}

// CreateCLI falls back to `gh pr create` / `glab mr create`.
func (s *remoteMRService) CreateCLI(ctx context.Context, repoPath string, provider service.RemoteProvider, baseBranch string) (*service.MRResult, error) {
	return s.c.MRCreateCLI(ctx, repoPath, provider, baseBranch)
}
