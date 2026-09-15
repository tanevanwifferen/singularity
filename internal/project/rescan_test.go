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
