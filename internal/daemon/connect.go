package daemon

import (
	"errors"
	"net"
	"time"
)

// ErrDaemonStartupTimeout is returned by WaitForSocket if no listener
// appears within the deadline.
var ErrDaemonStartupTimeout = errors.New("daemon startup timeout")

// WaitForSocket polls the given unix-socket path until a dial succeeds or
// timeout elapses. 50ms tick. The function returns nil on success,
// ErrDaemonStartupTimeout otherwise.
func WaitForSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c, err := net.Dial("unix", path); err == nil {
			_ = c.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return ErrDaemonStartupTimeout
}

// SocketReachable reports whether a dial to the given unix socket
// succeeds immediately. Used by the TUI auto-spawn path to decide
// whether to fork a daemon.
func SocketReachable(path string) bool {
	c, err := net.Dial("unix", path)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}
