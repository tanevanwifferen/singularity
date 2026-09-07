package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartupTimeoutErrorIncludesLogTail(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "daemon.log")
	const bind = "daemon: listen: unix socket path too long: /x/daemon.sock is 200 bytes"
	if err := os.WriteFile(log, []byte("2026/09/07 starting\n"+bind+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := startupTimeoutError(log)
	if !errors.Is(err, ErrDaemonStartupTimeout) {
		t.Fatalf("errors.Is(err, ErrDaemonStartupTimeout) = false for %v", err)
	}
	if !strings.Contains(err.Error(), bind) {
		t.Errorf("error %q does not include the logged cause", err)
	}
	if !strings.Contains(err.Error(), log) {
		t.Errorf("error %q does not name the log path", err)
	}
}

func TestStartupTimeoutErrorFallsBackWithoutLog(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	for name, path := range map[string]string{
		"missing": filepath.Join(dir, "absent.log"),
		"empty":   empty,
	} {
		err := startupTimeoutError(path)
		if err != ErrDaemonStartupTimeout {
			t.Errorf("%s log: got %v, want the bare ErrDaemonStartupTimeout", name, err)
		}
	}
}

func TestLogTailKeepsNewestBytes(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "daemon.log")
	body := strings.Repeat("old\n", 1000) + "the real error\n"
	if err := os.WriteFile(log, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	tail := logTail(log, 64)
	if len(tail) > 64 {
		t.Fatalf("tail is %d bytes, want at most 64", len(tail))
	}
	if !strings.HasSuffix(tail, "the real error") {
		t.Fatalf("tail %q does not end with the newest line", tail)
	}
}
