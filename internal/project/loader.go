package project

import (
	"fmt"
	"os"
	"sync"
)

// Loader manages loading and caching of projects from config
type Loader struct {
	config   *ProjectConfig
	projects map[string]*Project
	mu       sync.RWMutex
}

// NewLoader creates a new project loader from a config
func NewLoader(cfg *ProjectConfig) (*Loader, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid project config: %w", err)
	}

	return &Loader{
		config:   cfg,
		projects: make(map[string]*Project),
	}, nil
}

// NewLoaderFromFile loads a project config from file and creates a loader
func NewLoaderFromFile(path string) (*Loader, error) {
	cfg, err := LoadConfig(path)
	if err != nil {
		return nil, err
	}

	return NewLoader(cfg)
}

// LoadProject loads a project by key, refreshing all repos
func (l *Loader) LoadProject(key string) (*Project, error) {
	def, ok := l.config.Projects[key]
	if !ok {
		return nil, fmt.Errorf("project %q not found in config", key)
	}

	// Validate repo paths exist
	for _, repo := range def.Repos {
		info, err := os.Stat(repo.Path)
		if err != nil {
			return nil, fmt.Errorf("repo path %q (%s) does not exist: %w", repo.Name, repo.Path, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("repo path %q (%s) is not a directory", repo.Name, repo.Path)
		}
	}

	proj := NewProject(def)
	proj.Refresh()

	l.mu.Lock()
	l.projects[key] = proj
	l.mu.Unlock()

	return proj, nil
}

// GetProject returns a cached project, or nil if not loaded
func (l *Loader) GetProject(key string) *Project {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.projects[key]
}

// ListProjectKeys returns all available project keys from config
func (l *Loader) ListProjectKeys() []string {
	keys := make([]string, 0, len(l.config.Projects))
	for k := range l.config.Projects {
		keys = append(keys, k)
	}
	return keys
}

// ListLoadedProjects returns keys of all currently loaded projects
func (l *Loader) ListLoadedProjects() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	keys := make([]string, 0, len(l.projects))
	for k := range l.projects {
		keys = append(keys, k)
	}
	return keys
}

// RefreshProject reloads git info for all repos in a loaded project
func (l *Loader) RefreshProject(key string) error {
	l.mu.RLock()
	proj, ok := l.projects[key]
	l.mu.RUnlock()

	if !ok {
		return fmt.Errorf("project %q is not loaded", key)
	}

	proj.Refresh()
	return nil
}

// Config returns the underlying config
func (l *Loader) Config() *ProjectConfig {
	return l.config
}
