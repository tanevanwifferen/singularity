package project

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GenerateConfigFromDir scans dir recursively for git repositories and returns
// a ProjectConfig with a single project containing all found repos.
// The project key and name are derived from the base name of dir.
// Output is intended to be written to stdout and piped or merged manually.
func GenerateConfigFromDir(dir string) (*ProjectConfig, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve directory: %w", err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("cannot access directory %q: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", abs)
	}

	repos, err := findGitRepos(abs)
	if err != nil {
		return nil, fmt.Errorf("scan failed: %w", err)
	}
	if len(repos) == 0 {
		return nil, fmt.Errorf("no git repositories found under %q", abs)
	}

	projectName := filepath.Base(abs)
	projectKey := slugify(projectName)

	cfg := &ProjectConfig{
		Projects: map[string]ProjectDef{
			projectKey: {
				Name:  projectName,
				Repos: repos,
				Root:  abs,
			},
		},
	}
	return cfg, nil
}

// PrintGeneratedConfig writes the generated config as indented JSON to stdout.
func PrintGeneratedConfig(cfg *ProjectConfig) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(cfg)
}

// InitProjectFromDir scans dir for git repos and merges the resulting project
// into the projects config at configPath. Creates the file if it doesn't exist.
// Returns the project key and the number of repos added.
func InitProjectFromDir(dir, configPath string) (projectKey string, repoCount int, err error) {
	generated, err := GenerateConfigFromDir(dir)
	if err != nil {
		return "", 0, err
	}

	// Load existing config or start fresh.
	existing := &ProjectConfig{Projects: map[string]ProjectDef{}}
	if data, readErr := os.ReadFile(configPath); readErr == nil {
		if jsonErr := json.Unmarshal(data, existing); jsonErr != nil {
			return "", 0, fmt.Errorf("existing config at %q is invalid JSON: %w", configPath, jsonErr)
		}
		if existing.Projects == nil {
			existing.Projects = map[string]ProjectDef{}
		}
	}

	// Merge: each key from generated goes into existing (overwrite if present).
	for key, proj := range generated.Projects {
		existing.Projects[key] = proj
		projectKey = key
		repoCount = len(proj.Repos)
	}

	if err := SaveConfig(configPath, existing); err != nil {
		return "", 0, err
	}
	return projectKey, repoCount, nil
}

// RepoSyncResult reports what SyncProjectRepos changed in a project's
// config, by repo name.
type RepoSyncResult struct {
	// Dir is the directory that was actually scanned.
	Dir     string
	Added   []string
	Removed []string
	// Moved lists repos whose folder moved under Dir but kept its name; the
	// config entry's Path was updated in place.
	Moved []string
}

// Changed reports whether the sync altered the repo set.
func (r RepoSyncResult) Changed() bool {
	return len(r.Added) > 0 || len(r.Removed) > 0 || len(r.Moved) > 0
}

// SyncProjectRepos rescans dir for git repositories and reconciles the
// result with the repos already recorded for project key in the config at
// configPath: repos newly found under dir are added, and previously
// recorded repos that were under dir but are no longer found there are
// removed (covers subrepos added or removed on disk since the project was
// set up). A recorded repo that still exists on disk as a git repo is never
// removed, even when the walk failed to reach it (unreadable parent), so a
// transient scan miss cannot drop a repo. A repo whose folder moved under
// dir but kept its name is treated as moved and has its Path updated rather
// than being removed and re-added. Repos that are still found keep their
// existing Name and DefaultBranch so manual edits survive, and repos
// recorded outside dir are left untouched regardless of what the scan
// finds.
//
// If dir is "", the project's stored Root is used. Without a stored Root the
// scan directory is derived from the repo paths only when that is
// unambiguous: the deepest common ancestor of all repo paths must itself be
// one of the repos (a root repo with nested subrepos, or a single repo).
// Anything else (sibling repos under a shared parent) would sweep in every
// unrelated repo beside them, so an explicit dir is required instead. An
// explicit dir is recorded as the project's Root when none is stored yet.
func SyncProjectRepos(configPath, key, dir string) (RepoSyncResult, error) {
	var res RepoSyncResult
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return res, err
	}
	def, ok := cfg.Projects[key]
	if !ok {
		return res, fmt.Errorf("project %q not found in config %q", key, configPath)
	}

	if dir == "" {
		dir, err = defaultRescanDir(key, def)
		if err != nil {
			return res, err
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return res, fmt.Errorf("cannot resolve directory: %w", err)
	}
	if info, statErr := os.Stat(abs); statErr != nil || !info.IsDir() {
		return res, fmt.Errorf("%q is not a directory", abs)
	}
	res.Dir = abs

	found, err := findGitRepos(abs)
	if err != nil {
		return res, fmt.Errorf("scan failed: %w", err)
	}
	foundByPath := make(map[string]RepoDef, len(found))
	foundByName := make(map[string][]RepoDef)
	for _, r := range found {
		foundByPath[r.Path] = r
		foundByName[r.Name] = append(foundByName[r.Name], r)
	}

	existingPaths := make(map[string]bool, len(def.Repos))
	claimed := make(map[string]bool, len(def.Repos)) // found paths accounted for
	for _, r := range def.Repos {
		existingPaths[r.Path] = true
	}

	kept := make([]RepoDef, 0, len(def.Repos))
	for _, r := range def.Repos {
		switch {
		case !isUnderDir(r.Path, abs):
			// Outside the scanned tree: not our business.
			kept = append(kept, r)
		case foundByPath[r.Path].Path != "":
			claimed[r.Path] = true
			kept = append(kept, r)
		case !definitelyGone(r.Path):
			// Still there (or unknowable: unreadable parent, skipped by the
			// walk with a warning). Never treat a scan miss as removal.
			kept = append(kept, r)
		default:
			// Gone from its recorded path. Same folder name found elsewhere
			// under dir, and not already a configured repo? It moved.
			moved := false
			for _, cand := range foundByName[r.Name] {
				if existingPaths[cand.Path] || claimed[cand.Path] {
					continue
				}
				claimed[cand.Path] = true
				r.Path = cand.Path
				kept = append(kept, r)
				res.Moved = append(res.Moved, r.Name)
				moved = true
				break
			}
			if !moved {
				res.Removed = append(res.Removed, r.Name)
			}
		}
	}
	for _, r := range found {
		if existingPaths[r.Path] || claimed[r.Path] {
			continue
		}
		kept = append(kept, r)
		res.Added = append(res.Added, r.Name)
	}

	dirty := res.Changed()
	if def.Root == "" {
		def.Root = abs
		dirty = true
	}
	if !dirty {
		return res, nil
	}

	def.Repos = kept
	cfg.Projects[key] = def
	if err := cfg.Validate(); err != nil {
		return res, err
	}
	if err := SaveConfig(configPath, cfg); err != nil {
		return res, err
	}
	return res, nil
}

