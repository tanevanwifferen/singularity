package service

import "testing"

func TestNewProjectFromInfo(t *testing.T) {
	info := &ProjectInfo{
		Handle:       "proj-pbd",
		Key:          "pbd",
		Name:         "PBD Development",
		Loaded:       true,
		ContextFiles: []string{"/code/pbd/README.md"},
		Repos: []RepoSummary{
			{Name: "api", Path: "/code/pbd/api", DefaultBranch: "main", CurrentBranch: "feature", Dirty: true},
			{Name: "web", Path: "/code/pbd/web", DefaultBranch: "master"},
		},
	}

	proj := NewProjectFromInfo(info)
	if proj == nil {
		t.Fatal("NewProjectFromInfo returned nil")
	}
	if proj.Name != "PBD Development" {
		t.Errorf("Name = %q, want %q", proj.Name, "PBD Development")
	}
	if len(proj.ContextFiles) != 1 || proj.ContextFiles[0] != "/code/pbd/README.md" {
		t.Errorf("ContextFiles = %v, want the configured agent-context file", proj.ContextFiles)
	}
	if len(proj.Repos) != 2 {
		t.Fatalf("len(Repos) = %d, want 2", len(proj.Repos))
	}
	for i, want := range []RepoDef{
		{Name: "api", Path: "/code/pbd/api", DefaultBranch: "main"},
		{Name: "web", Path: "/code/pbd/web", DefaultBranch: "master"},
	} {
		got := proj.Repos[i]
		if got.Name != want.Name || got.Path != want.Path || got.DefaultBranch != want.DefaultBranch {
			t.Errorf("Repos[%d] = %+v, want %+v", i, got, want)
		}
	}
}

func TestNewProjectFromInfoNil(t *testing.T) {
	if proj := NewProjectFromInfo(nil); proj != nil {
		t.Errorf("NewProjectFromInfo(nil) = %+v, want nil", proj)
	}
}
