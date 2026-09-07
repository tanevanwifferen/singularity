package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListenUnixDefault(t *testing.T) {
	t.Setenv("SINGULARITY_HOME", t.TempDir())
	ln, url, err := Listen("")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	if !strings.HasPrefix(url, "unix://") {
		t.Fatalf("expected unix:// url, got %s", url)
	}
}

func TestListenUnixExplicitPath(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "x.sock")
	ln, url, err := Listen("unix://" + sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	if url != "unix://"+sock {
		t.Fatalf("url=%s want unix://%s", url, sock)
	}
	// Permissions must be 0600.
	st, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("socket perms = %v, want 0600", st.Mode().Perm())
	}
}

func TestListenTCP(t *testing.T) {
	ln, url, err := Listen("tcp://127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	if !strings.HasPrefix(url, "http://") {
		t.Fatalf("expected http:// url, got %s", url)
	}
}

func TestListenSweepsStaleSocket(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "x.sock")
	// Drop an empty file at the socket path — no listener.
	if err := os.WriteFile(sock, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, _, err := Listen("unix://" + sock)
	if err != nil {
		t.Fatalf("expected stale sweep, got %v", err)
	}
	defer ln.Close()
}

func TestListenRefusesInUse(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "x.sock")
	first, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	if _, _, err := Listen("unix://" + sock); err == nil {
		t.Fatal("expected error when socket in use")
	}
}

func TestListenUnixRejectsOverLongPath(t *testing.T) {
	dir := t.TempDir()
	// Nest 20-byte components until the socket path is over the limit, so
	// the test does not assume anything about where t.TempDir() lives.
	long := dir
	for len(filepath.Join(long, "daemon.sock")) < maxSocketPath {
		long = filepath.Join(long, strings.Repeat("d", 19))
	}
	sock := filepath.Join(long, "daemon.sock")

	_, _, err := Listen("unix://" + sock)
	if err == nil {
		t.Fatal("expected error for over-long socket path")
	}
	msg := err.Error()
	for _, want := range []string{
		sock,
		fmt.Sprintf("%d bytes", len(sock)),
		fmt.Sprintf("at most %d", maxSocketPath-1),
		"SINGULARITY_HOME",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
	// Rejected before bind: nothing should have been created on disk.
	if _, err := os.Stat(long); err == nil {
		t.Errorf("over-long socket dir %s was created", long)
	}
}

func TestListenUnixAcceptsPathWithinLimit(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "daemon.sock")
	if len(sock) >= maxSocketPath {
		t.Skipf("temp dir %s already too deep to build a valid socket path", dir)
	}
	ln, _, err := Listen("unix://" + sock)
	if err != nil {
		t.Fatalf("listen on %d-byte path: %v", len(sock), err)
	}
	defer ln.Close()
}
