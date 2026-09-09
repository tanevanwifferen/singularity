package engine

import (
	"sync/atomic"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/config"
)

// Backend abstracts the coding-agent subprocess protocol.
// Three implementations are provided: ClaudeBackend (stream-json), PiBackend
// (RPC), and herdrBackend (herdr driving an interactive claude — see
// backend_herdr.go for why: claude --print is rejected on Max-plan auth).
// Each agent owns its own Backend instance so implementations may keep per-agent state.
type Backend interface {
	// Name returns the backend identifier ("claude", "pi" or "herdr").
	Name() string

	// Binary returns the executable name (e.g. "claude", "pi").
	Binary() string

	// Args returns the CLI args for launching the subprocess.
	// model and effort may be empty strings (use backend defaults).
	//
	// Not every backend can honour every option: pi has no turn limit
	// (no CLI flag, no RPC command, no setting), and its tool allowlist
	// uses its own built-in tool names. A backend that cannot apply an
	// option must not drop it silently — it reports the fact as a
	// BackendError event on the agent's output stream so the user learns
	// the flag is a no-op for this backend.
	Args(model, effort string, maxTurns int, allowedTools []string) []string

	// Env returns additional environment variables for the subprocess.
	// Callers merge this with os.Environ().
	Env() []string

	// InitialInput serialises the first task message to send on stdin.
	// sessionID is unused by most backends but kept for symmetry.
	InitialInput(task, sessionID string) ([]byte, error)

	// FollowUpInput serialises a user follow-up message.
	// isStreaming is true when the agent is actively mid-response; some
	// backends (pi) use a different message type in that case.
	FollowUpInput(message, sessionID string, isStreaming bool) ([]byte, error)

	// PostStartCommands returns optional JSONL lines to write to stdin
	// immediately after the subprocess is started (before the initial task).
	// Used by pi to configure thinking level, auto-retry, etc.
	PostStartCommands(effort string) [][]byte

	// ParseEvent parses one JSONL line from the subprocess stdout.
	// Returns zero or more normalised events; never returns nil slice on success.
	ParseEvent(line []byte) ([]*BackendEvent, error)

	// OneShotCommand returns the binary and args for a lightweight one-shot
	// prompt call: one prompt in, one text answer out, on the cheap model the
	// model table configures for this backend (see Models.ClassifierModel).
	// Used by the smart router (classifier.go) and by the one-shot prompt
	// helper in internal/oneshot (commit messages, MR titles and descriptions,
	// worktree auto-commit and merge messages).
	//
	// This is not the call to use for work that needs tools or several turns —
	// see UnattendedSessionCommand for that.
	OneShotCommand(prompt string) (binary string, args []string)

	// UnattendedSessionCommand returns the binary and args for a full agent
	// session (tools enabled, multi-turn) that runs to completion without ever
	// blocking on interactive input. The prompt is passed on the command line.
	//
	// Callers use this on unattended code paths such as automatic rebase-conflict
	// resolution, where a session that stops to ask for permission would hang
	// silently. A backend that cannot guarantee non-interactive execution must
	// return an error explaining why; the caller is then required to fail loudly
	// instead of launching something that may block.
	UnattendedSessionCommand(prompt string) (binary string, args []string, err error)
}

// BackendEventKind identifies the normalised event category.
type BackendEventKind string

const (
	// BackendText is a text chunk from the assistant.
	BackendText BackendEventKind = "text"
	// BackendToolUse signals a tool call invocation.
	BackendToolUse BackendEventKind = "tool_use"
	// BackendToolResult carries the output of a completed tool call.
	BackendToolResult BackendEventKind = "tool_result"
	// BackendSessionInit carries session metadata (model name, session ID).
	BackendSessionInit BackendEventKind = "session_init"
	// BackendResult signals that the agent turn is complete.
	BackendResult BackendEventKind = "result"
	// BackendError carries an error message from the agent.
	BackendError BackendEventKind = "error"
	// BackendIgnore means the event carries no actionable information.
	BackendIgnore BackendEventKind = "ignore"
)

// BackendEvent is the normalised representation of one agent event.
// Only fields relevant to the event Kind are populated.
type BackendEvent struct {
	Kind BackendEventKind

	// Text / error content (BackendText, BackendError, BackendResult on error)
	Content string

	// Tool fields
	ToolName  string
	ToolID    string
	IsError   bool
	ToolInput map[string]interface{}

	// Session init
	SessionID string
	Model     string
	// PaneID is set by the herdr backend on session init: the herdr pane
	// hosting the agent's claude session, so a human can find it directly
	// (`herdr pane get <id>`, or attach to it). Other backends leave it empty.
	PaneID string

	// Result
	CostUSD       float64
	Subtype       string
	IsResultError bool
}

