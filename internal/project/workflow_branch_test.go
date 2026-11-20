package project

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/git"
)

// assertNothingCreated fails if a refused create left anything behind: a
// worktree, the base directory, a branch in any repo, or a state change.
func assertNothingCreated(t *testing.T, proj *Project, fw *FeatureWorkflow, baseDir, branch string, keep map[string]bool) {
	t.Helper()
	if _, err := os.Stat(baseDir); !os.IsNotExist(err) {
		t.Errorf("base dir exists after refusal (stat err %v)", err)
	}
	if got := fw.Status().WorktreesCreated; got != 0 {
		t.Errorf("%d worktrees marked created after refusal", got)
	}
	if fw.State != WorkflowInitializing {
		t.Errorf("state = %v, want untouched %v", fw.State, WorkflowInitializing)
	}
	for _, r := range proj.Repos {
		if !keep[r.Name] && git.BranchExists(r.Path, branch) {
			t.Errorf("repo %s: branch %q created despite refusal", r.Name, branch)
		}
	}
}

func requireBranchExists(t *testing.T, err error, branch string) *BranchExistsError {
	t.Helper()
	var be *BranchExistsError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want *BranchExistsError", err)
	}
	if be.Branch != branch {
		t.Errorf("error branch = %q, want %q", be.Branch, branch)
	}
	if !strings.Contains(err.Error(), branch) {
		t.Errorf("error %q does not name the branch", err)
	}
	return be
}

func TestCreateAllWorktrees_NewBranchHappyPath(t *testing.T) {
	proj, root := newTestProject(t)
	fw := NewFeatureWorkflow(proj, "feature/new", filepath.Join(root, "worktrees"))

	if err := fw.CheckBranchAvailable(); err != nil {
		t.Fatalf("CheckBranchAvailable on a fresh branch: %v", err)
	}
	if err := fw.CreateAllWorktrees(); err != nil {
		t.Fatalf("CreateAllWorktrees: %v", err)
	}
	if got := fw.Status().WorktreesCreated; got != 3 {
		t.Fatalf("created %d worktrees, want 3", got)
	}
}

func TestCreateAllWorktrees_RefusesLocalBranch(t *testing.T) {
	proj, root := newTestProject(t)
	baseDir := filepath.Join(root, "worktrees")
	web := proj.Repos[0]
	if err := git.CreateBranch(web.Path, "feature/x", "main"); err != nil {
		t.Fatal(err)
	}
	if err := git.Checkout(web.Path, "main"); err != nil {
		t.Fatal(err)
	}

	fw := NewFeatureWorkflow(proj, "feature/x", baseDir)
	be := requireBranchExists(t, fw.CreateAllWorktrees(), "feature/x")
	if len(be.Repos) != 1 || !strings.HasPrefix(be.Repos[0], "web (local") {
		t.Errorf("repos = %v, want only web (local)", be.Repos)
	}
	assertNothingCreated(t, proj, fw, baseDir, "feature/x", map[string]bool{"web": true})
}

func TestCreateAllWorktrees_RefusesRemoteOnlyBranch(t *testing.T) {
	proj, root := newTestProject(t)
	baseDir := filepath.Join(root, "worktrees")
	api := proj.Repos[1]
	// A remote-tracking ref with no local branch: someone else's branch.
	if out, err := exec.Command("git", "-C", api.Path, "update-ref", "refs/remotes/origin/feature/x", "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("update-ref: %s %v", out, err)
	}
	if git.BranchExists(api.Path, "feature/x") {
		t.Fatal("setup: branch must not exist locally")
	}

	fw := NewFeatureWorkflow(proj, "feature/x", baseDir)
	be := requireBranchExists(t, fw.CreateAllWorktrees(), "feature/x")
	if len(be.Repos) != 1 || !strings.Contains(be.Repos[0], "api (origin") {
		t.Errorf("repos = %v, want only api (origin)", be.Repos)
	}
	assertNothingCreated(t, proj, fw, baseDir, "feature/x", nil)
}

func TestCreateAllWorktrees_RefusesDefaultBranch(t *testing.T) {
	proj, root := newTestProject(t)
	baseDir := filepath.Join(root, "worktrees")

	fw := NewFeatureWorkflow(proj, "main", baseDir)
	be := requireBranchExists(t, fw.CreateAllWorktrees(), "main")
	if len(be.Repos) != 2 {
		t.Errorf("repos = %v, want web and api (lib's default is develop)", be.Repos)
	}
	assertNothingCreated(t, proj, fw, baseDir, "main", map[string]bool{"web": true, "api": true})
}

// A default branch with no ref in the repo at all (unborn or not fetched) is
// still not a valid workflow branch.
func TestCheckBranchAvailable_RefusesNamedDefaultWithoutRef(t *testing.T) {
	proj, root := newTestProject(t)
	proj.Repos[0].DefaultBranch = "trunk"

	fw := NewFeatureWorkflow(proj, "trunk", filepath.Join(root, "worktrees"))
	be := requireBranchExists(t, fw.CheckBranchAvailable(), "trunk")
	if len(be.Repos) != 1 || !strings.Contains(be.Repos[0], "web (default branch") {
		t.Errorf("repos = %v, want web (default branch)", be.Repos)
	}
}

// Re-running a create is refused: the first create made the branch, and a
// second workflow must not take ownership of it.
func TestCreateAllWorktrees_SecondCreateRefused(t *testing.T) {
	proj, root := newTestProject(t)
	baseDir := filepath.Join(root, "worktrees")

	first := NewFeatureWorkflow(proj, "feature/x", baseDir)
	if err := first.CreateAllWorktrees(); err != nil {
		t.Fatalf("first create: %v", err)
	}
	second := NewFeatureWorkflow(proj, "feature/x", baseDir)
	be := requireBranchExists(t, second.CreateAllWorktrees(), "feature/x")
	if len(be.Repos) != 3 {
		t.Errorf("repos = %v, want all three", be.Repos)
	}
	if got := second.Status().WorktreesCreated; got != 0 {
		t.Errorf("second workflow adopted %d worktrees", got)
	}
}

// SyncRepos must not hand a pre-existing branch to a repo that is new to the
// workflow, but still restores a tracked repo's own worktree.
func TestSyncRepos_RefusesExistingBranchInNewRepo(t *testing.T) {
	proj, root := newTestProject(t)
	small := &Project{Name: proj.Name, Repos: proj.Repos[:2]}
	fw := NewFeatureWorkflow(small, "feature/sync", filepath.Join(root, "worktrees"))
	if err := fw.CreateAllWorktrees(); err != nil {
		t.Fatal(err)
	}
	lib := proj.Repos[2]
	if err := git.CreateBranch(lib.Path, "feature/sync", "develop"); err != nil {
		t.Fatal(err)
	}
	if err := git.Checkout(lib.Path, "develop"); err != nil {
		t.Fatal(err)
	}

	fw.SyncRepos(proj, nil)

	wr := fw.Repos["lib"]
	if wr == nil || wr.WorktreeCreated || !strings.Contains(wr.Error, "already exists") {
		t.Fatalf("lib = %+v, want a refusal recorded", wr)
	}
}
