# git-frontend Architecture

## Overview

git-frontend uses a server-client architecture where the API daemon handles all git operations and the TUI (or web browser) consumes them over HTTP/WebSocket.

```
┌─────────────┐    HTTP/WS     ┌──────────────┐
│  TUI Client │◄──────────────►│  API Server  │
└─────────────┘                │  (daemon)    │
                               │              │
┌─────────────┐    HTTP/JSON   │  /api/*      │
│  Browser    │◄──────────────►│  /ws         │
└─────────────┘                └──────┬───────┘
                                      │
                                      ▼
                               ┌──────────────┐
                               │  Git Layer   │
                               │  (internal/  │
                               │   git)       │
                               └──────────────┘
```

## Components

### API Server (`internal/server/`)

Started with `git-frontend --server`. Exposes:

- **REST API** — JSON endpoints under `/api/` for repo info, branch comparison, diffs, commit messages, and MR/PR creation.
- **WebSocket** — `/ws` for real-time events (repo updates, branch changes).
- **Health check** — `/health` for monitoring.

### Client Library (`internal/client/`)

Go client that wraps the HTTP and WebSocket APIs. Used by the TUI when connecting to a remote server (`--client <url>`).

### TUI (`internal/app/`)

Terminal UI built with Bubble Tea. In local mode it calls the git layer directly. In client mode it uses the client library to talk to a remote server.

### Git Layer (`internal/git/`)

Core git operations: repo info, branch comparison, diffs, commit message generation, forge detection, and MR/PR creation.

### Shared Types (`internal/api/`)

Request/response types shared between server and client.

## Startup Modes

| Flag | Mode | Description |
|------|------|-------------|
| `--server` | Server | Headless API daemon on `--addr` (default `localhost:8080`) |
| `--client <url>` | Client | TUI connects to remote server |
| *(none)* | Local | TUI with direct git access |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/status` | Server status and version |
| GET | `/api/repo` | Repository info (JSON) |
| GET | `/api/repo/info` | Repository info (alias) |
| POST | `/api/repo/open` | Open/switch repository |
| POST | `/api/branch/compare` | Compare two branches |
| POST | `/api/branch/diff` | Get diff between branches |
| POST | `/api/commit/message` | Generate commit message |
| POST | `/api/mr/create` | Create merge request |
| GET | `/api/forge/auth` | Forge authentication status |
| WS | `/ws` | Real-time event stream |
