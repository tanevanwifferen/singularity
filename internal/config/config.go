package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// Config holds the application configuration
type Config struct {
	Version     string            `json:"version"`
	Theme       ThemeConfig       `json:"theme"`
	Git         GitConfig         `json:"git"`
	Forge       ForgeConfig       `json:"forge"`
	AI          AIConfig          `json:"ai"`
	Jira        JiraConfig        `json:"jira"`
	Profiles    map[string]Profile `json:"profiles"`
	ActiveProfile string          `json:"active_profile"`
}

// JiraConfig holds Jira integration settings
type JiraConfig struct {
	Enabled        bool   `json:"enabled"`
	BaseURL        string `json:"base_url"`        // e.g. "https://yourcompany.atlassian.net"
	Email          string `json:"email"`           // Jira Cloud email
	APIToken       string `json:"api_token"`       // Jira Cloud API token (or PAT for Server)
	DefaultProject string `json:"default_project"` // default project key, e.g. "PROJ"
}

// ThemeConfig holds UI theme settings
type ThemeConfig struct {
	Style       string `json:"style"`        // "default", "dark", "light"
	AccentColor string `json:"accent_color"`  // ANSI color name
}

// GitConfig holds git-related settings
type GitConfig struct {
	DefaultBranch    string `json:"default_branch"`    // default branch name
	AutoFetch        bool   `json:"auto_fetch"`        // auto-fetch remote info
	FetchInterval    int    `json:"fetch_interval"`     // seconds between fetches
	MaxBranchDepth   int    `json:"max_branch_depth"`   // max branches to show
	ShowRemoteBranches bool `json:"show_remote_branches"` // show remote branches
}

// ForgeConfig holds forge (GitHub/GitLab) settings
type ForgeConfig struct {
	DefaultHost   string   `json:"default_host"`   // "github.com", "gitlab.com"
	APIURL        string   `json:"api_url"`        // custom API URL
	Token         string   `json:"token"`         // stored auth token
	Organizations  []string `json:"organizations"` // default orgs to scope to
	AutoAssignMe  bool     `json:"auto_assign_me"` // auto-assign self to MRs
}

// AIConfig holds AI-related settings
type AIConfig struct {
	Provider     string `json:"provider"`     // "claude", "openai", "local"
	Model        string `json:"model"`       // model name
	CommitStyle  string `json:"commit_style"` // "conventional", "verbose", "minimal"
	MaxTokens    int    `json:"max_tokens"`   // max response tokens
	Temperature  float64 `json:"temperature"` // generation temperature
}

// Profile holds a named configuration profile
type Profile struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Config      Config     `json:"config"`
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	return &Config{
		Version:     "0.1.0",
		ActiveProfile: "default",
		Theme: ThemeConfig{
			Style:       "default",
			AccentColor: "220",
		},
		Git: GitConfig{
			DefaultBranch:    "main",
			AutoFetch:        true,
			FetchInterval:    60,
			MaxBranchDepth:   50,
			ShowRemoteBranches: true,
		},
		Forge: ForgeConfig{
			DefaultHost:   "github.com",
			AutoAssignMe:  true,
		},
		AI: AIConfig{
			Provider:     "claude",
			CommitStyle:  "conventional",
			MaxTokens:    1024,
			Temperature:  0.7,
		},
		Profiles: map[string]Profile{},
	}
}

// LoadConfig loads configuration from file
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// JIRA_API_TOKEN env var overrides config file value
	if token := os.Getenv("JIRA_API_TOKEN"); token != "" {
		config.Jira.APIToken = token
	}
	// Enable Jira automatically when BaseURL is set
	if config.Jira.BaseURL != "" {
		config.Jira.Enabled = true
	}

	return &config, nil
}

// SaveConfig saves configuration to file
func SaveConfig(path string, config *Config) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// GetConfigPath returns the default configuration path
func GetConfigPath() string {
	usr, err := user.Current()
	if err != nil {
		return ".singularity.json"
	}

	// Use XDG_CONFIG_HOME if set
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "singularity", "config.json")
	}

	return filepath.Join(usr.HomeDir, ".config", "singularity", "config.json")
}

// LoadDefaultConfig loads the default configuration
func LoadDefaultConfig() (*Config, error) {
	path := GetConfigPath()
	return LoadConfig(path)
}

// SaveDefaultConfig saves the default configuration
func SaveDefaultConfig(config *Config) error {
	path := GetConfigPath()

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	return SaveConfig(path, config)
}

