package engine

// Backend abstracts the coding-agent subprocess protocol.
// Two implementations are provided: ClaudeBackend (stream-json) and PiBackend (RPC).
// Each agent owns its own Backend instance so implementations may keep per-agent state.
type Backend interface {
	// Name returns the backend identifier ("claude" or "pi").
	Name() string

	// Binary returns the executable name (e.g. "claude", "pi").
	Binary() string

	// Args returns the CLI args for launching the subprocess.
	// model and effort may be empty strings (use backend defaults).
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
	// prompt call: one prompt in, one text answer out, on a cheap model.
	// Used by the smart router (classifier.go) and by the one-shot prompt
	// helper in internal/oneshot (commit messages, MR descriptions).
	OneShotCommand(prompt string) (binary string, args []string)
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

	// Result
	CostUSD       float64
	Subtype       string
	IsResultError bool
}

// NewClaudeBackend returns a Backend that drives the claude CLI via stream-json.
func NewClaudeBackend() Backend { return &claudeBackend{} }

// BackendByName resolves a backend by name ("claude" or "pi").
// Returns nil for unknown names so callers can fall back to the engine default.
func BackendByName(name string) Backend {
	switch name {
	case "claude":
		return NewClaudeBackend()
	case "pi":
		return NewPiBackend("")
	default:
		return nil
	}
}

// NewPiBackend returns a Backend that drives the pi CLI via RPC mode.
// classifyModel is the full model ID used for one-shot classification
// (e.g. "anthropic/claude-haiku-4-5"). Defaults to claude-haiku-4-5 if empty.
func NewPiBackend(classifyModel string) Backend {
	if classifyModel == "" {
		classifyModel = "anthropic/claude-haiku-4-5"
	}
	return &piBackend{classifyModel: classifyModel}
}
