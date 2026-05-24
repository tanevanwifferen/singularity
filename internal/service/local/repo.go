package local

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// localRepoService implements service.RepoService by calling internal/git
// directly. Subscribe is a stub today — there is no file watcher in
// internal/git; we return a closed channel and ErrUnavailable so the daemon
// surface stays honest until a watcher lands.
type localRepoService struct{}

// Open opens a repo and returns a populated RepoInfo.
func (s *localRepoService) Open(ctx context.Context, path string) (*service.RepoInfo, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	info, err := git.OpenRepo(path)
	if err != nil {
		return nil, wrapErr(err)
	}
	return info, nil
}

// Find walks upward from path looking for a git repository root.
func (s *localRepoService) Find(ctx context.Context, path string) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	root, err := git.FindRepo(path)
	if err != nil {
		return "", wrapErr(err)
	}
	return root, nil
}

// Subscribe is not yet implemented in internal/git (no file watcher); returns
// a closed channel and ErrUnavailable so views can gracefully degrade.
// TODO: wire a fsnotify-backed watcher to internal/git and emit on HEAD /
// branch ref / dirty-bit changes.
func (s *localRepoService) Subscribe(ctx context.Context, path string) (<-chan *service.RepoInfo, func(), error) {
	ch := make(chan *service.RepoInfo)
	close(ch)
	return ch, func() {}, service.ErrUnavailable
}
