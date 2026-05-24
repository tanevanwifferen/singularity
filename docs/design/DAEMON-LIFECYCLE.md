# Daemon Lifecycle Design — `singularityd`

> Phase A4 artifact. Defines how the daemon runs, how the TUI discovers it, and
> how lifecycle (spawn, signal, shutdown, crash recovery) is managed.
>
> Owner: `daemon-architect`. Consumers: `server-coder`, `client-coder`,
> `remote-coder`, `cmd/singularity` main.

---

## 0. TL;DR

- **Default transport**: HTTP/1.1 + WebSocket over a **Unix domain socket** at a
  fixed, per-user path. Filesystem permissions (0600) are the only auth.
- **State directory**: `~/.config/singularity/` on every platform. We do **not**
  honor `XDG_RUNTIME_DIR`. Rationale below.
- **Discovery**: client reads `~/.config/singularity/daemon.pid` and dials
  `~/.config/singularity/daemon.sock` next to it. `--server <url>` overrides.
- **Auto-spawn**: the bare `singularity` command (TUI) fork+execs itself as
  `singularity daemon --detach` when no daemon is reachable on the default
  socket. `--server` set explicitly + unreachable = hard error, never spawn.
- **TCP mode** is opt-in via `--listen tcp://host:port`. Requires bearer-token
  auth from a token file (`~/.config/singularity/token`, mode 0600).
- **Lock**: `O_CREAT|O_EXCL` on the pidfile is the spawn lock. Staleness checked
  with `syscall.Kill(pid, 0)` before nuking a leftover file.

---

## 1. State directory and paths

We use one directory for everything daemon-related:

```
~/.config/singularity/
  daemon.pid          # int PID, no newline noise — strconv.Itoa(os.Getpid())
  daemon.sock         # AF_UNIX socket, mode 0600
  daemon.log          # rolling log (truncated on start unless --append-log)
  daemon.yaml         # optional config (see §9)
  token               # bearer token for TCP mode, mode 0600
  state/              # reserved for future engine snapshotting (out of scope)
```

### Why `~/.config/singularity` everywhere?

The brief asked us to consider `$XDG_RUNTIME_DIR`. We rejected it:

- **macOS**: `XDG_RUNTIME_DIR` is essentially never set. The platform has no
  equivalent (Apple uses `/var/folders/<hash>/T/`, which is not per-user-tmpfs).
- **Linux**: `XDG_RUNTIME_DIR` is correct in principle (it's tmpfs, cleaned by
  systemd-logind on logout) but using a different path per-OS forces the
  discovery code to branch and complicates `singularity daemon status`.
- **Sockets in `~/.config`**: people object on the grounds that `~/.config` is
  for *config*, not runtime state. We don't care. This is a single-user dev
  tool; predictability beats spec-purism. The socket is recreated on every
  start, so a stale socket file is a non-issue.

The path is resolvable from `os.UserConfigDir()` (Go stdlib) which already
handles `$HOME`, Windows `%APPDATA%`, etc. We then append `/singularity`.

```go
func StateDir() (string, error) {
    base, err := os.UserConfigDir()
    if err != nil { return "", err }
    dir := filepath.Join(base, "singularity")
    return dir, os.MkdirAll(dir, 0700)
}
```

`0700` on the directory means even if the socket is world-readable by mistake,
the parent dir blocks access. Belt and suspenders.

### Override

A single env var, `SINGULARITY_HOME`, replaces the whole directory. Anything
inside is computed from it. No XDG, no per-file overrides. Keep it boring.

---

## 2. Listen addresses

### Default — Unix socket

```go
ln, err := net.Listen("unix", filepath.Join(stateDir, "daemon.sock"))
if err != nil { return err }
// Tighten perms — net.Listen creates with umask, we want exactly 0600.
if err := os.Chmod(socketPath, 0600); err != nil { return err }
srv := &http.Server{Handler: mux}
go srv.Serve(ln)
```

`http.Serve` does not care that the listener is a unix socket. WebSocket
upgrade through `gorilla/websocket` also does not care — it's just a
`net.Conn` underneath. Tested pattern, no surprises.

