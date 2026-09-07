package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
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

// maxLogTailBytes caps how much of daemon.log gets quoted back on a failed
// startup: enough for a bind failure plus a line of context, not enough to
// dump a whole session into the user's terminal.
const maxLogTailBytes = 2048

// startupTimeoutError builds the error for a spawned daemon that never
// reached its socket. A daemon that died on a real fault (an over-long
// socket path, a bad --listen spec) already wrote the cause to daemon.log,
// and throwing that away leaves the user with a bare "startup timeout" that
// looks identical to a merely slow start. Wraps ErrDaemonStartupTimeout so
// existing errors.Is callers keep matching.
func startupTimeoutError(logPath string) error {
	tail := logTail(logPath, maxLogTailBytes)
	if tail == "" {
		return ErrDaemonStartupTimeout
	}
	return fmt.Errorf("%w; last output in %s:\n%s", ErrDaemonStartupTimeout, logPath, tail)
}

// logTail returns up to max trailing bytes of the file at path, trimmed of
// surrounding whitespace. Any read problem yields "" — the caller falls back
// to the plain timeout rather than reporting a failure about the failure.
func logTail(path string, max int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	off, n := int64(0), st.Size()
	if n > int64(max) {
		off, n = n-int64(max), int64(max)
	}
	buf := make([]byte, n)
	// A short read is fine: whatever landed is still the newest output.
	read, _ := f.ReadAt(buf, off)
	return strings.TrimSpace(string(buf[:read]))
}
