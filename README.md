# Git Frontend

A TUI-based git operations center built in Go. Replaces VS Code for git-centric workflows with a server-client architecture, embedded Claude Code agent pool, and multi-repo project management.

## Quick Start

```bash
make build && make install

# Single repo — open the current directory
git-frontend

# Single repo — explicit path
git-frontend --repo ~/code/my-project

# Multi-repo project mode
git-frontend --project-config ~/.config/git-frontend/projects.json
```

See **[Repo Mode](docs/repo-mode.md)** and **[Project Mode](docs/project-mode.md)** for detailed guides.

## Modes

| Flag | Mode | Description |
|------|------|-------------|
| *(none)* | Repo | TUI for the current directory |
| `--repo <path>` | Repo | TUI for a specific repository |
| `--project-config <path>` | Project | Multi-repo dashboard |
| `--server` | Server | Headless API daemon (default `localhost:8080`) |
| `--client <url>` | Client | TUI connected to a remote server |

## Views

### Repo Mode

| View | Key | Description |
|------|-----|-------------|
| Overview | `F1` | Repo health: recent commits, stash/worktree counts, sync status |
| Branches | `F2` | Branch list with filtering, checkout, comparison, deletion |
| Commit | `F3` | Staging area with AI-powered commit message generation |
| Log | `F4` | Scrollable commit log with author/message filtering |
| Agents | `F5` | Split-pane agent console with live streaming output |
| Config | `F6` | Settings management |

**Git operations submenu** (press `g`):

| View | Key | Description |
|------|-----|-------------|
| Sync | `g,s` | Push, pull, fetch, rebase, force-push |
| Branch Compare | `g,b` | Two-branch comparison with squash merge detection |
| Stashes | `g,t` | Stash management: list, apply, pop, drop, create |
| Rebase | `g,r` | Interactive rebase: pick, reword, squash, fixup, drop |
| Worktrees | `g,w` | Worktree manager: create, remove, lock, unlock |
| Pipeline | `g,p` | CI/CD status for GitHub Actions and GitLab CI |
| Create PR | `g,c` | MR/PR creation with forge auto-detection |
| Jira | `g,j` | Jira issue browser (if enabled) |

### Project Mode

| View | Key | Description |
|------|-----|-------------|
| Project | `F1` | Cross-repo dashboard with expandable branch tree |
| Workflows | `F2` | Feature workflows: multi-repo worktrees, push, MR, agents |
| Agents | `F3` | Agent console with cross-repo context injection |

## Navigation

- `F1`–`F6` — primary views
- `g` — git operations submenu
- `Tab` / `Shift+Tab` — cycle views
- `[` / `]` — cycle repos in project mode
- `/` — search/filter in list views
- `?` — help overlay
- `Esc` — back/cancel
- Mouse clicks on tab bar supported

All keybindings are configurable via `~/.config/git-frontend/keybinds.json`.

## Features

- **Agent pool** — manage concurrent Claude Code subprocesses with structured JSON streaming, follow-up messaging, cost tracking, and worktree isolation
- **Multi-repo project management** — cross-repo branch comparison, status aggregation, context file injection for agents
- **Branch comparison** that understands squash merges by comparing tree content, not commit SHAs
- **AI-powered commit messages** via Claude Code integration
- **Native GitHub/GitLab integration** — MR/PR creation, CI/CD pipeline monitoring, forge auth auto-detection
- **Interactive rebase UI** — full pick/reword/edit/squash/fixup/drop support
- **LRU cache** with TTL for expensive git operations
- **Configurable keybindings** — global and per-view overrides via JSON
- **Dark/light themes** with semantic git-aware colors

## Configuration

### App Config (`~/.config/git-frontend/config.json`)

```json
{
  "theme": { "style": "dark", "accent_color": "220" },
  "git": { "default_branch": "main", "auto_fetch": true, "fetch_interval": 60 },
  "forge": { "default_host": "github.com" },
  "ai": { "provider": "claude", "commit_style": "conventional" },
  "jira": { "enabled": false, "base_url": "https://company.atlassian.net" }
}
```

### Keybindings (`~/.config/git-frontend/keybinds.json`)

```json
{
  "global": { "quit": "ctrl+q" },
  "views": {
    "branches": { "checkout": "enter", "delete": "d" }
  }
}
```

### Projects (`~/.config/git-frontend/projects.json`)

```json
{
  "projects": {
    "my-project": {
      "name": "My Project",
      "repos": [
        { "name": "frontend", "path": "~/code/frontend", "default_branch": "main" },
        { "name": "backend", "path": "~/code/backend", "default_branch": "main" }
      ],
      "context_files": ["README.md", "docs/architecture.md"]
    }
  }
}
```

Generate a project config by scanning a directory:

```bash
# Scan and print config to stdout (pipe-friendly)
git-frontend --generate-config-from-dir ~/code/my-org

# Scan current directory and add to projects config
git-frontend --init
```

## API

The server exposes a REST + WebSocket API. See **[Architecture](docs/architecture.md)** for endpoint reference.

## Build

```bash
make build    # Compile to build/git-frontend
make install  # Install to GOPATH/bin
make test     # Run tests
make fmt      # Format code
make tidy     # go mod tidy
make clean    # Remove build artifacts
```

## Tech Stack

- **Language**: Go 1.24
- **TUI**: [Bubbletea](https://github.com/charmbracelet/bubbletea) — Elm-inspired model-view-update
- **Components**: [Bubbles](https://github.com/charmbracelet/bubbles)
- **Styling**: [Lipgloss](https://github.com/charmbracelet/lipgloss)
- **WebSocket**: [gorilla/websocket](https://github.com/gorilla/websocket)
- **Git**: Native `git` CLI wrapper (no libgit2)
- **Forge**: `gh` and `glab` CLI integration

See [tech-decision.md](docs/tech-decision.md) for why Go over Rust.

---

_Made with care by Tane & Asina_