The socket file is removed on shutdown. On startup, if a stale socket file
exists, we attempt `net.Dial("unix", path)`; if the dial fails we `os.Remove`
it before listening. (`net.Listen` refuses to bind over an existing file.)

### Optional — TCP

```
singularity daemon --listen tcp://0.0.0.0:8420
```

For the rack-server use case. When TCP is in effect:

- A bearer token MUST be present (`~/.config/singularity/token`). If absent,
  daemon refuses to start with a non-zero exit and a clear message.
- All handlers require `Authorization: Bearer <token>`. WebSocket upgrade
  checks the header on the HTTP request before upgrading.
- We do **not** support TCP and unix simultaneously in v1. Pick one per
  daemon process. A future change could run both listeners; not now.

### Optional — TLS

`--listen https://host:port` enables TLS. Requires `--cert` and `--key` paths,
or `SINGULARITY_TLS_CERT`/`SINGULARITY_TLS_KEY`. We deliberately do NOT
auto-acme; that's an ops decision. For the local case, just use the unix
socket.

---

## 3. PID file

### Format

```
12345
```

Single integer, no trailing newline, no JSON. We never put a socket path or
hostname in here; everything is derivable from the state directory. Keeping
the format trivial means it survives partial writes (which can't happen
anyway, see below) and is trivially `cat`-able for debugging.

### Atomic creation

```go
f, err := os.OpenFile(pidPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
if err != nil {
    if os.IsExist(err) {
        if isStale(pidPath) {
            _ = os.Remove(pidPath)
            return tryCreate() // one retry
        }
        return ErrDaemonAlreadyRunning
    }
    return err
}
defer f.Close()
fmt.Fprintf(f, "%d", os.Getpid())
return f.Sync()
```

`O_CREAT|O_EXCL` is our spawn lock. Two processes racing to start a daemon
will collide here; the loser exits cleanly.

### Staleness detection

```go
func isStale(pidPath string) bool {
    data, err := os.ReadFile(pidPath)
    if err != nil { return true }
    pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
    if err != nil || pid <= 1 { return true }
    // syscall.Kill(pid, 0) returns nil if process exists,
    // ESRCH if it doesn't, EPERM if it exists but we can't signal it.
    err = syscall.Kill(pid, 0)
    switch {
    case err == nil:        return false                  // alive
    case errors.Is(err, syscall.ESRCH): return true       // dead
    case errors.Is(err, syscall.EPERM): return false      // alive, other user
    default:                return true
    }
}
```

EPERM (process exists but not ours) is treated as "running" — we don't want
to nuke another user's daemon and steal the socket. They almost certainly
won't be running with our exact `$HOME` anyway, but be defensive.

### Cleanup

`defer os.Remove(pidPath)` in the daemon's main, plus a signal handler that
removes it before `os.Exit`. We do not unlink on `panic` cleanup; let the
next start detect it as stale.

---

## 4. Discovery (client side)

```go
// internal/client/discover.go
func DefaultEndpoint() (string, error) {
    dir, err := StateDir()
    if err != nil { return "", err }
    pidPath := filepath.Join(dir, "daemon.pid")
    sockPath := filepath.Join(dir, "daemon.sock")

    if _, err := os.Stat(pidPath); err != nil {
        return "", ErrNoDaemon
    }
    if _, err := os.Stat(sockPath); err != nil {
        return "", ErrNoSocket
    }
    return "unix://" + sockPath, nil
}
```

The pidfile's presence is the "should be running" signal; the socket's
existence is the "is actually listening" signal. We require both. A pidfile
without a socket means the daemon crashed mid-startup; we report it but do
not auto-recover (auto-spawn will handle it once the stale-pid check sweeps
it away).

### Accepted URL schemes for `--server`

| Scheme        | Example                          | Transport                     |
|---------------|----------------------------------|-------------------------------|
| `unix://`     | `unix:///home/me/.config/...sock`| HTTP/1.1 over AF_UNIX         |
| `http://`     | `http://rack:8420`               | HTTP/1.1 over TCP             |
| `https://`    | `https://rack:8420`              | HTTP/1.1 + TLS over TCP       |

