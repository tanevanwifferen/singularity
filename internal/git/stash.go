package git

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// StashEntry represents a git stash entry
type StashEntry struct {
	Index   int       `json:"index"`
	SHA     string    `json:"sha"`
	Message string    `json:"message"`
	Author  string    `json:"author"`
	Date    time.Time `json:"date"`
	Files   []string  `json:"files"`
}

// Worktree represents a git worktree
type Worktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	HEAD   string `json:"head"`
	Locked bool   `json:"locked"`
}

// GetStashList returns all stash entries
func GetStashList(repoPath string) ([]StashEntry, error) {
	cmd := exec.Command("git", "-C", repoPath, "stash", "list", "--format=%H|%gd|%s|%an|%at")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get stash list: %w", err)
	}

	var entries []StashEntry
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "|", 5)
		if len(parts) < 5 {
			continue
		}

		entry := StashEntry{
			SHA:     parts[0],
			Message: parts[2],
			Author:  parts[3],
		}

		// Parse index from refs/stash
		if strings.HasPrefix(parts[1], "stash@") {
			fmt.Sscanf(parts[1], "stash@{%d}", &entry.Index)
		}

		// Parse timestamp
		var timestamp int64
		fmt.Sscanf(parts[4], "%d", &timestamp)
		entry.Date = time.Unix(timestamp, 0)

		entries = append(entries, entry)
	}

	return entries, nil
}

// GetStash retrieves a specific stash entry
func GetStash(repoPath string, index int) (*StashEntry, error) {
	// Get stash info
	cmd := exec.Command("git", "-C", repoPath, "stash", "show", fmt.Sprintf("stash@{%d}", index), "--format=%H|%s|%an")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get stash: %w", err)
	}

	parts := strings.SplitN(string(output), "|", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid stash format")
	}

	entry := &StashEntry{
		Index:   index,
		SHA:     strings.TrimSpace(parts[0]),
		Message: strings.TrimSpace(parts[1]),
		Author:  strings.TrimSpace(parts[2]),
	}

	// Get files
	cmd = exec.Command("git", "-C", repoPath, "stash", "show", fmt.Sprintf("stash@{%d}", index), "--name-only")
	_ = cmd.Run()
	output, err = cmd.Output()
	if err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(output)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				entry.Files = append(entry.Files, line)
			}
		}
	}

	return entry, nil
}

// CreateStash creates a new stash entry
func CreateStash(repoPath, message string, includeUntracked bool) (int, error) {
	args := []string{"-C", repoPath, "stash", "push", "-m", message}
	if includeUntracked {
		args = append(args, "-u")
	}

	cmd := exec.Command("git", args...)
	_, err := cmd.CombinedOutput()
	if err != nil {
		return -1, fmt.Errorf("failed to create stash: %w", err)
	}

	// Parse the new stash index
	cmd = exec.Command("git", "-C", repoPath, "stash", "list", "--format=%gd")
	output, _ := cmd.Output()

	var entry StashEntry
	fmt.Fscanf(strings.NewReader(string(output)), "stash@{%d}", &entry.Index)

	return entry.Index, nil
}

// ApplyStash applies a stash entry
func ApplyStash(repoPath string, index int, dropAfter bool) error {
	action := "apply"
	if dropAfter {
		action = "pop"
	}

	cmd := exec.Command("git", "-C", repoPath, "stash", action, fmt.Sprintf("stash@{%d}", index))
	_, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to apply stash: %w", err)
	}

	if dropAfter {
		return nil
	}

	return nil
}

// DropStash drops a stash entry
func DropStash(repoPath string, index int) error {
	cmd := exec.Command("git", "-C", repoPath, "stash", "drop", fmt.Sprintf("stash@{%d}", index))
	_, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to drop stash: %w", err)
	}
	return nil
}

// ClearStash removes all stash entries
func ClearStash(repoPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "stash", "clear")
	_, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to clear stash: %w", err)
	}
	return nil
}

// GetWorktrees returns all worktrees
func GetWorktrees(repoPath string) ([]Worktree, error) {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktrees: %w", err)
	}

	var worktrees []Worktree
	var current Worktree
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			// Empty line separates worktree entries
			if current.Path != "" {
				worktrees = append(worktrees, current)
			}
			current = Worktree{}
			continue
		}

		if strings.HasPrefix(line, "worktree ") {
			current.Path = strings.TrimPrefix(line, "worktree ")
		} else if strings.HasPrefix(line, "HEAD ") {
			current.HEAD = strings.TrimPrefix(line, "HEAD ")
		} else if strings.HasPrefix(line, "branch ") {
			ref := strings.TrimPrefix(line, "branch ")
			// Convert refs/heads/foo to foo
			current.Branch = strings.TrimPrefix(ref, "refs/heads/")
		} else if line == "locked" {
			current.Locked = true
		}
	}
	// Append last entry if output doesn't end with blank line
	if current.Path != "" {
		worktrees = append(worktrees, current)
	}

	return worktrees, nil
}

// CreateWorktree creates a new worktree.
// When createBranch is true and startPoint is non-empty, the new branch is based on startPoint
// (e.g. "origin/main") instead of HEAD.
func CreateWorktree(repoPath, worktreePath, branch string, createBranch bool, startPoint string) error {
	args := []string{"-C", repoPath, "worktree", "add"}

	if createBranch {
		args = append(args, "-b", branch, worktreePath)
		if startPoint != "" {
			args = append(args, startPoint)
		}
	} else {
		args = append(args, worktreePath, branch)
	}

	cmd := exec.Command("git", args...)
	_, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create worktree: %w", err)
	}

	return nil
}

// RemoveWorktree removes a worktree
func RemoveWorktree(repoPath, worktreePath string, force bool) error {
	args := []string{"-C", repoPath, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, worktreePath)

	cmd := exec.Command("git", args...)
	_, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to remove worktree: %w", err)
	}

	return nil
}

// PruneWorktrees prunes stale worktree references
func PruneWorktrees(repoPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "prune")
	_, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to prune worktrees: %w", err)
	}

	return nil
}

// LockWorktree locks a worktree
func LockWorktree(repoPath, worktreePath string) error {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "lock", worktreePath)
	_, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to lock worktree: %w", err)
	}

	return nil
}

// UnlockWorktree unlocks a worktree
func UnlockWorktree(repoPath, worktreePath string) error {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "unlock", worktreePath)
	_, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to unlock worktree: %w", err)
	}

	return nil
}
