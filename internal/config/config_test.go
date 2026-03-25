package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Version == "" {
		t.Error("expected non-empty Version")
	}
	if cfg.Git.DefaultBranch == "" {
		t.Error("expected non-empty DefaultBranch")
	}
	if cfg.Git.FetchInterval <= 0 {
		t.Errorf("expected positive FetchInterval, got %d", cfg.Git.FetchInterval)
	}
	if cfg.AI.Provider == "" {
		t.Error("expected non-empty AI.Provider")
	}
	if cfg.AI.MaxTokens <= 0 {
		t.Errorf("expected positive AI.MaxTokens, got %d", cfg.AI.MaxTokens)
	}
	if cfg.Profiles == nil {
		t.Error("expected Profiles map to be initialised")
	}
}

func TestValidate(t *testing.T) {
	t.Run("valid default config", func(t *testing.T) {
		cfg := DefaultConfig()
		if err := cfg.Validate(); err != nil {
			t.Errorf("DefaultConfig should be valid, got: %v", err)
		}
	})

	t.Run("empty version", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Version = ""
		if err := cfg.Validate(); err == nil {
			t.Error("expected error for empty version")
		}
	})

	t.Run("negative fetch interval", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Git.FetchInterval = -1
		if err := cfg.Validate(); err == nil {
			t.Error("expected error for negative fetch interval")
		}
	})

	t.Run("temperature out of range", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.AI.Temperature = 3.0
		if err := cfg.Validate(); err == nil {
			t.Error("expected error for temperature > 2")
		}
	})

	t.Run("temperature negative", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.AI.Temperature = -0.1
		if err := cfg.Validate(); err == nil {
			t.Error("expected error for negative temperature")
		}
	})

	t.Run("zero max tokens", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.AI.MaxTokens = 0
		if err := cfg.Validate(); err == nil {
			t.Error("expected error for zero max tokens")
		}
	})

	t.Run("empty default branch", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Git.DefaultBranch = ""
		if err := cfg.Validate(); err == nil {
			t.Error("expected error for empty default branch")
		}
	})
}

func TestLoadSaveConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	original := DefaultConfig()
	original.Git.DefaultBranch = "develop"
	original.AI.Provider = "openai"

	if err := SaveConfig(path, original); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if loaded.Git.DefaultBranch != "develop" {
		t.Errorf("DefaultBranch: got %q, want %q", loaded.Git.DefaultBranch, "develop")
	}
	if loaded.AI.Provider != "openai" {
		t.Errorf("AI.Provider: got %q, want %q", loaded.AI.Provider, "openai")
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	cfg, err := LoadConfig("/nonexistent/path/config.json")
	if err != nil {
		t.Fatalf("expected defaults on missing file, got error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config for missing file")
	}
}

func TestLoadConfigJIRATokenEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := DefaultConfig()
	cfg.Jira.APIToken = "file-token"
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	t.Setenv("JIRA_API_TOKEN", "env-token")

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Jira.APIToken != "env-token" {
		t.Errorf("expected env token to override file token, got %q", loaded.Jira.APIToken)
	}
}

func TestLoadConfigJiraAutoEnable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := DefaultConfig()
	cfg.Jira.BaseURL = "https://myco.atlassian.net"
	cfg.Jira.Enabled = false
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !loaded.Jira.Enabled {
		t.Error("expected Jira to be auto-enabled when BaseURL is set")
	}
}

func TestCreateProfile(t *testing.T) {
	cfg := DefaultConfig()

	if err := cfg.CreateProfile("work", "Work profile"); err != nil {
		t.Fatalf("CreateProfile failed: %v", err)
	}
	if _, ok := cfg.Profiles["work"]; !ok {
		t.Error("expected 'work' profile to exist")
	}

	// Duplicate profile should error
	if err := cfg.CreateProfile("work", "Another"); err == nil {
		t.Error("expected error for duplicate profile name")
	}

	// Empty name should error
	if err := cfg.CreateProfile("", "No name"); err == nil {
		t.Error("expected error for empty profile name")
	}

	// Overriding 'default' should error
	if err := cfg.CreateProfile("default", "Override default"); err == nil {
		t.Error("expected error for profile named 'default'")
	}
}

