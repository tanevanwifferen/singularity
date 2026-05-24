package remote

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// remoteForgeService implements service.ForgeService.
type remoteForgeService struct {
	c *client.Client
}

// DetectAuth returns the full ForgeAuth struct (token included).
func (s *remoteForgeService) DetectAuth(ctx context.Context) (*service.ForgeAuth, error) {
	return s.c.ForgeDetectAuth(ctx)
}

// Detect returns the lean ForgeInfo DTO.
func (s *remoteForgeService) Detect(ctx context.Context) (*service.ForgeInfo, error) {
	return s.c.ForgeDetect(ctx)
}

// DetectProvider returns the RemoteProvider for a repo.
func (s *remoteForgeService) DetectProvider(ctx context.Context, repoPath string) (service.RemoteProvider, error) {
	return s.c.ForgeDetectProvider(ctx, repoPath)
}
