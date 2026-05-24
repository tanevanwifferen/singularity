# API Surface Coverage Audit

Snapshot of the existing HTTP + WebSocket surface between `internal/server` and
`internal/client`. Phase A2 deliverable — pure catalog, no design.

Sources inspected:

- `internal/server/server.go` (routes + repo/branch/MR/forge/ws handlers)
- `internal/server/agent_handlers.go`
- `internal/server/project_handlers.go`
- `internal/client/client.go`
- `internal/api/types.go`
- `internal/app/ws.go` (WS consumer / `NewWSViewUpdater`)

---

## 1. HTTP endpoint table

| METHOD | Path | Request type | Response data type | Client method | Purpose |
|---|---|---|---|---|---|
| GET  | `/api/status`                  | —                              | `api.StatusResponse`              | `Client.GetStatus`           | Version + currently-open repo info. |
| POST | `/api/repo/open`               | `api.RepoRequest`              | `*git.RepoInfo`                   | `Client.OpenRepo`            | Set server's active repo path (auto-discovers if `path` empty). |
| GET  | `/api/repo/info`               | query `?path=`                 | `*git.RepoInfo`                   | `Client.GetRepoInfo`         | Read-only repo info for a path. |
| GET  | `/api/repo` *(alias)*          | query `?path=`                 | `*git.RepoInfo`                   | —                            | Browser-friendly alias of `/api/repo/info` (same handler). |
| POST | `/api/branch/compare`          | `api.BranchComparisonRequest`  | `*git.BranchComparison`           | `Client.CompareBranches`     | Ahead/behind/divergence summary between two branches. |
| POST | `/api/branch/diff`             | `api.BranchDiffRequest`        | `*git.BranchDiff`                 | `Client.GetBranchDiff`       | Full textual diff between two branches. |
| POST | `/api/commit/message`          | `api.CommitMessageRequest`     | `*git.CommitMessage`              | `Client.GenerateCommitMessage` | Generate commit message for staged changes. |
| POST | `/api/mr/create`               | `api.MRRequest`                | `*git.MergeRequest`               | `Client.CreateMR`            | Create merge/pull request on the detected forge. |
| GET  | `/api/forge/auth`              | —                              | `*git.ForgeAuth`                  | `Client.GetForgeAuth`        | Detect forge type + credentials. |
| GET  | `/api/project/list`            | —                              | `api.ProjectListResponse`         | —                            | List configured and currently-loaded project keys. |
| POST | `/api/project/load`            | `api.ProjectLoadRequest`       | `project.ProjectStatus` (via `proj.Status()`) | —                | Load a project by key, return its status. |
| GET  | `/api/project/status`          | query `?key=`                  | `project.ProjectStatus`           | —                            | Current status of a loaded project. |
| POST | `/api/project/refresh`         | `api.ProjectLoadRequest`       | `project.ProjectStatus`           | —                            | Re-scan project repos; also broadcasts `project_update`. |
| POST | `/api/project/branch/check`    | `api.ProjectBranchRequest`     | `project.BranchExistence` (`proj.BranchExistsAcross`) | —    | Which repos in the project have a given branch. |
| POST | `/api/project/branch/compare`  | `api.ProjectBranchRequest`     | `project.CrossRepoBranchComparison` | —                          | Cross-repo ahead/behind comparison of a branch. |
| GET  | `/api/project/context`         | query `?key=`                  | `string` (`proj.ContextSummary()`)| —                            | Text summary for Claude Code agent context. |
| POST | `/api/agent/start`             | `api.AgentStartRequest`        | `{session_id: string}`            | —                            | Spawn a Claude Code subprocess agent. |
| GET  | `/api/agent/status`            | query `?session_id=`           | inline anon struct (id/state/work_dir/task/timestamps/error/exit_code) | — | Status of one agent. |
| GET  | `/api/agent/output`            | query `?session_id=` (also `&offset=` documented but ignored) | `{session_id, output}` | — | Buffered output of one agent. |
| POST | `/api/agent/kill`              | `api.AgentQueryRequest`        | —                                 | —                            | Terminate one agent. |
| POST | `/api/agent/input`             | `api.AgentInputRequest`        | —                                 | —                            | Send a message to a running agent's stdin. |
| GET  | `/api/agent/list`              | —                              | `[]agentSummary` (server-local anon struct) | —                  | List all agents (summary form). |
| GET  | `/api/agent/stats`             | —                              | `engine.Stats` (whatever `s.engine.Stats()` returns) | —             | Engine-wide stats. |
| GET  | `/ws`                          | (WebSocket upgrade)            | streamed `api.WSMessage`s         | `Client.Connect` / `Disconnect` / `SendWSMessage` / `Subscribe` / `RefreshRepo` | Bidirectional event channel. |
| GET  | `/health`                      | —                              | `{status: "ok"}`                  | —                            | Liveness probe (not wrapped in `APIResponse`, no CORS). |

