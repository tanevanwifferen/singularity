// Command singularity is the dual-mode entry point: it runs the daemon
// (`singularity daemon …`) or the bubbletea TUI client (default). The TUI
// auto-spawns the daemon when no explicit --server flag is set and the
// default unix socket is unreachable. See docs/design/DAEMON-LIFECYCLE.md.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/app"
	"gitlab.com/tanevanwifferen1/singularity/internal/daemon"
	"gitlab.com/tanevanwifferen1/singularity/internal/project"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// version is set via ldflags at build time by goreleaser.
var version = "dev"

func main() {
	// Subcommand dispatch. Anything that doesn't match a known subcommand
	// falls through to the default TUI client, which uses its own
	// FlagSet — that's how `singularity --server foo` keeps working.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "daemon":
			os.Exit(runDaemonCmd(os.Args[2:]))
		case "project":
			os.Exit(runProjectCmd(os.Args[2:]))
		case "help", "-h", "--help":
			printUsage()
			return
		case "version", "--version":
			fmt.Printf("singularity %s\n", version)
			return
		}
	}
	os.Exit(runTUI(os.Args[1:]))
}

// printUsage prints the top-level help.
func printUsage() {
	fmt.Fprintf(os.Stderr, `singularity %s — agent-driven git TUI + daemon

Usage:
  singularity [flags]               start the TUI (auto-spawns daemon if needed)
  singularity daemon                 run the daemon in the foreground
  singularity daemon --detach        run the daemon detached, exit when ready
  singularity daemon status          report daemon PID, socket, uptime
  singularity daemon stop            send SIGTERM to the daemon
  singularity daemon init            generate auth token + config template
  singularity project init           scan cwd for git repos → projects config
  singularity project generate-config <dir>
                                     print a project config JSON for <dir>
  singularity version                print version
  singularity help                   this message

Default-mode flags (TUI):
  --server <url>     connect to an explicit daemon endpoint
                     (unix:///path/to/sock, http://host:port, https://host:port)
  --repo <path>      single-repo override (skips project mode)
  --project <key>    project from projects.json to open (default: the project
                     owning the cwd, else the first one)
  --project-config <path>
                     project config file (overrides daemon default)

See docs/design/DAEMON-LIFECYCLE.md for the full lifecycle spec.
`, version)
}

// ---- daemon subcommand ---------------------------------------------------

func runDaemonCmd(args []string) int {
	// Nested subcommands: `daemon status`, `daemon stop`, `daemon init`.
	if len(args) > 0 {
		switch args[0] {
		case "status":
			return runDaemonStatus()
		case "stop":
			return runDaemonStop()
		case "init":
			return runDaemonInit()
		case "-h", "--help", "help":
			fmt.Fprint(os.Stderr, daemonUsage)
			return 0
		}
	}

	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	listen := fs.String("listen", "", "listen spec: unix, unix:///path, tcp://host:port, host:port (default: unix socket)")
	detach := fs.Bool("detach", false, "fork+exec into background, wait for socket, exit 0 when ready")
	projCfg := fs.String("project-config", "", "path to project config file (overrides daemon.json)")
	maxAgents := fs.Int("max-agents", 0, "engine agent cap (0 = config default)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *detach {
		// We're the foreground process invoked with --detach: re-exec
		// ourselves as `daemon` (no --detach), wait for the socket, exit.
		paths := daemon.DefaultPaths()
		if daemon.SocketReachable(paths.Socket) {
			fmt.Fprintln(os.Stderr, "daemon already running")
			return 1
		}
		if err := daemon.Spawn(paths.Socket); err != nil {
			fmt.Fprintf(os.Stderr, "spawn daemon: %v\n", err)
			return 1
		}
		fmt.Printf("daemon listening at %s\n", paths.Socket)
		return 0
	}

	opts := daemon.RunOptions{
		Listen:        *listen,
		ProjectConfig: *projCfg,
		MaxAgents:     *maxAgents,
	}
	if err := daemon.Run(opts); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: %v\n", err)
		return 1
	}
	return 0
}

const daemonUsage = `Usage:
  singularity daemon [--listen <spec>] [--detach] [--project-config <path>] [--max-agents N]
  singularity daemon status
  singularity daemon stop
  singularity daemon init
`

func runDaemonStatus() int {
	info, err := daemon.Status(daemon.DefaultPaths())
	if err != nil {
		if err == daemon.ErrNoDaemon {
			fmt.Println("status:     not running")
			return 1
		}
		fmt.Fprintf(os.Stderr, "status: %v\n", err)
		return 1
	}
	daemon.PrintStatus(info)
	if !info.Alive {
		return 1
	}
	return 0
}

func runDaemonStop() int {
	if err := daemon.Stop(daemon.DefaultPaths(), 10*time.Second); err != nil {
		if err == daemon.ErrNoDaemon {
			fmt.Println("daemon not running")
			return 0
		}
		fmt.Fprintf(os.Stderr, "stop: %v\n", err)
		return 1
	}
	fmt.Println("daemon stopped")
	return 0
}

