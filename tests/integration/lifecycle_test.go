//go:build integration

package integration

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/daemon"
)

// TestPidfileAcquireContention exercises the spawn-lock: two daemons
// cannot both acquire the same pidfile.
func TestPidfileAcquireContention(t *testing.T) {
	dir := t.TempDir()
	pid := filepath.Join(dir, "daemon.pid")

	release, err := daemon.Acquire(pid)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	t.Cleanup(release)

	if _, err := daemon.Acquire(pid); !errors.Is(err, daemon.ErrDaemonAlreadyRunning) {
		t.Fatalf("second acquire: got %v, want ErrDaemonAlreadyRunning", err)
	}
}

// TestPidfileStaleSweep writes a fake pidfile pointing at a definitely-dead
// PID and verifies Acquire claims it on the retry.
func TestPidfileStaleSweep(t *testing.T) {
	dir := t.TempDir()
	pid := filepath.Join(dir, "daemon.pid")

	// PID 999999 is functionally never alive on Linux/macOS.
	if err := os.WriteFile(pid, []byte("999999"), 0o600); err != nil {
		t.Fatal(err)
	}
	release, err := daemon.Acquire(pid)
	if err != nil {
		t.Fatalf("acquire over stale: %v", err)
	}
	t.Cleanup(release)

	got, err := daemon.ReadPID(pid)
	if err != nil {
		t.Fatalf("ReadPID: %v", err)
	}
	if got != os.Getpid() {
		t.Errorf("stale pid not swept; got %d, want %d", got, os.Getpid())
	}
}

// TestDaemonSpawnLifecycle builds the singularity binary, runs `daemon`
// in the foreground via a subprocess, then verifies:
//   - daemon.Status reports alive once the socket appears
//   - SIGTERM cleans up socket + pidfile
//   - daemon.Stop on a stopped daemon returns ErrNoDaemon
func TestDaemonSpawnLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("daemon lifecycle is unix-only in v1")
	}
	requireGit(t)

	bin := buildSingularity(t)
	home := shortTempDir(t)
	paths := daemon.PathsFor(home)

	cmd := exec.Command(bin, "daemon")
	cmd.Env = append(os.Environ(), "SINGULARITY_HOME="+home)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	waitDone := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(waitDone)
	}()
	t.Cleanup(func() {
		select {
		case <-waitDone:
		default:
			_ = cmd.Process.Signal(syscall.SIGKILL)
			<-waitDone
		}
	})

	// Wait for socket.
	if err := daemon.WaitForSocket(paths.Socket, 5*time.Second); err != nil {
		t.Fatalf("daemon never bound socket: %v", err)
	}

	// Status reports alive + APIOK.
	info, err := daemon.Status(paths)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !info.Alive {
		t.Fatalf("Status: daemon should be alive, info=%+v", info)
	}
	if !info.APIOK {
		t.Errorf("Status: APIOK=false, info=%+v", info)
	}
	if info.PID == 0 {
		t.Errorf("Status: PID=0")
	}

	// Graceful shutdown via SIGTERM.
	if err := syscall.Kill(info.PID, syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM: %v", err)
	}
	select {
	case <-waitDone:
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not exit within 10s of SIGTERM")
	}

	// Cleanup: socket file + pidfile should be gone.
	if _, err := os.Stat(paths.Socket); err == nil {
		t.Errorf("socket file %s should be removed", paths.Socket)
	}
	if _, err := os.Stat(paths.Pidfile); err == nil {
		t.Errorf("pidfile %s should be removed", paths.Pidfile)
	}

	// Stop against a stopped daemon → ErrNoDaemon.
	if err := daemon.Stop(paths, 1*time.Second); !errors.Is(err, daemon.ErrNoDaemon) {
		t.Errorf("Stop on stopped daemon: got %v, want ErrNoDaemon", err)
	}
}

// TestDaemonStopRunning builds the binary, starts a daemon, then calls
// daemon.Stop and verifies the process exits + files are cleaned up.
//
// NOTE: cmd.Wait must run concurrently with daemon.Stop. The child becomes
// a zombie when it exits and remains visible to kill(pid, 0) until its
// parent reaps it — which means daemon.Stop's IsAlive poll would otherwise
// believe the process is still running and escalate to SIGKILL. In
// production, `singularity daemon stop` is a separate process and not the
// daemon's parent, so this is purely a test-harness wrinkle.
func TestDaemonStopRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("daemon lifecycle is unix-only in v1")
	}
	requireGit(t)

	bin := buildSingularity(t)
	home := shortTempDir(t)
	paths := daemon.PathsFor(home)

	cmd := exec.Command(bin, "daemon")
	cmd.Env = append(os.Environ(), "SINGULARITY_HOME="+home)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}

	// Reap the child concurrently so it doesn't linger as a zombie
	// during daemon.Stop's IsAlive poll.
	waitDone := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(waitDone)
	}()
	t.Cleanup(func() {
		select {
		case <-waitDone:
		default:
			_ = cmd.Process.Signal(syscall.SIGKILL)
			<-waitDone
		}
	})

	if err := daemon.WaitForSocket(paths.Socket, 5*time.Second); err != nil {
		t.Fatalf("daemon never bound socket: %v", err)
	}

	if err := daemon.Stop(paths, 5*time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if _, err := os.Stat(paths.Pidfile); err == nil {
		t.Errorf("pidfile %s should be removed after Stop", paths.Pidfile)
	}
}

// TestPidfileReadInvalid verifies ReadPID rejects malformed contents.
func TestPidfileReadInvalid(t *testing.T) {
	dir := t.TempDir()
	pid := filepath.Join(dir, "daemon.pid")
	if err := os.WriteFile(pid, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := daemon.ReadPID(pid); err == nil {
		t.Fatal("expected ReadPID to reject 'garbage'")
	}
	// PID 0 / 1 / negative all rejected.
	for _, bad := range []string{"0", "1", "-5"} {
		if err := os.WriteFile(pid, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := daemon.ReadPID(pid); err == nil {
			t.Errorf("expected ReadPID to reject %q", bad)
		}
	}
	// Sanity: a valid PID parses back.
	if err := os.WriteFile(pid, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := daemon.ReadPID(pid)
	if err != nil {
		t.Fatalf("ReadPID: %v", err)
	}
	if got != os.Getpid() {
		t.Errorf("got %d want %d", got, os.Getpid())
	}
}

// buildSingularity compiles cmd/singularity into t.TempDir() and returns
// the absolute path to the binary. The build is cached by the go tool
// across runs, so the first call is the slow one (~5s) and subsequent
// calls within the same session are fast.
func buildSingularity(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "singularity")
	cmd := exec.Command("go", "build", "-o", bin, "gitlab.com/tanevanwifferen1/singularity/cmd/singularity")
	cmd.Stderr = os.Stderr
	if out, err := cmd.Output(); err != nil {
		t.Fatalf("go build singularity: %v\n%s", err, out)
	}
	return bin
}
