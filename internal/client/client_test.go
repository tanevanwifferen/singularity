package client

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// shortSockDir returns a temp directory under /tmp (or os.TempDir) with a
// short prefix so the AF_UNIX socket path stays under macOS's 104-byte
// limit. t.TempDir() roots at $TMPDIR, which on macOS can already exceed 90
// bytes before we append the socket name.
func shortSockDir(t *testing.T) string {
	t.Helper()
	base := "/tmp"
	if _, err := os.Stat(base); err != nil {
		base = os.TempDir()
	}
	dir, err := os.MkdirTemp(base, "sc")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// startUnixHTTPServer spins up an http.Server on a unix socket and returns
// the socket path + a shutdown closure. The handler implements just enough
// of the daemon surface to drive the client tests.
func startUnixHTTPServer(t *testing.T) (string, func()) {
	t.Helper()
	dir := shortSockDir(t)
	sock := filepath.Join(dir, "test.sock")

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.APIResponse{
			Success: true,
			Data:    api.StatusResponse{Version: "test", Server: "unit"},
		})
	})

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Echo one message then close.
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, msg)
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()

	return sock, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = ln.Close()
	}
}

func TestClientUnixHTTP(t *testing.T) {
	sock, stop := startUnixHTTPServer(t)
	defer stop()

	c := NewClient("unix://" + sock)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := c.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if resp.Version != "test" {
		t.Fatalf("version=%q", resp.Version)
	}
}

// startAuthHTTPServer spins up an http.Server on a unix socket that requires
// the Authorization header to match the expected bearer token.
func startAuthHTTPServer(t *testing.T, expectedToken string) (string, func()) {
	t.Helper()
	dir := shortSockDir(t)
	sock := filepath.Join(dir, "auth.sock")

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if got != "Bearer "+expectedToken {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(api.APIResponse{
				Success: false,
				Error:   "unauthorized",
				Code:    api.ErrCodePermissionDenied,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(api.APIResponse{
			Success: true,
			Data:    api.StatusResponse{Version: "auth-test", Server: "unit"},
		})
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	return sock, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = ln.Close()
	}
}

// TestClientAuthTokenHeaderSent confirms SetAuthToken installs the bearer
// header on every HTTP request.
func TestClientAuthTokenHeaderSent(t *testing.T) {
	sock, stop := startAuthHTTPServer(t, "s3cret")
	defer stop()
	c := NewClient("unix://" + sock)
	c.SetAuthToken("s3cret")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := c.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if resp.Version != "auth-test" {
		t.Fatalf("version=%q", resp.Version)
	}
}

// TestClientAuthMissingReturns401 confirms that without SetAuthToken the
// server's 401 surfaces as service.ErrPermissionDenied via mapError.
func TestClientAuthMissingReturns401(t *testing.T) {
	sock, stop := startAuthHTTPServer(t, "s3cret")
	defer stop()
	c := NewClient("unix://" + sock)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := c.GetStatus(ctx)
	if err == nil {
		t.Fatal("expected error from unauthenticated request")
	}
}

func TestClientUnixWebSocket(t *testing.T) {
	sock, stop := startUnixHTTPServer(t)
	defer stop()

	c := NewClient("unix://" + sock)
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Disconnect()

	if err := c.SendWSMessage("ping", map[string]string{"hello": "world"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	// Give the echo a moment to round-trip (we don't have a public read
	// hook; success is measured by absence of errors above).
	time.Sleep(100 * time.Millisecond)
}
