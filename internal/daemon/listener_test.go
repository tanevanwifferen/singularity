package daemon

import (
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
