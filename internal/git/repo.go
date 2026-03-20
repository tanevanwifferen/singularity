package git

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RepoInfo holds information about a git repository
type RepoInfo struct {
	Path          string        `json:"path"`
	IsBare        bool          `json:"is_bare"`
	CurrentBranch string        `json:"current_branch"`
	HEAD          string        `json:"head"`
	Remotes       []RemoteInfo  `json:"remotes"`
	Branches      []BranchInfo  `json:"branches"`
	IsDirty       bool          `json:"is_dirty"`
}

// RemoteInfo holds information about a git remote
type RemoteInfo struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Fetch string `json:"fetch"`
	Push  string `json:"push"`
}

// BranchInfo holds information about a git branch
type BranchInfo struct {
	Name     string `json:"name"`
	Commit   string `json:"commit"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	Upstream string `json:"upstream"`
	IsLocal  bool   `json:"is_local"`
}

// OpenRepo opens and loads a git repository
func OpenRepo(path string) (*RepoInfo, error) {
	// Check if path is a git repo
	gitDir := filepath.Join(path, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("not a git repository: %s", path)
	}

	repo := &RepoInfo{
		Path: path,
	}

	// Check if bare repo
	if _, err := os.Stat(filepath.Join(gitDir, "config")); err == nil {
		cmd := exec.Command("git", "-C", path, "rev-parse", "--is-bare-repository")
		output, _ := cmd.Output()
		repo.IsBare = strings.TrimSpace(string(output)) == "true"
	}

	// Get current branch
	if branch, err := getCurrentBranch(path); err == nil {
		repo.CurrentBranch = branch
	}

	// Get HEAD commit
	if head, err := getHEAD(path); err == nil {
		repo.HEAD = head
	}

	// Get remotes
	if remotes, err := getRemotes(path); err == nil {
		repo.Remotes = remotes
	}

	// Get branches
	if branches, err := getBranches(path); err == nil {
		repo.Branches = branches
	}

	// Check if dirty (has uncommitted changes)
	if dirty, err := isDirty(path); err == nil {
		repo.IsDirty = dirty
	}

	return repo, nil
}

// getCurrentBranch returns the current branch name
func getCurrentBranch(path string) (string, error) {
	cmd := exec.Command("git", "-C", path, "symbolic-ref", "--short", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// getHEAD returns the current HEAD commit SHA
func getHEAD(path string) (string, error) {
	cmd := exec.Command("git", "-C", path, "rev-parse", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// getRemotes returns list of remotes
func getRemotes(path string) ([]RemoteInfo, error) {
	var remotes []RemoteInfo

	// Get remote names
	cmd := exec.Command("git", "-C", path, "remote", "-v")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	remotesMap := make(map[string]*RemoteInfo)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}

		name := parts[0]
		url := parts[1]
		kind := strings.Trim(parts[2], "()")

		if _, exists := remotesMap[name]; !exists {
			remotesMap[name] = &RemoteInfo{Name: name}
		}

		if kind == "fetch" {
			remotesMap[name].URL = url
		} else if kind == "push" {
			remotesMap[name].Push = url
		}
	}

	for _, remote := range remotesMap {
		remotes = append(remotes, *remote)
	}

	return remotes, nil
}

// getBranches returns list of local branches with upstream info
func getBranches(path string) ([]BranchInfo, error) {
	var branches []BranchInfo

	// Get all local branches with upstream info
	cmd := exec.Command("git", "-C", path, "for-each-ref",
		"--format=%(refname:short) %(objectname) %(upstream:short)",
		"refs/heads/")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		parts := strings.Split(line, " ")
		if len(parts) < 2 {
			continue
		}

		branch := BranchInfo{
			Name:    parts[0],
			Commit:  parts[1],
			IsLocal: true,
		}

		if len(parts) >= 3 && parts[2] != "" {
			branch.Upstream = parts[2]
			// Calculate ahead/behind
			if ahead, behind, err := getAheadBehind(path, branch.Name, branch.Upstream); err == nil {
				branch.Ahead = ahead
				branch.Behind = behind
			}
		}

		branches = append(branches, branch)
	}

	return branches, nil
}

// getAheadBehind calculates ahead/behind counts for a branch relative to upstream
func getAheadBehind(path, branch, upstream string) (int, int, error) {
	aheadCmd := exec.Command("git", "-C", path, "rev-list", "--count", fmt.Sprintf("%s..%s", upstream, branch))
	aheadOutput, err := aheadCmd.Output()
	if err != nil {
		return 0, 0, err
	}
	ahead := 0
	fmt.Sscanf(strings.TrimSpace(string(aheadOutput)), "%d", &ahead)

	behindCmd := exec.Command("git", "-C", path, "rev-list", "--count", fmt.Sprintf("%s..%s", branch, upstream))
	behindOutput, err := behindCmd.Output()
	if err != nil {
		return 0, 0, err
	}
	behind := 0
	fmt.Sscanf(strings.TrimSpace(string(behindOutput)), "%d", &behind)

	return ahead, behind, nil
}

// isDirty checks if the repo has uncommitted changes
func isDirty(path string) (bool, error) {
	cmd := exec.Command("git", "-C", path, "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return len(strings.TrimSpace(string(output))) > 0, nil
}

// FindRepo searches for a git repository starting from path and going up
func FindRepo(path string) (string, error) {
	for {
		gitDir := filepath.Join(path, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			return path, nil
		}

		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		path = parent
	}
	return "", fmt.Errorf("no git repository found")
}
