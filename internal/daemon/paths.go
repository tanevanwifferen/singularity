// Package daemon implements the daemon process lifecycle: pidfile + socket
// management, auto-spawn, signal handling, and the HTTP/WS Server wrapper.
// See docs/design/DAEMON-LIFECYCLE.md for the spec this implements.
package daemon

import (
	"os"
	"path/filepath"
)

// Paths bundles every well-known path the daemon owns. Resolved once at
// startup; everything is derived from a single state directory so the
// override story (SINGULARITY_HOME) stays a one-liner.
type Paths struct {
	Dir     string // ~/.config/singularity
	Pidfile string // <Dir>/daemon.pid
	Socket  string // <Dir>/daemon.sock
	Token   string // <Dir>/token
	Log     string // <Dir>/daemon.log
	Config  string // <Dir>/daemon.json (optional)
}

// envHome is the override env var documented in DAEMON-LIFECYCLE §1.
const envHome = "SINGULARITY_HOME"

// StateDir resolves the per-user daemon state directory and ensures it
// exists with 0700 permissions. Honors SINGULARITY_HOME; otherwise uses
// os.UserConfigDir() + "/singularity" (~/.config/singularity on Linux,
// ~/Library/Application Support/singularity on macOS).
func StateDir() (string, error) {
	if v := os.Getenv(envHome); v != "" {
		return v, os.MkdirAll(v, 0o700)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "singularity")
	return dir, os.MkdirAll(dir, 0o700)
}

// DefaultPaths returns the canonical Paths derived from StateDir. The
// directory is created (0700) as a side effect. Errors are deferred until
// the caller dereferences a path that requires the dir; for the common
// case where the dir already exists this never fails.
func DefaultPaths() Paths {
	dir, _ := StateDir()
	return PathsFor(dir)
}

// PathsFor returns Paths rooted at the given directory. Exposed for tests
// that want to point the lifecycle at a temp directory without touching
// SINGULARITY_HOME.
func PathsFor(dir string) Paths {
	return Paths{
		Dir:     dir,
		Pidfile: filepath.Join(dir, "daemon.pid"),
		Socket:  filepath.Join(dir, "daemon.sock"),
		Token:   filepath.Join(dir, "token"),
		Log:     filepath.Join(dir, "daemon.log"),
		Config:  filepath.Join(dir, "daemon.json"),
	}
}
