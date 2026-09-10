# Architecture

## Overview

singularity uses a layered architecture. In local mode the TUI talks directly to the git and engine layers. In server mode the TUI (or a browser) talks to a daemon over HTTP/WebSocket.

```
┌─────────────────────────────────────────┐
│  TUI Client (local mode)                │
│  Browser   (remote mode)                │
└──────────────────┬──────────────────────┘
                   │ HTTP / WebSocket
┌──────────────────▼──────────────────────┐
│  API Server  (internal/server/)         │
│  REST /api/* + WebSocket /ws            │
└──────┬──────────────┬────────────────┬──┘
       │              │                │
┌──────▼──────┐ ┌─────▼──────┐ ┌──────▼──────┐
│ Git Layer   │ │ Agent Pool │ │  Project    │
│ internal/   │ │ internal/  │ │  internal/  │
│ git/        │ │ engine/    │ │  project/   │
└─────────────┘ └────────────┘ └─────────────┘
```

## Components

### TUI (`internal/app/`)

Terminal UI built with Bubble Tea (Elm-inspired model-view-update). In local mode it calls the git, engine, and project layers directly. In client mode it uses the HTTP/WS client library to talk to a remote server.

**Sub-packages:**

| Package | Purpose |
|---------|---------|
| `views/` | View implementations (Overview, Branches, Commit, Log, Agents, Flows, Config, Sync, BranchCompare, Stashes, Rebase, Worktrees, Pipeline, CreatePR, Project, Workflows). `Flows` is the adversarial-review view — flow list plus the selected flow's round/step tree — registered right after `Agents` in both modes, so it takes `F6` in repo mode and `F7` in project mode and shifts `Config` one key along. |
| `components/` | Reusable UI primitives: scrollable lists, modals, filter inputs, spinners, text editor, viewport |
| `router.go` | View navigation — maps F-keys, submenu keys, and `Tab` to view switches; handles project vs repo mode routing |
| `keybinds.go` | Configurable keybinding manager — loads from JSON, falls back to defaults |
| `layout.go` | Terminal chrome: tab bar, status bar, help overlay |
| `ws.go` | WebSocket client for remote/server mode |

### API Server (`internal/server/`)

Started with `singularity --server`. Exposes:

- **REST API** — JSON endpoints under `/api/` (see endpoint reference below)
- **WebSocket** — `/ws` for real-time events (repo updates, branch changes, agent output)
- **Health check** — `/health`

The server is also started internally in local mode, so the TUI always speaks the same API regardless of whether it's local or remote.

### Agent Pool Engine (`internal/engine/`)

Manages a pool of Claude Code subprocesses (max 10 by default).

Each agent wraps a `claude` subprocess and provides:
- Structured JSON output streaming (text, tool calls, tool results, errors)
- Follow-up message input via stdin
- Cost tracking per session
- Optional worktree isolation — creates a temp branch, runs the agent, merges back on completion
- Timeout support
- Smart model routing — uses Sonnet to classify the task, then routes to Opus (planning) or Sonnet (implementation)

**Lifecycle states:** `Idle → Routing → Starting → Running → Complete / Error / Killed`

### Project Management (`internal/project/`)

Handles multi-repo project configuration and live status.

| Type | Purpose |
|------|---------|
| `ProjectConfig` | Top-level config structure (map of project keys → definitions) |
| `ProjectDef` | Project definition: repo list, context files |
| `Loader` | Loads and caches projects from the JSON config file |
| `Project` | In-memory representation with live refresh (concurrent across all repos) |
| `Repo` | Single repo within a project with current git info |

Key capabilities:
- Concurrent refresh of all repos
- Cross-repo branch existence check
- Aggregate status (dirty count, error count)
- Context summary for agents (reads `context_files` from each repo)

### Git Layer (`internal/git/`)

All git operations via native `git` CLI wrapper — no libgit2 dependency.

Responsibilities:
- `RepoInfo` — current branch, HEAD, remotes, branch list, dirty state
- Branch comparison with squash merge detection (compares tree content, not commit SHAs)
- Diff engine with ahead/behind counts
- Forge detection — identifies GitHub vs GitLab from remote URL, auto-detects auth via `gh`/`glab`
- MR/PR creation
- LRU cache with TTL for expensive operations

### Adversarial Review Flows (`internal/flow/`)

Drives repeated implement → review → fix rounds over one working directory until
a reviewer accepts the work or a round cap is hit — the case a queue DAG cannot
express, because the round count depends on a verdict that does not exist when
the work is submitted.

A flow is built *on top of* the queue rather than beside it: it owns one queue
(`flow-<flow_id>`) and appends one round's two tasks at a time — a work task
(`implement` in round 1, `fix` afterwards) and a `review` task that depends on
it, both in the flow's single work dir. Worktree isolation is refused, so every
round sees the same tree.

| File | Purpose |
|------|---------|
| `flow.go` | `Flow`, `Round`, `Verdict`, `Finding` and the state enums (wire-first, snake_case tags — `internal/service` and `internal/api` alias them) |
| `verdict.go` | Parser for the reviewer's JSON verdict; unparseable or missing is a reject, never an accept |
| `prompt.go` | Implement / fix / review prompt composition, including the verdict path and prior-round findings |
| `manager.go` | Record keeper: `Start`, `Get`, `List`, `Cancel`, `Remove`, `Tree` over a `TaskQueue` interface the package defines |
| `continue.go` | `Continue`: raises a non-accepted flow's cap and puts it back to `running`, settling a trailing round that never reached a verdict |
| `reconcile.go`, `rounds.go` | The reconciler — submits rounds, reads verdicts, applies the one re-review, settles the flow |
| `store.go`, `restore.go` | One JSON file per flow under `<state-dir>/flows`, plus a verdict dir each; restored on daemon start |

