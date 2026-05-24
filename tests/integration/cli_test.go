//go:build integration

package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCLISmoke exercises the subcommand parsing without spawning the TUI.
// Verifies:
//
//   - `singularity help` prints usage and exits 0.
//   - `singularity daemon --help` (or `daemon help`) prints daemon usage.
//   - `singularity daemon status` against an empty SINGULARITY_HOME reports
//     "not running" and exits non-zero.
//   - `singularity daemon init` creates token + config files.
func TestCLISmoke(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("CLI smoke is unix-only in v1")
	}
	bin := buildSingularity(t)
	home := t.TempDir()
	env := append(os.Environ(), "SINGULARITY_HOME="+home)

	t.Run("help", func(t *testing.T) {
		out, code := runCmd(t, bin, env, "help")
		if code != 0 {
			t.Errorf("`help` exit code = %d, want 0", code)
		}
		if !strings.Contains(out, "singularity") {
			t.Errorf("`help` output missing 'singularity':\n%s", out)
		}
	})

	t.Run("daemon_help", func(t *testing.T) {
		out, code := runCmd(t, bin, env, "daemon", "help")
		if code != 0 {
			t.Errorf("`daemon help` exit = %d, want 0", code)
		}
		if !strings.Contains(out, "daemon") {
			t.Errorf("`daemon help` missing 'daemon':\n%s", out)
		}
	})

	t.Run("daemon_status_not_running", func(t *testing.T) {
		out, code := runCmd(t, bin, env, "daemon", "status")
		// No pidfile in fresh SINGULARITY_HOME → "not running" + exit 1.
		if code != 1 {
			t.Errorf("`daemon status` exit = %d, want 1 (not running)", code)
		}
		if !strings.Contains(out, "not running") {
			t.Errorf("`daemon status` missing 'not running':\n%s", out)
		}
	})

	t.Run("daemon_init", func(t *testing.T) {
		out, code := runCmd(t, bin, env, "daemon", "init")
		if code != 0 {
			t.Errorf("`daemon init` exit = %d, want 0:\n%s", code, out)
		}
		// Expect the printed lines per main.go runDaemonInit.
		for _, want := range []string{"state dir:", "socket:", "token:", "config:"} {
			if !strings.Contains(out, want) {
				t.Errorf("`daemon init` output missing %q:\n%s", want, out)
			}
		}
		// Verify on-disk artifacts.
		for _, p := range []string{
			filepath.Join(home, "token"),
			filepath.Join(home, "daemon.json"),
		} {
			st, err := os.Stat(p)
			if err != nil {
				t.Errorf("expected %s to exist: %v", p, err)
				continue
			}
			if mode := st.Mode().Perm(); mode != 0o600 {
				t.Errorf("%s perms = %v, want 0600", p, mode)
			}
		}
	})
}

// runCmd executes bin with args, captures combined stdout/stderr, and
// returns the output + exit code. Exit code -1 means the process failed
// to start (which is a fatal test condition).
func runCmd(t *testing.T, bin string, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return buf.String(), exitErr.ExitCode()
	}
	if err != nil {
		t.Fatalf("run %v: %v", args, err)
		return buf.String(), -1
	}
	return buf.String(), 0
}
