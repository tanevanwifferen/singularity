package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// singlConfig holds session defaults loaded before CLI flags are parsed.
// Precedence: CLI flags > env vars > .singl.json (cwd or parent) > auto-detect.
type singlConfig struct {
	Server string `json:"server"` // SINGL_SERVER
	Repo   string `json:"repo"`   // SINGL_REPO, or auto-detected from .git
	JSON   bool   `json:"json"`   // SINGL_FORMAT=json
}

// loadConfig builds defaults that main() uses as flag initial values.
// CLI flags applied afterwards always win.
func loadConfig() singlConfig {
	var cfg singlConfig

	// 1. .singl.json — walk up from cwd
	if path, ok := findLocalConfig(); ok {
		if data, err := os.ReadFile(path); err == nil {
			if uerr := json.Unmarshal(data, &cfg); uerr != nil {
				fmt.Fprintf(os.Stderr, "warning: ignoring malformed %s: %v\n", path, uerr)
			}
		}
	}

	// 2. Env vars override the config file
	if v := os.Getenv("SINGL_SERVER"); v != "" {
		cfg.Server = v
	}
	if v := os.Getenv("SINGL_REPO"); v != "" {
		cfg.Repo = v
	}
	if os.Getenv("SINGL_FORMAT") == "json" {
		cfg.JSON = true
	}

	// 3. Repo still empty — auto-detect from cwd git root
	if cfg.Repo == "" {
		cfg.Repo = detectGitRepo()
	}

	return cfg
}

// findLocalConfig walks up from cwd looking for .singl.json.
func findLocalConfig() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		candidate := filepath.Join(dir, ".singl.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// detectGitRepo walks up from cwd looking for a .git entry (directory or
// file — the latter appears in worktrees). Returns the root path or "".
func detectGitRepo() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
