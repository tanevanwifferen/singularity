# Phase G Review

Reviewer: `reviewer` (read-only). Codebase: daemon/client migration, post-Phase F. Build is green; this pass surfaces issues, it does not fix them.

---

## Summary

**Counts:** 2 P0, 9 P1, 12 P2.

### P0 findings (one line each)

1. **No auth in TCP mode** — the daemon binds `tcp://` with zero authentication; the generated token is never verified anywhere. (`internal/server/server.go:204`, `internal/daemon/token.go`)
2. **All streaming subscriptions die immediately** — every `*Subscribe*` handler passes `r.Context()` to the service layer. `http.Request.Context()` is canceled when the handler returns, so the poller goroutine exits before the client can subscribe to the stream. The whole streaming surface (agent output, project status, sync progress, discovery, workflow, repo, pipeline) is non-functional. (`internal/server/agent_handlers.go:240,255`, `internal/server/project_handlers.go:177,258,281`, `internal/server/repo_handlers.go:83`, every other `*Subscribe`/stream handler)

### P1 findings (one line each)

1. CORS is wide-open (`Access-Control-Allow-Origin: *`) on TCP mode — any browser on any origin can drive the daemon. (`internal/server/server.go:207`)
2. `wsUpgrader.CheckOrigin` returns `true` unconditionally — CSRF / drive-by-WS from any origin. (`internal/server/server.go:89`)
3. `wsBroadcast` / `wsSend` / `wsHeartbeat` issue concurrent writes to the same `*websocket.Conn` without a per-conn write mutex; gorilla explicitly forbids that. Heartbeat + broadcast + stream pump can race. (`internal/server/ws_handlers.go:56,146,166`, `internal/server/streams.go:101`)
4. `srv.Shutdown` does not close existing WS connections, so graceful shutdown hangs for the full 5s timeout on every daemon stop. (`internal/daemon/cmd.go:162`, `internal/server/server.go:166`)
5. Per-agent poller in `localAgentService.startPoller` is a hard 100 ms tick with no exit on engine shutdown — it only exits when ctx cancels. Combined with P0/2 this is fine today; once that bug is fixed the poller needs to learn about `engine.Shutdown`. (`internal/service/local/agent.go:204`)
6. No request body size cap anywhere; an authenticated client can OOM the daemon by POSTing arbitrary JSON. (`internal/server/server.go:470`)
7. No path-traversal validation on `repo_path` / `path` parameters; TCP-mode callers can drive git operations against arbitrary filesystem paths. (`internal/server/branch_handlers.go:14`, every handler that calls `resolveRepoPath` or accepts `repo_path`)
8. README still advertises the dropped `--server` / `--client <url>` flags and the `localhost:8080` server-mode. Real users will copy stale instructions. (`README.md:113-114`)
9. Transitional `service.Project` / `service.Loader` aliases are imported by 17 view sites — the migration invariant "views never touch `internal/project`" leaks through these aliases. (`internal/service/types.go:246-255`, 17 usages under `internal/app/views/`)

### P2 findings (one line each)

