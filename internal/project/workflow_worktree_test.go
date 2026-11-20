package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/git"
)

// resolvedDir returns dir with symlinks resolved, matching how git reports
// worktree paths.
func resolvedDir(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

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

// TestCreateAllWorktrees_MirrorsProjectLayout: repos nested below the project
// root (pdflibrary/librarian) get the same sub-path under the workflow dir,
// so the workflow dir is a drop-in copy of the project tree.
func TestCreateAllWorktrees_MirrorsProjectLayout(t *testing.T) {
	root := resolvedDir(t, t.TempDir())
	nested := filepath.Join(root, "pdflibrary")
	defs := []RepoDef{
		{Name: "web", Path: initWorkflowTestRepo(t, root, "web", "main"), DefaultBranch: "main"},
		{Name: "librarian", Path: initWorkflowTestRepo(t, nested, "librarian", "main"), DefaultBranch: "main"},
		{Name: "discord_bot", Path: initWorkflowTestRepo(t, nested, "discord_bot", "main"), DefaultBranch: "main"},
	}
	proj := NewProject(ProjectDef{Name: "Test", Repos: defs})
	if got := proj.Root(); got != root {
		t.Fatalf("project root %q, want %q", got, root)
	}

	baseDir := filepath.Join(resolvedDir(t, t.TempDir()), "worktrees")
	fw := NewFeatureWorkflow(proj, "feature/x", baseDir)
	if err := fw.CreateAllWorktrees(); err != nil {
		t.Fatalf("CreateAllWorktrees: %v", err)
	}

	want := map[string]string{
		"web":         filepath.Join(fw.WorkflowDir(), "web"),
		"librarian":   filepath.Join(fw.WorkflowDir(), "pdflibrary", "librarian"),
		"discord_bot": filepath.Join(fw.WorkflowDir(), "pdflibrary", "discord_bot"),
	}
	for name, wantPath := range want {
		wr := fw.Repos[name]
		if wr.WorktreePath != wantPath {
			t.Errorf("repo %s: worktree path %q, want %q", name, wr.WorktreePath, wantPath)
		}
		if _, err := os.Stat(filepath.Join(wr.WorktreePath, "file.txt")); err != nil {
			t.Errorf("repo %s: worktree not checked out: %v", name, err)
		}
	}

	// Discovery must recover the same base dir from the nested layout.
	found, err := DiscoverWorkflows(proj, nil)
	if err != nil || len(found) != 1 {
		t.Fatalf("DiscoverWorkflows: %v (%d found)", err, len(found))
	}
	if found[0].BaseDir != baseDir {
		t.Errorf("discovered base dir %q, want %q", found[0].BaseDir, baseDir)
	}

	// Cleanup must also drop the intermediate pdflibrary/ dir.
	if err := fw.RemoveAllWorktrees(); err != nil {
		t.Fatalf("RemoveAllWorktrees: %v", err)
	}
	if _, err := os.Stat(fw.WorkflowDir()); !os.IsNotExist(err) {
		t.Errorf("workflow dir still present after cleanup: %v", err)
	}
}

// nestedTestProject builds a root repo (pbd-development) with a sub-repo
// living inside it, plus a flat sibling.
func nestedTestProject(t *testing.T) *Project {
	t.Helper()
	root := resolvedDir(t, t.TempDir())
	rootRepo := initWorkflowTestRepo(t, root, "pbd-development", "main")
	defs := []RepoDef{
		{Name: "pbd-development", Path: rootRepo, DefaultBranch: "main"},
		{Name: "api", Path: initWorkflowTestRepo(t, rootRepo, "api", "main"), DefaultBranch: "main"},
		{Name: "web", Path: initWorkflowTestRepo(t, rootRepo, "web", "main"), DefaultBranch: "main"},
	}
	return NewProject(ProjectDef{Name: "Test", Repos: defs})
}

// TestCreateAllWorktrees_NestedParentFirst: a sub-repo inside the root repo
// must not create the root's worktree dir before the root is added. Repeated
// to catch the former goroutine race.
func TestCreateAllWorktrees_NestedParentFirst(t *testing.T) {
	for i := 0; i < 5; i++ {
		proj := nestedTestProject(t)
		baseDir := filepath.Join(resolvedDir(t, t.TempDir()), "worktrees")
		fw := NewFeatureWorkflow(proj, "feature/nested", baseDir)

		if err := fw.CreateAllWorktrees(); err != nil {
			t.Fatalf("run %d: CreateAllWorktrees: %v", i, err)
		}
		rootWT := filepath.Join(fw.WorkflowDir(), "pbd-development")
		for name, want := range map[string]string{
			"pbd-development": rootWT,
			"api":             filepath.Join(rootWT, "api"),
			"web":             filepath.Join(rootWT, "web"),
		} {
			wr := fw.Repos[name]
			if wr.WorktreePath != want || !wr.WorktreeCreated || wr.Error != "" {
				t.Fatalf("run %d: repo %s: %+v, want path %q created", i, name, wr, want)
			}
			if _, err := os.Stat(filepath.Join(want, "file.txt")); err != nil {
				t.Fatalf("run %d: repo %s not checked out: %v", i, name, err)
			}
		}

		if err := fw.RemoveAllWorktrees(); err != nil {
			t.Fatalf("run %d: RemoveAllWorktrees: %v", i, err)
		}
		for name, wr := range fw.Repos {
			if wr.WorktreeCreated || wr.Error != "" {
				t.Errorf("run %d: repo %s not cleanly removed: %+v", i, name, wr)
			}
			if wts, _ := git.GetWorktrees(wr.OriginalPath); len(wts) != 1 {
				t.Errorf("run %d: repo %s still has worktrees: %+v", i, name, wts)
			}
		}
		if _, err := os.Stat(fw.WorkflowDir()); !os.IsNotExist(err) {
			t.Errorf("run %d: workflow dir still present: %v", i, err)
		}
	}
}

// TestSyncRepos_NestedNewRepoUsesLayout: a repo added later that sits inside
// another repo lands at its layout path, not directly under the workflow dir.
func TestSyncRepos_NestedNewRepoUsesLayout(t *testing.T) {
	proj := nestedTestProject(t)
	var rootDef RepoDef
	for _, r := range proj.Repos {
		if r.Name == "pbd-development" {
			rootDef = RepoDef{Name: r.Name, Path: r.Path, DefaultBranch: r.DefaultBranch}
		}
	}
	small := NewProject(ProjectDef{Name: "Test", Repos: []RepoDef{rootDef}})
	baseDir := filepath.Join(resolvedDir(t, t.TempDir()), "worktrees")
	fw := NewFeatureWorkflow(small, "feature/sync-nested", baseDir)
	if err := fw.CreateAllWorktrees(); err != nil {
		t.Fatal(err)
	}

	fw.SyncRepos(proj, nil)
	api := fw.Repos["api"]
	want := filepath.Join(fw.WorkflowDir(), "pbd-development", "api")
	if api == nil || api.WorktreePath != want || !api.WorktreeCreated || api.Error != "" {
		t.Fatalf("api after sync: %+v, want path %q", api, want)
	}
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

// newRemoteBackedRepo clones a bare remote into a working repo, then pushes a
// newer commit to the remote from a second clone, leaving the first clone's
// origin/main tracking ref stale. Returns the repo, the stale and the new tip.
func newRemoteBackedRepo(t *testing.T, root string) (repo, staleTip, newTip, bare string) {
	t.Helper()
	seed := initWorkflowTestRepo(t, root, "seed", "main")
	bare = filepath.Join(root, "remote.git")
	if out, err := exec.Command("git", "clone", "--bare", seed, bare).CombinedOutput(); err != nil {
		t.Fatalf("bare clone: %s %v", out, err)
	}
	repo = filepath.Join(root, "repo")
	if out, err := exec.Command("git", "clone", bare, repo).CombinedOutput(); err != nil {
		t.Fatalf("clone: %s %v", out, err)
	}
	staleTip = gitT(t, repo, "rev-parse", "origin/main")

	other := filepath.Join(root, "other")
	if out, err := exec.Command("git", "clone", bare, other).CombinedOutput(); err != nil {
		t.Fatalf("clone other: %s %v", out, err)
	}
	gitT(t, other, "config", "user.email", "test@test.com")
	gitT(t, other, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(other, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, other, "add", ".")
	gitT(t, other, "commit", "-m", "newer")
	gitT(t, other, "push", "origin", "main")
	newTip = gitT(t, other, "rev-parse", "HEAD")
	return repo, staleTip, newTip, bare
}

func TestEnsureWorktree_FetchesStaleRemoteTrackingRef(t *testing.T) {
	root := t.TempDir()
	repo, staleTip, newTip, _ := newRemoteBackedRepo(t, root)

	res, err := ensureWorktree(repo, filepath.Join(root, "wt"), "feature/x", "main")
	if err != nil {
		t.Fatal(err)
	}
	if res.Warning != "" {
		t.Errorf("unexpected warning: %s", res.Warning)
	}
	if got := gitT(t, res.Path, "rev-parse", "HEAD"); got != newTip || got == staleTip {
		t.Errorf("branch started at %s, want new tip %s (stale %s)", got, newTip, staleTip)
	}
	if res.BaseRef != "origin/main" || res.BaseCommit != newTip {
		t.Errorf("base = %s@%s, want origin/main@%s", res.BaseRef, res.BaseCommit, newTip)
	}
}

func TestEnsureWorktree_FetchFailureFallsBackWithWarning(t *testing.T) {
	root := t.TempDir()
	repo, staleTip, _, bare := newRemoteBackedRepo(t, root)
	if err := os.RemoveAll(bare); err != nil {
		t.Fatal(err)
	}

	res, err := ensureWorktree(repo, filepath.Join(root, "wt"), "feature/x", "main")
	if err != nil {
		t.Fatalf("fetch failure must not abort the create: %v", err)
	}
	if !strings.Contains(res.Warning, "origin/main") {
		t.Errorf("warning = %q, want mention of origin/main", res.Warning)
	}
	if res.BaseRef != "origin/main" || res.BaseCommit != staleTip {
		t.Errorf("base = %s@%s, want local origin/main@%s", res.BaseRef, res.BaseCommit, staleTip)
	}
}

func TestReattachWorktree_DoesNotFetchOrMoveBranch(t *testing.T) {
	root := t.TempDir()
	repo, staleTip, newTip, _ := newRemoteBackedRepo(t, root)
	wt := filepath.Join(root, "wt")
	gitT(t, repo, "worktree", "add", "-b", "feature/x", wt, staleTip)

	res, err := reattachWorktree(repo, wt, "feature/x", "main")
	if err != nil {
		t.Fatal(err)
	}
	if res.BaseRef != "" || res.Warning != "" {
		t.Errorf("reattach must not report a base or warning: %+v", res)
	}
	if got := gitT(t, repo, "rev-parse", "feature/x"); got != staleTip {
		t.Errorf("branch moved to %s, want untouched %s", got, staleTip)
	}
	if got := gitT(t, repo, "rev-parse", "origin/main"); got == newTip {
		t.Error("reattach fetched; it must leave refs alone")
	}
}
