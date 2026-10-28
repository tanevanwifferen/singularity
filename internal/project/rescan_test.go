package project

import (
	"path/filepath"
	"testing"
)

func TestRescanReposAddsRemovesAndMoves(t *testing.T) {
	root := t.TempDir()
	keptPath := filepath.Join(root, "kept")
	addedPath := filepath.Join(root, "added")
	movedOldPath := filepath.Join(root, "moved-old")
	movedNewPath := filepath.Join(root, "moved-new")

	initGitRepo(t, keptPath)
	initGitRepo(t, addedPath)
	initGitRepo(t, movedNewPath)

	p := NewProject(ProjectDef{
		Name: "test",
		Repos: []RepoDef{
			{Name: "kept", Path: keptPath, DefaultBranch: "main"},
			{Name: "removed", Path: filepath.Join(root, "removed"), DefaultBranch: "main"},
			{Name: "moved-new", Path: movedOldPath, DefaultBranch: "main"},
		},
	})

	added, removed, moved, err := p.RescanRepos(root)
	if err != nil {
		t.Fatalf("RescanRepos: %v", err)
	}

	if len(added) != 1 || added[0] != "added" {
		t.Errorf("added = %v, want [added]", added)
	}
	if len(removed) != 1 || removed[0] != "removed" {
		t.Errorf("removed = %v, want [removed]", removed)
	}
	if len(moved) != 1 || moved[0] != "moved-new" {
		t.Errorf("moved = %v, want [moved-new]", moved)
	}

	names := p.RepoNames()
	want := map[string]bool{"kept": true, "added": true, "moved-new": true}
	if len(names) != len(want) {
		t.Fatalf("RepoNames = %v, want keys of %v", names, want)
	}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected repo %q after rescan", n)
		}
	}

	moved2 := p.GetRepo("moved-new")
	if moved2 == nil || moved2.Path != movedNewPath {
		t.Errorf("moved repo path = %+v, want Path=%s", moved2, movedNewPath)
	}
}

func TestRescanReposNoChanges(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "solo")
	initGitRepo(t, repoPath)

	p := NewProject(ProjectDef{
		Name:  "test",
		Repos: []RepoDef{{Name: "solo", Path: repoPath, DefaultBranch: "main"}},
	})

	added, removed, moved, err := p.RescanRepos(root)
	if err != nil {
		t.Fatalf("RescanRepos: %v", err)
	}
	if len(added) != 0 || len(removed) != 0 || len(moved) != 0 {
		t.Errorf("expected no changes, got added=%v removed=%v moved=%v", added, removed, moved)
	}
}

func TestRescanReposEmptyDirFallsBackToFirstRepoParent(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "solo")
	initGitRepo(t, repoPath)

	p := NewProject(ProjectDef{
		Name:  "test",
		Repos: []RepoDef{{Name: "solo", Path: repoPath, DefaultBranch: "main"}},
	})

	added, removed, moved, err := p.RescanRepos("")
	if err != nil {
		t.Fatalf("RescanRepos: %v", err)
	}
	if len(added) != 0 || len(removed) != 0 || len(moved) != 0 {
		t.Errorf("expected no changes, got added=%v removed=%v moved=%v", added, removed, moved)
	}
}

// A root repo with nested subrepos (pbd-development/api-admin, …): with no
// dir given, the root repo itself must be scanned. Scanning its parent stops
// at the root repo and would drop every nested subrepo.
func TestRescanReposEmptyDirScansRootRepoWithNestedSubrepos(t *testing.T) {
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "root")
	subA := filepath.Join(rootPath, "sub-a")
	subB := filepath.Join(rootPath, "sub-b")
	subNew := filepath.Join(rootPath, "sub-new")
	for _, p := range []string{rootPath, subA, subB, subNew} {
		initGitRepo(t, p)
	}
	initGitRepo(t, filepath.Join(parent, "unrelated"))

	p := NewProject(ProjectDef{
		Name: "test",
		Repos: []RepoDef{
			{Name: "root", Path: rootPath, DefaultBranch: "main"},
			{Name: "sub-a", Path: subA, DefaultBranch: "main"},
			{Name: "sub-b", Path: subB, DefaultBranch: "main"},
		},
	})

	added, removed, moved, err := p.RescanRepos("")
	if err != nil {
		t.Fatalf("RescanRepos: %v", err)
	}
	if len(removed) != 0 || len(moved) != 0 {
		t.Errorf("removed=%v moved=%v, want none", removed, moved)
	}
	if len(added) != 1 || added[0] != "sub-new" {
		t.Errorf("added = %v, want [sub-new]", added)
	}
	if n := len(p.RepoNames()); n != 4 {
		t.Errorf("RepoNames = %v, want 4 repos", p.RepoNames())
	}
}

func TestSetReposReplacesInPlaceKeepingRepoInfo(t *testing.T) {
	p := NewProject(ProjectDef{
		Name: "test",
		Repos: []RepoDef{
			{Name: "kept", Path: "/old/kept", DefaultBranch: "main"},
			{Name: "gone", Path: "/old/gone", DefaultBranch: "main"},
		},
	})
	kept := p.GetRepo("kept")
	kept.Error = "marker"

	p.SetRepos([]RepoDef{
		{Name: "kept", Path: "/new/kept", DefaultBranch: "develop"},
		{Name: "added", Path: "/new/added", DefaultBranch: "main"},
	})

	if got := p.GetRepo("kept"); got != kept || got.Path != "/new/kept" || got.DefaultBranch != "develop" || got.Error != "marker" {
		t.Errorf("kept repo = %+v, want same *Repo with updated path/branch", got)
	}
	if p.GetRepo("gone") != nil {
		t.Error("gone repo still present")
	}
	if got := p.GetRepo("added"); got == nil || got.Path != "/new/added" {
		t.Errorf("added repo = %+v", got)
	}
}
