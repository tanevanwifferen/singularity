# Git Frontend

A TUI-based git operations center built in Go with a server-client architecture. Combines multi-repo project management, an embedded Claude Code agent orchestrator with smart LLM routing, and Jira integration into a single terminal interface.

## Quick Start

```bash
make build && make install

# Single repo — open the current directory
singularity

# Single repo — explicit path
singularity --repo ~/code/my-project

# Multi-repo project mode
singularity --project-config ~/.config/singularity/projects.json
```

## Core Features

### Agent Orchestrator

An embedded Claude Code agent pool that manages up to 10 concurrent subprocesses from within the TUI. Each agent runs in its own git worktree for full isolation — changes are merged back on completion, so agents never step on each other or your working tree.

- **Structured JSON streaming** — live output with tool calls, results, and cost tracking
- **Follow-up messaging** — send additional instructions to running agents via stdin
- **Worktree isolation** — each agent gets a disposable worktree, auto-merged on success
- **Timeout enforcement** — kill runaway agents after a configurable duration
- **Cross-repo context injection** — in project mode, agents receive context files from all repos in the project so they understand the full system

The agent view (`F5`) provides a split-pane console: agent list on the left, live streaming output on the right.

### Smart LLM Router

Before launching an agent, the router classifies the user's prompt using Haiku (fast and cheap) to determine the optimal model and effort level:

| Classification | Model | Use Case |
|----------------|-------|----------|
| Planning | Opus | Architecture, design, tradeoff analysis, investigation |
| Implementation | Sonnet | Code changes, bug fixes, refactoring, feature work |

The classifier also assigns an effort level (low/medium/high) based on task complexity, which is passed to the agent as context. This means simple fixes don't burn expensive tokens on deep reasoning, while complex architectural decisions get the full weight of Opus.

### Git Project Manager

Project mode (`--project-config`) manages multiple repositories as a single unit. A cross-repo dashboard aggregates branch status, dirty state, and sync health across all repos.

**Workflows** coordinate operations across repos simultaneously:
- **Multi-repo worktrees** — create matching feature branches across all project repos in one action
- **Cross-repo push** — push the same branch across multiple repos
- **Coordinated MR/PR creation** — create merge requests across repos with shared context
- **Branch comparison** — compare a feature branch across all repos, with squash merge detection that compares tree content instead of commit SHAs

Project config is generated automatically by scanning a directory:

```bash
git-frontend --generate-config-from-dir ~/code/my-org
git-frontend --init  # scan current directory
```

### Jira Integration

A built-in Jira client (`g,j` in the TUI) connects to Jira Cloud or Server/Data Center for issue browsing without leaving the terminal.

- **JQL search** — run arbitrary queries from the TUI
- **Issue browser** — view summaries, descriptions, status, priority, assignees, labels, and sprint info
- **Dual auth** — email + API token for Cloud, PAT for Server/Data Center
- **REST API v2/v3** — supports both Jira API versions

### Server-Client Architecture

The app runs in four modes, from local single-user to remote multi-client:

| Flag | Mode | Description |
|------|------|-------------|
| *(none)* | Repo | TUI for the current directory |
| `--repo <path>` | Repo | TUI for a specific repository |
| `--project-config <path>` | Project | Multi-repo dashboard |
| `--server` | Server | Headless REST + WebSocket daemon on `localhost:8080` |
| `--client <url>` | Client | TUI connected to a remote server |

The server exposes a full REST API for repo operations, branch management, commit message generation, MR creation, and project coordination. A WebSocket channel pushes real-time events (branch updates, pipeline status, agent output) to connected clients. Both local and remote TUI modes use the same API surface — in local mode, the server runs in-process.

## Views

### Repo Mode (F1–F6)

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
| Jira | `g,j` | Jira issue browser |

### Project Mode (F1–F3)

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

All keybindings are configurable via `~/.config/singularity/keybinds.json`.

## Configuration

### App Config (`~/.config/singularity/config.json`)

```json
{
  "theme": { "style": "dark", "accent_color": "220" },
  "git": { "default_branch": "main", "auto_fetch": true, "fetch_interval": 60 },
  "forge": { "default_host": "github.com" },
  "ai": { "provider": "claude", "commit_style": "conventional" },
  "jira": { "enabled": false, "base_url": "https://company.atlassian.net" }
}
```

### Keybindings (`~/.config/singularity/keybinds.json`)

```json
{
  "global": { "quit": "ctrl+q" },
  "views": {
    "branches": { "checkout": "enter", "delete": "d" }
  }
}
```

### Projects (`~/.config/singularity/projects.json`)

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
singularity --generate-config-from-dir ~/code/my-org

# Scan current directory and add to projects config
singularity --init
```

## API

The server exposes a REST + WebSocket API. See **[Architecture](docs/architecture.md)** for endpoint reference.


## Build

```bash
make build    # Compile to build/singularity
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
- **Agents**: Claude Code subprocess management

See [tech-decision.md](docs/tech-decision.md) for why Go over Rust.

---

_Made with care by Tane & Asina_