1. `--server` reachability check happens in `buildRemoteServices`, not `resolveEndpoint`; the auto-spawn comment in main.go is correct but the layering is awkward. (`cmd/singularity/main.go:282`, `cmd/singularity/client_mode.go:26`)
2. `daemon status` exit codes don't distinguish "not running" from "error" — both return 1. Spec section H suggests 0/1/2. (`cmd/singularity/main.go:132-146`)
3. `daemon init` has no `--force` flag, so token rotation as documented in DAEMON-LIFECYCLE §11 is impossible without manual file removal. (`cmd/singularity/main.go:162`, `internal/daemon/token.go:13`)
4. Pidfile uses default O_EXCL fine, but no `flock` — on NFS or other filesystems where O_EXCL is unreliable, two daemons could race. Out of scope but worth a comment.
5. Daemon-side `maxAgents` from `RunOptions`/config is discarded because the engine is constructed inside `server.New(addr, repoPath)` with a hardcoded `engine.New(10)`. (`internal/daemon/cmd.go:126`, `internal/server/server.go:95`)
6. `s.repoPath` is per-process mutable state on the server, set by `/api/repo/open` and used as a fallback by every `resolveRepoPath` call. In a multi-tab TUI scenario one tab's open mutates the implicit context for the other. (`internal/server/server.go:36-37`, `internal/server/repo_handlers.go:34`)
7. `handleProjectBranchCompare` returns 503 UNAVAILABLE with a "not yet wired" message — this is an orphan endpoint and should either be removed from registration or have a documented follow-up issue tag. (`internal/server/project_handlers.go:133`)
8. `wsBroadcast` swallows write errors and keeps the dead conn in `s.wsClients`. The conn is only removed when `wsReader` returns (i.e. the next read error). Worst case the broadcast loop keeps trying to write to a half-closed socket. (`internal/server/ws_handlers.go:165`)
9. `EnsureToken` returns the token contents verbatim (including any trailing newline) on subsequent calls; if a user edits the file with `echo`, the trailing `\n` becomes part of the token. Trim on read. (`internal/daemon/token.go:13-16`)
10. TODOs in `internal/service/local/repo.go:42` and `internal/service/local/pipeline.go:13` describe missing fsnotify/polling — fine to leave but track as a follow-up. The Subscribe methods today return `ErrUnavailable`.
11. Spawned-goroutine panic risk: `pumpStream` and `startPoller` have no `defer recover()`. Net/http catches handler panics, but goroutines you spawn yourself crash the daemon on panic. (`internal/server/streams.go:64`, `internal/service/local/agent.go:186`)
12. `daemon.json` schema permits only three fields and rejects nothing (json.Unmarshal silently ignores unknowns). DAEMON-LIFECYCLE §9 promised strict YAML; the daemon doc and the code disagree. (`internal/daemon/cmd.go:42-62`)

---

## A. Authentication / authorization

### A.1 (P0) — TCP mode is unauthenticated; the `token` apparatus is dead code

**Location:** `internal/server/server.go:204` (`registerRoutes`), `internal/daemon/token.go`, `internal/daemon/cmd.go:96-138` (`Run` never reads the token), `internal/client/client.go:226-238` (no `Authorization` header is ever set).

**Description:** `daemon init` generates a 32-byte hex token at `~/.config/singularity/token` (mode 0600). Nothing on the server side ever reads that file. `registerRoutes` wraps every endpoint in `wrap()` which sets permissive CORS and dispatches — there is no auth middleware. `Client.doRequest` builds an `http.NewRequestWithContext` but never adds `Authorization: Bearer`. DAEMON-LIFECYCLE §2 explicitly requires bearer-token auth for TCP mode and says the daemon "refuses to start with a non-zero exit" if the token is missing. None of that is implemented. For unix-socket mode the UID gate is acceptable per the spec; for TCP it is a P0 security bug — anyone with network access to the port has full control of the user's agents, git operations, repo paths, and Jira credentials.

**Suggested fix:** Add `internal/server/middleware.go` with a `requireToken(h http.HandlerFunc) http.HandlerFunc` that loads `~/.config/singularity/token` (or `SINGULARITY_TOKEN`) at server construction, performs constant-time comparison on `Authorization: Bearer …`, and returns 401 + `PERMISSION_DENIED` on mismatch. Wire it in `registerRoutes` *only* when `Server.requireAuth` is true (set by the daemon when the listener is TCP). Symmetrically, `Client.NewClient` should accept a token (env / flag / file) and inject the header via a wrapping `http.RoundTripper`. Refuse to start the daemon with `--listen tcp://…` when no token file exists.

### A.2 (P1) — `wsUpgrader.CheckOrigin` returns `true`

**Location:** `internal/server/server.go:89`.

**Description:** Every WS upgrade is accepted regardless of `Origin`. Once TCP mode is authenticated (A.1) this becomes a CSRF vector — a browser on `evil.example` can open a WS to `http://rack:8420/ws` and replay the user's cookies/credentials (there aren't any today, but the open posture is wrong). DAEMON-LIFECYCLE §11 promises `CheckOrigin` returns false unless origin matches `Host`.

