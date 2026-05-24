//go:build !windows

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Spawn re-execs the current binary as `daemon` (no --detach) with stdio
// redirected to the log file and the child placed in its own session via
// Setsid. The function returns after the child's socket becomes reachable
// (5s timeout) so callers can immediately dial the daemon.
//
// We deliberately avoid a classic double-fork: Go's runtime is hostile to
// bare fork(); re-exec gives the child a clean address space. See
// DAEMON-LIFECYCLE §5.
func Spawn(socketPath string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self: %w", err)
	}
	p := DefaultPaths()
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return fmt.Errorf("mkdir state dir: %w", err)
	}

	logFile, err := os.OpenFile(p.Log, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		_ = logFile.Close()
		return fmt.Errorf("open /dev/null: %w", err)
	}

	cmd := exec.Command(self, "daemon")
	cmd.Stdin = devNull
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Inherit env so SINGULARITY_HOME and friends propagate.
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		_ = devNull.Close()
		return fmt.Errorf("start daemon: %w", err)
	}

	// Parent doesn't keep these handles open.
	_ = logFile.Close()
	_ = devNull.Close()

	// Release the child so it doesn't become a zombie when this parent
	// (which is the TUI process) exits.
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}

	// Poll for socket readiness, or detect the child died early.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if SocketReachable(socketPath) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return ErrDaemonStartupTimeout
}