func TestSwitchProfile(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Git.DefaultBranch = "main"
	_ = cfg.CreateProfile("work", "Work")

	// Modify the profile config
	cfg.Profiles["work"] = Profile{
		Name:        "work",
		Description: "Work",
		Config: Config{
			Version: "0.1.0",
			Git:     GitConfig{DefaultBranch: "master", FetchInterval: 30},
			AI:      AIConfig{Provider: "claude", MaxTokens: 1024, Temperature: 0.7},
		},
	}

	if err := cfg.SwitchProfile("work"); err != nil {
		t.Fatalf("SwitchProfile failed: %v", err)
	}
	if cfg.ActiveProfile != "work" {
		t.Errorf("ActiveProfile: got %q, want %q", cfg.ActiveProfile, "work")
	}
	if cfg.Git.DefaultBranch != "master" {
		t.Errorf("DefaultBranch: got %q, want %q", cfg.Git.DefaultBranch, "master")
	}

	// Switch to nonexistent profile should error
	if err := cfg.SwitchProfile("nonexistent"); err == nil {
		t.Error("expected error for unknown profile")
	}
}

func TestUpdateJira(t *testing.T) {
	cfg := DefaultConfig()
	cfg.UpdateJira("https://co.atlassian.net", "user@co.com", "tok", "PROJ")

	if cfg.Jira.BaseURL != "https://co.atlassian.net" {
		t.Errorf("BaseURL: got %q", cfg.Jira.BaseURL)
	}
	if !cfg.Jira.Enabled {
		t.Error("expected Jira.Enabled=true when BaseURL is set")
	}
	if cfg.Jira.DefaultProject != "PROJ" {
		t.Errorf("DefaultProject: got %q", cfg.Jira.DefaultProject)
	}

	// Clearing BaseURL should disable Jira
	cfg.UpdateJira("", "", "", "")
	if cfg.Jira.Enabled {
		t.Error("expected Jira.Enabled=false when BaseURL is empty")
	}
}

func TestMerge(t *testing.T) {
	base := DefaultConfig()
	base.Git.DefaultBranch = "main"
	base.AI.Provider = "claude"

	overrides := &Config{
		Theme: ThemeConfig{Style: "dark"},
		Git:   GitConfig{DefaultBranch: "develop"},
		AI:    AIConfig{Provider: "openai"},
	}

	base.Merge(overrides)

	if base.Theme.Style != "dark" {
		t.Errorf("Theme.Style: got %q, want dark", base.Theme.Style)
	}
	if base.Git.DefaultBranch != "develop" {
		t.Errorf("DefaultBranch: got %q, want develop", base.Git.DefaultBranch)
	}
	if base.AI.Provider != "openai" {
		t.Errorf("AI.Provider: got %q, want openai", base.AI.Provider)
	}

	// nil override is a no-op
	before := base.Theme.Style
	base.Merge(nil)
	if base.Theme.Style != before {
		t.Error("Merge(nil) should be a no-op")
	}
}

func TestGetActiveProfileName(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.GetActiveProfileName() == "" {
		t.Error("expected non-empty active profile name")
	}

	cfg.ActiveProfile = ""
	if cfg.GetActiveProfileName() != "default" {
		t.Errorf("expected 'default' when ActiveProfile is empty, got %q", cfg.GetActiveProfileName())
	}
}

func TestSaveConfigCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "config.json")

	// Parent directory doesn't exist yet — SaveConfig itself should not create dirs,
	// but the write should fail cleanly.
	cfg := DefaultConfig()
	err := SaveConfig(path, cfg)
	if err == nil {
		t.Error("expected error when parent directory doesn't exist")
	}

	// Now create the parent and retry
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig failed after creating dir: %v", err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected config file to exist after save")
	}
}
