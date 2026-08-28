package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/git"
)

// initWorkflowTestRepo creates a git repo with one commit on `branch` and
// returns its path. No remote is configured, so it also exercises the
// origin/<default> fallback in ensureWorktree.
func initWorkflowTestRepo(t *testing.T, parent, name, branch string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "-C", dir, "init", "--initial-branch", branch},
		{"git", "-C", dir, "config", "user.email", "test@test.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("setup %v failed: %s %v", args, out, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "-C", dir, "add", "."},
		{"git", "-C", dir, "commit", "-m", "initial"},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("setup %v failed: %s %v", args, out, err)
		}
	}
	return dir
}

// newTestProject builds a three-repo project on disk. Two repos default to
// "main", one to "develop", so the per-repo default branch is exercised.
func newTestProject(t *testing.T) (*Project, string) {
	t.Helper()
	root := t.TempDir()
	defs := []RepoDef{
		{Name: "web", Path: initWorkflowTestRepo(t, root, "web", "main"), DefaultBranch: "main"},
		{Name: "api", Path: initWorkflowTestRepo(t, root, "api", "main"), DefaultBranch: "main"},
		{Name: "lib", Path: initWorkflowTestRepo(t, root, "lib", "develop"), DefaultBranch: "develop"},
	}
	return NewProject(ProjectDef{Name: "Test", Repos: defs}), root
}

// TestCreateAllWorktrees_AllReposIsolated is the core paradigm guard: a
// workflow always yields one worktree per repo in the project, on one branch.
func TestCreateAllWorktrees_AllReposIsolated(t *testing.T) {
	proj, root := newTestProject(t)
	baseDir := filepath.Join(root, "worktrees")

	fw := NewFeatureWorkflow(proj, "feature/x", baseDir)
	if err := fw.CreateAllWorktrees(); err != nil {
		t.Fatalf("CreateAllWorktrees: %v", err)
	}

	status := fw.Status()
	if status.WorktreesCreated != 3 {
		t.Fatalf("expected 3 worktrees, got %d (errors: %d)", status.WorktreesCreated, status.Errors)
	}
	if status.State != WorkflowActive {
		t.Errorf("expected state active, got %v", status.State)
	}

	for name, wr := range fw.Repos {
		want := filepath.Join(baseDir, "feature-x", name)
		if wr.WorktreePath != want {
			t.Errorf("repo %s: worktree path %q, want %q", name, wr.WorktreePath, want)
		}
		if _, err := os.Stat(filepath.Join(wr.WorktreePath, "file.txt")); err != nil {
			t.Errorf("repo %s: worktree not checked out: %v", name, err)
		}
		out, err := exec.Command("git", "-C", wr.WorktreePath, "rev-parse", "--abbrev-ref", "HEAD").Output()
		if err != nil {
			t.Fatalf("repo %s: current branch: %v", name, err)
		}
		if branch := strings.TrimSpace(string(out)); branch != "feature/x" {
			t.Errorf("repo %s: on branch %q, want feature/x", name, branch)
		}
	}
}

// TestCreateAllWorktrees_Idempotent verifies a repeated create adopts the
// existing worktrees instead of failing on "branch already exists".
func TestCreateAllWorktrees_Idempotent(t *testing.T) {
	proj, root := newTestProject(t)
	baseDir := filepath.Join(root, "worktrees")

	first := NewFeatureWorkflow(proj, "feature/x", baseDir)
	if err := first.CreateAllWorktrees(); err != nil {
		t.Fatalf("first create: %v", err)
	}

	second := NewFeatureWorkflow(proj, "feature/x", baseDir)
	if err := second.CreateAllWorktrees(); err != nil {
		t.Fatalf("second create: %v", err)
	}
	if got := second.Status().WorktreesCreated; got != 3 {
		t.Fatalf("expected 3 adopted worktrees, got %d", got)
	}
	for name, wr := range second.Repos {
		if wr.WorktreePath != first.Repos[name].WorktreePath {
			t.Errorf("repo %s: adopted path %q, want %q", name, wr.WorktreePath, first.Repos[name].WorktreePath)
		}
	}
}

// TestCreateAllWorktrees_AdoptsExistingBranch covers a branch that exists in a
// repo but has no worktree: it must be attached, not recreated.
func TestCreateAllWorktrees_AdoptsExistingBranch(t *testing.T) {
	proj, root := newTestProject(t)
	baseDir := filepath.Join(root, "worktrees")

	web := proj.Repos[0]
	if err := git.CreateBranch(web.Path, "feature/x", "main"); err != nil {
		t.Fatalf("pre-create branch: %v", err)
	}
	// Leave the repo on its default branch so the branch is free to be moved
	// into a worktree.
	if err := git.Checkout(web.Path, "main"); err != nil {
		t.Fatalf("checkout main: %v", err)
	}

	fw := NewFeatureWorkflow(proj, "feature/x", baseDir)
	if err := fw.CreateAllWorktrees(); err != nil {
		t.Fatalf("CreateAllWorktrees: %v", err)
	}
	if got := fw.Status().WorktreesCreated; got != 3 {
		t.Fatalf("expected 3 worktrees, got %d (%+v)", got, fw.Repos["web"].Error)
	}
}

// TestCreateAllWorktrees_PartialFailure checks a broken repo does not stop the
// others from getting isolated.
func TestCreateAllWorktrees_PartialFailure(t *testing.T) {
	proj, root := newTestProject(t)
	proj.Repos[2].Path = filepath.Join(root, "does-not-exist")

	fw := NewFeatureWorkflow(proj, "feature/x", filepath.Join(root, "worktrees"))
	err := fw.CreateAllWorktrees()
	if err == nil {
		t.Fatal("expected a partial-failure error")
	}
	status := fw.Status()
	if status.WorktreesCreated != 2 || status.Errors != 1 {
		t.Fatalf("expected 2 created / 1 error, got %d / %d", status.WorktreesCreated, status.Errors)
	}
}

func TestDefaultWorkflowBaseDir(t *testing.T) {
	got := DefaultWorkflowBaseDir("Test")
	if filepath.Base(got) != "Test" || filepath.Base(filepath.Dir(got)) != ".worktrees" {
		t.Errorf("unexpected default base dir %q", got)
	}
	if DefaultWorkflowBaseDir("") == got {
		t.Error("expected a different default for an unnamed project")
	}
}
