package project

import (
	"fmt"
	"path/filepath"
	"sort"
)

// RescanRepos scans dir for git repositories and reconciles p.Repos to
// match what's on disk: repos found on disk but missing from the project
// are added, repos in the project but no longer on disk are removed, and
// repos whose directory moved have their Path updated in place. Returns the
// names of repos added, removed and moved (each sorted).
//
// If dir is "", the root repo is scanned when one of the project's repos
// contains all the others (a root repo with nested subrepos); otherwise the
// parent directory of the project's first repo. Scanning the root repo's
// parent instead would stop at the root repo and drop every nested subrepo.
//
// RescanRepos only changes p in memory: it neither persists the config nor
// syncs workflows. ProjectService.UpdateRepos does both and is preferred
// whenever a daemon is available.
func (p *Project) RescanRepos(dir string) (added, removed, moved []string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if dir == "" {
		if len(p.Repos) == 0 {
			return nil, nil, nil, fmt.Errorf("no directory to scan: project has no repos and none was given")
		}
		def := ProjectDef{Name: p.Name}
		for _, r := range p.Repos {
			def.Repos = append(def.Repos, RepoDef{Name: r.Name, Path: r.Path})
		}
		if dir, err = defaultRescanDir(p.Name, def); err != nil {
			dir, err = filepath.Dir(p.Repos[0].Path), nil
		}
	}

	cfg, err := GenerateConfigFromDir(dir)
	if err != nil {
		return nil, nil, nil, err
	}
	var found []RepoDef
	for _, def := range cfg.Projects {
		found = def.Repos
		break
	}

	foundByName := make(map[string]RepoDef, len(found))
	for _, f := range found {
		foundByName[f.Name] = f
	}
	existingByName := make(map[string]*Repo, len(p.Repos))
	for _, r := range p.Repos {
		existingByName[r.Name] = r
	}

	var kept []*Repo
	for _, r := range p.Repos {
		f, ok := foundByName[r.Name]
		if !ok {
			removed = append(removed, r.Name)
			continue
		}
		if f.Path != r.Path {
			r.Path = f.Path
			moved = append(moved, r.Name)
		}
		kept = append(kept, r)
	}
	for _, f := range found {
		if _, ok := existingByName[f.Name]; !ok {
			kept = append(kept, &Repo{Name: f.Name, Path: f.Path, DefaultBranch: f.DefaultBranch})
			added = append(added, f.Name)
		}
	}

	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(moved)
	p.Repos = kept
	return added, removed, moved, nil
}
