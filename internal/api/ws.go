package api

import "time"

// WSMessage is the canonical WebSocket frame envelope. Every frame carries a
// Type (one of the WSEventXxx constants below, or "stream:<id>" for stream
// frames) and a payload whose Go type is documented in
// docs/design/WIRE-CONTRACT.md §3.
type WSMessage struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

// WSEvent type constants — server-to-client.
const (
	WSEventRepoUpdate        = "repo_update"
	WSEventBranchUpdate      = "branch_update"
	WSEventPipelineUpdate    = "pipeline_update"
	WSEventProjectUpdate     = "project_update"
	WSEventAgentStarted      = "agent_started"
	WSEventAgentOutput       = "agent_output"
	WSEventAgentComplete     = "agent_complete"
	WSEventAgentError        = "agent_error"
	WSEventWorkflowUpdated   = "workflow_updated"
	WSEventSyncProgress      = "sync_progress"
	WSEventDiscoveryProgress = "discovery_progress"
	WSEventError             = "error"
	WSEventSubscribed        = "subscribed"
)

// WSStreamPrefix is the prefix used by per-stream WS frame types. The full
// type is "stream:<id>" where <id> is a StreamStartResponse.StreamID.
const WSStreamPrefix = "stream:"

// WSMsgType constants — client-to-server.
const (
	WSMsgSubscribe       = "subscribe"
	WSMsgRefreshRepo     = "refresh_repo"
	WSMsgSubscribeStream = "subscribe_stream"
	WSMsgCancelStream    = "cancel_stream"
)

// BranchUpdatePayload is the payload of a "branch_update" WS frame.
type BranchUpdatePayload struct {
	Branch string `json:"branch"`
}

// ProjectUpdatePayload is the payload of a "project_update" WS frame. Wraps
// the bare ProjectStatus the legacy server emitted directly so the client
// can decode it as a stable envelope (fixes COVERAGE.md §3e mismatch).
type ProjectUpdatePayload struct {
	Handle   string         `json:"handle,omitempty"`
	Status   *ProjectStatus `json:"status"`
	RepoName string         `json:"repo_name,omitempty"`
}

// AgentStartedPayload is the payload of an "agent_started" WS frame. The
// canonical field is agent_id (renamed from session_id, fixing the COVERAGE.md
// §3e mismatch). Task and WorkDir help views render the new entry without
// a follow-up GET.
type AgentStartedPayload struct {
	AgentID string `json:"agent_id"`
	Task    string `json:"task,omitempty"`
	WorkDir string `json:"work_dir,omitempty"`
}

// AgentOutputPayload is the payload of an "agent_output" WS frame. The
// daemon emits one frame per new OutputEntry (not the full buffer) so the
// client tail can append incrementally.
type AgentOutputPayload struct {
	AgentID string       `json:"agent_id"`
	Entry   *OutputEntry `json:"entry"`
}

// AgentCompletePayload is the payload of an "agent_complete" WS frame.
type AgentCompletePayload struct {
	AgentID  string `json:"agent_id"`
	State    string `json:"state"`
	ExitCode int    `json:"exit_code"`
}

// AgentErrorPayload is the payload of an "agent_error" WS frame.
type AgentErrorPayload struct {
	AgentID string `json:"agent_id"`
	Error   string `json:"error"`
}

// ErrorPayload is the payload of an "error" WS frame.
type ErrorPayload struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// SubscribedPayload is the payload of a "subscribed" ack frame.
type SubscribedPayload struct {
	Status string `json:"status"`
}

// RefreshRepoPayload is the payload of an incoming "refresh_repo" message.
// Path is optional; when empty the daemon refreshes its current repo.
type RefreshRepoPayload struct {
	Path string `json:"path,omitempty"`
}

// SubscribeStreamPayload is the payload of an incoming "subscribe_stream"
// message. The client opens a subscription to a stream that was previously
// minted by an HTTP endpoint marked **stream** in WIRE-CONTRACT.md.
type SubscribeStreamPayload struct {
	StreamID string `json:"stream_id"`
}

// CancelStreamPayload is the payload of an incoming "cancel_stream" message.
type CancelStreamPayload struct {
	StreamID string `json:"stream_id"`
}

// StreamFrame is the payload shape for "stream:<id>" frames. Frame is the
// serialized event from the underlying stream channel (e.g. SyncProgressEvent,
// AgentEvent). Done=true marks the terminal frame for the stream.
type StreamFrame struct {
	StreamID  string      `json:"stream_id"`
	Frame     interface{} `json:"frame,omitempty"`
	Done      bool        `json:"done,omitempty"`
	Error     string      `json:"error,omitempty"`
	Code      string      `json:"code,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}
