package remote

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// remoteRepoService implements service.RepoService by calling the daemon's
// HTTP/WS endpoints.
type remoteRepoService struct {
	c *client.Client
}

// Open returns the RepoInfo for the given repo path.
func (r *remoteRepoService) Open(ctx context.Context, path string) (*service.RepoInfo, error) {
	return r.c.RepoOpen(ctx, path)
}

// Find walks upward from path looking for a git repository root.
func (r *remoteRepoService) Find(ctx context.Context, path string) (string, error) {
	return r.c.RepoFind(ctx, path)
}

// Subscribe streams RepoInfo updates for the given repo path.
func (r *remoteRepoService) Subscribe(ctx context.Context, path string) (<-chan *service.RepoInfo, func(), error) {
	return r.c.RepoSubscribe(ctx, path)
}