**Suggested fix:** Implement `CheckOrigin` to allow unix-socket peers unconditionally and TCP peers only when `Origin`'s host matches `Host` (or matches an allow-list flag). Same `requireAuth` gate as A.1.

### A.3 (P1) — CORS `*` on every route

**Location:** `internal/server/server.go:207-209`.

**Description:** `wrap` unconditionally writes `Access-Control-Allow-Origin: *` plus `Allow-Headers: Content-Type, Authorization`. The unix-socket case is harmless (no browser can reach AF_UNIX). The TCP case lets any origin make pre-flighted POSTs with credentials.

**Suggested fix:** When listener is unix, skip CORS entirely (no browser path). When TCP, mirror the request's `Origin` only if it appears on an allow-list; otherwise omit the header so the browser refuses the request.

---

## B. Input validation at the boundary

### B.1 (P1) — No path-traversal validation on `repo_path` / `path` / `work_dir`

**Location:** every handler that calls `s.resolveRepoPath(req.RepoPath)` or reads `path` from query params. Examples: `internal/server/repo_handlers.go:43`, `internal/server/branch_handlers.go:14,33,50,…`, `internal/server/agent_handlers.go:22-28` (agent start `project_path`).

**Description:** A TCP-mode authenticated client can pass `/etc`, `../../`, `/var/log/secret`, etc., and the daemon will run `git OpenRepo`/`git status` against it. Even when not a repo, the read happens. Unix-socket mode again sidesteps this because the kernel UID-gates the connection, but the daemon should not assume that.

**Suggested fix:** Add a helper `validateRepoPath(p string) error` that (a) rejects empty, (b) calls `filepath.Clean`, (c) rejects paths containing `..` after cleaning, (d) optionally constrains to a list of allowed roots from `daemon.json` (e.g. `~/code` and configured project repos). Call it at the top of every handler that takes a path.

### B.2 (P1) — JSON request bodies are unbounded

**Location:** `internal/server/server.go:470-475` (`parseJSON`).

**Description:** `json.NewDecoder(r.Body).Decode(v)` reads the entire body, no `http.MaxBytesReader`. An authenticated client can POST a multi-GB JSON blob to any endpoint and OOM the daemon. WS frames are also unbounded; gorilla's default read limit is also unconfigured.

**Suggested fix:** In `parseJSON`, wrap `r.Body = http.MaxBytesReader(w, r.Body, 1<<20)` (1 MiB; bigger for `/api/diff/*` if needed). On WS, set `conn.SetReadLimit(1 << 20)` after upgrade. Use `DisallowUnknownFields` on the decoder while you're there.

### B.3 (P2) — `subscribe_stream` accepts arbitrary IDs but bounds them by the registry

**Location:** `internal/server/ws_handlers.go:99-115`.

**Description:** A malicious client sending many `subscribe_stream` for fake IDs gets rejected with `unknown stream_id`; no memory growth. Real growth vector would be repeatedly POSTing `/api/agent/subscribe_all` (each call allocates a stream registry entry + goroutine). Today P0/2 means the goroutine exits immediately on handler return so this self-cleans, but once P0/2 is fixed an attacker could DoS by spawning thousands of streams.

**Suggested fix:** When P0/2 is fixed, cap `len(s.streams)` per WS connection (e.g. 32) and reject further `*Subscribe` POSTs from the same conn / token. Tie stream lifetime to the WS connection that subscribed (not the POST that created it) and tear down on disconnect.

### B.4 (P2) — `EnsureToken` returns raw file bytes without trim

**Location:** `internal/daemon/token.go:14`.

**Description:** If a user edits the token file and leaves a trailing `\n`, the in-memory token becomes `"abcdef\n"`; the client (once it learns to send headers) will send `Authorization: Bearer abcdef\n`. Compare-equal will fail. The handler comparison MUST also trim, but be defensive on the daemon side too.

**Suggested fix:** `strings.TrimSpace(string(data))` before returning, and document that the token file may include trailing whitespace.

---

## C. Panic safety in handlers

### C.1 (P2) — `pumpStream` / `startPoller` goroutines have no `defer recover()`

