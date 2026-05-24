package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireReleaseRoundTrip(t *testing.T) {
	dir := t.TempDir()
	pid := filepath.Join(dir, "daemon.pid")

	release, err := Acquire(pid)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := os.Stat(pid); err != nil {
		t.Fatalf("pidfile not created: %v", err)
	}

	// Read it back: should match our pid.
	got, err := ReadPID(pid)
	if err != nil {
		t.Fatalf("read pid: %v", err)
	}
	if got != os.Getpid() {
		t.Fatalf("got pid %d, want %d", got, os.Getpid())
	}

	release()
	if _, err := os.Stat(pid); !os.IsNotExist(err) {
		t.Fatalf("pidfile should be gone after release, got err=%v", err)
	}

	// Release is idempotent.
	release()
}

func TestAcquireContention(t *testing.T) {
	dir := t.TempDir()
	pid := filepath.Join(dir, "daemon.pid")

	release, err := Acquire(pid)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release()

	if _, err := Acquire(pid); err != ErrDaemonAlreadyRunning {
		t.Fatalf("second acquire: got %v, want ErrDaemonAlreadyRunning", err)
	}
}

func TestAcquireSweepsStale(t *testing.T) {
	dir := t.TempDir()
	pid := filepath.Join(dir, "daemon.pid")

	// PID 999999 is almost certainly not a live process.
	if err := os.WriteFile(pid, []byte("999999"), 0o600); err != nil {
		t.Fatal(err)
	}
	release, err := Acquire(pid)
	if err != nil {
		t.Fatalf("acquire over stale: %v", err)
	}
	defer release()
	got, _ := ReadPID(pid)
	if got != os.Getpid() {
		t.Fatalf("stale pid not swept; got %d", got)
	}
}

func TestIsAlive(t *testing.T) {
	if !IsAlive(os.Getpid()) {
		t.Fatal("self should be alive")
	}
	if IsAlive(999999) {
		t.Fatal("pid 999999 should not be alive")
	}
}
