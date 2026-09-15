package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/git"
)

// syncTestWorkflow creates a workflow over proj with every worktree made,
// so each SyncRepos test starts from a fully realised layout.
func syncTestWorkflow(t *testing.T, proj *Project, root string) *FeatureWorkflow {
	t.Helper()
	wf := NewFeatureWorkflow(proj, "feature/sync", filepath.Join(root, "worktrees"))
	if err := wf.CreateAllWorktrees(); err != nil {
		t.Fatalf("CreateAllWorktrees: %v", err)
	}
	return wf
}

// projectWithout returns a project with the named repo dropped.
func projectWithout(proj *Project, name string) *Project {
	def := ProjectDef{Name: proj.Name}
	for _, r := range proj.Repos {
		if r.Name != name {
			def.Repos = append(def.Repos, RepoDef{Name: r.Name, Path: r.Path, DefaultBranch: r.DefaultBranch})
		}
	}
	return NewProject(def)
}

func TestSyncRepos_AddsWorktreeForNewRepo(t *testing.T) {
	proj, root := newTestProject(t)
	wf := syncTestWorkflow(t, proj, root)

	newPath := initWorkflowTestRepo(t, root, "extra", "main")
	def := ProjectDef{Name: proj.Name}
	for _, r := range proj.Repos {
		def.Repos = append(def.Repos, RepoDef{Name: r.Name, Path: r.Path, DefaultBranch: r.DefaultBranch})
	}
	def.Repos = append(def.Repos, RepoDef{Name: "extra", Path: newPath, DefaultBranch: "main"})
	updated := NewProject(def)

	if !wf.SyncRepos(updated, nil) {
		t.Fatal("SyncRepos reported no change after a repo was added")
	}
	wr := wf.Repos["extra"]
	if wr == nil || !wr.WorktreeCreated || wr.Error != "" {
		t.Fatalf("extra repo not tracked with a worktree: %+v", wr)
	}
	if _, err := os.Stat(filepath.Join(wr.WorktreePath, "file.txt")); err != nil {
		t.Fatalf("worktree for extra missing on disk: %v", err)
	}
	if !git.BranchExists(newPath, "feature/sync") {
		t.Fatal("feature branch not created in the new repo")
	}
	// Idempotent: a second run over the same project changes nothing.
	if wf.SyncRepos(updated, nil) {
		t.Fatal("second SyncRepos over an unchanged project reported a change")
	}
}

