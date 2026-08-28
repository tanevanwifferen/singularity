package project

import (
	"fmt"
	"log"
	"os"
	"sort"
	"sync"
	"time"
)

// Loader manages loading and caching of projects from config
type Loader struct {
	config   *ProjectConfig
	projects map[string]*Project
	mu       sync.RWMutex

	// path is the config file this loader was built from ("" when the
	// config was supplied directly). When set, the loader re-reads the
	// file whenever it changed on disk — the daemon is long-lived and
	// users edit projects.json by hand, so a project added after the
	// daemon started must show up without a restart.
	path    string
	modTime time.Time
	size    int64
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
// that watches that file for edits (see Loader.path).
func NewLoaderFromFile(path string) (*Loader, error) {
	cfg, err := LoadConfig(path)
	if err != nil {
		return nil, err
	}

	l, err := NewLoader(cfg)
	if err != nil {
		return nil, err
	}
	l.path = path
	if info, serr := os.Stat(path); serr == nil {
		l.modTime, l.size = info.ModTime(), info.Size()
	}
	return l, nil
}

// reloadIfChanged re-reads the config file when its mtime or size moved
// since the last read. A malformed or invalid edit is reported but does not
// disturb the config already in memory, so a half-saved file can't take the
// daemon's project list away.
func (l *Loader) reloadIfChanged() error {
	l.mu.RLock()
	path := l.path
	l.mu.RUnlock()
	if path == "" {
		return nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat project config: %w", err)
	}

	l.mu.RLock()
	unchanged := info.ModTime().Equal(l.modTime) && info.Size() == l.size
	l.mu.RUnlock()
	if unchanged {
		return nil
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid project config: %w", err)
	}

	l.mu.Lock()
	l.config = cfg
	l.modTime, l.size = info.ModTime(), info.Size()
	// Drop cached projects whose definition is gone; the rest stay loaded
	// so open handles keep working.
	for key := range l.projects {
		if _, ok := cfg.Projects[key]; !ok {
			delete(l.projects, key)
		}
	}
	l.mu.Unlock()
	return nil
}

// LoadProject loads a project by key, refreshing all repos
func (l *Loader) LoadProject(key string) (*Project, error) {
	if err := l.reloadIfChanged(); err != nil {
		log.Printf("project config reload failed, using last good config: %v", err)
	}

	l.mu.RLock()
	def, ok := l.config.Projects[key]
	l.mu.RUnlock()
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

// ListProjectKeys returns all available project keys from config, sorted.
// Callers (the TUI's project cycling) rely on a stable order across calls,
// which raw map iteration does not provide.
//
// Picks up edits to the config file made since the daemon started.
func (l *Loader) ListProjectKeys() []string {
	if err := l.reloadIfChanged(); err != nil {
		log.Printf("project config reload failed, using last good config: %v", err)
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	keys := make([]string, 0, len(l.config.Projects))
	for k := range l.config.Projects {
		keys = append(keys, k)
	}
	sort.Strings(keys)
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
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.config
}
