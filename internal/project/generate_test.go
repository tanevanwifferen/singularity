package project

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initGitRepo creates a minimal git repo at dir so findGitRepos picks it up.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	cmd := exec.Command("git", "init", "-q", "-b", "main", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v: %s", dir, err, out)
	}
}

func writeConfigFile(t *testing.T, path string, cfg *ProjectConfig) {
	t.Helper()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestSyncProjectReposAddsAndRemoves(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "repos")
	keptPath := filepath.Join(repoDir, "kept")
	removedPath := filepath.Join(repoDir, "removed")
	addedPath := filepath.Join(repoDir, "added")

	initGitRepo(t, keptPath)
	initGitRepo(t, addedPath)
	// removedPath is recorded in config but no longer exists on disk / as a
	// git repo, simulating a subrepo that was deleted.

	cfgPath := filepath.Join(root, "projects.json")
	cfg := &ProjectConfig{Projects: map[string]ProjectDef{
		"proj": {
			Name: "proj",
			Repos: []RepoDef{
				{Name: "kept", Path: keptPath, DefaultBranch: "main"},
				{Name: "removed", Path: removedPath, DefaultBranch: "main"},
			},
		},
	}}
	writeConfigFile(t, cfgPath, cfg)

	res, err := SyncProjectRepos(cfgPath, "proj", repoDir)
	if err != nil {
		t.Fatalf("SyncProjectRepos: %v", err)
	}
	if len(res.Added) != 1 || res.Added[0] != "added" {
		t.Fatalf("added = %v, want [added]", res.Added)
	}
	if len(res.Removed) != 1 || res.Removed[0] != "removed" {
		t.Fatalf("removed = %v, want [removed]", res.Removed)
	}
	if res.Dir != repoDir {
		t.Fatalf("Dir = %q, want %q", res.Dir, repoDir)
	}

	got, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	def := got.Projects["proj"]
	names := make(map[string]bool, len(def.Repos))
	for _, r := range def.Repos {
		names[r.Name] = true
	}
	if !names["kept"] || !names["added"] || names["removed"] {
		t.Fatalf("repos after sync = %v, want kept+added, not removed", names)
	}
	// An explicit --dir on a project without a stored root becomes its root,
	// so the next update needs no --dir.
	if def.Root != repoDir {
		t.Fatalf("Root = %q, want explicit dir %q recorded", def.Root, repoDir)
	}
}

func TestSyncProjectReposLeavesReposOutsideDirUntouched(t *testing.T) {
	root := t.TempDir()
	scanDir := filepath.Join(root, "scan")
	elsewhere := filepath.Join(root, "elsewhere")
	kept := filepath.Join(scanDir, "kept")

	initGitRepo(t, kept)
	initGitRepo(t, elsewhere)

	cfgPath := filepath.Join(root, "projects.json")
	cfg := &ProjectConfig{Projects: map[string]ProjectDef{
		"proj": {
			Name: "proj",
			Repos: []RepoDef{
				{Name: "kept", Path: kept, DefaultBranch: "main"},
				{Name: "elsewhere", Path: elsewhere, DefaultBranch: "main"},
			},
		},
	}}
	writeConfigFile(t, cfgPath, cfg)

	res, err := SyncProjectRepos(cfgPath, "proj", scanDir)
	if err != nil {
		t.Fatalf("SyncProjectRepos: %v", err)
	}
	if res.Changed() {
		t.Fatalf("added=%v removed=%v moved=%v, want no changes (elsewhere repo is outside scanDir)", res.Added, res.Removed, res.Moved)
	}

	got, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(got.Projects["proj"].Repos) != 2 {
		t.Fatalf("repos after sync = %v, want both kept", got.Projects["proj"].Repos)
	}
}

func TestSyncProjectReposDefaultsDirToStoredRoot(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "repos")
	a := filepath.Join(repoDir, "a")
	b := filepath.Join(repoDir, "b")
	initGitRepo(t, a)
	initGitRepo(t, b)
	// A repo beside the project that must never be swept in.
	initGitRepo(t, filepath.Join(root, "unrelated"))

	cfgPath := filepath.Join(root, "projects.json")
	writeConfigFile(t, cfgPath, &ProjectConfig{Projects: map[string]ProjectDef{
		"proj": {
			Name:  "proj",
			Root:  repoDir,
			Repos: []RepoDef{{Name: "a", Path: a, DefaultBranch: "main"}},
		},
	}})

	res, err := SyncProjectRepos(cfgPath, "proj", "")
	if err != nil {
		t.Fatalf("SyncProjectRepos: %v", err)
	}
	if res.Dir != repoDir {
		t.Fatalf("scanned %q, want stored root %q", res.Dir, repoDir)
	}
	if len(res.Added) != 1 || res.Added[0] != "b" || len(res.Removed) != 0 {
		t.Fatalf("added=%v removed=%v, want added [b] only", res.Added, res.Removed)
	}
}