**Location:** `internal/server/streams.go:64`, `internal/service/local/agent.go:186-255`.

**Description:** `net/http`'s `conn.serve` wraps each handler in a recover (`go/src/net/http/server.go`), so a handler panic is logged and the conn closed. Goroutines we spawn ourselves are NOT covered. A panic in `pumpStream` (e.g. a nil deref in `frameOf`) crashes the daemon. Same for the poller goroutine and the engine `OnAgentUpdate` callback (`broadcastAgentUpdate`).

**Suggested fix:** Add a tiny helper `safeGo(label string, fn func())` that wraps `defer func(){ if r := recover(); r != nil { log.Printf("panic in %s: %v\n%s", label, r, debug.Stack()) } }()` and use it for every `go pumpStream(...)`, the poller goroutine, and the engine update broadcaster.

---

## D. Goroutine + resource leaks

### D.1 (P0 — see Summary item 2) — Stream subscriptions die on handler return

Already described in A.0 above. The technical leak risk is the inverse of what one would expect: the goroutines exit cleanly *too soon*, so nothing leaks, but the streaming API is non-functional.

**Suggested fix:** Decouple the subscription lifetime from the request context. In every `*Subscribe` HTTP handler, pass `context.Background()` (or a daemon-wide ctx) to the service `Subscribe`, hold the cancel closure in `streamEntry`, and only invoke the cancel from (a) explicit `cancel_stream` WS msg, (b) terminal frame in `pumpStream`, (c) `Server.Shutdown`. Tie liveness to a WS connection: when the conn that subscribed via `subscribe_stream` closes, cancel all its streams. Document the new ownership rule at the top of `streams.go`.

### D.2 (P1) — `srv.Shutdown` hangs on WS clients

**Location:** `internal/daemon/cmd.go:160-167`, `internal/server/server.go:166-171`.

**Description:** `http.Server.Shutdown` waits for hijacked connections to return from `ServeHTTP`. WS handlers block on `conn.ReadMessage` forever. Without explicit `conn.Close()` on shutdown, the 5-second `shutdownCtx` always trips, the daemon waits the full timeout, then proceeds to `engine.Shutdown`. Tested daemons therefore take 5s to stop even with no work in flight.

**Suggested fix:** In `daemon.Run` right before `srv.Shutdown`, walk `s.wsClients` and `conn.Close()` each; or expose `Server.CloseWSClients()` and call it. The deferred goroutine in `handleWebSocket` will then drop them from `wsClients`. Also register an `OnShutdown` hook to send a close frame so well-behaved clients see a clean termination.

### D.3 (P1) — Concurrent writes on the same `*websocket.Conn`

**Location:** `internal/server/ws_handlers.go:48-60` (heartbeat goroutine), `:146` (`wsSend`), `:166` (`wsBroadcast`), `internal/server/streams.go:101` (`broadcastStreamFrame`).

**Description:** gorilla/websocket documents that "applications are responsible for ensuring that no more than one goroutine calls the write methods … concurrently". The heartbeat goroutine writes pings on a ticker. `wsBroadcast` is invoked from request handlers and from the engine `OnAgentUpdate` callback. `broadcastStreamFrame` is called from per-stream pump goroutines. There is no per-connection write mutex; they can interleave and corrupt the frame stream or, worse, race the underlying TLS state on TCP mode.

**Suggested fix:** Wrap each `*websocket.Conn` in a `wsClient` struct with its own `sync.Mutex`; every write site (`wsSend`, `wsBroadcast`, `broadcastStreamFrame`, `wsHeartbeat`) takes that lock before `WriteMessage`/`WriteControl`. Store `*wsClient` (not `*websocket.Conn`) in `wsClients` and `streamEntry.subscribers`.

### D.4 (P1) — `localAgentService` poller has no engine-shutdown hook

**Location:** `internal/service/local/agent.go:204-255`.

