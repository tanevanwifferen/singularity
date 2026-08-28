package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeProjectsConfig writes a projects.json holding one repo per named
// project, all pointing at repoDir, and bumps the mtime so a subsequent
// reloadIfChanged sees the edit even on coarse-grained filesystems.
func writeProjectsConfig(t *testing.T, path, repoDir string, keys ...string) {
	t.Helper()

	var entries []string
	for _, k := range keys {
		entries = append(entries, `"`+k+`": {
			"name": "`+k+`",
			"repos": [{"name": "repo", "path": "`+repoDir+`", "default_branch": "main"}]
		}`)
	}
	body := `{"projects": {` + strings.Join(entries, ",") + `}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	future := time.Now().Add(time.Duration(len(keys)+1) * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

func TestListProjectKeysPicksUpConfigEdits(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "projects.json")
	writeProjectsConfig(t, cfgPath, dir, "alpha")

	l, err := NewLoaderFromFile(cfgPath)
	if err != nil {
		t.Fatalf("NewLoaderFromFile: %v", err)
	}
	if got := l.ListProjectKeys(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("initial keys = %v, want [alpha]", got)
	}

	// A project added after the loader was built must show up without a
	// daemon restart — this is what forced the hot reload.
	writeProjectsConfig(t, cfgPath, dir, "alpha", "beta")
	got := l.ListProjectKeys()
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("keys after edit = %v, want [alpha beta]", got)
	}
}

func TestLoadProjectPicksUpConfigEdits(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "projects.json")
	writeProjectsConfig(t, cfgPath, dir, "alpha")

	l, err := NewLoaderFromFile(cfgPath)
	if err != nil {
		t.Fatalf("NewLoaderFromFile: %v", err)
	}
	if _, err := l.LoadProject("beta"); err == nil {
		t.Fatal("expected beta to be unknown before the edit")
	}

	writeProjectsConfig(t, cfgPath, dir, "alpha", "beta")
	if _, err := l.LoadProject("beta"); err != nil {
		t.Fatalf("LoadProject(beta) after edit: %v", err)
	}
}

func TestReloadDropsRemovedProjects(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "projects.json")
	writeProjectsConfig(t, cfgPath, dir, "alpha", "beta")

	l, err := NewLoaderFromFile(cfgPath)
	if err != nil {
		t.Fatalf("NewLoaderFromFile: %v", err)
	}
	if _, err := l.LoadProject("beta"); err != nil {
		t.Fatalf("LoadProject(beta): %v", err)
	}

	writeProjectsConfig(t, cfgPath, dir, "alpha")
	if got := l.ListProjectKeys(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("keys after removal = %v, want [alpha]", got)
	}
	if p := l.GetProject("beta"); p != nil {
		t.Error("removed project should be evicted from the loaded cache")
	}
}

func TestReloadKeepsLastGoodConfigOnBadEdit(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "projects.json")
	writeProjectsConfig(t, cfgPath, dir, "alpha")

	l, err := NewLoaderFromFile(cfgPath)
	if err != nil {
		t.Fatalf("NewLoaderFromFile: %v", err)
	}

	// A half-written / malformed file must not take the project list away.
	if err := os.WriteFile(cfgPath, []byte(`{"projects": {`), 0o644); err != nil {
		t.Fatalf("write bad config: %v", err)
	}
	future := time.Now().Add(5 * time.Second)
	if err := os.Chtimes(cfgPath, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if got := l.ListProjectKeys(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("keys after bad edit = %v, want the last good [alpha]", got)
	}
}

func TestNewLoaderWithoutPathDoesNotReload(t *testing.T) {
	cfg := &ProjectConfig{Projects: map[string]ProjectDef{
		"alpha": {Name: "alpha", Repos: []RepoDef{{Name: "repo", Path: t.TempDir(), DefaultBranch: "main"}}},
	}}
	l, err := NewLoader(cfg)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	if got := l.ListProjectKeys(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("keys = %v, want [alpha]", got)
	}
}