func TestSyncProjectReposDefaultsDirToRootRepoWithNestedSubrepos(t *testing.T) {
	root := t.TempDir()
	// Sibling of the project that a parent-directory guess would pull in.
	initGitRepo(t, filepath.Join(root, "unrelated"))
	mono := filepath.Join(root, "mono")
	initGitRepo(t, mono)
	initGitRepo(t, filepath.Join(mono, "sub1"))
	initGitRepo(t, filepath.Join(mono, "sub2"))

	cfgPath := filepath.Join(root, "projects.json")
	writeConfigFile(t, cfgPath, &ProjectConfig{Projects: map[string]ProjectDef{
		"proj": {
			Name: "proj",
			Repos: []RepoDef{
				{Name: "mono", Path: mono, DefaultBranch: "main"},
				{Name: "sub1", Path: filepath.Join(mono, "sub1"), DefaultBranch: "main"},
			},
		},
	}})

	res, err := SyncProjectRepos(cfgPath, "proj", "")
	if err != nil {
		t.Fatalf("SyncProjectRepos: %v", err)
	}
	if res.Dir != mono {
		t.Fatalf("scanned %q, want the root repo %q", res.Dir, mono)
	}
	if len(res.Added) != 1 || res.Added[0] != "sub2" {
		t.Fatalf("added = %v, want [sub2] (never the unrelated sibling)", res.Added)
	}
	got, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.Projects["proj"].Root != mono {
		t.Fatalf("Root = %q, want derived root %q stored", got.Projects["proj"].Root, mono)
	}
}

func TestSyncProjectReposRequiresDirForSiblingReposWithoutRoot(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "repos", "a")
	b := filepath.Join(root, "repos", "b")
	initGitRepo(t, a)
	initGitRepo(t, b)

	cfgPath := filepath.Join(root, "projects.json")
	writeConfigFile(t, cfgPath, &ProjectConfig{Projects: map[string]ProjectDef{
		"proj": {
			Name: "proj",
			Repos: []RepoDef{
				{Name: "a", Path: a, DefaultBranch: "main"},
				{Name: "b", Path: b, DefaultBranch: "main"},
			},
		},
	}})

	_, err := SyncProjectRepos(cfgPath, "proj", "")
	if err == nil || !strings.Contains(err.Error(), "--dir") {
		t.Fatalf("err = %v, want an error asking for --dir (sibling repos share no root repo)", err)
	}
}

func TestSyncProjectReposKeepsRepoMissedByScanButPresentOnDisk(t *testing.T) {
	root := t.TempDir()
	scanDir := filepath.Join(root, "scan")
	kept := filepath.Join(scanDir, "kept")
	unreadableParent := filepath.Join(scanDir, "locked")
	hidden := filepath.Join(unreadableParent, "hidden")
	initGitRepo(t, kept)
	initGitRepo(t, hidden)
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	if err := os.Chmod(unreadableParent, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadableParent, 0o755) })

	cfgPath := filepath.Join(root, "projects.json")
	writeConfigFile(t, cfgPath, &ProjectConfig{Projects: map[string]ProjectDef{
		"proj": {
			Name: "proj",
			Root: scanDir,
			Repos: []RepoDef{
				{Name: "kept", Path: kept, DefaultBranch: "main"},
				{Name: "hidden", Path: hidden, DefaultBranch: "main"},
			},
		},
	}})

	res, err := SyncProjectRepos(cfgPath, "proj", "")
	if err != nil {
		t.Fatalf("SyncProjectRepos: %v", err)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("removed = %v; a repo the walk could not reach must not be dropped", res.Removed)
	}
}

func TestSyncProjectReposDetectsMovedRepo(t *testing.T) {
	root := t.TempDir()
	scanDir := filepath.Join(root, "scan")
	oldPath := filepath.Join(scanDir, "old", "svc")
	newPath := filepath.Join(scanDir, "new", "svc")
	initGitRepo(t, newPath)

	cfgPath := filepath.Join(root, "projects.json")
	writeConfigFile(t, cfgPath, &ProjectConfig{Projects: map[string]ProjectDef{
		"proj": {
			Name:  "proj",
			Root:  scanDir,
			Repos: []RepoDef{{Name: "svc", Path: oldPath, DefaultBranch: "develop"}},
		},
	}})

	res, err := SyncProjectRepos(cfgPath, "proj", "")
	if err != nil {
		t.Fatalf("SyncProjectRepos: %v", err)
	}
	if len(res.Moved) != 1 || res.Moved[0] != "svc" || len(res.Added) != 0 || len(res.Removed) != 0 {
		t.Fatalf("added=%v removed=%v moved=%v, want moved [svc] only", res.Added, res.Removed, res.Moved)
	}
	got, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	r := got.Projects["proj"].Repos[0]
	if r.Path != newPath || r.DefaultBranch != "develop" {
		t.Fatalf("repo after move = %+v, want path %q with default branch preserved", r, newPath)
	}
}

func TestSyncProjectReposNoopReturnsNoErrorAndNoWrite(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "repos")
	a := filepath.Join(repoDir, "a")
	initGitRepo(t, a)

	cfgPath := filepath.Join(root, "projects.json")
	cfg := &ProjectConfig{Projects: map[string]ProjectDef{
		"proj": {
			Name:  "proj",
			Root:  repoDir,
			Repos: []RepoDef{{Name: "a", Path: a, DefaultBranch: "main"}},
		},
	}}
	writeConfigFile(t, cfgPath, cfg)
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	res, err := SyncProjectRepos(cfgPath, "proj", repoDir)
	if err != nil {
		t.Fatalf("SyncProjectRepos: %v", err)
	}
	if res.Changed() {
		t.Fatalf("added=%v removed=%v moved=%v, want none", res.Added, res.Removed, res.Moved)
	}
	after, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.ModTime().Equal(after.ModTime()) {
		t.Fatalf("config was rewritten on a no-op sync")
	}
}
