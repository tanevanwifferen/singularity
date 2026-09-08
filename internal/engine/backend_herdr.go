package engine

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// herdrBackend drives an interactive `claude` session through herdr
// (https://herdr.dev), a terminal multiplexer purpose-built for coding
// agents, instead of shelling out to `claude --print`.
//
// Why this exists: `claude --print` (the mode ClaudeBackend and PiBackend's
// unattended/one-shot paths rely on) is rejected outright on a Claude Max
// subscription — only API/enterprise auth is allowed to use headless print
// mode. The interactive CLI has no such restriction, so this backend spawns
// a herdr pane running interactive `claude` and drives it through herdr's
// socket-backed CLI (`herdr agent start/prompt/wait/read`).
//
// Why herdr rather than a bare multiplexer: a raw pane only ever gives raw
// bytes, so "the agent's turn is done" has to be guessed from rendered text —
// exactly the kind of pattern-matching that breaks silently on a CLI update.
// herdr recognises the agent running in a pane and classifies its lifecycle
// (idle/working/blocked/done) itself, so `herdr agent prompt --wait` returns a
// real, herdr-computed completion signal instead of an inferred one, and
// `herdr agent start` blocks until herdr has confirmed the agent is actually
// ready for input rather than sleeping a guessed delay and hoping the TUI
// finished booting.
//
// Mechanics: Args returns a `singularity herdr-driver …` invocation
// (Binary/Args are one exec, per the Backend contract). That driver —
// herdr_driver.go, run in-process by cmd/singularity's herdr-driver
// subcommand — owns the herdr workspace, the claude session inside it and the
// per-turn herdr calls, and speaks a small base64 marker protocol on stdout
// that ParseEvent below reads back. Everything the driver needs out of
// herdr's JSON is parsed with encoding/json, on both sides of that protocol.
//
// Known limitation, no way around it from here: a pane is rendered text, so
// there are no structured tool-call events. The output stream carries
// BackendText (and, while a turn runs, incremental pane reads) but never
// BackendToolUse/BackendToolResult the way the claude and pi backends do.
// Documented for operators in cmd/singl/prime.md.
type herdrBackend struct {
	// name is this agent's herdr agent name. herdr requires it to be unique
	// among live agents, so it is derived from the agent ID (see
	// NewForAgent): one backend instance per agent, one herdr agent name per
	// backend instance. It doubles as the herdr workspace label.
	name string

	// launchModel is what Args() resolved and passed to `claude --model`,
	// echoed back as the BackendSessionInit model since nothing in herdr's
	// output confirms it independently.
	launchModel string

	// warnings queued by Args, flushed by ParseEvent (mirrors piBackend).
	warnMu   sync.Mutex
	warnings []string

	// initOnce guards the one-time BackendSessionInit emission (see
	// sessionInitEvents); it is the only guard needed since the driver
	// prints the __SINGL_INIT__ marker itself exactly once.
	initOnce sync.Once

	// promptTimeoutMS overrides herdrPromptTimeoutMS when the agent has its
	// own --timeout (see SetTurnTimeout). Zero means "use the default".
	promptTimeoutMS int
}

// NewHerdrBackend returns a Backend that drives an interactive claude CLI
// session through a herdr-managed pane.
//
// The instance it returns carries per-agent state, so it must not be shared
// between agents — Engine.StartAgent calls NewForAgent on it for exactly that
// reason (see PerAgentBackend in backend.go). The random name here is the
// fallback for a backend that never goes through StartAgent.
func NewHerdrBackend() Backend {
	return &herdrBackend{name: "singl-" + randomHex(6)}
}

// NewForAgent gives this agent its own herdr backend instance, named after
// the agent ID. Without it, two concurrent agents sharing one instance would
// both run `herdr agent start <same name>` — which herdr rejects, since names
// must be unique among live agents — and would share this instance's
// initOnce, so the second agent would never report a session init.
func (b *herdrBackend) NewForAgent(agentID string) Backend {
	return &herdrBackend{name: herdrAgentName(agentID)}
}

