package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// stubProjectService implements just the two ProjectService methods
// resolveProjects uses; the embedded nil interface panics loudly if anything
// else is ever called.
type stubProjectService struct {
	service.ProjectService
	keys  []string
	infos map[string]*service.ProjectInfo
	err   error
	loads atomic.Int32 // Load runs concurrently across keys
}

func (s *stubProjectService) List(context.Context) ([]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.keys, nil
}

func (s *stubProjectService) Load(_ context.Context, key string) (*service.ProjectInfo, error) {
	s.loads.Add(1)
	info, ok := s.infos[key]
	if !ok {
		return nil, service.ErrNotFound
	}
	return info, nil
}

func svcWith(p *stubProjectService) *service.Services {
	return &service.Services{Project: p}
}

func info(key, name string, repoPaths ...string) *service.ProjectInfo {
	i := &service.ProjectInfo{Handle: service.ProjectHandle("proj-" + key), Key: key, Name: name, Loaded: true}
	for _, p := range repoPaths {
		i.Repos = append(i.Repos, service.RepoSummary{Name: filepath.Base(p), Path: p, DefaultBranch: "main"})
	}
	return i
}

func TestResolveProjectsNoProjectsFallsBackToRepoMode(t *testing.T) {
	sel, err := resolveProjects(svcWith(&stubProjectService{}), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel != nil {
		t.Fatalf("expected nil selection for empty project list, got %+v", sel)
	}
}

func TestResolveProjectsUnavailableFallsBackToRepoMode(t *testing.T) {
	sel, err := resolveProjects(svcWith(&stubProjectService{err: service.ErrUnavailable}), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel != nil {
		t.Fatalf("expected nil selection when the daemon has no project config")
	}
}

func TestResolveProjectsLoadsAllProjects(t *testing.T) {
	stub := &stubProjectService{
		keys: []string{"alpha", "beta"},
		infos: map[string]*service.ProjectInfo{
			"alpha": info("alpha", "Alpha", "/code/alpha"),
			"beta":  info("beta", "Beta", "/code/beta"),
		},
	}
	t.Chdir(t.TempDir())
	sel, err := resolveProjects(svcWith(stub), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := stub.loads.Load(); got != 2 {
		t.Errorf("loads = %d, want 2 (every configured project loads up front)", got)
	}
	if len(sel.Keys) != 2 || len(sel.Infos) != 2 {
		t.Fatalf("expected both projects in the selection, got %+v", sel)
	}
	if sel.Keys[0] != "alpha" || sel.Keys[1] != "beta" {
		t.Errorf("Keys must keep daemon order, got %v", sel.Keys)
	}
	if sel.Key != "alpha" {
		t.Errorf("outside any project the first key is active, got %q", sel.Key)
	}
}

func TestResolveProjectsPicksProjectOwningCwd(t *testing.T) {
	root := t.TempDir()
	other := filepath.Join(root, "other")
	mine := filepath.Join(root, "mine")
	nested := filepath.Join(mine, "svc", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	stub := &stubProjectService{
		keys: []string{"alpha", "beta"},
		infos: map[string]*service.ProjectInfo{
			"alpha": info("alpha", "Alpha", other),
			"beta":  info("beta", "Beta", filepath.Join(mine, "svc")),
		},
	}

	t.Chdir(nested)
	sel, err := resolveProjects(svcWith(stub), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel == nil || sel.Key != "beta" {
		t.Fatalf("expected the project owning the cwd (beta), got %+v", sel)
	}
}

func TestResolveProjectsExplicitKey(t *testing.T) {
	stub := &stubProjectService{
		keys: []string{"alpha", "beta"},
		infos: map[string]*service.ProjectInfo{
			"alpha": info("alpha", "Alpha", "/code/alpha"),
			"beta":  info("beta", "Beta", "/code/beta"),
		},
	}
	sel, err := resolveProjects(svcWith(stub), "beta")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel == nil || sel.Key != "beta" {
		t.Fatalf("expected beta, got %+v", sel)
	}
}

func TestResolveProjectsUnknownKeyIsAnError(t *testing.T) {
	stub := &stubProjectService{
		keys:  []string{"alpha"},
		infos: map[string]*service.ProjectInfo{"alpha": info("alpha", "Alpha", "/code/alpha")},
	}
	_, err := resolveProjects(svcWith(stub), "nope")
	if err == nil {
		t.Fatal("expected an error for an unknown project key")
	}
	if !strings.Contains(err.Error(), "alpha") {
		t.Errorf("error should list available keys, got: %v", err)
	}
}

func TestResolveProjectsSkipsFailingProject(t *testing.T) {
	// beta is configured but fails to load (e.g. a repo path is gone):
	// the selection keeps working with the projects that did load.
	stub := &stubProjectService{
		keys:  []string{"alpha", "beta"},
		infos: map[string]*service.ProjectInfo{"alpha": info("alpha", "Alpha", "/code/alpha")},
	}
	t.Chdir(t.TempDir())
	sel, err := resolveProjects(svcWith(stub), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sel.Keys) != 1 || sel.Key != "alpha" {
		t.Fatalf("expected only alpha to survive, got %+v", sel)
	}

	// But explicitly requesting the broken project is an error, not a
	// silent fallback.
	if _, err := resolveProjects(svcWith(stub), "beta"); err == nil {
		t.Fatal("expected an error when the explicitly requested project fails to load")
	}
}

func TestResolveProjectsAllFailingIsAnError(t *testing.T) {
	stub := &stubProjectService{keys: []string{"alpha", "beta"}}
	if _, err := resolveProjects(svcWith(stub), ""); err == nil {
		t.Fatal("expected an error when no configured project loads")
	}
}

func TestPathWithin(t *testing.T) {
	sep := string(filepath.Separator)
	cases := []struct {
		path, dir string
		want      bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b/c", "/a/b", true},
		{"/a/bc", "/a/b", false},
		{"/a", "/a/b", false},
		{"/a" + sep + "b", "/a", true},
	}
	for _, c := range cases {
		if got := pathWithin(c.path, c.dir); got != c.want {
			t.Errorf("pathWithin(%q, %q) = %v, want %v", c.path, c.dir, got, c.want)
		}
	}
}
