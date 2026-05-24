package daemon

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// Stop sends SIGTERM to the daemon process named by the pidfile, waits up
// to timeout for it to exit (poll via IsAlive), then SIGKILLs if still
// alive. Removes the pidfile if it lingers. Reports ErrNoDaemon when there
// is no pidfile to act on.
func Stop(p Paths, timeout time.Duration) error {
	pid, err := ReadPID(p.Pidfile)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNoDaemon
		}
		return err
	}
	if !IsAlive(pid) {
		// Stale pidfile; sweep it.
		_ = os.Remove(p.Pidfile)
		return nil
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM to pid %d: %w", pid, err)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !IsAlive(pid) {
			_ = os.Remove(p.Pidfile)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Hard kill.
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("send SIGKILL to pid %d: %w", pid, err)
	}
	// Final settle.
	for i := 0; i < 20; i++ {
		if !IsAlive(pid) {
			_ = os.Remove(p.Pidfile)
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("pid %d still alive after SIGKILL", pid)
}
