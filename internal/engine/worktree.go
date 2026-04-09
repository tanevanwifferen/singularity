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

	// Auto-commit any uncommitted changes left by the agent.
	// If auto-commit fails (e.g. hook rejection), log the error but still attempt
	// to merge any commits the agent already made — don't skip the merge entirely.
	if committed, err := autoCommitWorktree(wtPath, a.ID, a.Task); err != nil {
		a.appendOutput("error", fmt.Sprintf("Warning: auto-commit of remaining changes failed: %v", err))
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

	// Force-delete the temporary branch. Using -D (not -d) ensures cleanup even
	// for unmerged branches (e.g. agent errored or was killed mid-task).
	delCmd := exec.Command("git", "-C", repoPath, "branch", "-D", branch)
	delCmd.Run()
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

	// Verify nothing was left uncommitted (e.g. new files missed by staging)
	verifyCmd := exec.Command("git", "-C", wtPath, "status", "--porcelain")
	verifyOut, err := verifyCmd.Output()
	if err != nil {
		return true, fmt.Errorf("post-commit verify: %w", err)
	}
	if remaining := strings.TrimSpace(string(verifyOut)); remaining != "" {
		return true, fmt.Errorf("files remain uncommitted after auto-commit:\n%s", remaining)
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
	msg = sanitizeCommitMsg(msg)
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
	msg = sanitizeCommitMsg(msg)
	if msg == "" {
		return fallback
	}
	return msg
}

// sanitizeCommitMsg strips patterns that would cause the commit-msg hook to reject
// the message (markdown headers, bold prefixes, horizontal rules, status emoji).
// Returns the cleaned first non-empty line so it stays within single-line conventions.
func sanitizeCommitMsg(msg string) string {
	// Collapse to the first non-empty line (messages should be single-line)
	for _, line := range strings.Split(msg, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Strip markdown headers: "## foo" → "foo"
		if after, ok := strings.CutPrefix(line, "## "); ok {
			line = strings.TrimSpace(after)
		}
		// Strip bold prefixes: "**Foo**: bar" → "Foo: bar"
		if strings.HasPrefix(line, "**") {
			line = strings.ReplaceAll(line, "**", "")
		}
		// Remove horizontal rule lines
		if line == "---" {
			continue
		}
		// Remove common status emoji
		for _, emoji := range []string{"✓", "✅", "❌", "🔧", "🚀", "💥", "⚠️"} {
			line = strings.ReplaceAll(line, emoji, "")
		}
		line = strings.TrimSpace(line)
		if line != "" {
			// Truncate to 72 chars
			if len(line) > 72 {
				line = line[:69] + "..."
			}
			return line
		}
	}
	return ""
}

// CleanupStaleWorktrees removes agent worktrees from previous sessions that
// don't correspond to any currently active agent. Preserves worktrees that
// have uncommitted changes or unmerged commits to avoid losing work.
// Runs in the background — safe to call from startup.
func CleanupStaleWorktrees(repoPath string, activeAgentIDs map[string]bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	// Parse git worktree list --porcelain to find all registered worktrees
	cmd := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return
	}

	type wtInfo struct {
		path   string
		branch string
	}
	var worktrees []wtInfo
	var current wtInfo
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			if current.path != "" {
				worktrees = append(worktrees, current)
			}
			current = wtInfo{}
			continue
		}
		if strings.HasPrefix(line, "worktree ") {
			current.path = strings.TrimPrefix(line, "worktree ")
		}
		if strings.HasPrefix(line, "branch refs/heads/") {
			current.branch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}
	if current.path != "" {
		worktrees = append(worktrees, current)
	}

	agentWtPrefix := filepath.Join(home, ".worktrees")

	// Determine the main branch to check merge status against
	mainBranch := gitMainBranch(repoPath)

	for _, wt := range worktrees {
		// Only touch agent worktrees under ~/.worktrees/
		if !strings.HasPrefix(wt.path, agentWtPrefix) {
			continue
		}
		dirName := filepath.Base(wt.path)
		if !strings.HasPrefix(dirName, "agent-") {
			continue
		}

		// Skip worktrees belonging to active agents
		agentID := strings.TrimPrefix(dirName, "agent-")
		if activeAgentIDs[agentID] {
			continue
		}

		// Skip if worktree has uncommitted changes
		if worktreeHasUncommittedChanges(wt.path) {
			continue
		}

		// Skip if branch has unmerged commits relative to main
		if wt.branch != "" {
			hasNew, err := branchHasNewCommits(repoPath, mainBranch, wt.branch)
			if err == nil && hasNew {
				continue // unmerged work, keep it
			}
		}

		cleanupWorktree(repoPath, wt.path, wt.branch)
	}

	// Also clean up orphaned directories on disk that git no longer tracks
	// (e.g. from a crash where git worktree remove never ran).
	// Check both ~/.worktrees/ (legacy) and ~/.worktrees/<repoName>/ (current).
	cleanupOrphanedWorktreeDirs(repoPath, agentWtPrefix, activeAgentIDs)
	repoName := filepath.Base(repoPath)
	repoSubdir := filepath.Join(agentWtPrefix, repoName)
	if repoSubdir != agentWtPrefix {
		cleanupOrphanedWorktreeDirs(repoPath, repoSubdir, activeAgentIDs)
	}

	// Final prune to clear any remaining stale git references
	pruneCmd := exec.Command("git", "-C", repoPath, "worktree", "prune")
	pruneCmd.Run()
}

