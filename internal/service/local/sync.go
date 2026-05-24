package local

import (
	"context"
	"strings"
	"sync"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// localSyncService implements service.SyncService. All long-running ops run
// in a goroutine and emit a terminal SyncProgressEvent — the underlying git
// helpers (Fetch/Pull/Push/...) are synchronous and return their output as a
// single string, so we cannot truly stream lines yet. The channel + cancel
// contract is honored.
type localSyncService struct {
	proj *localProjectService
}

// UpstreamStatus returns ahead/behind vs the configured upstream.
func (s *localSyncService) UpstreamStatus(ctx context.Context, repoPath string) (*service.UpstreamStatus, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	st, err := git.GetUpstreamStatus(repoPath)
	if err != nil {
		return nil, wrapErr(err)
	}
	return st, nil
}

// LastFetchTime returns the FETCH_HEAD mtime.
func (s *localSyncService) LastFetchTime(ctx context.Context, repoPath string) (time.Time, error) {
	if err := checkCtx(ctx); err != nil {
		return time.Time{}, err
	}
	t, err := git.GetLastFetchTime(repoPath)
	if err != nil {
		return t, wrapErr(err)
	}
	return t, nil
}

// Fetch fetches the given remote.
func (s *localSyncService) Fetch(ctx context.Context, repoPath, remote string) (<-chan service.SyncProgressEvent, func(), error) {
	return runSyncOp(ctx, repoPath, "fetch", func() (string, error) {
		return git.Fetch(repoPath, remote)
	})
}

// Pull does a standard pull.
func (s *localSyncService) Pull(ctx context.Context, repoPath string) (<-chan service.SyncProgressEvent, func(), error) {
	return runSyncOp(ctx, repoPath, "pull", func() (string, error) {
		return git.Pull(repoPath)
	})
}

// Push pushes the current branch.
func (s *localSyncService) Push(ctx context.Context, repoPath string, force bool) (<-chan service.SyncProgressEvent, func(), error) {
	return runSyncOp(ctx, repoPath, "push", func() (string, error) {
		return git.Push(repoPath, force)
	})
}

// PullRebase does `git pull --rebase`.
func (s *localSyncService) PullRebase(ctx context.Context, repoPath string) (<-chan service.SyncProgressEvent, func(), error) {
	return runSyncOp(ctx, repoPath, "pull_rebase", func() (string, error) {
		return git.PullRebase(repoPath)
	})
}

// SetUpstreamAndPush sets the upstream tracking remote and pushes.
func (s *localSyncService) SetUpstreamAndPush(ctx context.Context, repoPath, remote string) (<-chan service.SyncProgressEvent, func(), error) {
	return runSyncOp(ctx, repoPath, "set_upstream", func() (string, error) {
		return git.SetUpstreamAndPush(repoPath, remote)
	})
}

// SyncAllRepos runs fetch + pull-rebase + push across every repo in a
// project. Per-repo events are emitted from a single shared channel; a
// final Done event with empty RepoPath signals the bulk op completed.
func (s *localSyncService) SyncAllRepos(ctx context.Context, handle service.ProjectHandle, force bool) (<-chan service.SyncProgressEvent, func(), error) {
	if err := checkCtx(ctx); err != nil {
		return nil, nil, err
	}
	proj, err := s.proj.resolve(handle)
	if err != nil {
		return nil, nil, err
	}
	cctx, cancel := context.WithCancel(ctx)
	out := make(chan service.SyncProgressEvent, 32)

	go func() {
		defer close(out)
		var wg sync.WaitGroup
		sem := make(chan struct{}, 4)
		for _, r := range proj.Repos {
			wg.Add(1)
			go func(name, path string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				for _, step := range []struct {
					op string
					fn func() (string, error)
				}{
					{"fetch", func() (string, error) { return git.Fetch(path, "") }},
					{"pull_rebase", func() (string, error) { return git.PullRebase(path) }},
					{"push", func() (string, error) { return git.Push(path, force) }},
				} {
					select {
					case <-cctx.Done():
						return
					default:
					}
					line, err := step.fn()
					ev := service.SyncProgressEvent{
						RepoPath:  path,
						Op:        step.op,
						Line:      line,
						Timestamp: time.Now(),
					}
					if err != nil {
						ev.Err = err.Error()
					}
					select {
					case out <- ev:
					case <-cctx.Done():
						return
					}
					if err != nil {
						_ = name
						return
					}
				}
			}(r.Name, r.Path)
		}
		wg.Wait()
		select {
		case out <- service.SyncProgressEvent{Op: "sync_all", Done: true, Timestamp: time.Now()}:
		case <-cctx.Done():
		}
	}()

	return out, cancel, nil
}

// runSyncOp is the shared shell for the single-repo streaming ops. We run
// the blocking git helper in a goroutine, emit one terminal event when it
// returns, and honor ctx cancellation by abandoning the result (the git
// subprocess is not interruptible from here — a future refactor of
// internal/git should accept ctx).
func runSyncOp(ctx context.Context, repoPath, op string, run func() (string, error)) (<-chan service.SyncProgressEvent, func(), error) {
	if err := checkCtx(ctx); err != nil {
		return nil, nil, err
	}
	out := make(chan service.SyncProgressEvent, 4)
	cctx, cancel := context.WithCancel(ctx)
	go func() {
		defer close(out)
		line, err := run()
		select {
		case <-cctx.Done():
			return
		default:
		}
		ev := service.SyncProgressEvent{
			RepoPath:  repoPath,
			Op:        op,
			Line:      line,
			Done:      true,
			Timestamp: time.Now(),
		}
		if err != nil {
			ev.Err = err.Error()
			// Hint conflict state in the line so views can route accordingly.
			if strings.Contains(strings.ToLower(err.Error()), "conflict") {
				ev.Err = "conflict: " + err.Error()
			}
		}
		select {
		case out <- ev:
		case <-cctx.Done():
		}
	}()
	return out, cancel, nil
}