// herdrAgentName derives a valid herdr agent name from an agent ID. herdr
// requires `[a-z][a-z0-9_-]{0,31}`, so anything else is folded to '-' and the
// result is truncated; agent IDs are unique per daemon (engine.generateID),
// which is what makes the derived name unique among live agents.
func herdrAgentName(agentID string) string {
	var sb strings.Builder
	sb.WriteString("singl-")
	for _, r := range strings.ToLower(agentID) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			sb.WriteRune(r)
		default:
			sb.WriteByte('-')
		}
	}
	name := sb.String()
	if len(name) > 32 {
		// Keep the tail: it holds the ID's unique sequence number, where the
		// head is a constant prefix plus a timestamp shared by every agent
		// started in the same second.
		name = "s" + name[len(name)-31:]
	}
	return name
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failing is effectively unrecoverable, but a
		// timestamp-derived fallback still keeps session names unique
		// enough not to collide within one daemon process.
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(buf)
}

func (b *herdrBackend) Name() string { return "herdr" }

// Binary is the singularity binary itself, re-invoked as the herdr driver
// (see the type doc and herdr_driver.go). It is resolved the same way
// daemon.daemonBinary resolves the daemon: self when self is already the
// singularity binary, else a sibling, else PATH.
func (b *herdrBackend) Binary() string {
	if self, err := os.Executable(); err == nil {
		if filepath.Base(self) == "singularity" {
			return self
		}
		sibling := filepath.Join(filepath.Dir(self), "singularity")
		if _, err := os.Stat(sibling); err == nil {
			return sibling
		}
	}
	if p, err := exec.LookPath("singularity"); err == nil {
		return p
	}
	// Nothing found: exec will fail with a legible "not found" naming the
	// binary the operator needs on PATH, which beats guessing.
	return "singularity"
}

// herdrTerminationGrace is how long Agent.kill waits, after SIGTERM, for the
// driver to close its herdr workspace before resorting to SIGKILL (see
// TerminationGrace and agent_lifecycle.go). One `herdr workspace close`
// round-trip over a unix socket is milliseconds; this is slack for a busy
// server, not a budget.
const herdrTerminationGrace = 5 * time.Second

// TerminationGrace makes Agent.kill signal this backend's process politely
// first. It matters here more than for any other backend: the pane and the
// interactive claude inside it are owned by the herdr *server*, not by the
// driver, so a driver that dies without closing its workspace leaves a live
// unsupervised claude behind — and, for a --use-worktree agent, one whose cwd
// is the worktree kill() is about to delete.
func (b *herdrBackend) TerminationGrace() time.Duration { return herdrTerminationGrace }

// Preflight fails when the herdr CLI or a running herdr server is missing,
// which is the common misconfiguration: without a server every spawn dies
// immediately with a raw JSON error from `herdr workspace create`. The daemon
// reports this at startup (internal/daemon/cmd.go).
func (b *herdrBackend) Preflight() error { return herdrPreflight() }

// Args builds the driver invocation. claude's own flags go after `--`, where
// the driver passes them straight to `herdr agent start … -- <agent-args>`;
// no shell is involved anywhere on this path, so nothing needs quoting even
// though model/effort can originate from the HTTP API.
func (b *herdrBackend) Args(model, effort string, maxTurns int, allowedTools []string) []string {
	b.clearWarnings()

	b.launchModel = Models().ResolveModel("claude", model)

	promptTimeoutMS := herdrPromptTimeoutMS
	if b.promptTimeoutMS > 0 {
		promptTimeoutMS = b.promptTimeoutMS
	}
	args := []string{
		"herdr-driver",
		"--name", b.name,
		"--kind", "claude",
		"--prompt-timeout-ms", strconv.Itoa(promptTimeoutMS),
		"--poll-ms", strconv.Itoa(herdrDefaultPollMS),
		"--",
		"--permission-mode", "bypassPermissions",
	}
	if b.launchModel != "" {
		args = append(args, "--model", b.launchModel)
	}
	if effort != "" {
		args = append(args, "--effort", effort)
	}
	for _, tool := range allowedTools {
		args = append(args, "--allowedTools", tool)
	}

	// claude's interactive mode has no turn cap of its own (--max-turns is a
	// print-mode-only flag), same limitation piBackend reports.
	if maxTurns > 0 {
		b.warn(fmt.Sprintf("max_turns=%d is not supported by the herdr backend (claude has no turn "+
			"limit in interactive mode); use a timeout to bound it", maxTurns))
	}

	return args
}

// Env returns the driver subprocess's own environment. It does not carry
// anything intended for claude: claude runs inside a herdr pane as a child
// of the herdr *server*, not of the driver, so it inherits the server's
// environment regardless of what the driver's own env holds. Variables meant
// for claude go through `herdr workspace create --env` instead — see
// herdr_driver.go's start().
func (b *herdrBackend) Env() []string {
	return os.Environ()
}

