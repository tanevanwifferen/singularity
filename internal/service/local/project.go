package local

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/project"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// localProjectService implements service.ProjectService and owns the
// ProjectHandle → *project.Project registry. The handle is the project key
// from the loader's config prefixed with "proj-" (treated as opaque by
// callers). Handles are stateless: resolve loads the project on first use,
// so any handle derived from a configured key works without a prior Load —
// Load is just an eager resolve that also returns the lean Info.
//
// Subscribe / SubscribeWorkflows are stubs today — the daemon has no file
// watcher or workflow ticker yet. They return ErrUnavailable so the
// surface is honest until those land.
type localProjectService struct {
	loader *project.Loader

	mu       sync.RWMutex
	handles  map[service.ProjectHandle]*project.Project
	keyOf    map[service.ProjectHandle]string
	handleOf map[string]service.ProjectHandle
}

func newProjectService(loader *project.Loader) *localProjectService {
	return &localProjectService{
		loader:   loader,
		handles:  make(map[service.ProjectHandle]*project.Project),
		keyOf:    make(map[service.ProjectHandle]string),
		handleOf: make(map[string]service.ProjectHandle),
	}
}

// handlePrefix distinguishes a ProjectHandle from a raw config key.
const handlePrefix = "proj-"

// keyFromHandle recovers the config key a handle was minted from. Bare keys
// are accepted too, so `--project pbd` and `--project proj-pbd` both work.
func keyFromHandle(handle service.ProjectHandle) string {
	return strings.TrimPrefix(string(handle), handlePrefix)
}

// resolve returns the *project.Project for a handle, loading it from config
// on first use — handles carry no session state. Returns ErrNotFound for a
// key absent from config, ErrUnavailable when no loader is wired.
func (s *localProjectService) resolve(handle service.ProjectHandle) (*project.Project, error) {
	if s == nil || s.loader == nil {
		return nil, service.ErrUnavailable
	}
	s.mu.RLock()
	p, ok := s.handles[handle]
	s.mu.RUnlock()
	if ok {
		return p, nil
	}
	p, _, err := s.ensure(keyFromHandle(handle))
	return p, err
}

// ensure loads the project for key (a cache hit skips the disk walk) and
// registers its handle. It is the single path that mutates the registry.
func (s *localProjectService) ensure(key string) (*project.Project, service.ProjectHandle, error) {
	if s == nil || s.loader == nil {
		return nil, "", service.ErrUnavailable
	}
	handle := service.ProjectHandle(handlePrefix + key)

	s.mu.RLock()
	p, ok := s.handles[handle]
	s.mu.RUnlock()
	if ok {
		return p, handle, nil
	}

	p, err := s.loader.LoadProject(key)
	if err != nil {
		return nil, "", wrapErr(err)
	}

	s.mu.Lock()
	// Re-check: a concurrent ensure for the same key may have won the race;
	// keep its project so open references stay coherent.
	if existing, ok := s.handles[handle]; ok {
		p = existing
	} else {
		s.handles[handle] = p
		s.keyOf[handle] = key
		s.handleOf[key] = handle
	}
	s.mu.Unlock()
	return p, handle, nil
}

// keyForHandle returns the underlying project key used for workflow
// persistence (workflows-<key>.json).
func (s *localProjectService) keyForHandle(handle service.ProjectHandle) (string, error) {
	if s == nil || s.loader == nil {
		return "", service.ErrUnavailable
	}
	s.mu.RLock()
	k, ok := s.keyOf[handle]
	s.mu.RUnlock()
	if ok {
		return k, nil
	}
	// Not registered yet — resolve it, which loads and registers on success.
	if _, err := s.resolve(handle); err != nil {
		return "", err
	}
	return keyFromHandle(handle), nil
}

// List returns the configured project keys.
func (s *localProjectService) List(ctx context.Context) ([]string, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	if s.loader == nil {
		return nil, service.ErrUnavailable
	}
	return s.loader.ListProjectKeys(), nil
}

// Load loads a project by key and returns its lean Info + handle. Since
// handles resolve lazily this is optional — it exists to warm the cache and
// fetch the Info in one round-trip.
func (s *localProjectService) Load(ctx context.Context, key string) (*service.ProjectInfo, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	p, handle, err := s.ensure(key)
	if err != nil {
		return nil, err
	}
	return buildProjectInfo(handle, key, p), nil
}

// Info returns the cached lean Info for a loaded project.
func (s *localProjectService) Info(ctx context.Context, handle service.ProjectHandle) (*service.ProjectInfo, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	p, err := s.resolve(handle)
	if err != nil {
		return nil, err
	}
	key, _ := s.keyForHandle(handle)
	return buildProjectInfo(handle, key, p), nil
}

