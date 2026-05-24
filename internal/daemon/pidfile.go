package daemon

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// ErrDaemonAlreadyRunning is returned by Acquire when the pidfile exists and
// names a live process.
var ErrDaemonAlreadyRunning = errors.New("daemon already running")

// Acquire creates the pidfile at path using O_CREAT|O_EXCL|O_WRONLY (mode
// 0600), writes the current PID, and returns a release closure that
// unlinks the file. If the file already exists it is checked for staleness
// via IsAlive; a stale file is removed and creation retried once.
//
// The release closure is safe to call multiple times.
func Acquire(path string) (release func(), err error) {
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			if _, werr := fmt.Fprintf(f, "%d", os.Getpid()); werr != nil {
				_ = f.Close()
				_ = os.Remove(path)
				return nil, werr
			}
			_ = f.Sync()
			_ = f.Close()
			released := false
			return func() {
				if released {
					return
				}
				released = true
				_ = os.Remove(path)
			}, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		// Pidfile exists: check if it's stale.
		pid, perr := ReadPID(path)
		if perr == nil && IsAlive(pid) {
			return nil, ErrDaemonAlreadyRunning
		}
		// Stale — sweep it.
		if rerr := os.Remove(path); rerr != nil && !os.IsNotExist(rerr) {
			return nil, rerr
		}
	}
	return nil, ErrDaemonAlreadyRunning
}

// ReadPID parses an integer PID from the pidfile.
func ReadPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("malformed pidfile %s: %w", path, err)
	}
	if pid <= 1 {
		return 0, fmt.Errorf("invalid pid %d in %s", pid, path)
	}
	return pid, nil
}

// IsAlive reports whether a process with the given PID exists. We probe
// with signal 0 — kernel returns nil if the process exists, ESRCH if it
// doesn't, EPERM if it exists but isn't ours. Per spec EPERM is treated as
// alive (don't nuke another user's daemon).
func IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil:
		return true
	case errors.Is(err, syscall.ESRCH):
		return false
	case errors.Is(err, syscall.EPERM):
		return true
	default:
		return false
	}
}