// SetTurnTimeout implements TimeoutAwareBackend: it sizes the driver's
// `--prompt-timeout-ms` (otherwise a fixed herdrPromptTimeoutMS, 30 minutes)
// to the agent's own --timeout, so a turn legitimately longer than the
// default isn't cut short by herdr's own `timeout` error while claude keeps
// working unsupervised in the pane, and a shorter --timeout is actually
// honoured here instead of silently ignored.
func (b *herdrBackend) SetTurnTimeout(d time.Duration) {
	if d > 0 {
		b.promptTimeoutMS = int(d / time.Millisecond)
	}
}

// PostStartCommands is a no-op: effort and tools are launch flags (see Args),
// and herdr's agent-level commands have no equivalent of pi's runtime
// configuration commands.
func (b *herdrBackend) PostStartCommands(_ string) [][]byte { return nil }

// InitialInput and FollowUpInput both just hand text to the driver's stdin
// loop; there is no separate "steer" message type, so isStreaming is ignored.
func (b *herdrBackend) InitialInput(task, _ string) ([]byte, error) { return herdrTaskLine(task), nil }

func (b *herdrBackend) FollowUpInput(message, _ string, _ bool) ([]byte, error) {
	return herdrTaskLine(message), nil
}

// herdrTaskLine base64-encodes text so it survives the driver's line-oriented
// stdin untouched — task/message text can contain newlines or anything else
// that would otherwise have to be escaped.
func herdrTaskLine(text string) []byte {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	return []byte(herdrTaskPrefix + encoded + "\n")
}

// warn queues an option warning for the next ParseEvent call (mirrors piBackend.warn).
func (b *herdrBackend) warn(msg string) {
	b.warnMu.Lock()
	b.warnings = append(b.warnings, msg)
	b.warnMu.Unlock()
}

func (b *herdrBackend) clearWarnings() {
	b.warnMu.Lock()
	b.warnings = nil
	b.warnMu.Unlock()
}

func (b *herdrBackend) takeWarnings() []string {
	b.warnMu.Lock()
	warnings := b.warnings
	b.warnings = nil
	b.warnMu.Unlock()
	return warnings
}

// OneShotCommand runs a cheap one-shot prompt via `claude --print`, the same
// command ClaudeBackend uses: this has nothing to do with herdr — it is
// claude's own headless mode, which the smart-router classifier and the
// commit-message/MR-title helpers in internal/oneshot expect. On a Max-plan
// account (the reason this backend exists) print mode is refused and this
// call fails, exactly as ClaudeBackend's would; every caller already tolerates
// that by falling back to a heuristic (see internal/oneshot's doc comment).
// With API or enterprise auth it works normally.
func (b *herdrBackend) OneShotCommand(prompt string) (string, []string) {
	return "claude", []string{
		"--print",
		"--model", Models().ClassifierModel("claude"),
		"--output-format", "text",
		"-p", prompt,
	}
}

// herdrPromptResult decodes the JSON `herdr agent prompt --wait` prints on
// success or failure (see the herdr CLI's documented shapes: a success
// carries result.agent.agent_status, a failure carries error.code/message).
type herdrPromptResult struct {
	Result *struct {
		Agent *struct {
			AgentStatus string `json:"agent_status"`
		} `json:"agent"`
	} `json:"result"`
	Error *herdrError `json:"error"`
}

// ParseEvent parses one line from the driver's stdout: the one-time
// __SINGL_INIT__ marker, a __SINGL_STATE__ marker carrying the base64 JSON
// that settled the turn, a __SINGL_OUT__ marker carrying a base64 chunk of
// new pane text, a __SINGL_ERR__ marker carrying a driver-level error, or
// (harmlessly) anything else the driver's stdout happened to carry.
func (b *herdrBackend) ParseEvent(line []byte) ([]*BackendEvent, error) {
	events := b.parseLine(string(line))
	warnings := b.takeWarnings()
	if len(warnings) == 0 {
		return events, nil
	}
	out := make([]*BackendEvent, 0, len(warnings)+len(events))
	for _, msg := range warnings {
		out = append(out, &BackendEvent{Kind: BackendError, Content: msg})
	}
	return append(out, events...), nil
}