// CreateProfile creates a new configuration profile
func (c *Config) CreateProfile(name, description string) error {
	if name == "" {
		return fmt.Errorf("profile name cannot be empty")
	}
	if name == "default" {
		return fmt.Errorf("cannot override default profile")
	}
	if _, exists := c.Profiles[name]; exists {
		return fmt.Errorf("profile %s already exists", name)
	}

	c.Profiles[name] = Profile{
		Name:        name,
		Description: description,
		Config:      *c, // Copy current config
	}

	return nil
}

// SwitchProfile switches to a different profile
func (c *Config) SwitchProfile(name string) error {
	profile, exists := c.Profiles[name]
	if !exists {
		return fmt.Errorf("profile %s not found", name)
	}

	// Copy profile config into main config
	*c = profile.Config
	c.ActiveProfile = name

	return nil
}

// UpdateTheme updates theme settings
func (c *Config) UpdateTheme(style, accentColor string) {
	c.Theme.Style = style
	if accentColor != "" {
		c.Theme.AccentColor = accentColor
	}
}

// UpdateGit updates git settings
func (c *Config) UpdateGit(defaultBranch string, autoFetch bool, fetchInterval int) {
	c.Git.DefaultBranch = defaultBranch
	c.Git.AutoFetch = autoFetch
	if fetchInterval > 0 {
		c.Git.FetchInterval = fetchInterval
	}
}

// UpdateJira updates Jira settings
func (c *Config) UpdateJira(baseURL, email, apiToken, defaultProject string) {
	c.Jira.BaseURL = baseURL
	c.Jira.Email = email
	if apiToken != "" {
		c.Jira.APIToken = apiToken
	}
	c.Jira.DefaultProject = defaultProject
	c.Jira.Enabled = baseURL != ""
}

// UpdateAI updates AI settings
func (c *Config) UpdateAI(provider, model, commitStyle string) {
	if provider != "" {
		c.AI.Provider = provider
	}
	if model != "" {
		c.AI.Model = model
	}
	if commitStyle != "" {
		c.AI.CommitStyle = commitStyle
	}
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.Version == "" {
		return fmt.Errorf("version cannot be empty")
	}
	if c.Git.FetchInterval < 0 {
		return fmt.Errorf("fetch interval cannot be negative")
	}
	if c.AI.Temperature < 0 || c.AI.Temperature > 2 {
		return fmt.Errorf("temperature must be between 0 and 2")
	}
	if c.AI.MaxTokens < 1 {
		return fmt.Errorf("max tokens must be positive")
	}
	if c.Git.DefaultBranch == "" {
		return fmt.Errorf("default branch cannot be empty")
	}
	return nil
}

// Merge merges another config into this one (for overrides)
func (c *Config) Merge(overrides *Config) {
	if overrides == nil {
		return
	}

	// Theme overrides
	if overrides.Theme.Style != "" {
		c.Theme.Style = overrides.Theme.Style
	}
	if overrides.Theme.AccentColor != "" {
		c.Theme.AccentColor = overrides.Theme.AccentColor
	}

	// Git overrides
	if overrides.Git.DefaultBranch != "" {
		c.Git.DefaultBranch = overrides.Git.DefaultBranch
	}
	c.Git.AutoFetch = overrides.Git.AutoFetch
	if overrides.Git.FetchInterval > 0 {
		c.Git.FetchInterval = overrides.Git.FetchInterval
	}

	// AI overrides
	if overrides.AI.Provider != "" {
		c.AI.Provider = overrides.AI.Provider
	}
	if overrides.AI.Model != "" {
		c.AI.Model = overrides.AI.Model
	}
}

// GetActiveProfileName returns the name of the active profile
func (c *Config) GetActiveProfileName() string {
	if c.ActiveProfile == "" {
		return "default"
	}
	return c.ActiveProfile
}

// String returns a human-readable representation of the config
func (c *Config) String() string {
	var parts []string

	parts = append(parts, fmt.Sprintf("Version: %s", c.Version))
	parts = append(parts, fmt.Sprintf("Theme: %s (accent: %s)", c.Theme.Style, c.Theme.AccentColor))
	parts = append(parts, fmt.Sprintf("Default Branch: %s", c.Git.DefaultBranch))
	parts = append(parts, fmt.Sprintf("Auto Fetch: %v", c.Git.AutoFetch))
	parts = append(parts, fmt.Sprintf("AI Provider: %s (%s)", c.AI.Provider, c.AI.CommitStyle))
	parts = append(parts, fmt.Sprintf("Profile: %s", c.GetActiveProfileName()))

	return strings.Join(parts, "\n")
}
