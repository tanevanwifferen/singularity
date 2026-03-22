package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "projects.json")

	cfg := &ProjectConfig{
		Projects: map[string]ProjectDef{
			"myproject": {
				Name: "My Project",
				Repos: []RepoDef{
					{Path: "/tmp/frontend", Name: "web", DefaultBranch: "main"},
					{Path: "/tmp/backend", Name: "api", DefaultBranch: "main"},
					{Path: "/tmp/shared", Name: "lib", DefaultBranch: "develop"},
				},
			},
		},
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Load it back
	loaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if len(loaded.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(loaded.Projects))
	}

	proj, ok := loaded.Projects["myproject"]
	if !ok {
		t.Fatal("project 'myproject' not found")
	}

	if proj.Name != "My Project" {
		t.Errorf("expected project name 'My Project', got %q", proj.Name)
	}

	if len(proj.Repos) != 3 {
		t.Errorf("expected 3 repos, got %d", len(proj.Repos))
	}
}

func TestLoadConfigNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/config.json")
	if err == nil {
		t.Fatal("expected error for nonexistent config")
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ProjectConfig
		wantErr bool
	}{
		{
			name:    "empty projects",
			cfg:     ProjectConfig{Projects: map[string]ProjectDef{}},
			wantErr: true,
		},
		{
			name: "no repos",
			cfg: ProjectConfig{Projects: map[string]ProjectDef{
				"test": {Name: "Test", Repos: []RepoDef{}},
			}},
			wantErr: true,
		},
		{
			name: "missing path",
			cfg: ProjectConfig{Projects: map[string]ProjectDef{
				"test": {Name: "Test", Repos: []RepoDef{
					{Name: "web", DefaultBranch: "main"},
				}},
			}},
			wantErr: true,
		},
		{
			name: "missing name",
			cfg: ProjectConfig{Projects: map[string]ProjectDef{
				"test": {Name: "Test", Repos: []RepoDef{
					{Path: "/tmp/test", DefaultBranch: "main"},
				}},
			}},
			wantErr: true,
		},
		{
			name: "missing default_branch",
			cfg: ProjectConfig{Projects: map[string]ProjectDef{
				"test": {Name: "Test", Repos: []RepoDef{
					{Path: "/tmp/test", Name: "web"},
				}},
			}},
			wantErr: true,
		},
		{
			name: "duplicate repo names",
			cfg: ProjectConfig{Projects: map[string]ProjectDef{
				"test": {Name: "Test", Repos: []RepoDef{
					{Path: "/tmp/a", Name: "web", DefaultBranch: "main"},
					{Path: "/tmp/b", Name: "web", DefaultBranch: "main"},
				}},
			}},
			wantErr: true,
		},
		{
			name: "valid config",
			cfg: ProjectConfig{Projects: map[string]ProjectDef{
				"test": {Name: "Test", Repos: []RepoDef{
					{Path: "/tmp/a", Name: "web", DefaultBranch: "main"},
					{Path: "/tmp/b", Name: "api", DefaultBranch: "main"},
				}},
			}},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSaveAndLoadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "subdir", "projects.json")

	cfg := &ProjectConfig{
		Projects: map[string]ProjectDef{
			"test": {
				Name: "Test Project",
				Repos: []RepoDef{
					{Path: "/tmp/repo1", Name: "frontend", DefaultBranch: "main"},
				},
			},
		},
	}

	if err := SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	loaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	proj := loaded.Projects["test"]
	if proj.Name != "Test Project" {
		t.Errorf("expected 'Test Project', got %q", proj.Name)
	}
	if len(proj.Repos) != 1 {
		t.Errorf("expected 1 repo, got %d", len(proj.Repos))
	}
}

func TestExpandPath(t *testing.T) {
	// Test non-tilde path
	p, err := expandPath("/absolute/path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != "/absolute/path" {
		t.Errorf("expected /absolute/path, got %s", p)
	}

	// Test tilde expansion
	p, err = expandPath("~/code/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == "~/code/test" {
		t.Error("tilde was not expanded")
	}
}

func TestNewProject(t *testing.T) {
	def := ProjectDef{
		Name: "Test",
		Repos: []RepoDef{
			{Path: "/tmp/a", Name: "web", DefaultBranch: "main"},
			{Path: "/tmp/b", Name: "api", DefaultBranch: "develop"},
		},
	}

	proj := NewProject(def)
	if proj.Name != "Test" {
		t.Errorf("expected name 'Test', got %q", proj.Name)
	}
	if len(proj.Repos) != 2 {
		t.Errorf("expected 2 repos, got %d", len(proj.Repos))
	}

	names := proj.RepoNames()
	if len(names) != 2 {
		t.Errorf("expected 2 names, got %d", len(names))
	}
}

func TestGetRepo(t *testing.T) {
	proj := NewProject(ProjectDef{
		Name: "Test",
		Repos: []RepoDef{
			{Path: "/tmp/a", Name: "web", DefaultBranch: "main"},
		},
	})

	r := proj.GetRepo("web")
	if r == nil {
		t.Fatal("expected to find repo 'web'")
	}
	if r.Path != "/tmp/a" {
		t.Errorf("expected path /tmp/a, got %s", r.Path)
	}

	r = proj.GetRepo("nonexistent")
	if r != nil {
		t.Error("expected nil for nonexistent repo")
	}
}

func TestProjectContextSummary(t *testing.T) {
	proj := NewProject(ProjectDef{
		Name: "Test",
		Repos: []RepoDef{
			{Path: "/tmp/a", Name: "web", DefaultBranch: "main"},
		},
	})

	summary := proj.ContextSummary()
	if summary == "" {
		t.Error("expected non-empty summary")
	}
}

func TestNewLoaderValidation(t *testing.T) {
	// Invalid config should fail
	_, err := NewLoader(&ProjectConfig{})
	if err == nil {
		t.Error("expected error for empty config")
	}

	// Valid config should succeed
	cfg := &ProjectConfig{
		Projects: map[string]ProjectDef{
			"test": {
				Name: "Test",
				Repos: []RepoDef{
					{Path: "/tmp/test", Name: "web", DefaultBranch: "main"},
				},
			},
		},
	}
	loader, err := NewLoader(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	keys := loader.ListProjectKeys()
	if len(keys) != 1 || keys[0] != "test" {
		t.Errorf("unexpected keys: %v", keys)
	}
}

func TestFormatProjectStatus(t *testing.T) {
	status := &ProjectStatus{
		Name:       "Test",
		RepoCount:  2,
		DirtyCount: 1,
		Repos: []RepoStatus{
			{Name: "web", CurrentBranch: "main", HEAD: "abc1234567", BranchCount: 3},
			{Name: "api", CurrentBranch: "develop", HEAD: "def5678901", IsDirty: true, BranchCount: 5},
		},
	}

	formatted := FormatProjectStatus(status)
	if formatted == "" {
		t.Error("expected non-empty formatted status")
	}
}
