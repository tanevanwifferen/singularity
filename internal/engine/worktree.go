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

// mergeWorktreeBack merges the worktree branch back into the source branch.
// Returns the merge result status. Does NOT clean up the worktree — cleanup
// is deferred to kill() so follow-up messages can still be sent after completion.
// On merge conflict, launches a Claude session to rebase and retries.
func (a *Agent) mergeWorktreeBack() string {
	if a.worktreePath == "" || a.sourceRepoPath == "" {
		return ""
	}

	repoPath := a.sourceRepoPath
	sourceBranch := a.sourceBranch
	wtBranch := a.worktreeBranch
	wtPath := a.worktreePath

	// Auto-commit any uncommitted changes left by the agent
	if committed, err := autoCommitWorktree(wtPath, a.ID, a.Task); err != nil {
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

	// First merge attempt
	result := a.attemptMerge(repoPath, sourceBranch, wtBranch)
	if result == "conflict" {
		// Launch a Claude session to rebase the worktree branch onto the source branch
		a.appendOutput("system", fmt.Sprintf("Worktree: merge conflict — launching Claude session to rebase %s onto %s", wtBranch, sourceBranch))
		if rebaseErr := rebaseWithClaude(wtPath, sourceBranch, a.Task); rebaseErr != nil {
			a.appendOutput("error", fmt.Sprintf("Rebase session failed: %v", rebaseErr))
			return "conflict"
		}
		a.appendOutput("system", "Worktree: rebase complete — retrying merge")
		result = a.attemptMerge(repoPath, sourceBranch, wtBranch)
	}

	return result
}

// attemptMerge tries to merge wtBranch into sourceBranch using --no-ff.
// On conflict, aborts the merge and returns "conflict".
func (a *Agent) attemptMerge(repoPath, sourceBranch, wtBranch string) string {
	logCmd := exec.Command("git", "-C", repoPath, "log", "--oneline", fmt.Sprintf("%s..%s", sourceBranch, wtBranch))
	logOut, _ := logCmd.Output()
	mergeMsg := generateMergeMessage(string(logOut), a.Task, a.ID)

	cmd := exec.Command("git", "-C", repoPath, "merge", wtBranch, "--no-ff", "-m", mergeMsg)
	if out, err := cmd.CombinedOutput(); err != nil {
		abortCmd := exec.Command("git", "-C", repoPath, "merge", "--abort")
		abortCmd.Run()
		a.appendOutput("error", fmt.Sprintf("Merge conflict on branch %s:\n%s", wtBranch, string(out)))
		return "conflict"
	}

	a.appendOutput("system", fmt.Sprintf("Worktree: merged changes from %s into %s", wtBranch, sourceBranch))
	return "merged"
}

// rebaseWithClaude launches a non-interactive Claude session to rebase the worktree
// branch onto sourceBranch, resolving any conflicts. Called when a direct merge fails.
func rebaseWithClaude(wtPath, sourceBranch, task string) error {
	prompt := fmt.Sprintf(
		"Rebase the current git branch onto %s to resolve merge conflicts. "+
			"The original task context was: %s\n\n"+
			"Steps:\n"+
			"1. Run: git rebase %s\n"+
			"2. If there are conflicts, read the conflicting files, understand both sides, and resolve them correctly\n"+
			"3. Stage resolved files and run: git rebase --continue\n"+
			"4. Repeat until the rebase completes successfully\n"+
			"5. Do not abort the rebase — resolve all conflicts",
		sourceBranch, task, sourceBranch,
	)

	cmd := exec.Command("claude", "--print", "--permission-mode", "bypassPermissions", "-p", prompt)
	cmd.Dir = wtPath
	cmd.Env = append(os.Environ(), "CLAUDE_NO_ANALYTICS=true")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("claude rebase session: %w\n%s", err, string(out))
	}
	return nil
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
func autoCommitWorktree(wtPath, agentID, task string) (bool, error) {
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

	// Get the staged diff for commit message generation
	diffCmd := exec.Command("git", "-C", wtPath, "diff", "--cached", "--stat")
	diffOut, _ := diffCmd.Output()

	// Generate a descriptive commit message using Claude
	commitMsg := generateCommitMessage(string(diffOut), task, agentID)
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

// generateCommitMessage calls Claude (haiku) to produce a concise commit message
// from the staged diff stats and the agent's task. Falls back to a static message on failure.
func generateCommitMessage(diffStat, task, agentID string) string {
	fallback := fmt.Sprintf("Agent work from %s", agentID)

	prompt := fmt.Sprintf(
		"Generate a single-line git commit message (max 72 chars, no quotes) for these changes.\n\nTask: %s\n\nChanged files:\n%s",
		task, diffStat,
	)

	cmd := exec.Command("claude", "--print", "--model", "haiku", "-p", prompt)
	cmd.Env = append(os.Environ(), "CLAUDE_NO_ANALYTICS=true")
	out, err := cmd.Output()
	if err != nil {
		return fallback
	}

	msg := strings.TrimSpace(string(out))
	// Strip wrapping quotes if present
	if len(msg) >= 2 && msg[0] == '"' && msg[len(msg)-1] == '"' {
		msg = msg[1 : len(msg)-1]
	}
	if msg == "" {
		return fallback
	}
	return msg
}

// generateMergeMessage calls Claude (haiku) to produce a concise merge commit message
// from the branch's commit log and the agent's task. Falls back to a static message on failure.
func generateMergeMessage(commitLog, task, agentID string) string {
	fallback := fmt.Sprintf("Merge agent work from %s", agentID)

	prompt := fmt.Sprintf(
		"Generate a single-line git merge commit message (max 72 chars, no quotes) summarizing this agent's work.\n\nTask: %s\n\nCommits being merged:\n%s",
		task, commitLog,
	)

	cmd := exec.Command("claude", "--print", "--model", "haiku", "-p", prompt)
	cmd.Env = append(os.Environ(), "CLAUDE_NO_ANALYTICS=true")
	out, err := cmd.Output()
	if err != nil {
		return fallback
	}

	msg := strings.TrimSpace(string(out))
	if len(msg) >= 2 && msg[0] == '"' && msg[len(msg)-1] == '"' {
		msg = msg[1 : len(msg)-1]
	}
	if msg == "" {
		return fallback
	}
	return msg
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
