package main

import (
	"flag"
	"fmt"
	"os"

	"git-frontend/internal/app"
	"git-frontend/internal/server"
)

const version = "0.0.1"

func main() {
	// Parse command line flags
	serverMode := flag.Bool("server", false, "Start in server mode (headless API server)")
	serverAddr := flag.String("addr", "localhost:8080", "Server address (server mode only)")
	repoPath := flag.String("repo", "", "Repository path")
	clientURL := flag.String("client", "", "Connect to server at URL (client mode)")
	flag.Parse()

	// Server mode
	if *serverMode {
		srv := server.New(*serverAddr, *repoPath)
		fmt.Printf("Starting git-frontend server v%s on %s\n", version, *serverAddr)
		fmt.Printf("Repository: %s\n", *repoPath)
		fmt.Println("API endpoints:")
		fmt.Println("  GET  /api/status       - Server status")
		fmt.Println("  POST /api/repo/open     - Open repository")
		fmt.Println("  GET  /api/repo/info     - Get repo info")
		fmt.Println("  POST /api/branch/compare - Compare branches")
		fmt.Println("  POST /api/branch/diff   - Get branch diff")
		fmt.Println("  POST /api/commit/message - Generate commit message")
		fmt.Println("  POST /api/mr/create     - Create MR/PR")
		fmt.Println("  GET  /api/forge/auth    - Get forge auth status")
		fmt.Println("  WS   /ws               - WebSocket for events")
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
	if *repoPath != "" {
		a.SetRepoPath(*repoPath)
	}

	if err := a.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running app: %v\n", err)
		os.Exit(1)
	}
}
