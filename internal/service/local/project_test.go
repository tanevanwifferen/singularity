package local

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/project"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// newTestProjectService builds a localProjectService over one temp git repo
// configured as project "alpha".
func newTestProjectService(t *testing.T) *localProjectService {
	t.Helper()
	repoDir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	cfg := &project.ProjectConfig{Projects: map[string]project.ProjectDef{
		"alpha": {
			Name:  "Alpha",
			Repos: []project.RepoDef{{Name: "repo", Path: repoDir, DefaultBranch: "main"}},
		},
	}}
	loader, err := project.NewLoader(cfg)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	return newProjectService(loader)
}

// TestResolveIsStateless: any handle derived from a configured key works
// without a prior Load — the daemon holds no per-client load state.
func TestResolveIsStateless(t *testing.T) {
	s := newTestProjectService(t)
	ctx := context.Background()

	info, err := s.Info(ctx, service.ProjectHandle("proj-alpha"))
	if err != nil {
		t.Fatalf("Info without prior Load: %v", err)
	}
	if info.Name != "Alpha" || !info.Loaded {
		t.Errorf("unexpected info: %+v", info)
	}

	// A second resolve must reuse the registered project, not reload it.
	p1, _ := s.resolve("proj-alpha")
	p2, _ := s.resolve("proj-alpha")
	if p1 != p2 {
		t.Error("resolve reloaded an already-registered project")
	}
}

// TestResolveAcceptsBareKey: `--project alpha` works as well as `proj-alpha`,
// and both name the same underlying project.
func TestResolveAcceptsBareKey(t *testing.T) {
	s := newTestProjectService(t)

	p1, err := s.resolve(service.ProjectHandle("alpha"))
	if err != nil {
		t.Fatalf("resolve bare key: %v", err)
	}
	p2, err := s.resolve(service.ProjectHandle("proj-alpha"))
	if err != nil {
		t.Fatalf("resolve prefixed handle: %v", err)
	}
	if p1 != p2 {
		t.Error("bare key and prefixed handle resolved to different projects")
	}
}

// TestResolveUnknownKeyIsNotFound: an unconfigured key maps to ErrNotFound.
func TestResolveUnknownKeyIsNotFound(t *testing.T) {
	s := newTestProjectService(t)
	if _, err := s.resolve("proj-nope"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("resolve unknown key: err = %v, want ErrNotFound", err)
	}
}

// TestKeyForHandleWithoutLoad: workflow persistence needs the key even when
// the handle was never explicitly loaded.
func TestKeyForHandleWithoutLoad(t *testing.T) {
	s := newTestProjectService(t)
	key, err := s.keyForHandle("proj-alpha")
	if err != nil {
		t.Fatalf("keyForHandle: %v", err)
	}
	if key != "alpha" {
		t.Errorf("key = %q, want %q", key, "alpha")
	}
}