// Status returns the aggregated multi-repo status snapshot.
func (s *localProjectService) Status(ctx context.Context, handle service.ProjectHandle) (*service.ProjectStatus, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	p, err := s.resolve(handle)
	if err != nil {
		return nil, err
	}
	return p.Status(), nil
}

// Refresh re-scans the project's repos and returns the fresh status.
func (s *localProjectService) Refresh(ctx context.Context, handle service.ProjectHandle) (*service.ProjectStatus, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	p, err := s.resolve(handle)
	if err != nil {
		return nil, err
	}
	p.Refresh()
	return p.Status(), nil
}

// BranchExists checks which repos in the project carry the named branch.
func (s *localProjectService) BranchExists(ctx context.Context, handle service.ProjectHandle, branch string) (*service.BranchExistence, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	p, err := s.resolve(handle)
	if err != nil {
		return nil, err
	}
	return p.BranchExistsAcross(branch), nil
}

// ContextSummary returns the text summary handed to Claude Code.
func (s *localProjectService) ContextSummary(ctx context.Context, handle service.ProjectHandle) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	p, err := s.resolve(handle)
	if err != nil {
		return "", err
	}
	return p.ContextSummary(), nil
}

// DefaultConfigPath returns the daemon's path to its default project config.
func (s *localProjectService) DefaultConfigPath(ctx context.Context) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	return project.GetDefaultConfigPath(), nil
}

// Subscribe is a stub today; no watcher wired.
func (s *localProjectService) Subscribe(ctx context.Context, handle service.ProjectHandle) (<-chan service.ProjectEvent, func(), error) {
	if _, err := s.resolve(handle); err != nil {
		return nil, nil, err
	}
	ch := make(chan service.ProjectEvent)
	close(ch)
	return ch, func() {}, service.ErrUnavailable
}

// CreateWorkflow creates a new multi-repo feature workflow: one worktree per
// repo in the project, all on the same branch, under
// <baseDir>/<branch>/<repo>. The worktrees are actually created here — a
// workflow is the unit of isolation in singularity, so a project never ends up
// with only some of its repos isolated. The result is persisted so
// LoadWorkflows / the TUI see it.
//
// Calling it twice for the same branch is safe: existing worktrees are adopted
// and the persisted workflow is updated in place.
//
// On partial failure the workflow is still returned (and persisted) with the
// failing repos carrying a per-repo Error; only a total failure is an error,
// so callers never lose the layout of the worktrees that did get created.
func (s *localProjectService) CreateWorkflow(ctx context.Context, handle service.ProjectHandle, branch, baseDir string) (*service.FeatureWorkflow, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	p, err := s.resolve(handle)
	if err != nil {
		return nil, err
	}
	key, err := s.keyForHandle(handle)
	if err != nil {
		return nil, err
	}
	if baseDir == "" {
		baseDir = project.DefaultWorkflowBaseDir(p.Name)
	}

	// Reuse a persisted workflow for the same branch so repeated creates are
	// idempotent instead of forking a second layout for the same feature.
	var wf *service.FeatureWorkflow
	var others []*service.FeatureWorkflow
	if persisted, lerr := project.LoadWorkflows(key, p); lerr == nil {
		for _, existing := range persisted {
			if existing.BranchName == branch && existing.BaseDir == baseDir {
				wf = existing
				continue
			}
			others = append(others, existing)
		}
	}
	if wf == nil {
		wf = project.NewFeatureWorkflow(p, branch, baseDir)
	}

	createErr := wf.CreateAllWorktrees()

	// Persist even on partial failure: the worktrees that did get created are
	// real and must stay tracked, or cleanup later misses them.
	saveErr := project.SaveWorkflows(key, append(others, wf))

	if createErr != nil && wf.Status().WorktreesCreated == 0 {
		return nil, wrapErr(createErr)
	}
	if saveErr != nil {
		return wf, wrapErr(saveErr)
	}
	return wf, nil
}

