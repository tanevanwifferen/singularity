# Daemon/Client Migration Plan

> Canonical brief. Every subagent reads this file before touching code.

## 1. Target architecture

```
   ┌──────────────────────────┐         HTTP+WS         ┌──────────────────────────┐
   │  singularity (TUI)       │  ◄───────────────────►  │  singularityd (daemon)   │
   │  bubbletea views         │   localhost:8080  or    │  owns: engine, project   │
   │  only knows the service  │   unix:~/.config/...    │  loader, all git ops     │
   │  interface package       │                         │  registry, all WS state  │
   └──────────────────────────┘                         └──────────────────────────┘
```

- **Daemon** (`singularity daemon`): long-lived process. Owns the agent `engine.Engine`,
  the `project.Loader`, repo caches, and all subprocess lifecycle. Exposes HTTP+WS API.
- **Client** (`singularity`, default): bubbletea TUI. Holds *no* git/engine/project
  state directly. Talks only to `internal/service` interfaces, which under the hood
  call the HTTP+WS client SDK.
- **Transport**: HTTP/1.1 + WebSocket over TCP *or* Unix domain socket. Default to
  unix socket for same-machine use (faster, no auth needed); TCP when explicitly
  configured for remote (rack-server use case).
- **No more local-direct mode**: even when both daemon and TUI run on the same box,
  the TUI goes through the service abstraction. This is the whole point — the TUI
  becomes location-agnostic.

## 2. Key invariants

1. **One source of truth.** All git/engine/project state lives in the daemon. The
   TUI is a view-only mirror.
2. **Mutations are RPC.** Every action the user takes (create branch, commit,
   start agent, etc.) is an HTTP POST or WS message to the daemon.
3. **Streaming over WS.** Agent output, project status refresh, pipeline status
   changes, etc. — server pushes; client subscribes.
4. **Service interfaces are the contract.** Views import only `internal/service`,
   never `internal/git`, `internal/engine`, `internal/project`, `internal/jira`.
5. **Types stay shared.** `internal/git.RepoInfo` and friends remain in their
   current package (they're already JSON-tagged). Importing types ≠ importing
   operations. Operations are the boundary.

## 3. Layered package layout (target)

```
internal/
  api/         types only — request/response shapes, WS event names
  service/     Go interfaces (RepoService, BranchService, ...)
    local/     impls calling internal/git, internal/engine, ... directly  ← used by daemon
    remote/    impls calling internal/client SDK                          ← used by TUI
  client/      HTTP+WS SDK (one file per capability)
  server/      HTTP+WS handlers (one file per capability) — calls service/local
  git/         (unchanged) git operations + RepoInfo etc. types
  engine/      (unchanged) agent subprocess engine
  project/     (unchanged) multi-repo project loader
  jira/        (unchanged)
  config/      (unchanged)
  daemon/      NEW — daemon lifecycle: pidfile, socket path, supervisor, signal handling
  app/         TUI — views now take service interfaces via constructors
cmd/
  singularity/ TUI client + thin `daemon` subcommand
```

## 4. Phases & dependencies

| Phase | Task IDs | What | Output |
|-------|----------|------|--------|
| **0 Plan** | #1 | this doc | docs/MIGRATION-PLAN.md |
| **A1 Audit views** | #2 | catalog every view→backend call site | docs/audit/CALL-SITES.md |
| **A2 Audit server gap** | #3 | what endpoints exist vs needed | docs/audit/COVERAGE.md |
| **A3 Service interfaces** | #4 | Go interfaces | internal/service/interfaces.go |
| **A4 Daemon lifecycle** | #5 | pidfile/socket/auto-spawn design | docs/design/DAEMON-LIFECYCLE.md |
| **B1 Expand server** | #6 | all handlers | internal/server/*_handlers.go |
| **B2 Expand client SDK** | #7 | all client methods | internal/client/*.go |
| **C1 LocalService** | #8 | service impls calling git/engine | internal/service/local/*.go |
| **C2 RemoteService** | #9 | service impls calling client SDK | internal/service/remote/*.go |
| **D Migrate views** | #10 | views consume service interfaces | internal/app/views/*.go |
| **E Daemon command** | #11 | `singularity daemon` + auto-spawn | internal/daemon/, cmd/singularity/main.go |
| **F Tests** | #12 | integration roundtrip | tests/integration/ |
| **G Review** | #13 | security + quality | review notes |

Phases A and B can overlap once interfaces freeze. C/D dominate the work.

## 5. Subagent coordination protocol

- Lead = main Claude session. Subagents are named (`auditor-views`, `auditor-coverage`,
  `interface-architect`, `daemon-architect`, `server-coder`, `client-coder`,
  `local-coder`, `remote-coder`, `view-migrator-1`, ..., `tester`, `reviewer`).
- Each subagent reads this file, then its referenced inputs, then produces its
  artifact, then `SendMessage`s the lead with a one-paragraph summary.
- Lead reviews artifact, updates task status, kicks the next subagent(s).
- No subagent edits another subagent's artifact. If a downstream subagent finds
  a problem upstream, it `SendMessage`s the lead with the issue; lead decides
  whether to re-spawn upstream or patch downstream.
- Conventions:
  - Go files: `gofmt`-compatible, ≤500 lines, table-driven tests where reasonable.
  - JSON: snake_case keys (matches existing api/types.go).
  - HTTP: REST-ish — `GET /api/<resource>`, `POST /api/<resource>/<action>`.
  - WS events: `<resource>_<verb>` (e.g., `branch_updated`, `agent_output`).
  - All new types live in `internal/api/types.go` (split into files if it grows past 500 lines).

## 6. Open questions (lead resolves before relevant phase)

- **Auth**: server is currently CORS-open + no auth. For remote-over-TCP use,
  need at minimum a shared-secret bearer token. Phase G addresses; daemon listens
  on unix socket by default which sidesteps this for the local case.
- **Auto-spawn daemon**: should `singularity` (no subcommand) start the daemon
  if not running? Pro: zero-config local UX. Con: surprising on a misconfigured
  machine. Decision: yes, auto-spawn on unix-socket default, but exit with clear
  error if `--server <url>` is explicit and unreachable.
- **State persistence**: daemon owns in-memory engine state. If daemon restarts,
  active agents die. Out of scope for this migration; track as follow-up.
- **Backwards compat**: not preserved. This is a single-user project; drop
  `--repo`/`--project-config` in favor of daemon-side configuration. Document the
  new CLI in README.

## 7. Done criteria

- `singularity daemon` starts a daemon listening on default socket.
- `singularity` (no args) connects, presents the TUI, mutations work end-to-end.
- `singularity --server http://rack:8080` connects over TCP.
- `go build ./...` clean, `go test ./...` green.
- No `internal/app/views/*.go` file imports `internal/git`, `internal/engine`,
  `internal/project`, or `internal/jira`.
- Review pass complete; auth at least scaffolded for TCP mode.
