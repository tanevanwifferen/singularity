# Architecture: Server-Client Model

**Date:** 2026-03-21  
**Status:** Implemented

## Overview

Git Frontend has been refactored from a monolithic TUI application into a **server-client architecture** that enables both terminal and web-based interfaces.

```
┌─────────────────────────────────────────────────────────────────┐
│                        Clients                                  │
├─────────────────────┬─────────────────────────────────────────┤
│   TUI Client        │          Web Browser / Electron          │
│  (bubbletea)        │           (REST API + WS)                │
└──────────┬──────────┴─────────────────────────────────────────┘
           │ HTTP / WebSocket
           ▼
┌─────────────────────────────────────────────────────────────────┐
│                      git-frontend Server                        │
├─────────────────────────────────────────────────────────────────┤
│  HTTP Server (Go net/http)                                      │
│  ├── REST API: /api/*                                          │
│  └── WebSocket: /ws                                            │
├─────────────────────────────────────────────────────────────────┤
│                     API Layer                                   │
│  internal/api/types.go  - Shared types                         │
├─────────────────────────────────────────────────────────────────┤
│                   Business Logic                                │
│  internal/git/*.go    - Git operations (unchanged)              │
├─────────────────────────────────────────────────────────────────┤
│                   Data Layer                                    │
│  internal/config/*.go  - Configuration (unchanged)              │
└─────────────────────────────────────────────────────────────────┘
```

## Server Mode

Start the server with:

```bash
git-frontend --server --addr localhost:8080 --repo /path/to/repo
```

The server exposes:

### REST API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/status` | Server status and current repo info |
| POST | `/api/repo/open` | Open a repository |
| GET | `/api/repo/info` | Get repository information |
| POST | `/api/branch/compare` | Compare two branches |
| POST | `/api/branch/diff` | Get file-level diff between branches |
| POST | `/api/commit/message` | Generate a commit message from staged changes |
| POST | `/api/mr/create` | Create a merge/pull request |
| GET | `/api/forge/auth` | Get forge (GitHub/GitLab) auth status |
| GET | `/health` | Health check endpoint |

### WebSocket Events

Connect to `/ws` for real-time updates:

```javascript
// Subscribe to events
ws.send(JSON.stringify({ type: "subscribe" }))

// Request repo refresh
ws.send(JSON.stringify({ type: "refresh_repo" }))

// Receive events
ws.onmessage = (event) => {
  const msg = JSON.parse(event.data)
  switch (msg.type) {
    case "repo_update":
      console.log("Repo updated:", msg.payload)
      break
    case "branch_update":
      console.log("Branch updated:", msg.payload.branch)
      break
    case "error":
      console.error("Error:", msg.payload.error)
      break
  }
}
```

## Client Library

A Go client library is provided for building clients:

```go
import "git-frontend/internal/client"

c := client.NewClient("http://localhost:8080")

// HTTP API calls
status, err := c.GetStatus()
repo, err := c.OpenRepo("/path/to/repo")
comparison, err := c.CompareBranches(path, "main", "feature")
diff, err := c.GetBranchDiff(path, "main", "feature")
msg, err := c.GenerateCommitMessage(path)

// WebSocket for real-time updates
c.SetUpdateHandler(func(event *api.WSMessage) {
    fmt.Printf("Event: %s\n", event.Type)
})
c.Connect()
c.Subscribe()
```

## TUI as Client

The TUI now works in two modes:

1. **Standalone Mode** (default): TUI directly accesses git operations
2. **Server Mode**: TUI runs as a client to the server

To start the server and connect TUI later:

```bash
# Terminal 1: Start server
git-frontend --server --repo /path/to/repo

# Terminal 2: Run TUI (future - not yet implemented)
git-frontend --client ws://localhost:8080
```

## Future: Web Frontend

The architecture enables adding an Electron or web-based frontend:

```
┌─────────────────────────────────────────────────────────────────┐
│                     Web Browser / Electron                      │
├─────────────────────────────────────────────────────────────────┤
│  Web UI (HTML/CSS/JS or React/Vue)                              │
│  └── Connects to git-frontend server via REST + WebSocket       │
└─────────────────────────────────────────────────────────────────┘
```

### Web Integration Example

```javascript
// Fetch repo info
const res = await fetch('http://localhost:8080/api/repo/info?path=/my/repo')
const data = await res.json()

// Get branch diff
const diff = await fetch('http://localhost:8080/api/branch/diff', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ repo_path: '/my/repo', branch_a: 'main', branch_b: 'feature' })
})

// Subscribe to real-time updates
const ws = new WebSocket('ws://localhost:8080/ws')
ws.onmessage = (e) => updateUI(JSON.parse(e.data))
```

## Running Both Server and TUI

For development, you can run the server in one terminal and TUI in another:

```bash
# Terminal 1: Server
cd /home/node/code/git-frontend
go run cmd/git-frontend/main.go --server --repo .

# Terminal 2: TUI (or test API directly)
curl http://localhost:8080/api/status
curl http://localhost:8080/health
```

## Design Decisions

### Why Go Standard Library HTTP Server?

- Minimal dependencies (only adds gorilla/websocket for WS)
- Good enough performance for git operations (I/O bound)
- Easy to embed in other applications
- Simple deployment (single binary)

### Why WebSocket Over HTTP for Real-time?

- Branch status updates require low-latency pushes
- CI pipeline status should update automatically
- Reduces polling overhead
- Natural fit for tea-style message passing

### Why UNIX Socket Option?

For local-only deployments, UNIX sockets provide:
- Better security (only local access)
- No network configuration needed
- Lower latency

Future enhancement: `--socket /path/to/socket` flag

## Module Structure

```
internal/
├── api/
│   └── types.go          # Shared API types (server ↔ client)
├── server/
│   ├── server.go         # HTTP server + WebSocket handling
│   └── handlers.go      # Route handlers (embedded)
├── client/
│   └── client.go         # HTTP + WebSocket client library
├── app/
│   └── app.go            # TUI (can use client or direct git ops)
├── git/
│   └── *.go              # Git operations (unchanged)
└── config/
    └── config.go         # Configuration (unchanged)
```

## Security Considerations

- Server binds to `localhost` by default (not exposed externally)
- WebSocket has no authentication (currently)
- For production: add auth middleware, TLS, rate limiting

## TODO

- [ ] Add authentication to API
- [ ] Support UNIX sockets for local mode
- [ ] Implement remote TUI client mode
- [ ] Add rate limiting
- [ ] TLS support for production deployment
- [ ] Web UI implementation
- [ ] Electron wrapper