func TestSyncRepos_RemovesCleanWorktreeKeepsRemoteAndStopsAgents(t *testing.T) {
	proj, root := newTestProject(t)
	wf := syncTestWorkflow(t, proj, root)
	lib := wf.Repos["lib"]
	libPath := lib.OriginalPath
	worktree := lib.WorktreePath

	// A bare "remote" with the feature branch pushed, to prove the sync
	// never deletes it.
	remote := filepath.Join(root, "lib-remote.git")
	for _, args := range [][]string{
		{"git", "init", "--bare", "-q", remote},
		{"git", "-C", libPath, "remote", "add", "origin", remote},
		{"git", "-C", worktree, "push", "-q", "-u", "origin", "feature/sync"},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	var stopped []string
	stop := func(path string) error {
		stopped = append(stopped, path)
		return nil
	}
	if !wf.SyncRepos(projectWithout(proj, "lib"), stop) {
		t.Fatal("SyncRepos reported no change after a repo was removed")
	}
	if _, ok := wf.Repos["lib"]; ok {
		t.Fatalf("lib still tracked: %+v", wf.Repos["lib"])
	}
	if len(stopped) != 1 || stopped[0] != worktree {
		t.Fatalf("stop called with %v, want [%s]", stopped, worktree)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree still on disk (stat err %v)", err)
	}
	if git.BranchExists(libPath, "feature/sync") {
		t.Fatal("local feature branch should be deleted from the retired repo")
	}
	if !git.RefExists(remote, "refs/heads/feature/sync") {
		t.Fatal("remote feature branch was deleted; the sync must never touch the remote")
	}
	if len(wf.Repos) != 2 {
		t.Fatalf("expected the two remaining repos, got %d", len(wf.Repos))
	}
}

func TestSyncRepos_KeepsDirtyWorktreeWithError(t *testing.T) {
	proj, root := newTestProject(t)
	wf := syncTestWorkflow(t, proj, root)
	worktree := wf.Repos["lib"].WorktreePath
	if err := os.WriteFile(filepath.Join(worktree, "wip.txt"), []byte("unsaved\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wf.SyncRepos(projectWithout(proj, "lib"), nil)

	wr := wf.Repos["lib"]
	if wr == nil {
		t.Fatal("dirty repo was dropped from tracking")
	}
	if !strings.Contains(wr.Error, "uncommitted") {
		t.Fatalf("Error = %q, want an uncommitted-changes explanation", wr.Error)
	}
	if _, err := os.Stat(filepath.Join(worktree, "wip.txt")); err != nil {
		t.Fatalf("uncommitted work was destroyed: %v", err)
	}

	// Once the user cleans up, rerunning finishes the removal.
	if err := os.Remove(filepath.Join(worktree, "wip.txt")); err != nil {
		t.Fatal(err)
	}
	if !wf.SyncRepos(projectWithout(proj, "lib"), nil) {
		t.Fatal("retry reported no change")
	}
	if _, ok := wf.Repos["lib"]; ok {
		t.Fatalf("lib still tracked after clean retry: %+v", wf.Repos["lib"])
	}
}

func TestSyncRepos_StopFailureKeepsWorktree(t *testing.T) {
	proj, root := newTestProject(t)
	wf := syncTestWorkflow(t, proj, root)
	worktree := wf.Repos["lib"].WorktreePath

	wf.SyncRepos(projectWithout(proj, "lib"), func(string) error {
		return os.ErrPermission
	})

	wr := wf.Repos["lib"]
	if wr == nil || !strings.Contains(wr.Error, "stop agents") {
		t.Fatalf("expected lib kept with a stop-agents error, got %+v", wr)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("worktree removed although agents could not be stopped: %v", err)
	}
}

func TestSyncRepos_OriginalRepoGoneDropsOrphanedWorktree(t *testing.T) {
	proj, root := newTestProject(t)
	wf := syncTestWorkflow(t, proj, root)
	lib := wf.Repos["lib"]
	if err := os.RemoveAll(lib.OriginalPath); err != nil {
		t.Fatal(err)
	}

	if !wf.SyncRepos(projectWithout(proj, "lib"), nil) {
		t.Fatal("SyncRepos reported no change")
	}
	if _, ok := wf.Repos["lib"]; ok {
		t.Fatalf("lib still tracked after its repo vanished: %+v", wf.Repos["lib"])
	}
	if _, err := os.Stat(lib.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("orphaned worktree directory still present (stat err %v)", err)
	}
}

func TestSyncRepos_WorktreeDeletedByHandIsPruned(t *testing.T) {
	proj, root := newTestProject(t)
	wf := syncTestWorkflow(t, proj, root)
	lib := wf.Repos["lib"]
	if err := os.RemoveAll(lib.WorktreePath); err != nil {
		t.Fatal(err)
	}

	wf.SyncRepos(projectWithout(proj, "lib"), nil)

	if _, ok := wf.Repos["lib"]; ok {
		t.Fatalf("lib still tracked: %+v", wf.Repos["lib"])
	}
	if git.BranchExists(lib.OriginalPath, "feature/sync") {
		t.Fatal("stale worktree bookkeeping blocked the branch delete")
	}
}

func TestSyncRepos_RetriesFailedWorktreeCreate(t *testing.T) {
	proj, root := newTestProject(t)
	wf := syncTestWorkflow(t, proj, root)
	// Simulate an earlier create that failed: entry tracked, no worktree.
	lib := wf.Repos["lib"]
	if err := git.RemoveWorktree(lib.OriginalPath, lib.WorktreePath, true); err != nil {
		t.Fatal(err)
	}
	lib.WorktreeCreated = false
	lib.Error = "create worktree: simulated"

	if !wf.SyncRepos(proj, nil) {
		t.Fatal("SyncRepos did not retry the missing worktree")
	}
	if !lib.WorktreeCreated || lib.Error != "" {
		t.Fatalf("lib after retry: %+v", lib)
	}
	if _, err := os.Stat(lib.WorktreePath); err != nil {
		t.Fatalf("worktree not recreated: %v", err)
	}
}

func TestSyncRepos_MovedRepoUpdatesPathAndRepairsWorktree(t *testing.T) {
	proj, root := newTestProject(t)
	wf := syncTestWorkflow(t, proj, root)
	lib := wf.Repos["lib"]
	moved := filepath.Join(root, "moved", "lib")
	if err := os.MkdirAll(filepath.Dir(moved), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(lib.OriginalPath, moved); err != nil {
		t.Fatal(err)
	}
	def := ProjectDef{Name: proj.Name}
	for _, r := range proj.Repos {
		path := r.Path
		if r.Name == "lib" {
			path = moved
		}
		def.Repos = append(def.Repos, RepoDef{Name: r.Name, Path: path, DefaultBranch: r.DefaultBranch})
	}

	if !wf.SyncRepos(NewProject(def), nil) {
		t.Fatal("SyncRepos reported no change after a repo moved")
	}
	if lib.OriginalPath != moved || lib.Error != "" {
		t.Fatalf("lib after move: %+v", lib)
	}
	// The worktree must still resolve to its (moved) main repo.
	out, err := exec.Command("git", "-C", lib.WorktreePath, "rev-parse", "--abbrev-ref", "HEAD").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "feature/sync" {
		t.Fatalf("worktree broken after move: %v\n%s", err, out)
	}
}
