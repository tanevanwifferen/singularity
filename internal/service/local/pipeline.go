package local

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// localPipelineService implements service.PipelineService. Subscribe is a
// stub today — the daemon has no background pipeline-polling loop yet; we
// return a closed channel and ErrUnavailable so the surface is honest.
// TODO: emit pipeline_update events from a periodic poller per repo.
type localPipelineService struct{}

// Statuses returns the pipeline state for each given branch in one call.
func (s *localPipelineService) Statuses(ctx context.Context, repoPath string, branches []service.BranchInfo) (map[string]*service.PipelineInfo, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	out, err := git.GetBranchPipelineStatuses(repoPath, branches)
	if err != nil {
		return nil, wrapErr(err)
	}
	return out, nil
}

// Retry retries the failed pipeline on the given branch.
func (s *localPipelineService) Retry(ctx context.Context, repoPath, branch string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	return wrapErr(git.RetryPipeline(repoPath, branch))
}

// Subscribe — not yet wired (no poller). Returns ErrUnavailable.
func (s *localPipelineService) Subscribe(ctx context.Context, repoPath string) (<-chan service.PipelineEvent, func(), error) {
	ch := make(chan service.PipelineEvent)
	close(ch)
	return ch, func() {}, service.ErrUnavailable
}
