// Package clipboard provides OS-local clipboard helpers for the TUI client.
//
// Clipboard operations are inherently client-side: when the TUI runs against
// a remote daemon, copying text to the user's terminal clipboard cannot
// involve the daemon at all. This package is the local executor so views can
// keep calling Copy(...) without routing through the service layer.
//
// Previously this code lived in internal/git as git.CopyToClipboard; it was
// moved here because clipboard handling is not a git operation. The function
// signature is preserved so callers only need to update their import.
package clipboard

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Copy copies text to the system clipboard. It tries platform-appropriate
// tools: wl-copy (Wayland), xclip, xsel (X11), pbcopy (macOS).
func Copy(text string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "linux":
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
