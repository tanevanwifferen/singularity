package project

import (
	"fmt"
	"sync"

	"git-frontend/internal/git"
)

// Project represents a loaded multi-repo project with live state
type Project struct {
	Name         string
	Repos        []*Repo
	ContextFiles []string // Paths to files injected into agent prompts
	mu           sync.RWMutex
}

// Repo represents a single repository within a project
type Repo struct {
	Name          string         `json:"name"`
	Path          string         `json:"path"`
	DefaultBranch string         `json:"default_branch"`
	Info          *git.RepoInfo  `json:"info,omitempty"`
	Error         string         `json:"error,omitempty"`
}

// RepoStatus summarizes a repo's state for the dashboard
type RepoStatus struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	CurrentBranch string `json:"current_branch"`
	DefaultBranch string `json:"default_branch"`
	IsDirty       bool   `json:"is_dirty"`
	HEAD          string `json:"head"`
	BranchCount   int    `json:"branch_count"`
	Error         string `json:"error,omitempty"`
}

// ProjectStatus is the aggregate status of all repos in a project
type ProjectStatus struct {
	Name       string       `json:"name"`
	RepoCount  int          `json:"repo_count"`
	Repos      []RepoStatus `json:"repos"`
	DirtyCount int          `json:"dirty_count"`
	ErrorCount int          `json:"error_count"`
}

// BranchExistence tracks whether a branch exists across repos
type BranchExistence struct {
	Branch string            `json:"branch"`
	Repos  map[string]bool   `json:"repos"` // repo name -> exists
}

// NewProject creates a Project from a ProjectDef
func NewProject(def ProjectDef) *Project {
	repos := make([]*Repo, len(def.Repos))
	for i, rd := range def.Repos {
		repos[i] = &Repo{
			Name:          rd.Name,
			Path:          rd.Path,
			DefaultBranch: rd.DefaultBranch,
		}
	}

	return &Project{
		Name:         def.Name,
		Repos:        repos,
		ContextFiles: def.ContextFiles,
	}
}

// GetRepo returns a repo by name, or nil if not found
func (p *Project) GetRepo(name string) *Repo {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, r := range p.Repos {
		if r.Name == name {
			return r
		}
	}
	return nil
}

// RepoNames returns the names of all repos in the project
func (p *Project) RepoNames() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	names := make([]string, len(p.Repos))
	for i, r := range p.Repos {
		names[i] = r.Name
	}
	return names
}

// RepoPaths returns all repo paths in the project
func (p *Project) RepoPaths() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	paths := make([]string, len(p.Repos))
	for i, r := range p.Repos {
		paths[i] = r.Path
	}
	return paths
}

// Refresh reloads git info for all repos concurrently
func (p *Project) Refresh() {
	p.mu.Lock()
	defer p.mu.Unlock()

	var wg sync.WaitGroup
	for _, repo := range p.Repos {
		wg.Add(1)
		go func(r *Repo) {
			defer wg.Done()
			r.Refresh()
		}(repo)
	}
	wg.Wait()
}

// Status returns the aggregate status of all repos
func (p *Project) Status() *ProjectStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()

	status := &ProjectStatus{
		Name:      p.Name,
		RepoCount: len(p.Repos),
		Repos:     make([]RepoStatus, 0, len(p.Repos)),
	}

	for _, r := range p.Repos {
		rs := r.Status()
		status.Repos = append(status.Repos, rs)
		if rs.IsDirty {
			status.DirtyCount++
		}
		if rs.Error != "" {
			status.ErrorCount++
		}
	}

	return status
}

// BranchExistsAcross checks if a branch exists in all, some, or no repos
func (p *Project) BranchExistsAcross(branch string) *BranchExistence {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := &BranchExistence{
		Branch: branch,
		Repos:  make(map[string]bool),
	}

	for _, r := range p.Repos {
		if r.Info == nil {
			result.Repos[r.Name] = false
			continue
		}
		found := false
		for _, b := range r.Info.Branches {
			if b.Name == branch {
				found = true
				break
			}
		}
		result.Repos[r.Name] = found
	}

	return result
}

// ContextSummary returns a text summary of the project for Claude Code agents
func (p *Project) ContextSummary() string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	summary := fmt.Sprintf("Project: %s (%d repos)\n", p.Name, len(p.Repos))
	for _, r := range p.Repos {
		summary += fmt.Sprintf("\n--- Repo: %s ---\n", r.Name)
		summary += fmt.Sprintf("  Path: %s\n", r.Path)
		summary += fmt.Sprintf("  Default branch: %s\n", r.DefaultBranch)
		if r.Info != nil {
			summary += fmt.Sprintf("  Current branch: %s\n", r.Info.CurrentBranch)
			summary += fmt.Sprintf("  HEAD: %s\n", r.Info.HEAD)
			if r.Info.IsDirty {
				summary += "  Status: dirty (uncommitted changes)\n"
			} else {
				summary += "  Status: clean\n"
			}
			summary += fmt.Sprintf("  Branches: %d\n", len(r.Info.Branches))
		}
		if r.Error != "" {
			summary += fmt.Sprintf("  Error: %s\n", r.Error)
		}
	}

	return summary
}

// Refresh reloads git info for a single repo
func (r *Repo) Refresh() {
	info, err := git.OpenRepo(r.Path)
	if err != nil {
		r.Error = err.Error()
		r.Info = nil
		return
	}
	r.Info = info
	r.Error = ""
}

// Status returns the status of a single repo
func (r *Repo) Status() RepoStatus {
	rs := RepoStatus{
		Name:          r.Name,
		Path:          r.Path,
		DefaultBranch: r.DefaultBranch,
		Error:         r.Error,
	}

	if r.Info != nil {
		rs.CurrentBranch = r.Info.CurrentBranch
		rs.IsDirty = r.Info.IsDirty
		rs.HEAD = r.Info.HEAD
		rs.BranchCount = len(r.Info.Branches)
	}

	return rs
}