No `tcp://` or `ws://` — the URL describes the HTTP endpoint; WebSocket
upgrade is automatic for streaming endpoints.

---

## 5. Auto-spawn rules

Decision matrix when the user runs `singularity` (no subcommand):

| `--server` flag | Daemon at default socket | Behavior                          |
|-----------------|--------------------------|-----------------------------------|
| unset           | reachable                | connect, run TUI                  |
| unset           | unreachable / absent     | **fork+exec daemon, wait, TUI**   |
| set, reachable  | n/a                      | connect, run TUI                  |
| set, unreachable| n/a                      | **exit 1 with clear error**       |

We never spawn a daemon to satisfy an explicit `--server` value. Spawning
remotely is impossible; spawning locally when the user asked for a remote
URL would silently mask a typo. Fail loudly.

### How auto-spawn works

```go
func ensureDaemon() error {
    if reachable(defaultSocket) { return nil }

    self, err := os.Executable()
    if err != nil { return err }

    logPath := filepath.Join(stateDir, "daemon.log")
    logFile, err := os.OpenFile(logPath,
        os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
    if err != nil { return err }

    cmd := exec.Command(self, "daemon", "--detach")
    cmd.Stdout = logFile
    cmd.Stderr = logFile
    cmd.Stdin = nil
    cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // detach from TTY
    if err := cmd.Start(); err != nil { return err }
    // Don't Wait — child detaches itself. We poll the socket.
    return waitForSocket(defaultSocket, 5*time.Second)
}

func waitForSocket(path string, timeout time.Duration) error {
    deadline := time.Now().Add(timeout)
    for time.Now().Before(deadline) {
        if c, err := net.Dial("unix", path); err == nil {
            _ = c.Close()
            return nil
        }
        time.Sleep(50 * time.Millisecond)
    }
    return ErrDaemonStartupTimeout
}
```

`Setsid` (Linux) / `Setpgid` (everywhere) detaches the child from our TTY so
that hitting Ctrl-C on the TUI later doesn't also kill the daemon. The
pidfile's `O_EXCL` create races safely: if two TUI instances start at the
same time, exactly one daemon-spawn wins the pidfile lock; the other's child
exits with `ErrDaemonAlreadyRunning` and its parent then finds the socket
reachable.

---

## 6. Subcommand surface

```
singularity                       # TUI client (default), auto-spawns daemon
singularity --server <url>        # TUI client against explicit endpoint
singularity daemon                # run daemon in foreground (systemd, debug)
singularity daemon --detach       # fork into background, exit when ready
singularity daemon --listen <url> # override listen address
singularity daemon status         # print PID, socket, uptime, agent count
singularity daemon stop           # SIGTERM the daemon, wait, clean up
singularity daemon init           # generate token, write default config
```

### `daemon` (foreground)

- Creates pidfile, binds socket, starts HTTP server, blocks on signal.
- Logs to stderr.
- Exits 0 on clean shutdown, non-zero on bind/lock failure.
- This is what systemd / launchd should invoke. No `--detach` needed under a
  supervisor.

### `daemon --detach`

- Fork+exec strategy: re-exec self as `daemon` (no `--detach`) with stdio
  redirected to the log file, `Setsid`, then the parent waits up to 5s for
  the socket to appear and exits 0 or non-zero accordingly.
- We do NOT use a classic double-fork. Go's runtime is hostile to bare
  `fork()`; the re-exec dance gives us a clean address space and avoids the
  multi-threaded-fork landmine.

### `daemon status`

```
$ singularity daemon status
pid:        12345
socket:     /home/tane/.config/singularity/daemon.sock
listen:     unix
uptime:     3h42m
agents:     2 running, 0 pending
log:        /home/tane/.config/singularity/daemon.log
```

Implementation: hits `GET /api/daemon/info` on the daemon. If the daemon
isn't running, reports that and exits 1.

### `daemon stop`

- Reads pidfile, sends SIGTERM, waits up to 10s for the process to exit
  (poll with `kill(pid, 0)`).