**Description:** Independent of P0/2, when the daemon shuts down `engine.Shutdown` runs but the poller goroutines (if any are alive) will keep ticking until their parent ctx cancels. In foreground daemons that ctx IS the request ctx, so it cancels naturally — see D.1 caveat. Once streams are properly lifecycle-managed (per D.1's fix), the poller needs an explicit "engine stopped" signal or it'll keep calling `s.eng.GetAgent` on a torn-down engine.

**Suggested fix:** Have the local-services constructor take an explicit `done <-chan struct{}` that `Run` closes during shutdown, and add `case <-done: return` alongside `<-cctx.Done()` in the poller's select.

### D.5 (P1) — Socket / pidfile after SIGKILL

**Location:** `internal/daemon/cmd.go:69-175`, `internal/daemon/listener.go:62-71`, `internal/daemon/pidfile.go:23-55`.

**Description:** SIGKILL bypasses defers. On the next start, the leftover pidfile is detected as stale by `Acquire` (process dead, ESRCH) and removed; the leftover socket is detected by `listenUnix`'s pre-bind sweep and removed. So restart works. Verified: this is handled.

**No fix needed.** Noted in case anyone is told otherwise; the implementation matches DAEMON-LIFECYCLE §3.

---

## E. Error propagation correctness

### E.1 — Sentinel round-trip is correct

`internal/service/errors.go` defines 10 sentinels (`ErrMRAlreadyExists`, `ErrNotFound`, `ErrConflict`, `ErrAgentLimit`, `ErrNoForge`, `ErrRebaseInProgress`, `ErrNoRebaseInProgress`, `ErrPermissionDenied`, `ErrUnavailable`, `ErrCanceled`). `internal/api/errors.go` defines 12 codes (the 10 above + `BAD_REQUEST` and `INTERNAL`). `server.codeForServiceErr` maps all 10 sentinels via `errors.Is`. `client.mapError` reconstructs all 10 with `fmt.Errorf("%s: %w", msg, sentinel)`. Views can therefore `errors.Is(err, service.ErrXxx)` end-to-end. Wrap uses `%w` (not `%v`) — verified.

**No finding.**

### E.2 (P2) — `daemon.json` schema mismatch with DAEMON-LIFECYCLE §9

**Location:** `internal/daemon/cmd.go:42-46`.

**Description:** Spec promises YAML with `yaml.UnmarshalStrict` rejecting unknown keys; implementation uses JSON `Unmarshal` which silently drops unknown keys. Also missing fields: `log_path`, `log_level`, `log_format`, `tls.*`, `auth.token_file`. Either the doc lies or the code does.

**Suggested fix:** Either narrow the doc to match (JSON, three fields, unknowns ignored) or implement the spec. The latter is roughly 30 lines of yaml.v3 + a `Strict` option.

---

## F. Concurrency / data-race risk

### F.1 (P1) — See D.3 (WS write race)

### F.2 — `streams` and `wsClients` map locking

Reviewed: every read/write of `s.streams` is under `streamMu`; every read/write of `s.wsClients` is under `wsMux`; every read/write of `agentOutputOffsets` is under `outputMu`; every per-entry `subscribers` map is under `streamEntry.mu`. One subtle point: `handleWebSocket`'s defer first takes `wsMux`, then walks streams taking each `streamEntry.mu` while holding `streamMu`. If anything else acquires those locks in reverse order there's a deadlock window — but only `cancelStream` touches `streams` and it doesn't take `entry.mu`, so the order is consistent.

**No finding.**

### F.3 (P2) — Engine `OnAgentUpdate` callback contention

**Location:** `internal/server/engine_callbacks.go:20`, `internal/engine/engine.go:395-405`.

**Description:** The engine documents "non-blocking" callbacks. `broadcastAgentUpdate` does `wsBroadcast` (which holds `wsMux.RLock` and writes to every conn synchronously) and reads `agentOutputOffsets`. With many clients on TCP, a slow client can stall the engine notifier and back up the engine. Combined with D.3 (no per-conn write lock) the consequences are amplified.

**Suggested fix:** Make `wsBroadcast` enqueue to a per-conn buffered channel drained by one writer goroutine per conn. The engine callback returns instantly.

---

## G. CLI UX

### G.1 — `--server` unreachable: hard error confirmed

`cmd/singularity/main.go:282-298` → `buildRemoteServices` → probes `/api/status` with 5s timeout, returns `daemon unreachable at …` error which `runTUI` prints and exits 1. Matches DAEMON-LIFECYCLE §3.

**No finding.**

### G.2 — Auto-spawn race

Two TUIs launching in parallel: both check `SocketReachable` (false), both check `IsAlive(pid)` (no pidfile yet → false), both call `Spawn`. Each spawn re-execs `singularity daemon`. The child wins the `O_CREAT|O_EXCL` race in `Acquire`; the loser child exits with `ErrDaemonAlreadyRunning` (logged to `daemon.log`). Both parents then poll the socket; both find it (winner created it). Both connect. Edge case: loser child runs `daemon.Run` past `Acquire` failure → exits immediately, parent's `Spawn` polls up to 5s, sees winner's socket appear, returns nil. Safe.

**No finding.** Worth a `// Race-safe via O_EXCL` comment in `resolveEndpoint`.

### G.3 — `daemon stop` against non-running daemon

`internal/daemon/stop.go:17-26`: `ReadPID` returns IsNotExist error → `ErrNoDaemon` → main.go:151 prints "daemon not running" and exits 0. Clean.

**No finding.**

### G.4 (P2) — `daemon status` exit codes don't follow `0=running, 1=not running, 2=error`

**Location:** `cmd/singularity/main.go:132-146`.

**Description:** Spec H says 0/1/2. Code: both `ErrNoDaemon` and any other error return 1. There is no path to 2.

**Suggested fix:** `if err == ErrNoDaemon { return 1 }` (not running) and `else { fmt.Fprintf(stderr, ...); return 2 }` (error). Distinguishes operator-relevant failures.

### G.5 (P2) — `daemon init` cannot rotate the token

**Location:** `cmd/singularity/main.go:162`, `internal/daemon/token.go:13`.

**Description:** DAEMON-LIFECYCLE §11 says "`daemon init --force` regenerates". Neither the flag nor the regeneration is implemented. Users have to `rm token` manually.

**Suggested fix:** Add a `force` boolean to the `init` subcommand parsing; pass it to a new `EnsureTokenForce(path string, force bool)` that always overwrites when force is set.

---

## H. Invariant compliance (MIGRATION-PLAN §7)

### H.1 — Views do not import git/engine/project/jira

```
grep -rE '"gitlab.com/.../(git|engine|project|jira)"' internal/app/
```

returns no matches. Verified.

**No finding.**

### H.2 (P1) — Transitional aliases leak the project package into views

**Location:** `internal/service/types.go:238-255`, 17 usages in `internal/app/views/{project*,workflows,jira}.go`.

**Description:** The aliases `service.Project = project.Project` and `service.Loader = project.Loader` are explicitly flagged in the source as "TODO(phase-D-followup)". Because they're type aliases, views holding a `*service.Project` are holding the *real* `*project.Project` and can call any of its methods — i.e. they're invoking git/engine/project operations directly, just laundered through a `service.*` symbol. This violates Invariant 4 ("Service interfaces are the contract").

**Suggested fix:** Promote `ProjectHandle` to the only thing views see. Replace every `proj *service.Project` field with `handle service.ProjectHandle` + a cached `*service.ProjectInfo`. Move every method call (`proj.Refresh`, `proj.Repos`, `proj.Status`, `proj.BranchExistsAcross`) behind `ProjectService` methods that take the handle. This is the deeper Phase D refactor the comment promises; until done, the migration's done-criteria are not actually met.

---

## I. Documentation

### I.1 (P1) — README references dropped flags

**Location:** `README.md:104-116`.

**Description:** Lines 113-114 advertise `--server` (as "Server mode") and `--client <url>`. Neither exists. The current `--server <url>` is the daemon endpoint URL, totally different semantics. New users following the table will be confused. MIGRATION-PLAN §6 explicitly committed to dropping these.

**Suggested fix:** Replace the table with a daemon/TUI explanation. Daemon is the new "Server"; TUI is the new "Client"; `--server <url>` is the endpoint override. The Quick Start section (lines 7-17) is already correct; just delete or rewrite the obsolete table.

### I.2 — Daemon usage is discoverable

`singularity help` lists `daemon`, `daemon --detach`, `daemon status`, `daemon stop`, `daemon init`. `cmd/singularity/main.go:43-67`. Acceptable.

**No finding.**

---

## J. Code smells / quality

### J.1 (P2) — `internal/daemon/cmd.go` discards `maxAgents`

**Location:** `internal/daemon/cmd.go:81-87,126`. The variable is computed, then `_ = maxAgents` swallows it because `server.New` calls `engine.New(10)` with a hardcoded cap.

**Suggested fix:** Either add `Server.SetEngine` or expose `server.NewWithEngine(addr, engine, repoPath)` and construct the engine in `daemon.Run` with the resolved cap. Today `--max-agents 5` is a lie.

### J.2 (P2) — Server holds mutable `repoPath`

**Location:** `internal/server/server.go:37`, set by `handleRepoOpen` at line 34.

**Description:** Per-process implicit repo state contradicts MIGRATION-PLAN §2.5 ("every call carries its repo path"). The daemon is meant to be stateless w.r.t. "current repo". Still alive for legacy compatibility, but every new endpoint either requires `repo_path` in the payload or falls through `resolveRepoPath` to this implicit state.

**Suggested fix:** Phase out `resolveRepoPath` once views have migrated to always sending `repo_path`. Track via TODO; for now reject empty `repo_path` in any new handler.

### J.3 (P2) — `handleProjectBranchCompare` is registered but unimplemented

**Location:** `internal/server/server.go:331`, body in `internal/server/project_handlers.go:131-135`.

**Description:** Returns 503 with "not yet wired to service layer". Registering an endpoint that you immediately fail is acceptable transitional behavior, but it should carry a phase tag (`// TODO(phase-G)` or remove the route altogether).

**Suggested fix:** Either delete the route or tag the TODO with a tracked follow-up issue.

### J.4 — File sizes

Largest file in the new packages: `internal/server/server.go` at 475 lines, just under the 500-line cap. `internal/server/agent_handlers.go` at 301. All under the limit.

**No finding.**

### J.5 — `gofmt`/`go vet` clean

`go vet ./...` returns no findings. `gofmt -l` returns nothing.

**No finding.**

### J.6 (P2) — `wsBroadcast` doesn't reap dead conns

**Location:** `internal/server/ws_handlers.go:158-170`.

**Description:** A failed `WriteMessage` is logged and ignored; the conn stays in `wsClients`. The conn's reader goroutine will eventually error and run the cleanup defer, but that can take many seconds (next ReadMessage) and during that window every broadcast pays the cost of writing to a dead socket.

**Suggested fix:** On write error, take `wsMux.Lock()` (need to upgrade from RLock), `delete(s.wsClients, conn)`, `conn.Close()`. Easier: collect dead conns into a local slice while holding RLock, then re-acquire Write and delete.

### J.7 — Orphan TODOs

All TODOs are tagged `phase-D-followup` (in `internal/service/helpers.go` and `internal/service/types.go`) or describe future watcher work in `internal/service/local/{repo,pipeline}.go`. None are orphaned; each is reachable from a tracked phase.

**No finding.** Recommend explicit issues filed against H.2 above.

---

## Closing note

The migration's mechanical scope (interface plumbing, wire contract, transports) is in solid shape — the error-code round-trip, pidfile lifecycle, auto-spawn, and stale-socket recovery all work as specified. The two real holes are:

1. The token-auth code path was started but never threaded through (P0 #1) — symptomatically harmless on the default unix-socket transport, fatal the moment a user adds `--listen tcp://`.
2. Every `*Subscribe` endpoint passes `r.Context()` to a long-lived poller (P0 #2). The daemon advertises a streaming API; in practice the API self-cancels milliseconds after subscription.

Everything else is hardening (concurrency on WS writes, path validation, body limits, docs cleanup) or the explicit Phase-D-followup leak via `service.Project`/`service.Loader` aliases.
