package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// UpstreamStatus holds the sync status of the current branch relative to its upstream.
type UpstreamStatus struct {
	Branch   string `json:"branch"`
	Upstream string `json:"upstream"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	IsDirty  bool   `json:"is_dirty"`
}

// GetUpstreamStatus returns the current branch's relationship to its upstream.
func GetUpstreamStatus(repoPath string) (*UpstreamStatus, error) {
	branch, err := getCurrentBranch(repoPath)
	if err != nil {
		return nil, fmt.Errorf("not on a branch: %w", err)
	}

	status := &UpstreamStatus{Branch: branch}

	// Get upstream
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--abbrev-ref", branch+"@{upstream}")
	output, err := cmd.Output()
	if err != nil {
		// No upstream configured
		return status, nil
	}
	status.Upstream = strings.TrimSpace(string(output))

	// Get ahead/behind
	if status.Upstream != "" {
		ahead, behind, err := getAheadBehind(repoPath, branch, status.Upstream)
		if err == nil {
			status.Ahead = ahead
			status.Behind = behind
		}
	}

	// Check dirty
	dirty, err := isDirty(repoPath)
	if err == nil {
		status.IsDirty = dirty
	}

	return status, nil
}

// Fetch runs git fetch for the given remote (or all remotes if remote is empty).
func Fetch(repoPath string, remote string) (string, error) {
	args := []string{"-C", repoPath, "fetch", "--prune"}
	if remote != "" {
		args = append(args, remote)
	} else {
		args = append(args, "--all")
	}

	cmd := exec.Command("git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("fetch failed: %s", strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

// Pull runs git pull on the current branch.
func Pull(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "pull")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("pull failed: %s", strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

// Push runs git push on the current branch.
// When the local branch name differs from the upstream branch name (common in
// worktrees), it uses an explicit HEAD:<remote-branch> refspec to avoid the
// "upstream branch name does not match" error.
func Push(repoPath string, force bool) (string, error) {
	args := []string{"-C", repoPath, "push"}
	if force {
		args = append(args, "--force-with-lease")
	}

	// Detect upstream; if it exists and has a different name than the local
	// branch, build an explicit refspec so git doesn't reject the push.
	if status, err := GetUpstreamStatus(repoPath); err == nil && status.Upstream != "" {
		// Upstream is "remote/branch" — strip the remote prefix.
		parts := strings.SplitN(status.Upstream, "/", 2)
		if len(parts) == 2 && parts[1] != status.Branch {
			remote := parts[0]
			remoteBranch := parts[1]
			args = append(args, remote, "HEAD:"+remoteBranch)
		}
	}

	cmd := exec.Command("git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("push failed: %s", strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

// PullRebase runs git pull --rebase on the current branch.
func PullRebase(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "pull", "--rebase")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("rebase failed: %s", strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

// GetLastFetchTime returns the last time a fetch was performed by checking FETCH_HEAD mtime.
func GetLastFetchTime(repoPath string) (time.Time, error) {
	fetchHead := filepath.Join(repoPath, ".git", "FETCH_HEAD")
	info, err := os.Stat(fetchHead)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// HasConflicts checks if there are merge conflicts in the working tree.
func HasConflicts(repoPath string) (bool, []string, error) {
	cmd := exec.Command("git", "-C", repoPath, "diff", "--name-only", "--diff-filter=U")
	output, err := cmd.Output()
	if err != nil {
		return false, nil, err
	}

	text := strings.TrimSpace(string(output))
	if text == "" {
		return false, nil, nil
	}

	files := strings.Split(text, "\n")
	return true, files, nil
}

// SetUpstreamAndPush pushes the current branch and sets it to track the remote branch.
func SetUpstreamAndPush(repoPath string, remote string) (string, error) {
	branch, err := getCurrentBranch(repoPath)
	if err != nil {
		return "", fmt.Errorf("not on a branch: %w", err)
	}

	cmd := exec.Command("git", "-C", repoPath, "push", "-u", remote, branch)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("push failed: %s", strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}
