package daemon

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Listen binds a net.Listener for the daemon's HTTP+WS surface. spec
// accepts the following forms:
//
//	""                     → default unix socket at DefaultPaths().Socket
//	"unix"                 → same as ""
//	"unix:///abs/path.sock" → AF_UNIX at that absolute path
//	"tcp://host:port"      → AF_INET listener
//	"host:port"            → AF_INET listener (shorthand)
//
// For unix sockets, a stale socket file (no live listener behind it) is
// removed before bind. The parent directory is created with 0700 and the
// socket itself is chmodded to 0600 after bind.
//
// Returns the listener and a canonical URL the client should dial — e.g.
// "unix:///abs/path.sock" or "http://host:port".
func Listen(spec string) (net.Listener, string, error) {
	switch {
	case spec == "" || spec == "unix":
		p := DefaultPaths()
		return listenUnix(p.Socket)

	case strings.HasPrefix(spec, "unix://"):
		u, err := url.Parse(spec)
		if err != nil {
			return nil, "", fmt.Errorf("invalid unix url %q: %w", spec, err)
		}
		path := u.Path
		if path == "" {
			return nil, "", fmt.Errorf("invalid unix url %q: missing path", spec)
		}
		return listenUnix(path)

	case strings.HasPrefix(spec, "tcp://"):
		hostPort := strings.TrimPrefix(spec, "tcp://")
		return listenTCP(hostPort)

	default:
		// Bare host:port shorthand.
		if strings.Contains(spec, ":") && !strings.Contains(spec, "://") {
			return listenTCP(spec)
		}
		return nil, "", fmt.Errorf("unsupported listen spec %q", spec)
	}
}

func listenUnix(path string) (net.Listener, string, error) {
	// Ensure parent dir exists 0700.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, "", fmt.Errorf("mkdir socket dir: %w", err)
	}
	// Sweep stale socket file if no listener is behind it.
	if _, err := os.Stat(path); err == nil {
		if c, derr := net.Dial("unix", path); derr == nil {
			_ = c.Close()
			return nil, "", fmt.Errorf("socket %s already in use", path)
		}
		if rerr := os.Remove(path); rerr != nil {
			return nil, "", fmt.Errorf("remove stale socket %s: %w", path, rerr)
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, "", fmt.Errorf("chmod socket: %w", err)
	}
	return ln, "unix://" + path, nil
}

func listenTCP(hostPort string) (net.Listener, string, error) {
	ln, err := net.Listen("tcp", hostPort)
	if err != nil {
		return nil, "", err
	}
	return ln, "http://" + ln.Addr().String(), nil
}
