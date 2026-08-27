package local

import (
	"context"
	"sync"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/project"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// localProjectService implements service.ProjectService and owns the
// ProjectHandle → *project.Project registry. The handle is the project key
// from the loader's config (treated as opaque by callers).
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

// resolve looks up the *project.Project for an opaque handle. Returns
// ErrNotFound on miss, ErrUnavailable when no loader is wired.
func (s *localProjectService) resolve(handle service.ProjectHandle) (*project.Project, error) {
	if s == nil || s.loader == nil {
		return nil, service.ErrUnavailable
	}
	s.mu.RLock()
	p, ok := s.handles[handle]
	s.mu.RUnlock()
	if !ok {
		return nil, service.ErrNotFound
	}
	return p, nil
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
	if !ok {
		return "", service.ErrNotFound
	}
	return k, nil
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

// Load loads a project by key and returns its lean Info + handle.
func (s *localProjectService) Load(ctx context.Context, key string) (*service.ProjectInfo, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	if s.loader == nil {
		return nil, service.ErrUnavailable
	}
	p, err := s.loader.LoadProject(key)
	if err != nil {
		return nil, wrapErr(err)
	}
	s.mu.Lock()
	handle, ok := s.handleOf[key]
	if !ok {
		handle = service.ProjectHandle("proj-" + key)
		s.handleOf[key] = handle
	}
	s.handles[handle] = p
	s.keyOf[handle] = key
	s.mu.Unlock()
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

// CreateWorkflow creates a new multi-repo feature workflow. An empty baseDir
// falls back to project.DefaultWorkflowBaseDir — a slugified (space-free)
// directory under ~/.worktrees, reusing the legacy raw-name directory when
// one already exists.
func (s *localProjectService) CreateWorkflow(ctx context.Context, handle service.ProjectHandle, branch, baseDir string) (*service.FeatureWorkflow, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	p, err := s.resolve(handle)
	if err != nil {
		return nil, err
	}
	if baseDir == "" {
		baseDir = project.DefaultWorkflowBaseDir(p.Name)
	}
	wf := project.NewFeatureWorkflow(p, branch, baseDir)
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
		Handle: handle,
		Key:    key,
		Name:   p.Name,
		Loaded: true,
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
