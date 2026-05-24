package remote

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// remotePipelineService implements service.PipelineService.
type remotePipelineService struct {
	c *client.Client
}

// Statuses returns the pipeline state for each given branch in one call.
func (s *remotePipelineService) Statuses(ctx context.Context, repoPath string, branches []service.BranchInfo) (map[string]*service.PipelineInfo, error) {
	return s.c.PipelineStatuses(ctx, repoPath, branches)
}

// Retry retries the failed pipeline on the given branch.
func (s *remotePipelineService) Retry(ctx context.Context, repoPath, branch string) error {
	return s.c.PipelineRetry(ctx, repoPath, branch)
}

// Subscribe streams pipeline status changes for the repo.
func (s *remotePipelineService) Subscribe(ctx context.Context, repoPath string) (<-chan service.PipelineEvent, func(), error) {
	return s.c.PipelineSubscribe(ctx, repoPath)
}
