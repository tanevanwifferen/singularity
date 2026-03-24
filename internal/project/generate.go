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
			// Don't recurse into this repo.
			return filepath.SkipDir
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
