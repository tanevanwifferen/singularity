package project

import (
	"fmt"
	"path/filepath"
	"sort"
)

// RescanRepos scans dir (or, if empty, the project's scan root; see
// defaultScanDir) for git repositories and reconciles p.Repos to match
// what's on disk: repos found on disk but missing from the project are
// added, repos in the project but no longer on disk are removed, and repos
// whose directory moved have their Path updated in place. Returns the names
// of repos added, removed and moved (each sorted).
//
// A scan that would drop most of the project's repos is refused with an
// error and p.Repos is left untouched: that is what scanning the wrong
// directory looks like, and a project cut down to one repo silently breaks
// every multi-repo operation that shares it.
//
// RescanRepos only changes p in memory: it neither persists the config nor
// syncs workflows. ProjectService.UpdateRepos does both and is preferred
// whenever a daemon is available.
func (p *Project) RescanRepos(dir string) (added, removed, moved []string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if dir == "" {
		dir, err = p.defaultScanDir()
		if err != nil {
			return nil, nil, nil, err
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

	var missing int
	for _, r := range p.Repos {
		if _, ok := foundByName[r.Name]; !ok {
			missing++
		}
	}
	if missing >= 2 && missing*2 > len(p.Repos) {
		return nil, nil, nil, fmt.Errorf("refusing to rescan %s: it would remove %d of %d repos (wrong scan directory?)", dir, missing, len(p.Repos))
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

// defaultScanDir picks the directory to rescan when the caller named none.
// The configured RootDir wins. Otherwise it is the deepest directory shared
// by every repo path: that is the top repo itself when the project is a repo
// with nested repos (pbd-development), and the common parent for sibling
// repos. Scanning the parent of the first repo would miss every nested repo.
// Callers hold p.mu.
func (p *Project) defaultScanDir() (string, error) {
	if p.RootDir != "" {
		return p.RootDir, nil
	}
	ancestor := ""
	for _, r := range p.Repos {
		if r.Path == "" {
			continue
		}
		if ancestor == "" {
			ancestor = filepath.Clean(r.Path)
			continue
		}
		ancestor = commonDir(ancestor, filepath.Clean(r.Path))
	}
	if ancestor == "" {
		return "", fmt.Errorf("no directory to scan: project has no repos and none was given")
	}
	return ancestor, nil
}