// PerAgentBackend is implemented by backends whose instances carry per-agent
// state and therefore must not be shared between agents. Engine.StartAgent
// calls NewForAgent for every agent, including the ones that fall back to the
// engine's single default backend — which is where sharing would otherwise
// happen, since one instance is installed as that default at startup
// (internal/daemon/cmd.go).
//
// herdrBackend needs this: its state includes the herdr agent name, which
// herdr requires to be unique among live agents, so two concurrent agents on
// one instance would fight over the same herdr session. A stateless backend
// simply does not implement it.
type PerAgentBackend interface {
	// NewForAgent returns a fresh Backend for the given agent ID. The
	// receiver is left untouched and may be reused as a template.
	NewForAgent(agentID string) Backend
}

// GracefulBackend is implemented by backends whose subprocess owns resources
// that outlive it unless it is given the chance to release them. Agent.kill
// sends SIGTERM and waits up to this long for a clean exit before falling
// back to SIGKILL; a backend that does not implement it is SIGKILLed
// immediately, as before.
//
// herdrBackend needs this too: the pane and the interactive claude inside it
// belong to the herdr server, not to the driver process, so a driver killed
// outright leaves both running.
type GracefulBackend interface {
	TerminationGrace() time.Duration
}

// PreflightChecker is implemented by backends with external prerequisites
// that can be checked cheaply up front (a CLI on PATH, a server running).
// The daemon calls it once at startup for the configured default backend and
// logs what it reports, so a misconfiguration surfaces there instead of as a
// cryptic failure on the first spawn.
type PreflightChecker interface {
	Preflight() error
}

// TimeoutAwareBackend is implemented by backends whose subprocess has its own
// internal per-turn wait, independent of Engine.StartAgent's overall
// AgentOptions.Timeout kill-switch, that needs to be sized to it. Without
// this a backend-internal default can time out a turn well before the
// operator's own --timeout would, or ignore a shorter one entirely.
//
// herdrBackend needs this: `herdr agent prompt --wait` has its own
// --prompt-timeout-ms, separate from (and previously oblivious to) the
// agent's --timeout.
type TimeoutAwareBackend interface {
	// SetTurnTimeout tells the backend the agent's configured overall
	// timeout. Called once, before the backend's subprocess is started.
	SetTurnTimeout(d time.Duration)
}

// currentModels holds the active model table. nil means "use the compiled-in
// defaults", which keeps backend construction free of filesystem access.
var currentModels atomic.Pointer[config.ModelsConfig]

// SetModels installs the model table used by all backends to resolve short
// model names and the classifier model. Call once at startup; when unset the
// compiled-in defaults from config.DefaultModelsConfig apply. Entries the
// table omits are filled in from those defaults.
func SetModels(models *config.ModelsConfig) {
	models.ApplyDefaults()
	currentModels.Store(models)
}

// Models returns the active model table. Never nil.
func Models() *config.ModelsConfig {
	if models := currentModels.Load(); models != nil {
		return models
	}
	return config.DefaultModelsConfig()
}

// NewClaudeBackend returns a Backend that drives the claude CLI via stream-json.
func NewClaudeBackend() Backend { return &claudeBackend{} }

// BackendByName resolves a backend by name ("claude", "pi" or "herdr").
// Returns nil for unknown names so callers can fall back to the engine default.
func BackendByName(name string) Backend {
	switch name {
	case "claude":
		return NewClaudeBackend()
	case "pi":
		return NewPiBackend("")
	case "herdr":
		return NewHerdrBackend()
	default:
		return nil
	}
}

// ConfiguredBackend resolves the backend from the user's persisted AI.Provider
// config ("claude", "pi" or "herdr"). Used wherever a backend is needed before
// the daemon has had a chance to apply config (see cmd.go) or without a daemon
// at all — the classifier's nil-backend fallback, in particular.
//
// pi requires enterprise tokens that can run out, so it is never used as the
// last-resort default here: when config can't be read or names an unknown
// provider, this falls back to herdr, which has no such quota to exhaust.
func ConfiguredBackend() Backend {
	if cfg, err := config.LoadDefaultConfig(); err == nil && cfg != nil {
		if b := BackendByName(cfg.AI.Provider); b != nil {
			return b
		}
	}
	return NewHerdrBackend()
}

// NewPiBackend returns a Backend that drives the pi CLI via RPC mode.
// oneShotModel is the full model ID used for one-shot prompt calls
// (e.g. "anthropic/claude-haiku-4-5"). When empty it is resolved from the
// model table's classifier_model entry (see Models) at call time.
func NewPiBackend(oneShotModel string) Backend {
	return &piBackend{oneShotModel: oneShotModel}
}
