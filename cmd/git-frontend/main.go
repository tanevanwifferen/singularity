package main

import (
	"flag"
	"fmt"
	"os"

	"git-frontend/internal/app"
	"git-frontend/internal/project"
	"git-frontend/internal/server"
)

// version is set via ldflags at build time by goreleaser.
var version = "dev"

func main() {
	// Parse command line flags
	serverMode := flag.Bool("server", false, "Start in server mode (headless API server)")
	serverAddr := flag.String("addr", "localhost:8080", "Server address (server mode only)")
	repoPath := flag.String("repo", "", "Repository path")
	clientURL := flag.String("client", "", "Connect to server at URL (client mode)")
	projectConfig := flag.String("project-config", "", "Path to project config file (multi-repo)")
	initFlag := flag.Bool("init", false, "Scan current directory for git repos and add as a project to the projects config")
	generateConfigDir := flag.String("generate-config-from-dir", "", "Scan directory for git repos and print a project config JSON to stdout (pipe-friendly)")
	flag.Parse()

	// --init: scan cwd, merge into projects config, exit.
	if *initFlag {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot determine current directory: %v\n", err)
			os.Exit(1)
		}
		cfgPath := project.GetDefaultConfigPath()
		key, count, err := project.InitProjectFromDir(cwd, cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Added project %q with %d repo(s) to %s\n", key, count, cfgPath)
		return
	}

	// --generate-config-from-dir: scan dir, print JSON to stdout, exit.
	if *generateConfigDir != "" {
		cfg, err := project.GenerateConfigFromDir(*generateConfigDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if err := project.PrintGeneratedConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing output: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Server mode
	if *serverMode {
		srv := server.New(*serverAddr, *repoPath)

		// Load project config if provided
		if *projectConfig != "" {
			loader, err := project.NewLoaderFromFile(*projectConfig)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to load project config: %v\n", err)
				os.Exit(1)
			}
			srv.SetProjectLoader(loader)
			fmt.Printf("Project config loaded: %s\n", *projectConfig)
		}

		fmt.Printf("Starting git-frontend server v%s on %s\n", version, *serverAddr)
		fmt.Printf("Repository: %s\n", *repoPath)
		fmt.Println("API endpoints:")
		fmt.Println("  GET  /api/status              - Server status")
		fmt.Println("  POST /api/repo/open            - Open repository")
		fmt.Println("  GET  /api/repo/info            - Get repo info")
		fmt.Println("  POST /api/branch/compare       - Compare branches")
		fmt.Println("  POST /api/branch/diff          - Get branch diff")
		fmt.Println("  POST /api/commit/message       - Generate commit message")
		fmt.Println("  POST /api/mr/create            - Create MR/PR")
		fmt.Println("  GET  /api/forge/auth           - Get forge auth status")
		fmt.Println("  GET  /api/project/list         - List projects")
		fmt.Println("  POST /api/project/load         - Load a project")
		fmt.Println("  GET  /api/project/status       - Get project status")
		fmt.Println("  POST /api/project/refresh      - Refresh project")
		fmt.Println("  POST /api/project/branch/check - Check branch across repos")
		fmt.Println("  POST /api/project/branch/compare - Compare branch across repos")
		fmt.Println("  GET  /api/project/context      - Get project context for agents")
		fmt.Println("  WS   /ws                       - WebSocket for events")
		fmt.Println("\nPress Ctrl+C to stop")

		if err := srv.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Client mode (TUI)
	if *clientURL != "" {
		// Connect to remote server
		fmt.Printf("Connecting to git-frontend server at %s...\n", *clientURL)
		// TODO: Implement remote client mode
		fmt.Println("Remote client mode not yet implemented, starting local TUI")
	}

	// Local TUI mode (default)
	a := app.New()

	// Determine project config path: explicit flag > default location
	projCfg := *projectConfig
	if projCfg == "" {
		defaultPath := project.GetDefaultConfigPath()
		if _, err := os.Stat(defaultPath); err == nil {
			projCfg = defaultPath
		}
	}

	if *repoPath != "" {
		// Explicit --repo always wins: single-repo mode
		a.SetRepoPath(*repoPath)
	} else if projCfg != "" {
		// Project config found: multi-repo mode
		a.SetProjectPath(projCfg)
	}

	if err := a.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running app: %v\n", err)
		os.Exit(1)
	}
}
