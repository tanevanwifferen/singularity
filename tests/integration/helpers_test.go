//go:build integration

// Package integration exercises the daemon ↔ TUI client wire end-to-end.
//
// Each test uses t.TempDir() for SINGULARITY_HOME so runs are hermetic.
// startTestDaemon spins up the same server composition as
// internal/daemon.Run, but in-process so the test goroutine can shut it
// down without going through SIGTERM (signal.Notify in Run would otherwise
// trip the test process itself).
//
// Tests skip if `git` is not on PATH.
package integration

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/config"
	"gitlab.com/tanevanwifferen1/singularity/internal/daemon"
	"gitlab.com/tanevanwifferen1/singularity/internal/server"
	"gitlab.com/tanevanwifferen1/singularity/internal/service/local"
)

// requireGit skips the test if git is not on PATH. Integration tests that
// touch a repo need a real binary.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not found on PATH: %v", err)
	}
}

// initGitRepo creates a tiny repository at dir with one empty commit so
// service calls have something real to read. Branch is forced to "main"
// for determinism (git's default branch is user-configurable).
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// Override commit author so the operation does not depend on a
		// system git config that may not be present in CI.
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("commit", "--allow-empty", "-m", "initial")
}

// testDaemon bundles the moving parts of an in-process daemon for tests.
type testDaemon struct {
	Paths   daemon.Paths
	Socket  string
	URL     string
	Client  *client.Client
	srv     *server.Server
	ln      net.Listener
	release func()
	serveCh chan error
}

// startTestDaemon spins up an in-process daemon listening on a unix socket
// inside a short tempdir (NOT t.TempDir() — the macOS AF_UNIX path limit
// is 104 bytes and t.TempDir() can blow past that). Sets SINGULARITY_HOME
// to the same dir, returns a connected client and a cleanup that shuts the
// server down and removes the pidfile.
//
// The server is built with the same wiring as internal/daemon.Run minus
// signal handling: we want the test process to keep its own signal
// disposition. Cleanup is automatic via t.Cleanup.
func startTestDaemon(t *testing.T) *testDaemon {
	t.Helper()
	home := shortTempDir(t)
	t.Setenv("SINGULARITY_HOME", home)

	paths := daemon.PathsFor(home)
	release, err := daemon.Acquire(paths.Pidfile)
	if err != nil {
		t.Fatalf("acquire pidfile: %v", err)
	}

	ln, url, err := daemon.Listen("unix://" + paths.Socket)
	if err != nil {
		release()
		t.Fatalf("listen: %v", err)
	}

	srv := server.New(url, "")
	srv.SetServices(local.New(srv.Engine(), nil, config.JiraConfig{}))

	serveCh := make(chan error, 1)
	go func() { serveCh <- srv.Serve(ln) }()

	// Wait until the socket is actually accepting connections.
	if err := waitDial(paths.Socket, 3*time.Second); err != nil {
		release()
		_ = ln.Close()
		t.Fatalf("daemon never came up: %v", err)
	}

	c := client.NewClient(url)

	td := &testDaemon{
		Paths:   paths,
		Socket:  paths.Socket,
		URL:     url,
		Client:  c,
		srv:     srv,
		ln:      ln,
		release: release,
		serveCh: serveCh,
	}

	t.Cleanup(td.shutdown)
	return td
}

// shutdown gracefully tears down the in-process daemon. Idempotent: a
// second call is a no-op because Shutdown on a stopped server returns nil.
func (d *testDaemon) shutdown() {
	if d == nil || d.srv == nil {
		return
	}
	_ = d.Client.Disconnect()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = d.srv.Shutdown(ctx)
	if eng := d.srv.Engine(); eng != nil {
		eng.Shutdown()
	}
	// Drain the Serve goroutine; ignore http.ErrServerClosed which is
	// the canonical "we stopped on purpose" sentinel.
	select {
	case err := <-d.serveCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			// best-effort log via t — we don't have *t here, so swallow.
			_ = err
		}
	case <-time.After(2 * time.Second):
	}
	if d.release != nil {
		d.release()
		d.release = nil
	}
	d.srv = nil
}

// waitDial polls the unix socket until a dial succeeds or timeout elapses.
func waitDial(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := net.Dial("unix", path)
		if err == nil {
			_ = c.Close()
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("socket never reachable")
}

// repoFixture creates a git repo under a sibling directory of the daemon's
// state dir and returns its absolute path.
func repoFixture(t *testing.T) string {
	t.Helper()
	requireGit(t)
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := exec.Command("mkdir", "-p", repo).Run(); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	initGitRepo(t, repo)
	return repo
}

// shortCtx returns a context with a 5s deadline — sufficient for any
// synchronous service method we exercise.
func shortCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// shortTempDir creates a tempdir under /tmp (or os.TempDir() on systems
// without /tmp) with a short name so the resulting AF_UNIX path stays
// under macOS's 104-byte limit. t.TempDir() roots at $TMPDIR which on
// macOS expands to /var/folders/<hash>/T/<test name>/NNN — easily 90+
// bytes before we even append "/daemon.sock".
//
// Registered as a t.Cleanup so the dir is removed after the test.
func shortTempDir(t *testing.T) string {
	t.Helper()
	base := "/tmp"
	if _, err := os.Stat(base); err != nil {
		base = os.TempDir()
	}
	dir, err := os.MkdirTemp(base, "sng")
	if err != nil {
		t.Fatalf("mkdir short tempdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// Compile-time assertion: avoid unused-import lint failures in helpers
// when individual tests stub out usage. The api import is referenced by
// sibling files (stream_test.go, errors_test.go, daemon_roundtrip_test.go).
var _ = api.WSMsgRefreshRepo
