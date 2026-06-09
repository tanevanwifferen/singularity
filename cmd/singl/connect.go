package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/daemon"
)

// newClient creates an HTTP-only client. Suitable for all non-streaming
// subcommands — no WebSocket goroutine overhead.
func newClient() (*client.Client, error) {
	endpoint, err := resolveEndpoint(globals.server)
	if err != nil {
		return nil, err
	}
	c := client.NewClient(endpoint)
	if isTCPEndpoint(endpoint) {
		tok, err := loadToken()
		if err != nil {
			return nil, err
		}
		c.SetAuthToken(tok)
	}
	return c, nil
}

// newStreamClient creates a client and opens the WebSocket connection.
// Required for streaming subcommands: agents watch, sync fetch/pull/push,
// pipeline watch.
func newStreamClient() (*client.Client, error) {
	c, err := newClient()
	if err != nil {
		return nil, err
	}
	if err := c.Connect(); err != nil {
		return nil, fmt.Errorf("websocket connect: %w", err)
	}
	return c, nil
}

// resolveEndpoint picks the daemon URL: explicit --server flag, otherwise
// auto-detects the default unix socket (spawning a daemon if none running).
// Mirrors the logic in cmd/singularity/main.go:resolveEndpoint.
func resolveEndpoint(serverFlag string) (string, error) {
	if serverFlag != "" {
		return serverFlag, nil
	}
	paths := daemon.DefaultPaths()
	if daemon.SocketReachable(paths.Socket) {
		return "unix://" + paths.Socket, nil
	}
	// Daemon may be starting — pidfile alive but socket not yet ready.
	if pid, err := daemon.ReadPID(paths.Pidfile); err == nil && daemon.IsAlive(pid) {
		if werr := daemon.WaitForSocket(paths.Socket, 5*time.Second); werr != nil {
			return "", fmt.Errorf("daemon pid %d alive but socket unreachable: %w", pid, werr)
		}
		return "unix://" + paths.Socket, nil
	}
	// Auto-spawn.
	fmt.Fprintln(os.Stderr, "no daemon running; spawning one...")
	if err := daemon.Spawn(paths.Socket); err != nil {
		return "", fmt.Errorf("auto-spawn daemon: %w", err)
	}
	return "unix://" + paths.Socket, nil
}

func isTCPEndpoint(url string) bool {
	return strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
}

// loadToken resolves the bearer token for TCP daemon connections.
// Precedence: SINGULARITY_TOKEN env var, then ~/.config/singularity/token.
func loadToken() (string, error) {
	if v := strings.TrimSpace(os.Getenv("SINGULARITY_TOKEN")); v != "" {
		return v, nil
	}
	paths := daemon.DefaultPaths()
	data, err := os.ReadFile(paths.Token)
	if err == nil {
		if tok := strings.TrimSpace(string(data)); tok != "" {
			return tok, nil
		}
	}
	return "", fmt.Errorf("no auth token: set SINGULARITY_TOKEN or run `singularity daemon init`")
}

// withTimeout returns a 30 s context, suitable for most API calls.
func withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 30*time.Second)
}
