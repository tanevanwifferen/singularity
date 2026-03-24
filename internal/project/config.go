package project

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// ProjectConfig is the top-level config file structure
type ProjectConfig struct {
	Projects map[string]ProjectDef `json:"projects"`
}

// ProjectDef defines a project with multiple repos
type ProjectDef struct {
	Name         string    `json:"name"`
	Repos        []RepoDef `json:"repos"`
	ContextFiles []string  `json:"context_files,omitempty"`
}

// RepoDef defines a single repo within a project
type RepoDef struct {
	Path          string `json:"path"`
	Name          string `json:"name"`
	DefaultBranch string `json:"default_branch"`
}

// LoadConfig loads a project config from a JSON or YAML file.
// Supports .json files. YAML support can be added when gopkg.in/yaml.v3 is available.
func LoadConfig(path string) (*ProjectConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read project config: %w", err)
	}

	var cfg ProjectConfig

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse project config: %w", err)
		}
	case ".yaml", ".yml":
		// Try JSON-compatible subset (YAML is a superset of JSON)
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse project config (YAML requires gopkg.in/yaml.v3): %w", err)
		}
	default:
		// Try JSON by default
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse project config: %w", err)
		}
	}

	// Expand ~ in repo paths and context file paths
	for projName, proj := range cfg.Projects {
		for i, repo := range proj.Repos {
			expanded, err := expandPath(repo.Path)
			if err != nil {
				return nil, fmt.Errorf("failed to expand path for repo %s in project %s: %w", repo.Name, projName, err)
			}
			cfg.Projects[projName].Repos[i].Path = expanded
		}
		for i, cf := range proj.ContextFiles {
			expanded, err := expandPath(cf)
			if err != nil {
				return nil, fmt.Errorf("failed to expand context file path %q in project %s: %w", cf, projName, err)
			}
			cfg.Projects[projName].ContextFiles[i] = expanded
		}
	}

	return &cfg, nil
}

// SaveConfig saves a project config to a JSON file
func SaveConfig(path string, cfg *ProjectConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal project config: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write project config: %w", err)
	}

	return nil
}

// GetDefaultConfigPath returns the default project config path
func GetDefaultConfigPath() string {
	usr, err := user.Current()
	if err != nil {
		return ".singularity-projects.json"
	}

	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "singularity", "projects.json")
	}

	return filepath.Join(usr.HomeDir, ".config", "singularity", "projects.json")
}

// Validate checks that the config is well-formed
func (c *ProjectConfig) Validate() error {
	if len(c.Projects) == 0 {
		return fmt.Errorf("no projects defined")
	}

	for key, proj := range c.Projects {
		if proj.Name == "" {
			return fmt.Errorf("project %q has no name", key)
		}
		if len(proj.Repos) == 0 {
			return fmt.Errorf("project %q has no repos", key)
		}

		names := make(map[string]bool)
		for _, repo := range proj.Repos {
			if repo.Path == "" {
				return fmt.Errorf("repo %q in project %q has no path", repo.Name, key)
			}
			if repo.Name == "" {
				return fmt.Errorf("repo in project %q has no name (path: %s)", key, repo.Path)
			}
			if names[repo.Name] {
				return fmt.Errorf("duplicate repo name %q in project %q", repo.Name, key)
			}
			names[repo.Name] = true
			if repo.DefaultBranch == "" {
				return fmt.Errorf("repo %q in project %q has no default_branch", repo.Name, key)
			}
		}
	}

	return nil
}

// expandPath expands ~ to the user's home directory
func expandPath(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}

	usr, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("failed to get current user: %w", err)
	}

	if path == "~" {
		return usr.HomeDir, nil
	}

	if strings.HasPrefix(path, "~/") {
		return filepath.Join(usr.HomeDir, path[2:]), nil
	}

	return path, nil
}
