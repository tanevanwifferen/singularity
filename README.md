# Git Frontend - Mission Control for Your Code

A TUI-based git operations center built in Go. Replaces VS Code for git-centric workflows with a server-client architecture, embedded Claude Code agent pool, and multi-repo project management.

## Architecture

```
┌─────────────────────────────────────┐
│     TUI Client (local mode)         │
│     Browser (remote mode)           │
└──────────────┬──────────────────────┘
               │ HTTP/WebSocket
┌──────────────▼──────────────────────┐
│    API Server (headless daemon)     │
│  - REST endpoints (/api/*)          │
│  - WebSocket (/ws)                  │
│  - Agent pool engine                │
│  - Project management               │
└──────────────┬──────────────────────┘
               │
┌──────────────▼──────────────────────┐
│  Core Layers                        │
│  - Git operations (CLI wrapper)     │
│  - Forge detection (GitHub/GitLab)  │
│  - CLI integration (gh, glab)       │
└─────────────────────────────────────┘
```

### Startup Modes

| Flag | Mode | Description |
|------|------|-------------|
| `--server` | Server | Headless API daemon on `--addr` (default `localhost:8080`) |
| `--client <url>` | Client | TUI connects to remote server |
| *(none)* | Local | TUI with direct git access |

## Features

- **Server-client architecture** — HTTP/WebSocket API daemon with TUI or browser frontends
- **Agent pool engine** — manage multiple concurrent Claude Code subprocesses with lifecycle tracking, streaming output, and configurable limits
- **Multi-repo project management** — JSON-configured projects spanning multiple repositories with cross-repo branch comparison and status aggregation
- **Branch comparison** that understands squash merges
- **AI-powered commit messages** via Claude Code integration
- **Native GitHub/GitLab integration** — MR/PR creation, CI/CD pipeline status, forge auth detection
- **Interactive rebase UI** — pick, reword, edit, squash, fixup, drop
- **Stash and worktree management**
- **LRU cache** with TTL for expensive git operations

## Tech Stack

- **Language**: Go 1.24
- **TUI**: [Bubbletea](https://github.com/charmbracelet/bubbletea) (Elm-inspired model-view-update)
- **Styling**: [Lipgloss](https://github.com/charmbracelet/lipgloss)
- **WebSocket**: [gorilla/websocket](https://github.com/gorilla/websocket)
- **Git**: Native git CLI wrapper (no libgit2 dependency)
- **Forge**: `gh` and `glab` CLI integration

## Project Structure

```
git-frontend/
├── cmd/git-frontend/        # Entry point & CLI flags
├── internal/
│   ├── server/              # HTTP/WebSocket API server
│   ├── engine/              # Claude Code agent pool engine
│   ├── project/             # Multi-repo project management
│   ├── git/                 # Git operations layer
│   ├── app/                 # Bubbletea TUI application
│   ├── client/              # HTTP/WS client library
│   ├── api/                 # Shared request/response types
│   ├── config/              # Application configuration
│   └── session/             # Session management
├── docs/                    # Architecture & decision docs
├── Makefile
├── go.mod
└── go.sum
```

## API Endpoints

### Repository
| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/status` | Server status and version |
| POST | `/api/repo/open` | Open/switch repository |
| GET | `/api/repo/info` | Repository information |

### Branches & Diffs
| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/branch/compare` | Compare two branches (ahead/behind) |
| POST | `/api/branch/diff` | Diff statistics between branches |

### Commits & Merge Requests
| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/commit/message` | Generate commit message via AI |
| POST | `/api/mr/create` | Create MR/PR on GitHub/GitLab |
| GET | `/api/forge/auth` | Forge authentication status |

### Projects (Multi-Repo)
| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/project/list` | List available/loaded projects |
| POST | `/api/project/load` | Load project from config |
| GET | `/api/project/status` | Project status (all repos) |
| POST | `/api/project/refresh` | Refresh all repos |
| POST | `/api/project/branch/check` | Check branch across repos |
| POST | `/api/project/branch/compare` | Compare branch across repos |
| GET | `/api/project/context` | Project context for agents |

### Agent Pool
| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/agent/start` | Start Claude Code agent |
| POST | `/api/agent/message` | Send message to agent |
| GET | `/api/agent/status` | Agent state |
| GET | `/api/agent/output` | Agent output stream |
| POST | `/api/agent/kill` | Terminate agent |
| GET | `/api/agent/list` | List all agents |
| GET | `/api/agent/stats` | Pool statistics |

### WebSocket
| Path | Description |
|------|-------------|
| `/ws` | Real-time events (repo/branch/pipeline updates, agent output) |

## Build & Run

```bash
make build      # Compile to build/git-frontend
make run        # Run directly
make test       # Run all tests
make install    # Install to GOPATH/bin
make fmt        # Format code
make tidy       # go mod tidy
make clean      # Remove build artifacts
```

## Configuration

Projects are configured in `~/.config/git-frontend/projects.json`:

```json
{
  "projects": [
    {
      "name": "my-project",
      "repos": [
        { "name": "frontend", "path": "~/code/frontend", "default_branch": "main" },
        { "name": "backend", "path": "~/code/backend", "default_branch": "main" }
      ]
    }
  ]
}
```

---

_Made with care by Tane & Asina_
