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
	return newProjectService(loader, nil), root
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
	// The default base dir slugifies the project name (see
	// project.DefaultWorkflowBaseDir), so paths never contain spaces or caps.
	want := filepath.Join(home, ".worktrees", "alpha")
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

// recordingAgents is an AgentService that serves a fixed set of agents and
// records which of them RemoveWorkflow asks it to terminate. onTerminate,
// when set, runs inside every Terminate call so a test can observe the
// worktrees' state at that moment.
type recordingAgents struct {
	service.AgentService

	snaps       []service.AgentSnapshot
	terminated  []string
	failFor     string
	onTerminate func(id string)
}

func (r *recordingAgents) List(context.Context) ([]service.AgentSnapshot, error) {
	return r.snaps, nil
}

func (r *recordingAgents) Terminate(_ context.Context, id string) error {
	r.terminated = append(r.terminated, id)
	if r.onTerminate != nil {
		r.onTerminate(id)
	}
	if id == r.failFor {
		return errors.New("process would not die")
	}
	return nil
}

// TestRemoveWorkflowTerminatesAgentsFirst is the daemon-side half of "deleting
// a workflow kills its agents": every agent working anywhere inside the
// workflow dies (the recorded one and any started straight in a worktree),
// agents elsewhere are left alone, and the kill happens while the worktrees
// are still on disk, not after they are gone.
func TestRemoveWorkflowTerminatesAgentsFirst(t *testing.T) {
	s, _ := newWorkflowTestService(t)
	ctx := context.Background()
	wf, err := s.CreateWorkflow(ctx, "proj-alpha", "feature/x", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	wf.SetWorkflowAgentID("recorded")
	if err := s.SaveWorkflows(ctx, "proj-alpha", []*service.FeatureWorkflow{wf}); err != nil {
		t.Fatalf("SaveWorkflows: %v", err)
	}

	dir := wf.WorkflowDir()
	agents := &recordingAgents{snaps: []service.AgentSnapshot{
		{ID: "recorded", WorkDir: dir},
		{ID: "in-web", WorkDir: filepath.Join(dir, "web")},
		{ID: "in-api", WorkDir: filepath.Join(dir, "api")},
		{ID: "sibling", WorkDir: dir + "-other"},
		{ID: "elsewhere", WorkDir: t.TempDir()},
	}}
	agents.onTerminate = func(id string) {
		for _, wr := range wf.Repos {
			if _, err := os.Stat(wr.WorktreePath); err != nil {
				t.Errorf("terminating %s: worktree %s already gone (%v)", id, wr.WorktreePath, err)
			}
		}
	}
	s.agent = agents

	if _, err := s.RemoveWorkflow(ctx, "proj-alpha", "feature/x"); err != nil {
		t.Fatalf("RemoveWorkflow: %v", err)
	}
	want := []string{"recorded", "in-web", "in-api"}
	if !sameSet(agents.terminated, want) {
		t.Errorf("terminated %v, want %v", agents.terminated, want)
	}
	for _, wr := range wf.Repos {
		if _, err := os.Stat(wr.WorktreePath); !os.IsNotExist(err) {
			t.Errorf("worktree %s still on disk (err=%v)", wr.WorktreePath, err)
		}
	}
}

// TestRemoveWorkflowKeepsWorktreesWhenAgentSurvives: an agent that cannot be
// stopped must not have its directory deleted underneath it. The removal
// fails, the worktrees stay, and the workflow stays persisted for a retry.
func TestRemoveWorkflowKeepsWorktreesWhenAgentSurvives(t *testing.T) {
	s, _ := newWorkflowTestService(t)
	ctx := context.Background()
	wf, err := s.CreateWorkflow(ctx, "proj-alpha", "feature/x", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	s.agent = &recordingAgents{
		snaps:   []service.AgentSnapshot{{ID: "stuck", WorkDir: filepath.Join(wf.WorkflowDir(), "web")}},
		failFor: "stuck",
	}

	if _, err := s.RemoveWorkflow(ctx, "proj-alpha", "feature/x"); err == nil {
		t.Fatal("RemoveWorkflow succeeded with an agent still alive")
	}
	for _, wr := range wf.Repos {
		if _, err := os.Stat(wr.WorktreePath); err != nil {
			t.Errorf("worktree %s removed under a live agent (%v)", wr.WorktreePath, err)
		}
	}
	wfs, err := s.LoadWorkflows(ctx, "proj-alpha")
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	if len(wfs) != 1 {
		t.Errorf("workflow dropped from persistence despite failed removal: %d left", len(wfs))
	}
}

func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]bool, len(got))
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			return false
		}
	}
	return true
}
