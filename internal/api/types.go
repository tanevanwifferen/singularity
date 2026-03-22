package api

import "git-frontend/internal/git"

// API types shared between server and client

// StatusResponse is the response for /api/status
type StatusResponse struct {
	Version   string `json:"version"`
	Server    string `json:"server"`
	RepoPath  string `json:"repo_path,omitempty"`
	RepoInfo  *git.RepoInfo `json:"repo_info,omitempty"`
	Error     string `json:"error,omitempty"`
}

// RepoRequest is the request for repo operations
type RepoRequest struct {
	Path string `json:"path"`
}

// BranchComparisonRequest is the request for branch comparison
type BranchComparisonRequest struct {
	RepoPath  string `json:"repo_path"`
	BranchA  string `json:"branch_a"`
	BranchB  string `json:"branch_b"`
}

// BranchDiffRequest is the request for branch diff
type BranchDiffRequest struct {
	RepoPath  string `json:"repo_path"`
	BranchA  string `json:"branch_a"`
	BranchB  string `json:"branch_b"`
}

// CommitMessageRequest is the request for commit message generation
type CommitMessageRequest struct {
	RepoPath string `json:"repo_path"`
}

// MRRequest is the request for creating a merge request
type MRRequest struct {
	RepoPath      string   `json:"repo_path"`
	SourceBranch  string   `json:"source_branch"`
	TargetBranch  string   `json:"target_branch"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Reviewers     []string `json:"reviewers"`
}

// WSMessage represents a WebSocket message
type WSMessage struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

// WSEvent types
const (
	WSEventBranchUpdate  = "branch_update"
	WSEventRepoUpdate    = "repo_update"
	WSEventPipelineUpdate = "pipeline_update"
	WSEventError         = "error"
)

// APIResponse is a generic API response
type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// Agent Engine API types

// AgentStartRequest is the request for starting a new agent
type AgentStartRequest struct {
	ProjectPath  string   `json:"project_path"`
	Task         string   `json:"task"`
	Model        string   `json:"model,omitempty"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
	MaxTurns     int      `json:"max_turns,omitempty"`
	TimeoutSecs  int      `json:"timeout_secs,omitempty"`
}

// AgentMessageRequest is the request for sending a message to an agent
type AgentMessageRequest struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

// AgentQueryRequest is the request for querying an agent
type AgentQueryRequest struct {
	SessionID string `json:"session_id"`
	Offset    int    `json:"offset,omitempty"`
}

// WSEvent types for agent engine
const (
	WSEventAgentStarted  = "agent_started"
	WSEventAgentOutput   = "agent_output"
	WSEventAgentComplete = "agent_complete"
	WSEventAgentError    = "agent_error"
)

// WSEvent types for project updates
const (
	WSEventProjectUpdate = "project_update"
)

// Project API types

// ProjectListResponse is the response for listing available projects
type ProjectListResponse struct {
	Projects []string `json:"projects"`
	Loaded   []string `json:"loaded"`
}

// ProjectLoadRequest is the request for loading a project
type ProjectLoadRequest struct {
	Key string `json:"key"`
}

// ProjectBranchRequest is the request for cross-repo branch operations
type ProjectBranchRequest struct {
	Key    string `json:"key"`
	Branch string `json:"branch"`
}