- If still alive at 10s, prints a warning and sends SIGKILL.
- Removes the pidfile if the process is gone but the file remains.
- Exits 0 on success, 1 on timeout-after-SIGKILL.

### `daemon init`

- Generates `~/.config/singularity/token` (32 random bytes, hex-encoded,
  mode 0600) if absent.
- Writes a default `daemon.yaml` if absent.
- Idempotent. Safe to re-run.

---

## 7. Signals and shutdown

Daemon main loop:

```go
ctx, cancel := signal.NotifyContext(context.Background(),
    syscall.SIGTERM, syscall.SIGINT)
defer cancel()

go srv.Serve(ln)
<-ctx.Done()

shutdownCtx, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel2()

_ = srv.Shutdown(shutdownCtx)   // stop accepting, drain HTTP
_ = engine.Shutdown(shutdownCtx) // SIGTERM agents, wait
_ = os.Remove(socketPath)
_ = os.Remove(pidPath)
```

- SIGTERM and SIGINT are equivalent. SIGHUP is reserved for future
  config-reload; v1 ignores it (explicitly — don't let it kill the daemon).
- `engine.Shutdown` is responsible for signalling its child agent processes
  and waiting on them. 10s ceiling; after that we exit anyway and any
  surviving agents become the kernel's problem (they'll get SIGHUP from the
  session leader going away if they're in our process group).
- `srv.Shutdown` will also walk WebSocket connections; the WS handler should
  watch `ctx.Done()` and send a close frame.

---

## 8. Logging

- **Foreground** (`singularity daemon`): structured logs to stderr.
- **Detached**: same logger, writer redirected to `daemon.log`. Truncated on
  every start (single-user dev tool; we don't need rotation).
- **Format**: `slog` with `slog.NewTextHandler`. Example line:

  ```
  time=2026-05-23T10:14:22.193+02:00 level=INFO msg="daemon started"
      pid=12345 socket=/home/tane/.config/singularity/daemon.sock
  ```

  JSON handler is available behind `--log-format=json` for ops scraping.

- **Levels**: default INFO. `--log-level=debug` (or `SINGULARITY_LOG=debug`)
  for noisy mode. Engine subprocess output is its own stream over WS and
  does NOT go through the daemon's main logger.

---

## 9. Config file (optional)

`~/.config/singularity/daemon.yaml`. All fields optional; flags override.

```yaml
listen: unix                     # unix | tcp | https
listen_addr: ""                  # empty = default unix path; for tcp: "0.0.0.0:8420"
log_path: ""                     # empty = ~/.config/singularity/daemon.log
log_level: info                  # debug | info | warn | error
log_format: text                 # text | json
max_agents: 16                   # engine cap
default_project_config: ""       # path to a project.yaml auto-loaded on start
tls:
  cert: ""
  key: ""
auth:
  token_file: ""                 # default ~/.config/singularity/token
```

Loader precedence: **flags > env > config file > built-in defaults**. Unknown
keys are an error (`yaml.UnmarshalStrict`); typos shouldn't be silent.

We chose YAML over JSON for one reason: hand-editability. The daemon never
writes this file (except `daemon init` writing a commented template).

---

## 10. HTTP-over-unix client

Go's `net/http` requires a real URL with a host. The trick is to override
`http.Transport.DialContext` to ignore the host and dial the socket, then
use a sentinel hostname in the URL.

```go
// internal/client/client.go
type Client struct {
    base string           // e.g. "http://unix" or "http://rack:8420"
    http *http.Client
}

func New(endpoint string) (*Client, error) {
    u, err := url.Parse(endpoint)
    if err != nil { return nil, err }

    switch u.Scheme {
    case "unix":
        sock := u.Path
        tr := &http.Transport{
            DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
                var d net.Dialer
                return d.DialContext(ctx, "unix", sock)
            },
        }
        return &Client{
            base: "http://unix", // host is ignored by our dialer
            http: &http.Client{Transport: tr, Timeout: 30 * time.Second},
        }, nil
    case "http", "https":
        return &Client{
            base: endpoint,
            http: &http.Client{Timeout: 30 * time.Second},
        }, nil
    default:
        return nil, fmt.Errorf("unsupported scheme %q", u.Scheme)
    }
}

func (c *Client) Get(ctx context.Context, path string) (*http.Response, error) {
    req, _ := http.NewRequestWithContext(ctx, "GET", c.base+path, nil)
    return c.http.Do(req)
}
```

### WebSocket over unix

`gorilla/websocket.Dialer` exposes `NetDialContext`, same trick:

```go
func (c *Client) DialWS(ctx context.Context, path string) (*websocket.Conn, error) {
    d := websocket.Dialer{
        NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
            var nd net.Dialer
            return nd.DialContext(ctx, "unix", c.sockPath)
        },
    }
    // ws:// URL still needs a host token; "unix" works as a placeholder.
    return d.DialContext(ctx, "ws://unix"+path, nil)
}
```

For TCP, the URL is the real one and we don't override `NetDialContext`.

### Auth header injection

For TCP/HTTPS endpoints, the client wraps `http.Client.Transport` in a
small round-tripper that adds `Authorization: Bearer <token>`. The token is
loaded once at `Client` construction from `~/.config/singularity/token` (or
`--token` flag, or `SINGULARITY_TOKEN` env).

---

## 11. Security notes

- **Unix socket**: `chmod 0600` after bind. Combined with the directory's
  0700, only the owning user (and root) can connect. No auth on the wire is
  fine because the kernel already authenticated the peer's UID.
- **TCP without TLS**: refuse to start unless `--insecure` is also passed.
  Bearer-token over plaintext is leak-prone; force users to either run
  HTTPS or acknowledge the risk.
- **Token rotation**: out of scope. `daemon init --force` regenerates; users
  must restart the daemon and update any remote clients.
- **CORS**: the existing server is wide-open. For TCP mode we tighten to
  reject cross-origin WS upgrades (`websocket.Upgrader.CheckOrigin` returns
  false unless origin matches `Host`). Unix-mode keeps it open since no
  browser can reach an AF_UNIX socket anyway.
- **Pidfile race**: O_EXCL handles it. We never trust the pidfile contents
  without a `kill(pid, 0)` check.
- **Log file**: 0600. May contain repo paths and command lines — treat as
  sensitive.

---

## 12. Open items deferred

1. **Engine state persistence across daemon restarts**: noted in
   MIGRATION-PLAN §6. Not addressed here.
2. **Multi-tenant daemon** (one daemon, multiple users): explicitly out of
   scope. We assume one daemon per user account.
3. **Windows support**: AF_UNIX works on Win10+ but our `Setsid`/signal
   handling does not. Defer; Linux + macOS only for v1.
4. **launchd / systemd unit files**: ship as examples under `contrib/` once
   the daemon command is stable.

---

## 13. Summary of decisions

| Topic              | Decision                                                  |
|--------------------|-----------------------------------------------------------|
| State dir          | `~/.config/singularity` (skip XDG_RUNTIME_DIR)            |
| Default transport  | HTTP+WS over unix socket, mode 0600                       |
| TCP                | Opt-in via `--listen tcp://`, requires bearer token       |
| Pidfile            | `daemon.pid`, integer only, `O_CREAT|O_EXCL` lock         |
| Stale detection    | `kill(pid, 0)`; ESRCH → stale, EPERM → assume alive       |
| Discovery          | pidfile + sibling socket; `--server` URL overrides        |
| Auto-spawn         | yes, only when no `--server` flag and socket unreachable  |
| Detach mechanism   | re-exec self with stdio redirected + `Setsid`             |
| Shutdown           | SIGTERM → `srv.Shutdown` → `engine.Shutdown` → cleanup    |
| Shutdown timeout   | 10s                                                       |
| Logging            | slog text (default), file when detached, stderr otherwise |
| Config             | optional YAML; flags > env > file > defaults              |
| Unix-HTTP client   | `Transport.DialContext` override, sentinel `http://unix`  |
| WS over unix       | gorilla `Dialer.NetDialContext` override                  |
