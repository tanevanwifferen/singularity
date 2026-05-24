package service

import "time"

// Event taxonomy for streaming subscriptions. Every streaming method on a
// service returns a <-chan of one of these event types plus a func() that
// cancels the subscription (idempotent; safe to call after the channel
// closes). Channels are closed by the implementation when the stream ends
// for any reason (cancellation, daemon shutdown, ctx done).

// AgentEvent is one update on the agent stream. It carries either a state
// transition (Kind == AgentEventState) or an output line
// (Kind == AgentEventOutput). Mirrors the existing WS events agent_started /
// agent_output / agent_complete / agent_error from internal/api.
type AgentEvent struct {
	Kind    AgentEventKind `json:"kind"`
	AgentID string         `json:"agent_id"`
	// State is populated when Kind == AgentEventState.
	State AgentState `json:"state,omitempty"`
	// Output is populated when Kind == AgentEventOutput.
	Output *OutputEntry `json:"output,omitempty"`
	// Err is populated when Kind == AgentEventError.
	Err       string    `json:"error,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// AgentEventKind enumerates the variants of AgentEvent.
type AgentEventKind string

const (
	AgentEventStarted  AgentEventKind = "started"
	AgentEventState    AgentEventKind = "state"
	AgentEventOutput   AgentEventKind = "output"
	AgentEventComplete AgentEventKind = "complete"
	AgentEventError    AgentEventKind = "error"
)

// ProjectEvent is one update on the project status stream — fired when the
// daemon detects a repo change (file watcher / git refresh) and re-aggregates
// project status. Mirrors WS event "project_update" / "repo_update".
type ProjectEvent struct {
	Handle ProjectHandle  `json:"handle"`
	Status *ProjectStatus `json:"status,omitempty"`
	// RepoName is set when only one repo changed; empty for full refreshes.
	RepoName  string    `json:"repo_name,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// PipelineEvent is one update on the pipeline status stream for a repo's
// tracked branches. Mirrors the planned (currently never-broadcast)
// "pipeline_update" WS event from internal/api/types.go.
type PipelineEvent struct {
	RepoPath  string                   `json:"repo_path"`
	Pipelines map[string]*PipelineInfo `json:"pipelines"`
	Timestamp time.Time                `json:"timestamp"`
}

// WorkflowEvent is one update on the feature-workflow stream — replaces the
// current workflowTickCmd polling. Mirrors planned "workflow_updated" WS event.
type WorkflowEvent struct {
	WorkflowKey string          `json:"workflow_key"` // branch name
	State       WorkflowState   `json:"state"`
	Status      *WorkflowStatus `json:"status,omitempty"`
	RepoUpdates []*WorkflowRepo `json:"repo_updates,omitempty"`
	Err         string          `json:"error,omitempty"`
	Timestamp   time.Time       `json:"timestamp"`
}

// SyncProgressEvent is one chunk of output from a long-running sync op
// (push/pull/fetch/pull-rebase/rebase-onto-main). Streamed instead of
// captured-as-blob per CALL-SITES gotcha #7. Mirrors planned "sync_progress".
type SyncProgressEvent struct {
	RepoPath  string    `json:"repo_path"`
	OpID      string    `json:"op_id"`
	Op        string    `json:"op"` // "push" | "pull" | "fetch" | "pull_rebase" | "rebase_onto_main"
	Line      string    `json:"line"`
	Done      bool      `json:"done"`
	Err       string    `json:"error,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// DiscoveryProgressEvent reports progress while DiscoverWorkflows scans the
// project for existing per-repo worktrees. Long-running enough to deserve
// streaming per CALL-SITES gotcha #7.
type DiscoveryProgressEvent struct {
	RepoName  string    `json:"repo_name"`
	Found     int       `json:"found"`
	Total     int       `json:"total"`
	Done      bool      `json:"done"`
	Err       string    `json:"error,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}
