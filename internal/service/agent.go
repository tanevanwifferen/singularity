package service

import "context"

// AgentService covers everything the TUI does with the agent engine: spawn,
// resume, send input, kill/remove, list, snapshot, output streaming, and
// engine-wide stats. The engine itself lives entirely daemon-side per
// CALL-SITES §3.6 — Shutdown / SetSoundConfig / PruneStaleWorktrees do
// NOT appear here (those are daemon-startup / admin-CLI concerns).
//
// The historic Engine.OnAgentUpdate callback (the "biggest doesn't-fit-RPC
// hook" per the audit) is exposed as Subscribe: a channel of AgentEvent that
// the daemon writes into. ParseJiraActions is exposed via JiraService.
type AgentService interface {
	// Start spawns a new agent for the given working dir + task.
	// Returns ErrAgentLimit when the engine's MaxAgents cap is reached.
	Start(ctx context.Context, workDir, prompt string, opts AgentOptions) (agentID string, err error)

	// Resume creates a new agent that inherits the prior agent's
	// conversation history and appends the new user message.
	Resume(ctx context.Context, agentID, message string, opts AgentOptions) (newAgentID string, err error)

	// SendInput pushes a message to a running agent's stdin.
	SendInput(ctx context.Context, agentID, message string) error

	// Kill terminates the agent's subprocess (SIGTERM, then SIGKILL).
	Kill(ctx context.Context, agentID string) error

	// Remove drops the agent from the engine's registry. Idempotent.
	Remove(ctx context.Context, agentID string) error

	// List returns a snapshot of every agent currently known to the engine.
	List(ctx context.Context) ([]AgentSnapshot, error)

	// Get returns one agent's snapshot or ErrNotFound.
	Get(ctx context.Context, agentID string) (*AgentSnapshot, error)

	// Output returns buffered output entries from offset onward (suitable
	// for backfill before subscribing to the live stream).
	Output(ctx context.Context, agentID string, offset int) ([]OutputEntry, error)

	// Stats returns engine-wide counts (running/idle/total).
	Stats(ctx context.Context) (EngineStats, error)

	// MaxAgents returns the engine's configured max-concurrent cap.
	MaxAgents(ctx context.Context) (int, error)

	// Subscribe streams AgentEvents for a single agent (state transitions
	// + output lines). Used by the agent view's live tail. Closing the
	// channel signals the stream ended (agent removed, ctx canceled).
	Subscribe(ctx context.Context, agentID string) (<-chan AgentEvent, func(), error)

	// SubscribeAll streams AgentEvents for every agent. Used by views that
	// render multi-agent status (workflows.go, overview.go).
	SubscribeAll(ctx context.Context) (<-chan AgentEvent, func(), error)

	// ReloadModelsConfig reloads the global models configuration from disk.
	// New agents will use the updated model aliases and classifier model.
	ReloadModelsConfig()
}
