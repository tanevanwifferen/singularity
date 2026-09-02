package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/git"
)

// commitInWorktree adds a commit inside a workflow worktree so the feature
// branch is genuinely ahead of the repo's default branch.
func commitInWorktree(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "-C", dir, "add", "."},
		{"git", "-C", dir, "commit", "-m", "feature: " + name},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %s %v", args, out, err)
		}
	}
}

// TestMergeAllToDefault merges a workflow branch into every repo's local
// default branch, including a repo whose default branch is not "main".
func TestMergeAllToDefault(t *testing.T) {
	proj, root := newTestProject(t)
	fw := NewFeatureWorkflow(proj, "feature/merge-me", filepath.Join(root, "wt"))
	if err := fw.CreateAllWorktrees(); err != nil {
		t.Fatalf("CreateAllWorktrees: %v", err)
	}
	for _, name := range []string{"web", "api", "lib"} {
		commitInWorktree(t, fw.GetRepo(name).WorktreePath, name+".txt")
	}

	for _, st := range fw.MergeStatuses() {
		if !st.Eligible {
			t.Fatalf("%s not eligible: %s", st.RepoName, st.Reason)
		}
		if st.Ahead != 1 {
			t.Errorf("%s: ahead = %d, want 1", st.RepoName, st.Ahead)
		}
	}

	for _, r := range fw.MergeAllToDefault(false) {
		if !r.Merged {
			t.Fatalf("%s not merged: skipped=%v reason=%s conflicts=%v", r.RepoName, r.Skipped, r.Reason, r.Conflicts)
		}
	}

	// The commit must now be reachable from each repo's default branch, and
	// a second merge must report "already merged" rather than merging twice.
	for _, name := range []string{"web", "api", "lib"} {
		wr := fw.GetRepo(name)
		if _, err := os.Stat(filepath.Join(wr.OriginalPath, name+".txt")); err != nil {
			t.Errorf("%s: merged file missing in main checkout: %v", name, err)
		}
		ahead, _, err := git.CompareBranchesSimple(wr.OriginalPath, wr.DefaultBranch, fw.BranchName)
		if err != nil {
			t.Fatalf("%s compare: %v", name, err)
		}
		if ahead != 0 {
			t.Errorf("%s: still %d commits ahead of %s", name, ahead, wr.DefaultBranch)
		}
	}
	for _, r := range fw.MergeAllToDefault(false) {
		if !r.Skipped || r.Reason != "already merged" {
			t.Errorf("%s: second merge = %+v, want skipped/already merged", r.RepoName, r)
		}
	}
}

// TestMergeAllToDefault_SkipsDirtyAndOffBranchCheckouts guards the preflight:
// we never merge into a checkout that has uncommitted work or is parked on
// another branch.
func TestMergeAllToDefault_SkipsDirtyAndOffBranchCheckouts(t *testing.T) {
	proj, root := newTestProject(t)
	fw := NewFeatureWorkflow(proj, "feature/preflight", filepath.Join(root, "wt"))
	if err := fw.CreateAllWorktrees(); err != nil {
		t.Fatalf("CreateAllWorktrees: %v", err)
	}
	for _, name := range []string{"web", "api", "lib"} {
		commitInWorktree(t, fw.GetRepo(name).WorktreePath, name+".txt")
	}

	// web: uncommitted change in the main checkout.
	if err := os.WriteFile(filepath.Join(fw.GetRepo("web").OriginalPath, "file.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// api: main checkout parked on another branch.
	apiPath := fw.GetRepo("api").OriginalPath
	if out, err := exec.Command("git", "-C", apiPath, "checkout", "-b", "elsewhere").CombinedOutput(); err != nil {
		t.Fatalf("checkout: %s %v", out, err)
	}

	byRepo := map[string]RepoMergeResult{}
	for _, r := range fw.MergeAllToDefault(false) {
		byRepo[r.RepoName] = r
	}
	if r := byRepo["web"]; !r.Skipped || r.Reason != "main checkout has uncommitted changes" {
		t.Errorf("web = %+v, want skipped for dirty checkout", r)
	}
	if r := byRepo["api"]; !r.Skipped || r.Reason == "" {
		t.Errorf("api = %+v, want skipped for off-branch checkout", r)
	}
	if r := byRepo["lib"]; !r.Merged {
		t.Errorf("lib = %+v, want merged", r)
	}
}