// defaultRescanDir picks the directory to rescan for a project when the
// caller didn't name one. See SyncProjectRepos for the rules.
func defaultRescanDir(key string, def ProjectDef) (string, error) {
	if def.Root != "" {
		return def.Root, nil
	}
	if len(def.Repos) == 0 {
		return "", fmt.Errorf("project %q has no repos to derive a rescan directory from; pass --dir", key)
	}
	ancestor := filepath.Clean(def.Repos[0].Path)
	for _, r := range def.Repos[1:] {
		ancestor = commonPrefixDir(ancestor, r.Path)
	}
	for _, r := range def.Repos {
		if filepath.Clean(r.Path) == ancestor {
			return ancestor, nil
		}
	}
	return "", fmt.Errorf("project %q has no stored root and its repos share no root repo (common parent %q would pull in unrelated repos); pass --dir", key, ancestor)
}

// isGitRepo reports whether path has a .git entry (dir or worktree file).
func isGitRepo(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

// definitelyGone reports whether path is known not to be a git repo any
// more: its .git entry does not exist. A stat that fails for any other
// reason (permissions, I/O) is inconclusive and reported as not gone, so a
// rescan errs on the side of keeping a configured repo.
func definitelyGone(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err != nil && os.IsNotExist(err)
}

// isUnderDir reports whether path is dir itself or lies within it.
func isUnderDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// commonPrefixDir returns the deepest directory common to a and b.
func commonPrefixDir(a, b string) string {
	sep := string(filepath.Separator)
	aParts := strings.Split(filepath.Clean(a), sep)
	bParts := strings.Split(filepath.Clean(b), sep)
	n := len(aParts)
	if len(bParts) < n {
		n = len(bParts)
	}
	i := 0
	for i < n && aParts[i] == bParts[i] {
		i++
	}
	if i <= 1 {
		return sep
	}
	return strings.Join(aParts[:i], sep)
}

// findGitRepos walks root and collects RepoDefs for every directory that
// contains a .git entry. It does not descend into git repos themselves.
func findGitRepos(root string) ([]RepoDef, error) {
	var repos []RepoDef

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Skip directories we can't read.
			fmt.Fprintf(os.Stderr, "warning: skipping %q: %v\n", path, err)
			return filepath.SkipDir
		}

		if !d.IsDir() {
			return nil
		}

		// Check for .git (file or directory — covers git worktrees too)
		gitPath := filepath.Join(path, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			branch := detectDefaultBranch(path)
			repos = append(repos, RepoDef{
				Path:          path,
				Name:          filepath.Base(path),
				DefaultBranch: branch,
			})
			// Don't recurse into this repo's own contents, but if this is the
			// root itself, keep walking so nested sub-repos are still found.
			if path != root {
				return filepath.SkipDir
			}
		}

		return nil
	})

	return repos, err
}

// detectDefaultBranch tries several heuristics to determine the default branch.
// Falls back to "main" if nothing is conclusive.
func detectDefaultBranch(repoPath string) string {
	// 1. Ask git for the remote HEAD symbolic ref.
	if branch := gitSymbolicRef(repoPath, "refs/remotes/origin/HEAD"); branch != "" {
		// e.g. "refs/remotes/origin/main" → "main"
		parts := strings.Split(branch, "/")
		return parts[len(parts)-1]
	}

	// 2. Check local HEAD (works on freshly cloned or init'd repos).
	if branch := gitSymbolicRef(repoPath, "HEAD"); branch != "" {
		// e.g. "refs/heads/main" → "main"
		return strings.TrimPrefix(branch, "refs/heads/")
	}

	// 3. Look for common branch names in local refs.
	for _, candidate := range []string{"main", "master", "develop", "trunk"} {
		ref := filepath.Join(repoPath, ".git", "refs", "heads", candidate)
		if _, err := os.Stat(ref); err == nil {
			return candidate
		}
	}

	return "main"
}

// gitSymbolicRef runs `git symbolic-ref <ref>` in repoPath and returns the
// trimmed output, or "" on error.
func gitSymbolicRef(repoPath, ref string) string {
	cmd := exec.Command("git", "-C", repoPath, "symbolic-ref", ref)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// slugify converts a string into a safe project-key (lowercase, hyphens).
func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
