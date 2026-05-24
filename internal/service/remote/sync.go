package remote

import (
	"context"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// remoteSyncService implements service.SyncService.
type remoteSyncService struct {
	c *client.Client
}

// UpstreamStatus returns ahead/behind vs the configured upstream.
func (s *remoteSyncService) UpstreamStatus(ctx context.Context, repoPath string) (*service.UpstreamStatus, error) {
	return s.c.SyncUpstreamStatus(ctx, repoPath)
}

// LastFetchTime returns the timestamp of the last `git fetch`.
func (s *remoteSyncService) LastFetchTime(ctx context.Context, repoPath string) (time.Time, error) {
	return s.c.SyncLastFetchTime(ctx, repoPath)
}

// Fetch fetches the given remote.
func (s *remoteSyncService) Fetch(ctx context.Context, repoPath, remote string) (<-chan service.SyncProgressEvent, func(), error) {
	return s.c.SyncFetch(ctx, repoPath, remote)
}

// Pull does a standard pull.
func (s *remoteSyncService) Pull(ctx context.Context, repoPath string) (<-chan service.SyncProgressEvent, func(), error) {
	return s.c.SyncPull(ctx, repoPath)
}

// Push pushes the current branch.
func (s *remoteSyncService) Push(ctx context.Context, repoPath string, force bool) (<-chan service.SyncProgressEvent, func(), error) {
	return s.c.SyncPush(ctx, repoPath, force)
}

// PullRebase does `git pull --rebase`.
func (s *remoteSyncService) PullRebase(ctx context.Context, repoPath string) (<-chan service.SyncProgressEvent, func(), error) {
	return s.c.SyncPullRebase(ctx, repoPath)
}

// SetUpstreamAndPush sets the upstream tracking remote and pushes.
func (s *remoteSyncService) SetUpstreamAndPush(ctx context.Context, repoPath, remote string) (<-chan service.SyncProgressEvent, func(), error) {
	return s.c.SyncSetUpstreamAndPush(ctx, repoPath, remote)
}

// SyncAllRepos runs the smart-sync flow across every repo in a project.
func (s *remoteSyncService) SyncAllRepos(ctx context.Context, handle service.ProjectHandle, force bool) (<-chan service.SyncProgressEvent, func(), error) {
	return s.c.SyncAllRepos(ctx, handle, force)
}