Note: every wrapped JSON response is `api.APIResponse{Success, Data, Error}` —
the "Response data type" column above is what lives inside `Data`.

---

## 2. WebSocket events table

Event constants live in `internal/api/types.go`. Client-side dispatch lives in
`internal/app/ws.go` (`NewWSViewUpdater`).

### 2a. Server → client (broadcasts / direct sends)

| Event name | Constant | Producer | Payload shape | Consumer handler (in `NewWSViewUpdater`) |
|---|---|---|---|---|
| `repo_update`     | `WSEventRepoUpdate`     | `Server.BroadcastRepoUpdate`; also `handleWSMessage` on `refresh_repo` | `*git.RepoInfo` | `repo_update` → `WSRepoUpdateMsg` |
| `branch_update`   | `WSEventBranchUpdate`   | `Server.BroadcastBranchUpdate`                                       | `map[string]string{"branch": ...}` | `branch_update` → `WSBranchUpdateMsg` |
| `pipeline_update` | `WSEventPipelineUpdate` | *(none — constant defined but never broadcast)*                       | n/a            | `pipeline_update` → `WSPipelineUpdateMsg` |
| `error`           | `WSEventError`          | `Server.wsSendError`                                                  | `map[string]string{"error": ...}` | *(no dedicated handler in `NewWSViewUpdater`)* |
| `agent_started`   | `WSEventAgentStarted`   | `handleAgentStart`                                                    | `map[string]string{"session_id": ..., "task": ...}` — client decodes as `{agent_id}` (mismatch, see §3) | `agent_started` → `WSAgentEventMsg` |
| `agent_output`    | `WSEventAgentOutput`    | *(none — constant defined, no server broadcast site)*                 | client expects `{agent_id, output, source, timestamp}` | `agent_output` → `WSAgentOutputMsg` |
| `agent_complete`  | `WSEventAgentComplete`  | *(none)*                                                              | client expects `{agent_id}` | `agent_complete` → `WSAgentEventMsg` |
| `agent_error`     | `WSEventAgentError`     | *(none)*                                                              | client expects `{agent_id, error}` | `agent_error` → `WSAgentEventMsg` |
| `project_update`  | `WSEventProjectUpdate`  | `handleProjectRefresh`                                                | `project.ProjectStatus` (sent as `proj.Status()`); client decodes as `{status: ...}` (mismatch, see §3) | `project_update` → `WSProjectUpdateMsg` |
| `subscribed`      | *(no constant)*         | `handleWSMessage` (reply to `subscribe`)                              | `map[string]string{"status": "ok"}` | *(no handler)* |

### 2b. Client → server (incoming WS messages)

Handled in `Server.handleWSMessage` switch on `msg.Type`.

| Event name | Producer (client helper) | Server handling |
|---|---|---|
| `subscribe`    | `Client.Subscribe`           | replies with `subscribed` ack. |
| `refresh_repo` | `Client.RefreshRepo`         | re-opens `s.repoPath`, broadcasts `repo_update`. |
| *(anything else)* | —                         | `wsSendError("unknown message type: …")`. |

---

## 3. Gap analysis

### 3a. Orphan server APIs (server endpoint exists, no client method)

The TUI cannot reach any of these via the SDK today; they're HTTP-only.

- `GET  /api/repo` (alias of `/api/repo/info`)
- `GET  /api/project/list`
- `POST /api/project/load`
- `GET  /api/project/status`
- `POST /api/project/refresh`
- `POST /api/project/branch/check`
- `POST /api/project/branch/compare`
- `GET  /api/project/context`
- `POST /api/agent/start`
- `GET  /api/agent/status`
- `GET  /api/agent/output`
- `POST /api/agent/kill`
- `POST /api/agent/input`
- `GET  /api/agent/list`
- `GET  /api/agent/stats`
- `GET  /health`