func runDaemonInit() int {
	p := daemon.DefaultPaths()
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir state dir: %v\n", err)
		return 1
	}
	tok, err := daemon.EnsureToken(p.Token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ensure token: %v\n", err)
		return 1
	}
	if _, err := os.Stat(p.Config); os.IsNotExist(err) {
		template := `{
  "listen": "",
  "default_project_config": "",
  "max_agents": 16
}
`
		if err := os.WriteFile(p.Config, []byte(template), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "write config template: %v\n", err)
			return 1
		}
	}
	fmt.Printf("state dir:  %s\n", p.Dir)
	fmt.Printf("socket:     %s\n", p.Socket)
	fmt.Printf("token:      %s\n", tok)
	fmt.Printf("config:     %s\n", p.Config)
	return 0
}

// ---- project subcommand --------------------------------------------------

func runProjectCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, projectUsage)
		return 2
	}
	switch args[0] {
	case "init":
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot determine current directory: %v\n", err)
			return 1
		}
		cfgPath := project.GetDefaultConfigPath()
		key, count, err := project.InitProjectFromDir(cwd, cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		fmt.Printf("Added project %q with %d repo(s) to %s\n", key, count, cfgPath)
		return 0
	case "generate-config":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: singularity project generate-config <dir>")
			return 2
		}
		cfg, err := project.GenerateConfigFromDir(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		if err := project.PrintGeneratedConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing output: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprint(os.Stderr, projectUsage)
		return 2
	}
}

const projectUsage = `Usage:
  singularity project init                    scan cwd, add a project entry
  singularity project generate-config <dir>   print a project config JSON
`

// ---- default: TUI client -------------------------------------------------

func runTUI(args []string) int {
	fs := flag.NewFlagSet("singularity", flag.ContinueOnError)
	serverURL := fs.String("server", "", "daemon endpoint URL (unix:///..., http://..., https://...)")
	repoPath := fs.String("repo", "", "single-repo override — skips project mode")
	projectKey := fs.String("project", "", "project key from projects.json to open (default: the project owning the cwd, else the first one)")
	projectConfig := fs.String("project-config", "", "project config file (used by --server unset + auto-spawn)")
	_ = projectConfig // currently consumed only by the daemon path; retained for forward-compat
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Resolve the endpoint URL.
	endpoint, err := resolveEndpoint(*serverURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	fmt.Printf("Connecting to singularity daemon at %s...\n", endpoint)
	svc, c, err := buildRemoteServices(endpoint)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	defer c.Disconnect()
	fmt.Println("Connected.")

	a := app.New(svc)
	switch {
	case *repoPath != "":
		// Explicit single-repo override wins over the project config.
		a.SetRepoPath(*repoPath)
	default:
		// Default: load every configured project up front so the TUI can
		// switch between them in memory. Falls through to single-repo mode
		// on the cwd when no project config exists.
		sel, perr := resolveProjects(svc, *projectKey)
		if perr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", perr)
			return 1
		}
		if sel != nil {
			projects := make(map[string]*service.Project, len(sel.Keys))
			for _, key := range sel.Keys {
				projects[key] = service.NewProjectFromInfo(sel.Infos[key])
			}
			a.SetProjects(sel.Keys, projects, sel.Key)
			fmt.Printf("Loaded %d project(s); opening %s.\n", len(sel.Keys), sel.Infos[sel.Key].Name)
		}
	}
	if err := a.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running app: %v\n", err)
		return 1
	}
	return 0
}

// resolveEndpoint figures out which URL to connect to:
//   - explicit --server: trust it; if unreachable, hard error.
//   - no --server:       use default unix socket; if unreachable, auto-spawn.
func resolveEndpoint(serverFlag string) (string, error) {
	if serverFlag != "" {
		return serverFlag, nil
	}
	paths := daemon.DefaultPaths()
	if daemon.SocketReachable(paths.Socket) {
		return "unix://" + paths.Socket, nil
	}
	// Auto-spawn. But first verify no live process is holding the pidfile —
	// a present-but-not-yet-listening daemon means we should wait, not
	// spawn a second one.
	if pid, err := daemon.ReadPID(paths.Pidfile); err == nil && daemon.IsAlive(pid) {
		// Daemon is starting up; wait for socket.
		if werr := daemon.WaitForSocket(paths.Socket, 5*time.Second); werr != nil {
			return "", fmt.Errorf("daemon pid %d is alive but socket not reachable: %w", pid, werr)
		}
		return "unix://" + paths.Socket, nil
	}
	fmt.Println("No daemon running; spawning one...")
	if err := daemon.Spawn(paths.Socket); err != nil {
		return "", fmt.Errorf("auto-spawn daemon: %w", err)
	}
	return "unix://" + paths.Socket, nil
}
