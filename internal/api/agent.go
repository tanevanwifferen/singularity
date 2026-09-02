package api

import (
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// Agent DTOs. AgentSnapshot is aliased from internal/engine (via service)
// purely so client code can name the type; for wire transport the server
// projects it into AgentSnapshotDTO with snake_case JSON tags (the engine's
// AgentSnapshot has no tags and would serialize as PascalCase).
type (
	AgentSnapshot = service.AgentSnapshot
	OutputEntry   = service.OutputEntry
	AgentOptions  = service.AgentOptions
	EngineStats   = service.EngineStats
)

// AgentSnapshotDTO is the snake_case wire projection of engine.AgentSnapshot.
// It is the value that travels inside APIResponse.Data for Agent.Get / List
// and inside the agent_started / agent_complete WS payloads when a full
// snapshot is broadcast.
type AgentSnapshotDTO struct {
	ID        string     `json:"id"`
	WorkDir   string     `json:"work_dir"`
	Task      string     `json:"task"`
	Summary   string     `json:"summary"`
	State     string     `json:"state"`
	CreatedAt time.Time  `json:"created_at"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	// ExitCode is only set once the agent reached a terminal state
	// (complete/error/killed); nil (omitted) while it is still running.
	ExitCode     *int    `json:"exit_code,omitempty"`
	Error        string  `json:"error,omitempty"`
	TotalCostUSD float64 `json:"total_cost_usd,omitempty"`
	MergeResult  string  `json:"merge_result,omitempty"`
}

// AgentSnapshotToDTO projects an engine.AgentSnapshot into its wire shape.
// The state enum is rendered via its String() form so JSON consumers see
// "running" rather than the integer ordinal. ExitCode is omitted for
// non-terminal states: the engine's zero value would otherwise read as a
// successful exit while the agent is still running.
func AgentSnapshotToDTO(s AgentSnapshot) AgentSnapshotDTO {
	dto := AgentSnapshotDTO{
		ID:           s.ID,
		WorkDir:      s.WorkDir,
		Task:         s.Task,
		Summary:      s.Summary,
		State:        s.State.String(),
		CreatedAt:    s.CreatedAt,
		StartedAt:    s.StartedAt,
		EndedAt:      s.EndedAt,
		Error:        s.Error,
		TotalCostUSD: s.TotalCostUSD,
		MergeResult:  s.MergeResult,
	}
	if s.State.Terminal() {
		code := s.ExitCode
		dto.ExitCode = &code
	}
	return dto
}

// AgentStartRequest is the body for POST /api/agent/start.
type AgentStartRequest struct {
	ProjectPath  string   `json:"project_path"`
	Task         string   `json:"task"`
	Model        string   `json:"model,omitempty"`
	Effort       string   `json:"effort,omitempty"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
	MaxTurns     int      `json:"max_turns,omitempty"`
	TimeoutSecs  int      `json:"timeout_secs,omitempty"`
	ContextFiles []string `json:"context_files,omitempty"`
	SmartRoute   bool     `json:"smart_route,omitempty"`
	UseWorktree  bool     `json:"use_worktree,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	// Backend selects the agent runtime: "claude" or "pi".
	// Empty means use the daemon's current default.
	Backend string `json:"backend,omitempty"`
}

// AgentResumeRequest is the body for POST /api/agent/resume.
type AgentResumeRequest struct {
	AgentID      string   `json:"agent_id"`
	Message      string   `json:"message"`
	Model        string   `json:"model,omitempty"`
	Effort       string   `json:"effort,omitempty"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
	MaxTurns     int      `json:"max_turns,omitempty"`
	TimeoutSecs  int      `json:"timeout_secs,omitempty"`
	ContextFiles []string `json:"context_files,omitempty"`
	SmartRoute   bool     `json:"smart_route,omitempty"`
	UseWorktree  bool     `json:"use_worktree,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	// Backend selects the agent runtime: "claude" or "pi".
	// Empty means use the daemon's current default.
	Backend string `json:"backend,omitempty"`
}

// AgentStartResponse is the body for POST /api/agent/start and resume, plus
// the JiraService AI workflows that spawn an agent.
type AgentStartResponse struct {
	AgentID string `json:"agent_id"`
}

// AgentQueryRequest is the body for POST /api/agent/kill, /api/agent/remove
// (and the legacy GET /api/agent/output?session_id= query when JSON-bodied).
type AgentQueryRequest struct {
	AgentID string `json:"agent_id"`
	// SessionID is the legacy field name (kept for backward compat with the
	// pre-rename TUI). New code uses AgentID.
	SessionID string `json:"session_id,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}

// ResolvedID returns AgentID if set, falling back to SessionID. Used by
// handlers that must accept either spelling during the rename window.
func (q AgentQueryRequest) ResolvedID() string {
	if q.AgentID != "" {
		return q.AgentID
	}
	return q.SessionID
}

// AgentInputRequest is the body for POST /api/agent/input.
type AgentInputRequest struct {
	AgentID string `json:"agent_id"`
	// SessionID is the legacy field name; see AgentQueryRequest.ResolvedID.
	SessionID string `json:"session_id,omitempty"`
	Message   string `json:"message"`
}

// ResolvedID returns AgentID if set, falling back to SessionID.
func (q AgentInputRequest) ResolvedID() string {
	if q.AgentID != "" {
		return q.AgentID
	}
	return q.SessionID
}

// AgentListResponse is the body for GET /api/agent/list.
type AgentListResponse struct {
	Agents []AgentSnapshotDTO `json:"agents"`
}

// AgentOutputResponse is the body for GET /api/agent/output.
type AgentOutputResponse struct {
	AgentID string        `json:"agent_id"`
	Entries []OutputEntry `json:"entries"`
}

// AgentMaxResponse is the body for GET /api/agent/max.
type AgentMaxResponse struct {
	Max int `json:"max"`
}

// AgentSubscribeRequest is the body for POST /api/agent/subscribe.
type AgentSubscribeRequest struct {
	AgentID string `json:"agent_id"`
}
