package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// CherryPick cherry-picks the given commit into the current branch.
func CherryPick(repoPath, hash string) error {
	cmd := exec.Command("git", "-C", repoPath, "cherry-pick", hash)
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if strings.Contains(msg, "conflict") || strings.Contains(msg, "CONFLICT") {
			return fmt.Errorf("cherry-pick conflict: %s", msg)
		}
		return fmt.Errorf("cherry-pick failed: %s", msg)
	}
	return nil
}

// ResetToCommit resets the current branch to the given commit.
// mode must be one of "soft", "mixed", or "hard".
func ResetToCommit(repoPath, hash, mode string) error {
	switch mode {
	case "soft", "mixed", "hard":
		// valid
	default:
		return fmt.Errorf("invalid reset mode: %q (must be soft, mixed, or hard)", mode)
	}
	cmd := exec.Command("git", "-C", repoPath, "reset", "--"+mode, hash)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("reset failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

// ResetRepoToMain fetches the remote and hard-resets the current branch to
// origin/<defaultBranch>, discarding all local changes and commits.
func ResetRepoToMain(repoPath, defaultBranch string) error {
	if defaultBranch == "" {
		defaultBranch = "main"
	}
	// Fetch latest from origin
	fetchCmd := exec.Command("git", "-C", repoPath, "fetch", "origin")
	if out, err := fetchCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("fetch failed: %s", strings.TrimSpace(string(out)))
	}
	// Hard reset to origin/<defaultBranch>
	ref := "origin/" + defaultBranch
	resetCmd := exec.Command("git", "-C", repoPath, "reset", "--hard", ref)
	if out, err := resetCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("reset failed: %s", strings.TrimSpace(string(out)))
	}
	// Clean untracked files and directories
	cleanCmd := exec.Command("git", "-C", repoPath, "clean", "-fd")
	if out, err := cleanCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("clean failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// AmendCommitMessage amends the HEAD commit with a new message.
func AmendCommitMessage(repoPath, newMessage string) error {
	if newMessage == "" {
		return fmt.Errorf("commit message cannot be empty")
	}
	cmd := exec.Command("git", "-C", repoPath, "commit", "--amend", "-m", newMessage)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("amend failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

// StageFiles stages the given paths into the index (git add). With all set,
// every change in the working tree is staged (git add -A) and paths are
// ignored. At least one of paths/all must be provided.
func StageFiles(repoPath string, paths []string, all bool) error {
	args := []string{"-C", repoPath, "add"}
	if all {
		args = append(args, "-A")
	} else {
		if len(paths) == 0 {
			return fmt.Errorf("no files to stage: pass file paths or use all")
		}
		args = append(args, "--")
		args = append(args, paths...)
	}
	cmd := exec.Command("git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("stage failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

// CreateCommit commits the staged changes with the given message and returns
// the new commit hash.
func CreateCommit(repoPath, message string) (string, error) {
	if message == "" {
		return "", fmt.Errorf("commit message cannot be empty")
	}
	cmd := exec.Command("git", "-C", repoPath, "commit", "-m", message)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("commit failed: %s", strings.TrimSpace(string(output)))
	}
	hashCmd := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD")
	hashOut, err := hashCmd.Output()
	if err != nil {
		// The commit itself succeeded; missing hash is not fatal.
		return "", nil
	}
	return strings.TrimSpace(string(hashOut)), nil
}

// CopyToClipboard moved to internal/app/clipboard during the daemon/client
// migration — clipboard is OS-local and not a git operation.
