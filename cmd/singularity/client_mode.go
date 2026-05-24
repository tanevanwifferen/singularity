package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/daemon"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
	serviceremote "gitlab.com/tanevanwifferen1/singularity/internal/service/remote"
)

// buildRemoteServices constructs a *service.Services backed by the daemon at
// serverURL. It opens the HTTP+WebSocket connection (so streaming subscriptions
// work) and verifies the daemon is reachable via /api/status before returning.
//
// For http/https endpoints the client loads a bearer token from the
// SINGULARITY_TOKEN env var (preferred) or the default token file.
// Unix-socket endpoints skip the token entirely — the daemon does not
// require auth there.
//
// The caller is responsible for the lifetime of the returned *client.Client:
// closing it shuts down the WS reader goroutine and any active streams.
func buildRemoteServices(serverURL string) (*service.Services, *client.Client, error) {
	c := client.NewClient(serverURL)
	if isTCPEndpoint(serverURL) {
		tok, err := loadTokenForTCP()
		if err != nil {
			return nil, nil, err
		}
		c.SetAuthToken(tok)
	}

	// Probe the daemon before opening the WS so we fail fast with a clear
	// error rather than the gorilla-websocket dial diagnostic.
	probeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.GetStatus(probeCtx); err != nil {
		return nil, nil, fmt.Errorf("daemon unreachable at %s: %w", serverURL, err)
	}

	if err := c.Connect(); err != nil {
		return nil, nil, fmt.Errorf("failed to open WebSocket to %s: %w", serverURL, err)
	}
	if err := c.Subscribe(); err != nil {
		_ = c.Disconnect()
		return nil, nil, fmt.Errorf("failed to subscribe to WS events: %w", err)
	}

	return serviceremote.New(c), c, nil
}

// isTCPEndpoint returns true when serverURL is an http:// or https:// URL.
// unix:// endpoints (and the empty default) fall through to the local
// socket path which does not require auth.
func isTCPEndpoint(serverURL string) bool {
	return strings.HasPrefix(serverURL, "http://") || strings.HasPrefix(serverURL, "https://")
}

// loadTokenForTCP resolves the bearer token used when talking to a TCP
// daemon. Precedence: SINGULARITY_TOKEN env var, then the on-disk file at
// daemon.DefaultPaths().Token. Returns a friendly error mentioning both
// sources if neither is set. We deliberately read the file directly (not via
// EnsureToken) so the client never silently mints a token the daemon hasn't
// seen.
func loadTokenForTCP() (string, error) {
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
	return "", fmt.Errorf("no auth token available for TCP daemon: set SINGULARITY_TOKEN or run `singularity daemon init` to generate %s", paths.Token)
}
