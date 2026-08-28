package local

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/project"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// newWorkflowTestService builds a localProjectService over a two-repo project
// and redirects workflow persistence into a temp XDG config home.
func newWorkflowTestService(t *testing.T) (*localProjectService, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	root := t.TempDir()
	defs := make([]project.RepoDef, 0, 2)
	for _, name := range []string{"web", "api"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{
			{"init", "-b", "main"},
			{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-m", "init"},
		} {
			cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		defs = append(defs, project.RepoDef{Name: name, Path: dir, DefaultBranch: "main"})
	}

	cfg := &project.ProjectConfig{Projects: map[string]project.ProjectDef{
		"alpha": {Name: "Alpha", Repos: defs},
	}}
	loader, err := project.NewLoader(cfg)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	return newProjectService(loader), root
}

// TestCreateWorkflowCreatesWorktreesForEveryRepo is the paradigm guard at the
// service boundary: creating a workflow isolates the whole project, not just
// the repo the caller happened to think about.
func TestCreateWorkflowCreatesWorktreesForEveryRepo(t *testing.T) {
	s, root := newWorkflowTestService(t)
	ctx := context.Background()
	baseDir := filepath.Join(root, "worktrees")

	wf, err := s.CreateWorkflow(ctx, "proj-alpha", "feature/x", baseDir)
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	if len(wf.Repos) != 2 {
		t.Fatalf("expected 2 repos in the workflow, got %d", len(wf.Repos))
	}
	for name, wr := range wf.Repos {
		if !wr.WorktreeCreated {
			t.Errorf("repo %s: no worktree created (%s)", name, wr.Error)
		}
		want := filepath.Join(baseDir, "feature-x", name)
		if wr.WorktreePath != want {
			t.Errorf("repo %s: path %q, want %q", name, wr.WorktreePath, want)
		}
		if _, statErr := os.Stat(wr.WorktreePath); statErr != nil {
			t.Errorf("repo %s: worktree missing on disk: %v", name, statErr)
		}
	}

	// The workflow must be persisted, or the TUI and `workflows list` never
	// see work started from the CLI.
	loaded, err := s.LoadWorkflows(ctx, "proj-alpha")
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	if len(loaded) != 1 || loaded[0].BranchName != "feature/x" {
		t.Fatalf("expected the workflow to be persisted, got %+v", loaded)
	}
}

// TestCreateWorkflowIsIdempotent: re-creating the same branch adopts the
// existing worktrees and does not duplicate the persisted workflow.
func TestCreateWorkflowIsIdempotent(t *testing.T) {
	s, root := newWorkflowTestService(t)
	ctx := context.Background()
	baseDir := filepath.Join(root, "worktrees")

	if _, err := s.CreateWorkflow(ctx, "proj-alpha", "feature/x", baseDir); err != nil {
		t.Fatalf("first CreateWorkflow: %v", err)
	}
	wf, err := s.CreateWorkflow(ctx, "proj-alpha", "feature/x", baseDir)
	if err != nil {
		t.Fatalf("second CreateWorkflow: %v", err)
	}
	if got := wf.Status().WorktreesCreated; got != 2 {
		t.Errorf("expected 2 worktrees after re-create, got %d", got)
	}

	loaded, err := s.LoadWorkflows(ctx, "proj-alpha")
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 persisted workflow, got %d", len(loaded))
	}
}

// TestCreateWorkflowDefaultsBaseDir: an empty base dir falls back to the same
// default the TUI uses, so both surfaces produce one layout.
func TestCreateWorkflowDefaultsBaseDir(t *testing.T) {
	s, _ := newWorkflowTestService(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	wf, err := s.CreateWorkflow(context.Background(), "proj-alpha", "feature/y", "")
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	want := filepath.Join(home, ".worktrees", "Alpha")
	if wf.BaseDir != want {
		t.Errorf("base dir %q, want %q", wf.BaseDir, want)
	}
}

// TestCreateWorkflowUnknownProject keeps the error surface honest.
func TestCreateWorkflowUnknownProject(t *testing.T) {
	s, _ := newWorkflowTestService(t)
	_, err := s.CreateWorkflow(context.Background(), "proj-nope", "feature/x", "")
	if !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for an unknown project, got %v", err)
	}
}

// TestRemoveWorkflowCleansEveryRepo: remove tears down the worktrees created
// by CreateWorkflow and drops the workflow from persistence.
func TestRemoveWorkflowCleansEveryRepo(t *testing.T) {
	s, _ := newWorkflowTestService(t)
	ctx := context.Background()
	baseDir := t.TempDir()
	created, err := s.CreateWorkflow(ctx, "proj-alpha", "feature/x", baseDir)
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}

	removed, err := s.RemoveWorkflow(ctx, "proj-alpha", "feature/x")
	if err != nil {
		t.Fatalf("RemoveWorkflow: %v", err)
	}
	for name, wr := range removed.Repos {
		if wr.Error != "" {
			t.Errorf("repo %s: unexpected error %q", name, wr.Error)
		}
		if wr.WorktreeCreated {
			t.Errorf("repo %s: worktree still marked created", name)
		}
	}
	for _, wr := range created.Repos {
		if _, err := os.Stat(wr.WorktreePath); !os.IsNotExist(err) {
			t.Errorf("worktree %s still on disk (err=%v)", wr.WorktreePath, err)
		}
	}

	wfs, err := s.LoadWorkflows(ctx, "proj-alpha")
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	if len(wfs) != 0 {
		t.Errorf("workflow still persisted after remove: %d left", len(wfs))
	}
}

// TestRemoveWorkflowUnknownBranch keeps the error surface honest.
func TestRemoveWorkflowUnknownBranch(t *testing.T) {
	s, _ := newWorkflowTestService(t)
	if _, err := s.RemoveWorkflow(context.Background(), "proj-alpha", "feature/nope"); err == nil {
		t.Fatal("expected error for unknown workflow branch")
	}
}
