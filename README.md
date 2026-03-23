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
| `--repo <path>` | Local | TUI with direct git access for a specific repo |
| `--project-config <path>` | Project | Multi-repo mode with project config file |
| *(none)* | Local | TUI with direct git access in current directory |

## TUI Views

The interface is organized into primary views (F-key access) and git operation views (submenu via `g`):

| View | Access | Description |
|------|--------|-------------|
| **Overview** | `F1` | Repo health dashboard: recent commits, stash/worktree counts, sync status |
| **Branches** | `F2` | Interactive branch list with filtering, checkout, comparison, deletion |
| **Commit** | `F3` | Staging area with AI-powered commit message generation (`ctrl+s` shortcut) |
| **Log** | `F4` | Scrollable commit log with author/message filtering and pagination |
| **Agents** | `F5` | Split-pane agent console: agent list + live streaming output + follow-up input |
| **Sync** | `g,s` | Push, pull, fetch, rebase, force-push, set-upstream operations |
| **Branch Compare** | `g,b` | Two-branch comparison with squash merge detection and tree diff |
| **Stashes** | `g,t` | Stash management: list, apply, pop, drop, create |
| **Rebase** | `g,r` | Interactive rebase UI: pick, reword, edit, squash, fixup, drop |
| **Worktrees** | `g,w` | Worktree manager: create, remove, lock, unlock, prune |
| **Pipeline** | `g,p` | CI/CD status dashboard for GitHub Actions and GitLab CI |
| **Create PR** | `g,c` | MR/PR creation with forge auto-detection, description editor, reviewer selection |
| **Project** | `F1` (multi-repo) | Cross-repo dashboard: expandable branch tree, branch checking, agent sync |

### Navigation

- `F1`-`F5` — primary views
- `g` — git operations submenu
- `Tab` / `Shift+Tab` — cycle views
- `?` — help overlay with all keybindings
- `/` — search/filter in list views
- `Esc` — back/cancel
- Mouse clicks on tab bar supported

All keybindings are configurable via `~/.config/git-frontend/keybinds.json`.

## Features

- **Server-client architecture** — HTTP/WebSocket API daemon with TUI or browser frontends
- **Agent pool engine** — manage concurrent Claude Code subprocesses with structured JSON streaming, follow-up messaging via stdin, cost tracking, and configurable limits
- **Multi-repo project management** — JSON-configured projects with cross-repo branch comparison, status aggregation, context file injection for agents, and expandable branch tree UI
- **Branch comparison** that understands squash merges
- **AI-powered commit messages** via Claude Code integration
- **Native GitHub/GitLab integration** — MR/PR creation, CI/CD pipeline monitoring, forge auth detection
- **Interactive rebase UI** — pick, reword, edit, squash, fixup, drop
- **Stash and worktree management** with branch picker and lock/unlock
- **Configurable keybindings** — global and per-view overrides via JSON
- **Theme support** — dark/light themes with semantic git-aware colors
- **Rich component library** — scrollable lists, filters, modals, spinners, text editor, viewports
- **LRU cache** with TTL for expensive git operations

## Tech Stack

- **Language**: Go 1.24
- **TUI**: [Bubbletea](https://github.com/charmbracelet/bubbletea) (Elm-inspired model-view-update)
- **Components**: [Bubbles](https://github.com/charmbracelet/bubbles)
- **Styling**: [Lipgloss](https://github.com/charmbracelet/lipgloss)
- **WebSocket**: [gorilla/websocket](https://github.com/gorilla/websocket)
- **Git**: Native git CLI wrapper (no libgit2 dependency)
- **Forge**: `gh` and `glab` CLI integration

## Project Structure

```
git-frontend/
├── cmd/git-frontend/        # Entry point & CLI flags
├── internal/
│   ├── app/                 # Bubbletea TUI application
│   │   ├── views/           # 13 view implementations
│   │   ├── components/      # Reusable UI components
│   │   ├── router.go        # View navigation & submenu system
│   │   ├── keybinds.go      # Configurable keybinding manager
│   │   ├── layout.go        # Terminal chrome & tab bar
│   │   └── ws.go            # WebSocket client
│   ├── server/              # HTTP/WebSocket API server
│   ├── engine/              # Claude Code agent pool engine
│   ├── project/             # Multi-repo project management
│   ├── git/                 # Git operations layer
│   ├── client/              # HTTP/WS client library
│   ├── api/                 # Shared request/response types
│   ├── config/              # Application configuration
│   ├── session/             # Session management
│   └── theme/               # Dark/light theme definitions
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
| POST | `/api/agent/message` | Send follow-up message to running agent |
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

### App Config

`~/.config/git-frontend/config.json`:

```json
{
  "theme": { "style": "dark", "accent": "#7C3AED" },
  "git": { "default_branch": "main", "auto_fetch": true, "fetch_interval": 300 },
  "forge": { "default_host": "github" },
  "ai": { "provider": "claude", "commit_style": "conventional" }
}
```

### Keybindings

`~/.config/git-frontend/keybinds.json`:

```json
{
  "global": { "quit": "ctrl+q", "refresh": "R" },
  "views": {
    "branches": { "checkout": "enter", "delete": "d" }
  }
}
```

### Projects

`~/.config/git-frontend/projects.json`:

```json
{
  "projects": [
    {
      "name": "my-project",
      "repos": [
        { "name": "frontend", "path": "~/code/frontend", "default_branch": "main" },
        { "name": "backend", "path": "~/code/backend", "default_branch": "main" }
      ],
      "context_files": ["README.md", "docs/architecture.md"]
    }
  ]
}
```

---

_Made with care by Tane & Asina_
