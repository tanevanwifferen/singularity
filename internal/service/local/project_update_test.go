package local

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/project"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

func gitInitWithCommit(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// newUpdateTestService builds a file-backed localProjectService (UpdateRepos
// rewrites the config on disk) over a scan directory with repos web+api.
func newUpdateTestService(t *testing.T, agents service.AgentService) (*localProjectService, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	scanDir := filepath.Join(root, "repos")
	var defs []project.RepoDef
	for _, name := range []string{"web", "api"} {
		dir := filepath.Join(scanDir, name)
		gitInitWithCommit(t, dir)
		defs = append(defs, project.RepoDef{Name: name, Path: dir, DefaultBranch: "main"})
	}
	cfgPath := project.GetDefaultConfigPath()
	cfg := &project.ProjectConfig{Projects: map[string]project.ProjectDef{
		"alpha": {Name: "Alpha", Root: scanDir, Repos: defs},
	}}
	if err := project.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	loader, err := project.NewLoaderFromFile(cfgPath)
	if err != nil {
		t.Fatalf("NewLoaderFromFile: %v", err)
	}
	return newProjectService(loader, agents), scanDir
}

func TestUpdateReposSyncsWorkflowsAndStopsAgentsInRemovedWorktree(t *testing.T) {
	agents := &recordingAgents{}
	s, scanDir := newUpdateTestService(t, agents)
	ctx := context.Background()
	baseDir := filepath.Join(filepath.Dir(scanDir), "worktrees")

	wf, err := s.CreateWorkflow(ctx, "proj-alpha", "feature/x", baseDir)
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	apiWorktree := wf.Repos["api"].WorktreePath
	webWorktree := wf.Repos["web"].WorktreePath
	agents.snaps = []service.AgentSnapshot{
		{ID: "in-api", WorkDir: filepath.Join(apiWorktree, "src")},
		{ID: "in-web", WorkDir: webWorktree},
		{ID: "elsewhere", WorkDir: t.TempDir()},
	}

	// A subrepo appears and another disappears from disk.
	gitInitWithCommit(t, filepath.Join(scanDir, "lib"))
	if err := os.RemoveAll(filepath.Join(scanDir, "api")); err != nil {
		t.Fatal(err)
	}

	res, err := s.UpdateRepos(ctx, "proj-alpha", "")
	if err != nil {
		t.Fatalf("UpdateRepos: %v", err)
	}
	if res.Dir != scanDir {
		t.Fatalf("scanned %q, want stored root %q", res.Dir, scanDir)
	}
	if len(res.Added) != 1 || res.Added[0] != "lib" || len(res.Removed) != 1 || res.Removed[0] != "api" {
		t.Fatalf("added=%v removed=%v, want +lib -api", res.Added, res.Removed)
	}
	if len(res.Workflows) != 1 || res.Workflows[0] != "feature/x" {
		t.Fatalf("workflows synced = %v, want [feature/x]", res.Workflows)
	}
	if len(res.WorkflowErrors) != 0 {
		t.Fatalf("workflow errors: %v", res.WorkflowErrors)
	}
	if len(agents.terminated) != 1 || agents.terminated[0] != "in-api" {
		t.Fatalf("terminated %v, want only the agent inside the removed worktree", agents.terminated)
	}

	// In-memory project reflects the new repo set.
	info, err := s.Info(ctx, "proj-alpha")
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	var names []string
	for _, r := range info.Repos {
		names = append(names, r.Name)
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "lib" || names[1] != "web" {
		t.Fatalf("project repos = %v, want [lib web]", names)
	}

	// Persisted workflow reflects the sync and the worktrees match.
	loaded, err := s.LoadWorkflows(ctx, "proj-alpha")
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 persisted workflow, got %d", len(loaded))
	}
	got := loaded[0]
	if _, ok := got.Repos["api"]; ok {
		t.Fatal("removed repo still in persisted workflow")
	}
	lib := got.Repos["lib"]
	if lib == nil || !lib.WorktreeCreated {
		t.Fatalf("new repo has no worktree in persisted workflow: %+v", lib)
	}
	if _, err := os.Stat(lib.WorktreePath); err != nil {
		t.Fatalf("lib worktree missing: %v", err)
	}
	if _, err := os.Stat(apiWorktree); !os.IsNotExist(err) {
		t.Fatalf("orphaned api worktree still on disk (stat err %v)", err)
	}
}

func TestUpdateReposReconcilesWorkflowsWithoutConfigChange(t *testing.T) {
	s, scanDir := newUpdateTestService(t, nil)
	ctx := context.Background()
	baseDir := filepath.Join(filepath.Dir(scanDir), "worktrees")

	wf, err := s.CreateWorkflow(ctx, "proj-alpha", "feature/x", baseDir)
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	// Simulate an earlier run that updated the config but never finished
	// the worktree sync: drop web from the persisted workflow by hand.
	delete(wf.Repos, "web")
	if err := s.SaveWorkflows(ctx, "proj-alpha", []*service.FeatureWorkflow{wf}); err != nil {
		t.Fatalf("SaveWorkflows: %v", err)
	}

	res, err := s.UpdateRepos(ctx, "proj-alpha", "")
	if err != nil {
		t.Fatalf("UpdateRepos: %v", err)
	}
	if len(res.Added)+len(res.Removed)+len(res.Moved) != 0 {
		t.Fatalf("config changed unexpectedly: %+v", res)
	}
	if len(res.Workflows) != 1 {
		t.Fatalf("workflow not reconciled although its repo set was stale: %+v", res)
	}
	loaded, err := s.LoadWorkflows(ctx, "proj-alpha")
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	web := loaded[0].Repos["web"]
	if web == nil || !web.WorktreeCreated {
		t.Fatalf("web not brought back into the workflow: %+v", web)
	}

	// And a fully consistent state is a clean no-op.
	res, err = s.UpdateRepos(ctx, "proj-alpha", "")
	if err != nil {
		t.Fatalf("UpdateRepos (noop): %v", err)
	}
	if len(res.Workflows) != 0 || len(res.WorkflowErrors) != 0 {
		t.Fatalf("expected no-op, got %+v", res)
	}
}

func TestUpdateReposReportsDirtyWorktreeAndRetries(t *testing.T) {
	s, scanDir := newUpdateTestService(t, nil)
	ctx := context.Background()
	baseDir := filepath.Join(filepath.Dir(scanDir), "worktrees")

	wf, err := s.CreateWorkflow(ctx, "proj-alpha", "feature/x", baseDir)
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	apiWorktree := wf.Repos["api"].WorktreePath
	wip := filepath.Join(apiWorktree, "wip.txt")
	if err := os.WriteFile(wip, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// api leaves the project config, but its repo stays on disk: move it
	// out of the scan directory.
	if err := os.Rename(filepath.Join(scanDir, "api"), filepath.Join(filepath.Dir(scanDir), "api-parked")); err != nil {
		t.Fatal(err)
	}
	// The worktree's .git file points at the old repo location; repair so
	// git can still read it, as it could for a repo removed from config
	// in place.
	if out, err := exec.Command("git", "-C", filepath.Join(filepath.Dir(scanDir), "api-parked"), "worktree", "repair", apiWorktree).CombinedOutput(); err != nil {
		t.Fatalf("worktree repair: %v\n%s", err, out)
	}

	res, err := s.UpdateRepos(ctx, "proj-alpha", "")
	if err != nil {
		t.Fatalf("UpdateRepos: %v", err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != "api" {
		t.Fatalf("removed = %v, want [api]", res.Removed)
	}
	if len(res.WorkflowErrors) != 1 {
		t.Fatalf("workflow errors = %v, want the dirty-worktree refusal", res.WorkflowErrors)
	}
	if _, err := os.Stat(wip); err != nil {
		t.Fatalf("uncommitted work destroyed: %v", err)
	}
}
