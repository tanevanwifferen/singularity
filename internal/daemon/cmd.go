package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/config"
	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
	"gitlab.com/tanevanwifferen1/singularity/internal/oneshot"
	"gitlab.com/tanevanwifferen1/singularity/internal/project"
	"gitlab.com/tanevanwifferen1/singularity/internal/server"
	"gitlab.com/tanevanwifferen1/singularity/internal/service/local"
)

// RunOptions controls Run's behaviour. Zero value is the default: foreground
// daemon listening on the default unix socket.
type RunOptions struct {
	// Listen overrides the listen spec ("", "unix", "unix:///path",
	// "tcp://host:port", "host:port"). Empty means use the default unix
	// socket at Paths.Socket.
	Listen string

	// ProjectConfig is an optional path to a project config file. If empty,
	// the daemon's config file (daemon.json) may supply a default; failing
	// that, no project is auto-loaded.
	ProjectConfig string

	// MaxAgents caps the agent engine. 0 = use config default (16 if no
	// config either).
	MaxAgents int
}

// fileConfig is the on-disk daemon.json schema. YAML is deferred (spec §9
// allows it as future work; the daemon command itself doesn't need it
// today, and pulling in a YAML dep is not worth it for one file).
type fileConfig struct {
	Listen        string `json:"listen,omitempty"`
	ProjectConfig string `json:"default_project_config,omitempty"`
	MaxAgents     int    `json:"max_agents,omitempty"`
}

// loadFileConfig reads daemon.json if present. Missing file is not an
// error. Unknown keys are tolerated (JSON Decoder).
func loadFileConfig(path string) (fileConfig, error) {
	var c fileConfig
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, nil
}

// Run starts the daemon: acquires the pidfile, binds the listener, builds
// the server with local services, installs signal handlers, and blocks
// until shutdown. Returns nil on clean shutdown.
func Run(opts RunOptions) error {
	paths := DefaultPaths()
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		return fmt.Errorf("mkdir state dir: %w", err)
	}

	// Load optional file config; flags-in-opts override.
	fc, err := loadFileConfig(paths.Config)
	if err != nil {
		return err
	}
	listenSpec := firstNonEmpty(opts.Listen, fc.Listen)
	projCfgPath := firstNonEmpty(opts.ProjectConfig, fc.ProjectConfig)
	// If no project config was explicitly provided, fall back to the default
	// path (~/.config/singularity/projects.json). This ensures that auto-spawned
	// daemons pick up the user's project config without requiring daemon.json.
	if projCfgPath == "" {
		defaultCfg := project.GetDefaultConfigPath()
		if _, ferr := os.Stat(defaultCfg); ferr == nil {
			projCfgPath = defaultCfg
			log.Printf("auto-loading default project config: %s", projCfgPath)
		}
	}
	maxAgents := opts.MaxAgents
	if maxAgents == 0 {
		maxAgents = fc.MaxAgents
	}
	if maxAgents == 0 {
		maxAgents = 16
	}

	// Spawn lock: O_CREAT|O_EXCL on the pidfile.
	release, err := Acquire(paths.Pidfile)
	if err != nil {
		return err
	}
	defer release()

	// Bind listener.
	ln, listenURL, err := Listen(listenSpec)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	// Track whether the listener is unix (for socket file cleanup on exit).
	unixSocketPath := ""
	if ua, ok := ln.Addr().(*net.UnixAddr); ok {
		unixSocketPath = ua.Name
	}

	// Build server + local services.
	srv := server.New(listenURL, "")

	// For TCP listeners, require a pre-provisioned bearer token. Unix
	// sockets rely on filesystem permissions (0600) and skip token auth.
	// We deliberately read the token file directly (rather than via
	// EnsureToken, which would silently mint one) so an operator who
	// forgot to run `daemon init` sees a hard failure instead of starting
	// a daemon with a freshly generated token they don't have.
	if unixSocketPath == "" {
		data, terr := os.ReadFile(paths.Token)
		if terr != nil {
			_ = ln.Close()
			if os.IsNotExist(terr) {
				return fmt.Errorf("TCP listener requires a bearer token at %s — run `singularity daemon init` first", paths.Token)
			}
			return fmt.Errorf("read token for TCP listener: %w", terr)
		}
		tok := strings.TrimSpace(string(data))
		if tok == "" {
			_ = ln.Close()
			return fmt.Errorf("token file %s is empty — run `singularity daemon init` to regenerate", paths.Token)
		}
		srv.SetAuthToken(tok)
		log.Printf("TCP listener: bearer-token auth enabled (token loaded from %s)", paths.Token)
	} else {
		srv.SetUnixListener(true)
	}

	var loader *project.Loader
	if projCfgPath != "" {
		l, err := project.NewLoaderFromFile(projCfgPath)
		if err != nil {
			_ = ln.Close()
			return fmt.Errorf("load project config: %w", err)
		}
		loader = l
		srv.SetProjectLoader(loader)
	}

	// The engine is owned by the server (constructed in server.New); apply
	// the configured cap on the shared instance before any agent starts.
	srv.Engine().SetMaxAgents(maxAgents)

	// Model aliases and classifier models live in models.json next to
	// config.json; the file is created with the defaults when absent.
	engine.SetModels(config.LoadDefaultModelsConfig())

	var jiraCfg config.JiraConfig
	if cfg, lerr := config.LoadDefaultConfig(); lerr == nil && cfg != nil {
		jiraCfg = cfg.Jira
		// Apply backend from AI provider config ("claude" or "pi").
		if b := engine.BackendByName(cfg.AI.Provider); b != nil {
			srv.Engine().SetDefaultBackend(b)
			log.Printf("agent backend: %s (from config AI.Provider)", cfg.AI.Provider)
		}
	}

	// Install the same backend for one-shot prompt calls (commit messages, MR
	// descriptions). Packages outside the engine — internal/git — read this
	// process-wide default instead of importing the engine.
	oneshot.SetDefault(srv.Engine().DefaultBackend())
	srv.SetServices(local.New(srv.Engine(), loader, jiraCfg))

	log.Printf("singularity daemon listening at %s (pid %d)", listenURL, os.Getpid())

	// Serve in a goroutine; the main goroutine waits on signals.
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	// Signal handling. Per spec, SIGHUP is ignored.
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	signal.Ignore(syscall.SIGHUP)
	defer signal.Stop(sigCh)

	select {
	case sig := <-sigCh:
		log.Printf("received %s, shutting down", sig)
	case err := <-serveErr:
		// Server exited on its own (e.g., listener closed).
		if err != nil {
			cleanupSocket(unixSocketPath)
			return fmt.Errorf("serve: %w", err)
		}
	}

	// Graceful shutdown: HTTP first (5s) so in-flight requests drain,
	// then engine (which signals agents), then socket file, then pidfile
	// (via the deferred release closure).
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		log.Printf("http shutdown: %v", err)
	}
	if eng := srv.Engine(); eng != nil {
		eng.Shutdown()
	}
	// Wait briefly for Serve to return so the listener is closed.
	select {
	case <-serveErr:
	case <-time.After(2 * time.Second):
	}
	cleanupSocket(unixSocketPath)
	return nil
}

func cleanupSocket(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
