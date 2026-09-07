package engine

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gitlab.com/tanevanwifferen1/singularity/internal/config"
)

// AgentState represents the lifecycle state of an agent
type AgentState int

const (
	AgentIdle AgentState = iota
	AgentRouting
	AgentStarting
	AgentRunning
	AgentComplete
	AgentError
	AgentKilled
)

func (s AgentState) String() string {
	switch s {
	case AgentIdle:
		return "idle"
	case AgentRouting:
		return "routing"
	case AgentStarting:
		return "starting"
	case AgentRunning:
		return "running"
	case AgentComplete:
		return "complete"
	case AgentError:
		return "error"
	case AgentKilled:
		return "killed"
	default:
		return "unknown"
	}
}

// Terminal reports whether the state is a final one: the agent has stopped
// and will not transition again (complete, error, or killed).
func (s AgentState) Terminal() bool {
	return s == AgentComplete || s == AgentError || s == AgentKilled
}

// Agent wraps a coding-agent subprocess with structured output streaming.
// The concrete protocol (claude stream-json, pi RPC, …) is delegated to Backend.
type Agent struct {
	ID        string     `json:"id"`
	WorkDir   string     `json:"work_dir"`
	Task      string     `json:"task"`
	Summary   string     `json:"summary"` // one-line summary for display in agent list
	State     AgentState `json:"state"`
	CreatedAt time.Time  `json:"created_at"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Error     string     `json:"error,omitempty"`
	ExitCode  int        `json:"exit_code"`

	// Output buffer
	output   []OutputEntry
	outputMu sync.Mutex

	// Process management
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdinMu  sync.Mutex
	stdout   io.ReadCloser
	stderr   io.ReadCloser
	done     chan struct{}
	doneOnce sync.Once
	mu       sync.Mutex

	// Configuration
	backend       Backend
	model         string
	effort        string
	allowedTools  []string
	maxTurns      int
	contextFiles  []string
	promptLogNote string // logged instead of the full task (see AgentOptions.PromptLogNote)

	// Cost tracking (populated from BackendResult event)
	TotalCostUSD float64 `json:"total_cost_usd,omitempty"`

	// Smart routing result (nil if not routed)
	RouteResult *ClassificationResult `json:"route_result,omitempty"`

	// sessionID assigned by the backend (used by claude follow-up envelopes)
	sessionID string

	// Worktree isolation fields
	useWorktree    bool
	worktreePath   string
	worktreeBranch string
	sourceRepoPath string
	sourceBranch   string
	MergeResult    string `json:"merge_result,omitempty"`

	// Sound notification config (copied from Engine at start time)
	soundCfg config.SoundConfig

	// notify is called after output or state changes to signal the engine's observer.
	notify func()
}

// OutputEntry represents a single output chunk from the agent.
// Source is one of: "text", "tool_use", "tool_result", "system", "error", "result", "user_input"
type OutputEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"`
	Content   string    `json:"content"`

	// Structured fields for tool events
	ToolName string `json:"tool_name,omitempty"`
	ToolID   string `json:"tool_id,omitempty"`
	IsError  bool   `json:"is_error,omitempty"`
}

// newAgent creates a new agent instance
func newAgent(id, workDir, task string, opts AgentOptions, backend Backend) *Agent {
	summary := opts.Summary
	if summary == "" {
		summary = extractSummary(task)
	}
	return &Agent{
		ID:            id,
		WorkDir:       workDir,
		Task:          task,
		Summary:       summary,
		State:         AgentIdle,
		CreatedAt:     time.Now(),
		output:        make([]OutputEntry, 0),
		done:          make(chan struct{}),
		backend:       backend,
		model:         opts.Model,
		effort:        opts.Effort,
		allowedTools:  opts.AllowedTools,
		maxTurns:      opts.MaxTurns,
		contextFiles:  opts.ContextFiles,
		promptLogNote: opts.PromptLogNote,
		useWorktree:   opts.UseWorktree,
	}
}

// extractSummary derives a one-line summary from the task prompt.
func extractSummary(task string) string {
	for _, line := range strings.SplitN(task, "\n", 10) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 80 {
			return line[:77] + "..."
		}
		return line
	}
	return task
}

// promptLogLimit caps how much of a prompt is copied into the output stream.
// The full task stays available via the agent's Task field (`agents get`).
const promptLogLimit = 4000

// truncateForLog shortens s to at most max bytes (snapped to a rune boundary),
// appending an explicit marker with the elided size — never a silent cut.
func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return fmt.Sprintf("%s\n… [%d bytes elided; full task available via agents get]", s[:cut], len(s)-cut)
}

// truncate shortens a string to maxLen, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