func (b *herdrBackend) parseLine(line string) []*BackendEvent {
	switch {
	case strings.HasPrefix(line, herdrMarkerInit):
		paneID, err := decodeHerdrPayload(line, herdrMarkerInit)
		if err != nil {
			return []*BackendEvent{{Kind: BackendError, Content: fmt.Sprintf("herdr driver: bad init encoding: %v", err)}}
		}
		return b.sessionInitEvents(paneID)

	case strings.HasPrefix(line, herdrMarkerState):
		decoded, err := decodeHerdrPayload(line, herdrMarkerState)
		if err != nil {
			return []*BackendEvent{{Kind: BackendError, Content: fmt.Sprintf("herdr driver: bad state encoding: %v", err)}}
		}
		return []*BackendEvent{b.resultEventFromState(decoded)}

	case strings.HasPrefix(line, herdrMarkerOut):
		decoded, err := decodeHerdrPayload(line, herdrMarkerOut)
		if err != nil {
			return []*BackendEvent{{Kind: BackendError, Content: fmt.Sprintf("herdr driver: bad output encoding: %v", err)}}
		}
		return textEventsFromPane(decoded)

	case strings.HasPrefix(line, herdrMarkerErr):
		decoded, err := decodeHerdrPayload(line, herdrMarkerErr)
		if err != nil {
			return []*BackendEvent{{Kind: BackendError, Content: fmt.Sprintf("herdr driver: bad error encoding: %v", err)}}
		}
		return []*BackendEvent{{Kind: BackendError, Content: decoded}}

	default:
		return []*BackendEvent{{Kind: BackendIgnore}}
	}
}

func decodeHerdrPayload(line, marker string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, marker))
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// resultEventFromState turns the response that settled one turn into a
// BackendResult. This is not a guess: "done"/"idle" are herdr's own
// settled-state classification, and anything else (an error object,
// "blocked", or an unrecognised status) is reported as an error rather than
// inferred as success — a blocked turn is not a completed one. herdr's
// agent_prompt_stalled is handled earlier, by the driver, which re-checks the
// real state with `herdr agent wait` before anything reaches here.
func (b *herdrBackend) resultEventFromState(raw string) *BackendEvent {
	raw = strings.TrimSpace(raw)
	var parsed herdrPromptResult
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return &BackendEvent{Kind: BackendResult, IsResultError: true,
			Content: fmt.Sprintf("herdr agent prompt: unparseable response: %s", raw)}
	}
	if parsed.Error != nil {
		return &BackendEvent{Kind: BackendResult, IsResultError: true,
			Content: fmt.Sprintf("herdr agent prompt failed (%s): %s", parsed.Error.Code, parsed.Error.Message)}
	}
	status := ""
	if parsed.Result != nil && parsed.Result.Agent != nil {
		status = parsed.Result.Agent.AgentStatus
	}
	switch status {
	case "done", "idle":
		return &BackendEvent{Kind: BackendResult, Subtype: "success"}
	case "blocked":
		return &BackendEvent{Kind: BackendResult, IsResultError: true,
			Content: "agent is blocked awaiting approval or input (herdr status: blocked)"}
	default:
		return &BackendEvent{Kind: BackendResult, IsResultError: true,
			Content: fmt.Sprintf("herdr agent prompt returned unrecognised status %q", status)}
	}
}

// textEventsFromPane turns one chunk of new pane text into BackendText
// events, one per non-blank line. The chunk is already a diff against the
// previous read (herdrDiffSnapshot, driver side), so nothing here has to
// worry about the transcript repeating.
func textEventsFromPane(chunk string) []*BackendEvent {
	var events []*BackendEvent
	for _, l := range strings.Split(chunk, "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		events = append(events, &BackendEvent{Kind: BackendText, Content: l})
	}
	if len(events) == 0 {
		return []*BackendEvent{{Kind: BackendIgnore}}
	}
	return events
}

// sessionInitEvents emits the one-time BackendSessionInit for this agent.
// paneID is the driver's __SINGL_INIT__ payload: the herdr pane hosting this
// agent's claude session, carried through so a human reading the agent
// record can find it directly.
func (b *herdrBackend) sessionInitEvents(paneID string) []*BackendEvent {
	var events []*BackendEvent
	b.initOnce.Do(func() {
		events = append(events, &BackendEvent{
			Kind:      BackendSessionInit,
			Model:     b.launchModel,
			SessionID: b.name,
			PaneID:    paneID,
		})
	})
	return events
}
