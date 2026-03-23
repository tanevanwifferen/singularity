package git

import (
	"fmt"
	"os/exec"
	"runtime"
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

// CopyToClipboard copies text to the system clipboard.
// It tries platform-appropriate tools: wl-copy (Wayland), xclip, xsel (X11),
// pbcopy (macOS).
func CopyToClipboard(text string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "linux":
		// Try wl-copy first (Wayland), then xclip, then xsel
		if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.Command("wl-copy")
		} else if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else if _, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.Command("xsel", "--clipboard", "--input")
		} else {
			return fmt.Errorf("no clipboard tool found (install wl-copy, xclip, or xsel)")
		}
	case "darwin":
		cmd = exec.Command("pbcopy")
	default:
		return fmt.Errorf("clipboard not supported on %s", runtime.GOOS)
	}

	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clipboard copy failed: %w", err)
	}
	return nil
}