**Flow states:** `pending → running`, then `accepted`, `rejected` (cap reached),
`errored` or `cancelled`. `Continue` is the only edge back out of a terminal
state: from `rejected`, `errored` or `cancelled` to `running`, with round
numbering carrying on. Progress reaches clients as `flow_updated` WS frames
carrying the whole flow, plus the ordinary `queue_task_changed` frames its tasks
produce.

### Configuration (`internal/config/`)

Loads from `~/.config/singularity/config.json`. Supports:
- Multiple named profiles
- Theme customization (dark/light, accent colors)
- Git settings (default branch, auto-fetch interval)
- AI provider settings
- Jira integration
- Environment variable overrides (e.g. `JIRA_API_TOKEN`)

### Client Library (`internal/client/`)

Go HTTP/WS client used by the TUI when connecting to a remote server (`--client <url>`). Wraps all REST endpoints and the WebSocket stream.

### Shared Types (`internal/api/`)

Request/response types shared between server and client to avoid duplication and keep the API contract explicit.

### Session (`internal/session/`)

Session state management — tracks active repo, active project, and per-session settings.

### Theme (`internal/theme/`)

Dark and light theme definitions with semantic git-aware color roles (clean, dirty, ahead, behind, conflict, etc.).

## Startup Flow

```
main()
├── Parse flags
├── --init / --generate-config-from-dir → run, exit
├── --server → start API daemon (+ optional project config)
├── --client <url> → start TUI in remote mode
└── default → start TUI in local mode
    ├── --repo <path>   → repo mode (explicit path)
    ├── project config exists at default path → project mode
    └── cwd is a git repo → repo mode (current directory)
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
| GET | `/api/project/status` | Status for all repos in project |
| POST | `/api/project/refresh` | Refresh all repos |
| POST | `/api/project/branch/check` | Check branch existence across repos |
| POST | `/api/project/branch/compare` | Compare branch across repos |
| GET | `/api/project/context` | Project context for agents |

### Agent Pool
| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/agent/start` | Start a Claude Code agent |
| POST | `/api/agent/message` | Send follow-up message to running agent |
| GET | `/api/agent/status` | Agent state |
| GET | `/api/agent/output` | Agent output stream |
| POST | `/api/agent/kill` | Soft-close agent (process keeps running for follow-ups) |
| POST | `/api/agent/terminate` | Kill agent process, keep its record |
| GET | `/api/agent/list` | List all agents in pool |
| GET | `/api/agent/stats` | Pool statistics |

### Adversarial Review Flows
| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/flow/start` | Start a flow (work dir, goal, review goal, max rounds, work/review opts) |
| POST | `/api/flow/continue` | Give a flow that finished unaccepted more rounds against the same goal: `rounds` is an increment on its cap (0 = 3), the record is extended rather than forked (409 for an accepted or still-running flow, 400 past the 20-round ceiling or if the work dir has gone) |
| GET | `/api/flow/list` | List flows, optionally filtered by `state` |
| GET | `/api/flow/get` | One flow by `flow_id`, rounds and verdicts included |
| GET | `/api/flow/tree` | Flat, parent-linked node list: flow → rounds → steps, with task and agent state |
| POST | `/api/flow/cancel` | Cancel the flow and the tasks it created |
| POST | `/api/flow/remove` | Forget a terminal flow's record and its verdict dir (409 while it is still active) |

### WebSocket
| Path | Description |
|------|-------------|
| `/ws` | Real-time events: repo/branch/pipeline updates, agent output, `queue_task_changed`, `flow_updated` |

## Project Structure

```
singularity/
├── cmd/singularity/        # Entry point & CLI flags
├── internal/
│   ├── app/                 # Bubbletea TUI application
│   │   ├── views/           # View implementations
│   │   ├── components/      # Reusable UI components
│   │   ├── router.go        # View navigation & submenu system
│   │   ├── keybinds.go      # Configurable keybinding manager
│   │   ├── layout.go        # Terminal chrome & tab bar
│   │   └── ws.go            # WebSocket client
│   ├── server/              # HTTP/WebSocket API daemon
│   ├── engine/              # Claude Code agent pool engine
│   ├── flow/                # Adversarial review flows (implement → review → fix rounds)
│   ├── project/             # Multi-repo project management
│   ├── git/                 # Git operations (CLI wrapper)
│   ├── client/              # HTTP/WS client library
│   ├── api/                 # Shared request/response types
│   ├── config/              # Application configuration
│   ├── session/             # Session management
│   ├── theme/               # Dark/light theme definitions
│   └── jira/                # Jira integration
├── docs/
│   ├── architecture.md      # This file
│   ├── repo-mode.md         # Single-repo usage guide
│   ├── project-mode.md      # Multi-repo project mode guide
│   └── tech-decision.md     # Why Go over Rust
├── Makefile
├── go.mod
└── go.sum
```