// RemoveWorkflow tears down the persisted workflow for `branch`: worktrees
// removed for every repo, local + remote feature branches deleted, workflow
// dropped from persistence when fully clean. Mirrors the TUI cleanup path.
//
// On partial failure the workflow is returned with per-repo Errors and kept
// persisted so a retry can finish the teardown.
func (s *localProjectService) RemoveWorkflow(ctx context.Context, handle service.ProjectHandle, branch string) (*service.FeatureWorkflow, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	p, err := s.resolve(handle)
	if err != nil {
		return nil, err
	}
	key, err := s.keyForHandle(handle)
	if err != nil {
		return nil, err
	}
	persisted, err := project.LoadWorkflows(key, p)
	if err != nil {
		return nil, wrapErr(err)
	}
	var wf *service.FeatureWorkflow
	var others []*service.FeatureWorkflow
	for _, existing := range persisted {
		if wf == nil && existing.BranchName == branch {
			wf = existing
			continue
		}
		others = append(others, existing)
	}
	if wf == nil {
		return nil, wrapErr(fmt.Errorf("no workflow for branch %q in project %q", branch, key))
	}

	_ = wf.RemoveAllWorktrees() // always nil today; per-repo errors land on wf.Repos

	remaining := others
	failed := false
	for _, wr := range wf.Repos {
		if wr.Error != "" {
			failed = true
			break
		}
	}
	if failed {
		// Keep the half-removed workflow tracked so a retry can finish it.
		remaining = append(remaining, wf)
	}
	if saveErr := project.SaveWorkflows(key, remaining); saveErr != nil {
		return wf, wrapErr(saveErr)
	}
	return wf, nil
}

// LoadWorkflows reads persisted workflows for the project from disk.
func (s *localProjectService) LoadWorkflows(ctx context.Context, handle service.ProjectHandle) ([]*service.FeatureWorkflow, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	p, err := s.resolve(handle)
	if err != nil {
		return nil, err
	}
	key, err := s.keyForHandle(handle)
	if err != nil {
		return nil, err
	}
	wfs, err := project.LoadWorkflows(key, p)
	if err != nil {
		return nil, wrapErr(err)
	}
	return wfs, nil
}

// SaveWorkflows persists the given workflow set to disk.
func (s *localProjectService) SaveWorkflows(ctx context.Context, handle service.ProjectHandle, workflows []*service.FeatureWorkflow) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if _, err := s.resolve(handle); err != nil {
		return err
	}
	key, err := s.keyForHandle(handle)
	if err != nil {
		return err
	}
	return wrapErr(project.SaveWorkflows(key, workflows))
}

// DiscoverWorkflowsAllRepos scans every repo for existing worktrees that
// look like in-flight workflows. The underlying DiscoverWorkflows is
// synchronous; we run it in a goroutine and emit a single terminal event.
func (s *localProjectService) DiscoverWorkflowsAllRepos(ctx context.Context, handle service.ProjectHandle, skip map[string]bool) (<-chan service.DiscoveryProgressEvent, func(), error) {
	if err := checkCtx(ctx); err != nil {
		return nil, nil, err
	}
	p, err := s.resolve(handle)
	if err != nil {
		return nil, nil, err
	}
	out := make(chan service.DiscoveryProgressEvent, 4)
	cctx, cancel := context.WithCancel(ctx)
	go func() {
		defer close(out)
		wfs, derr := project.DiscoverWorkflows(p, skip)
		select {
		case <-cctx.Done():
			return
		default:
		}
		ev := service.DiscoveryProgressEvent{
			Found:     len(wfs),
			Total:     len(wfs),
			Done:      true,
			Timestamp: time.Now(),
		}
		if derr != nil {
			ev.Err = derr.Error()
		}
		select {
		case out <- ev:
		case <-cctx.Done():
		}
	}()
	return out, cancel, nil
}

// SubscribeWorkflows is a stub today; no ticker wired.
func (s *localProjectService) SubscribeWorkflows(ctx context.Context, handle service.ProjectHandle) (<-chan service.WorkflowEvent, func(), error) {
	if _, err := s.resolve(handle); err != nil {
		return nil, nil, err
	}
	ch := make(chan service.WorkflowEvent)
	close(ch)
	return ch, func() {}, service.ErrUnavailable
}

// buildProjectInfo projects a loaded *project.Project to the lean wire DTO.
func buildProjectInfo(handle service.ProjectHandle, key string, p *project.Project) *service.ProjectInfo {
	info := &service.ProjectInfo{
		Handle:       handle,
		Key:          key,
		Name:         p.Name,
		Loaded:       true,
		ContextFiles: p.ContextFiles,
	}
	for _, r := range p.Repos {
		summary := service.RepoSummary{
			Name:          r.Name,
			Path:          r.Path,
			DefaultBranch: r.DefaultBranch,
		}
		if r.Info != nil {
			summary.CurrentBranch = r.Info.CurrentBranch
			summary.Dirty = r.Info.IsDirty
		}
		info.Repos = append(info.Repos, summary)
	}
	info.Context = p.ContextSummary()
	return info
}