// worktreeHasUncommittedChanges returns true if the worktree has any staged,
// unstaged, or untracked changes.
func worktreeHasUncommittedChanges(wtPath string) bool {
	cmd := exec.Command("git", "-C", wtPath, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return true // assume dirty on error to be safe
	}
	return strings.TrimSpace(string(out)) != ""
}

// gitMainBranch returns "main" or "master" depending on what exists in the repo.
func gitMainBranch(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--verify", "refs/heads/main")
	if err := cmd.Run(); err == nil {
		return "main"
	}
	return "master"
}

// cleanupOrphanedWorktreeDirs removes agent-* directories under baseDir that
// are no longer registered as git worktrees (leftover from crashes).
func cleanupOrphanedWorktreeDirs(repoPath, baseDir string, activeAgentIDs map[string]bool) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return
	}

	// Build set of paths git still knows about
	cmd := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return
	}
	knownPaths := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			knownPaths[strings.TrimPrefix(line, "worktree ")] = true
		}
	}

	// Resolve the canonical git dir so we can verify worktree ownership below.
	mainGitDir := ""
	if gdOut, gdErr := exec.Command("git", "-C", repoPath, "rev-parse", "--git-dir").Output(); gdErr == nil {
		gd := strings.TrimSpace(string(gdOut))
		if !filepath.IsAbs(gd) {
			gd = filepath.Join(repoPath, gd)
		}
		mainGitDir = gd
	}

	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "agent-") {
			continue
		}
		agentID := strings.TrimPrefix(entry.Name(), "agent-")
		if activeAgentIDs[agentID] {
			continue
		}
		fullPath := filepath.Join(baseDir, entry.Name())
		if knownPaths[fullPath] {
			continue // git still tracks it, handled above
		}
		// Only remove directories that belong to the current repo.
		// Directories whose .git file points to a different repo are from
		// another project and must not be touched.
		if mainGitDir != "" && !worktreeBelongsToRepo(fullPath, mainGitDir) {
			continue
		}
		// Orphaned directory — remove it
		os.RemoveAll(fullPath)
	}
}

// worktreeBelongsToRepo returns true if the worktree directory was created for
// the repo whose git dir is mainGitDir. It reads the worktree's .git file,
// which contains "gitdir: <path>/worktrees/<name>", and checks that the path
// is rooted under mainGitDir. If the .git file is absent the directory is
// treated as a true orphan belonging to this repo (safe to remove).
func worktreeBelongsToRepo(wtPath, mainGitDir string) bool {
	data, err := os.ReadFile(filepath.Join(wtPath, ".git"))
	if err != nil {
		// No .git file — truly orphaned, treat as belonging to this repo.
		return true
	}
	content := strings.TrimSpace(string(data))
	if !strings.HasPrefix(content, "gitdir: ") {
		return false
	}
	gitdirRef := strings.TrimPrefix(content, "gitdir: ")
	if !filepath.IsAbs(gitdirRef) {
		gitdirRef = filepath.Join(wtPath, gitdirRef)
	}
	gitdirRef = filepath.Clean(gitdirRef)
	return strings.HasPrefix(gitdirRef, mainGitDir)
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
