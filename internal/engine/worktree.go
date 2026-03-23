package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// setupWorktree creates a temporary git worktree for agent isolation.
// It creates a new branch based on the current HEAD and sets up the worktree
// in ~/.worktrees/<repo-name>/agent-<id>/.
func (a *Agent) setupWorktree() error {
	repoPath := a.WorkDir

	// Get the current branch name
	currentBranch, err := gitCurrentBranch(repoPath)
	if err != nil {
		return fmt.Errorf("get current branch: %w", err)
	}

	// Generate worktree branch name and path
	repoName := filepath.Base(repoPath)
	branchName := fmt.Sprintf("agent/%s/%s", sanitizeBranch(a.ID), sanitizeBranch(currentBranch))
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	wtPath := filepath.Join(home, ".worktrees", repoName, "agent-"+a.ID)

	// Create parent directory
	if err := os.MkdirAll(filepath.Dir(wtPath), 0o755); err != nil {
		return fmt.Errorf("create worktree parent dir: %w", err)
	}

	// Create the worktree with a new branch based on HEAD
	cmd := exec.Command("git", "-C", repoPath, "worktree", "add", "-b", branchName, wtPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create worktree: %w\n%s", err, string(out))
	}

	a.sourceRepoPath = repoPath
	a.sourceBranch = currentBranch
	a.worktreePath = wtPath
	a.worktreeBranch = branchName
	a.WorkDir = wtPath

	return nil
}

// mergeWorktreeBack merges the worktree branch back into the source branch
// and cleans up the worktree. Returns the merge result status.
func (a *Agent) mergeWorktreeBack() string {
	if a.worktreePath == "" || a.sourceRepoPath == "" {
		return ""
	}

	repoPath := a.sourceRepoPath
	sourceBranch := a.sourceBranch
	wtBranch := a.worktreeBranch
	wtPath := a.worktreePath

	defer func() {
		// Always clean up: remove worktree and delete temp branch
		cleanupWorktree(repoPath, wtPath, wtBranch)
	}()

	// Auto-commit any uncommitted changes left by the agent
	if committed, err := autoCommitWorktree(wtPath, a.ID); err != nil {
		a.appendOutput("error", fmt.Sprintf("Failed to auto-commit worktree changes: %v", err))
		return "error"
	} else if committed {
		a.appendOutput("system", "Worktree: auto-committed uncommitted changes before merge")
	}

	// Check if the worktree branch has any new commits compared to source
	hasChanges, err := branchHasNewCommits(repoPath, sourceBranch, wtBranch)
	if err != nil {
		a.appendOutput("error", fmt.Sprintf("Failed to check worktree changes: %v", err))
		return "error"
	}
	if !hasChanges {
		a.appendOutput("system", "Worktree: no changes to merge")
		return "no-changes"
	}

	// Merge the worktree branch into the source branch
	// Use --no-ff to preserve the merge as a distinct event
	cmd := exec.Command("git", "-C", repoPath, "merge", wtBranch, "--no-ff",
		"-m", fmt.Sprintf("Merge agent work from %s", a.ID))
	if out, err := cmd.CombinedOutput(); err != nil {
		// Merge conflict - abort and leave changes on the branch
		abortCmd := exec.Command("git", "-C", repoPath, "merge", "--abort")
		abortCmd.Run()
		a.appendOutput("error", fmt.Sprintf("Worktree merge conflict, changes left on branch %s:\n%s", wtBranch, string(out)))
		return "conflict"
	}

	a.appendOutput("system", fmt.Sprintf("Worktree: merged changes from %s into %s", wtBranch, sourceBranch))
	return "merged"
}

// cleanupWorktree removes the worktree and deletes the temporary branch.
func cleanupWorktree(repoPath, wtPath, branch string) {
	// Remove the worktree (force in case of uncommitted files)
	rmCmd := exec.Command("git", "-C", repoPath, "worktree", "remove", "--force", wtPath)
	rmCmd.Run()

	// Prune stale worktree references
	pruneCmd := exec.Command("git", "-C", repoPath, "worktree", "prune")
	pruneCmd.Run()

	// Delete the temporary branch (only if it was merged or has no unique commits)
	delCmd := exec.Command("git", "-C", repoPath, "branch", "-d", branch)
	if delCmd.Run() != nil {
		// -d failed (not merged) — don't force-delete, leave the branch for manual recovery
	}
}

// autoCommitWorktree stages and commits any uncommitted changes in the worktree.
// Returns true if a commit was made, false if the worktree was already clean.
func autoCommitWorktree(wtPath, agentID string) (bool, error) {
	// Check for any changes (staged, unstaged, or untracked)
	statusCmd := exec.Command("git", "-C", wtPath, "status", "--porcelain")
	out, err := statusCmd.Output()
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	if strings.TrimSpace(string(out)) == "" {
		return false, nil // nothing to commit
	}

	// Stage everything
	addCmd := exec.Command("git", "-C", wtPath, "add", "-A")
	if out, err := addCmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("git add: %w\n%s", err, string(out))
	}

	// Commit with agent attribution
	commitMsg := fmt.Sprintf("Agent work from %s", agentID)
	commitCmd := exec.Command("git", "-C", wtPath, "commit", "-m", commitMsg)
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("git commit: %w\n%s", err, string(out))
	}

	return true, nil
}

// branchHasNewCommits checks whether branchB has commits not in branchA.
func branchHasNewCommits(repoPath, branchA, branchB string) (bool, error) {
	cmd := exec.Command("git", "-C", repoPath, "rev-list", "--count", fmt.Sprintf("%s..%s", branchA, branchB))
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	count := strings.TrimSpace(string(out))
	return count != "0", nil
}

// gitCurrentBranch returns the current branch name for the given repo.
func gitCurrentBranch(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// sanitizeBranch makes a string safe for use in git branch names.
func sanitizeBranch(s string) string {
	r := strings.NewReplacer(
		" ", "-",
		"..", "-",
		"~", "-",
		"^", "-",
		":", "-",
		"?", "-",
		"*", "-",
		"[", "-",
		"\\", "-",
	)
	return r.Replace(s)
}