Of the 24 HTTP endpoints (counting the `/api/repo` alias and `/health`), the
client SDK covers **8** of them. **16** are orphan.

### 3b. Client methods calling nonexistent endpoints

None. Every path in `internal/client/client.go` (`/api/status`, `/api/repo/open`,
`/api/repo/info`, `/api/branch/compare`, `/api/branch/diff`, `/api/commit/message`,
`/api/mr/create`, `/api/forge/auth`, `/ws`) is registered in
`Server.registerRoutes`. No runtime 404s expected from current SDK calls.

### 3c. WS events referenced by client but never sent by server

Registered in `NewWSViewUpdater` but the server has no broadcast site:

- `agent_output`  — constant exists (`WSEventAgentOutput`) but no `wsBroadcast` call. Engine output never reaches the TUI today.
- `agent_complete` — constant exists, no broadcast site.
- `agent_error`   — constant exists, no broadcast site.
- `pipeline_update` — constant exists, no broadcast site anywhere in the server.

### 3d. WS events sent by server but not handled by client

- `error` (`WSEventError`) — server emits on bad WS frames; `NewWSViewUpdater`
  registers no handler. Currently dropped silently (or hits a generic handler
  only if one is added via `RegisterAllHandler`).
- `subscribed` — ack reply, no client handler (harmless).

### 3e. Payload-shape mismatches (would deserialize to zero values)

These are routed correctly but the JSON keys don't line up; client handlers
will receive empty/zero fields:

- `agent_started`: server sends `{"session_id": ..., "task": ...}`; client
  decodes into `wsAgentIDPayload` (presumably `agent_id`). `AgentID` ends up
  empty in `WSAgentEventMsg`.
- `project_update`: server sends `proj.Status()` directly as payload; client
  decodes into `wsProjectPayload{Status: ...}` expecting an outer `{"status": ...}`
  envelope. `Status` ends up zero-valued.

Flag for the server-coder / client-coder phases — fix by aligning on one shape
(simplest: server wraps these in `{...}` envelopes the client already expects,
or client decodes the bare struct).

---

## 4. Notes

- **Auth / CORS**: no authentication anywhere. `withCORS` wraps every `/api/*`
  route with `Access-Control-Allow-Origin: *` and permissive methods/headers.
  `/ws` accepts any origin (`CheckOrigin` returns `true`). `/health` is *not*
  wrapped in `withCORS` and is not in `APIResponse` form.
- **Method enforcement is inconsistent**. Agent handlers (`start`, `kill`,
  `input`) explicitly reject non-`POST`. The repo/branch/MR/project handlers
  parse JSON unconditionally and will happily try to decode a GET body — they
  rely on `parseJSON`'s `Content-Type` check rather than verb checking.
- **Repo path implicit state.** Many handlers fall back to `s.repoPath` (set
  by `/api/repo/open`) when the request omits one. The server holds
  per-process mutable repo state — relevant to the daemon design (no longer
  one TUI per server).
- **Alias route**: `/api/repo` and `/api/repo/info` map to the same handler;
  documented inline as "alias for browser access."
- **Inline anonymous response types** on the server side (`handleAgentStatus`,
  `handleAgentList`) mean the client can't share a struct with the server even
  if a client method existed. Phase B should promote these to `api/types.go`.
- **`AgentQueryRequest.Offset`** is defined in `api/types.go` and documented
  on `GET /api/agent/output` ("`&offset=0`") but neither handler reads it. Dead
  field for now.
- **`handleProjectLoad` / `handleProjectRefresh`** require `requireProjectLoader`
  (returns 400 if no project config). The TUI currently has no way to inject a
  loader through the server (`SetProjectLoader` is a Go-side setter only).
- **`/health`** returns raw `{"status":"ok"}`, not wrapped in `APIResponse`,
  and skips CORS. Fine for probes but inconsistent with the rest of the API.
- **WebSocket auth**: none. The upgrader allows all origins; anyone reachable
  on the port can subscribe and broadcast-receive every event.
