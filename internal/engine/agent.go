package engine

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

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
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdinMu sync.Mutex
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	done    chan struct{}
	mu      sync.Mutex

	// Configuration
	backend      Backend
	model        string
	effort       string
	allowedTools []string
	maxTurns     int
	contextFiles []string

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
		ID:           id,
		WorkDir:      workDir,
		Task:         task,
		Summary:      summary,
		State:        AgentIdle,
		CreatedAt:    time.Now(),
		output:       make([]OutputEntry, 0),
		done:         make(chan struct{}),
		backend:      backend,
		model:        opts.Model,
		effort:       opts.Effort,
		allowedTools: opts.AllowedTools,
		maxTurns:     opts.MaxTurns,
		contextFiles: opts.ContextFiles,
		useWorktree:  opts.UseWorktree,
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

// start launches the coding-agent subprocess via the configured Backend.
func (a *Agent) start() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.State != AgentIdle && a.State != AgentRouting {
		return fmt.Errorf("agent %s is in state %s, expected idle or routing", a.ID, a.State)
	}

	a.State = AgentStarting

	binary, args := a.backend.Binary(), a.backend.Args(a.model, a.effort, a.maxTurns, a.allowedTools)
	a.cmd = exec.Command(binary, args...)
	a.cmd.Dir = a.WorkDir
	a.cmd.Env = a.backend.Env()

	var err error
	a.stdin, err = a.cmd.StdinPipe()
	if err != nil {
		a.setState(AgentError)
		a.Error = fmt.Sprintf("stdin pipe: %v", err)
		return fmt.Errorf("agent %s stdin pipe: %w", a.ID, err)
	}
	a.stdout, err = a.cmd.StdoutPipe()
	if err != nil {
		a.setState(AgentError)
		a.Error = fmt.Sprintf("stdout pipe: %v", err)
		return fmt.Errorf("agent %s stdout pipe: %w", a.ID, err)
	}
	a.stderr, err = a.cmd.StderrPipe()
	if err != nil {
		a.setState(AgentError)
		a.Error = fmt.Sprintf("stderr pipe: %v", err)
		return fmt.Errorf("agent %s stderr pipe: %w", a.ID, err)
	}

	if err := a.cmd.Start(); err != nil {
		a.setState(AgentError)
		a.Error = fmt.Sprintf("start: %v", err)
		return fmt.Errorf("agent %s start: %w", a.ID, err)
	}

	now := time.Now()
	a.StartedAt = &now
	a.State = AgentRunning

	if a.RouteResult != nil {
		a.appendOutputLocked("system", fmt.Sprintf("Routed → model=%s effort=%s (%s: %s)",
			a.RouteResult.Model, a.RouteResult.Effort, a.RouteResult.Category, a.RouteResult.Reason))
	}
	a.appendOutputLocked("system", fmt.Sprintf("Agent %s started [%s]", a.ID, a.backend.Name()))

	go a.streamOutput(a.stdout)
	go a.streamStderr(a.stderr)
	go a.waitForExit()

	// Send post-start configuration commands (e.g. thinking level for pi).
	for _, cmd := range a.backend.PostStartCommands(a.effort) {
		a.stdinMu.Lock()
		_, _ = a.stdin.Write(cmd)
		a.stdinMu.Unlock()
	}

	// Send the initial task.
	go func() {
		task := a.buildTask()
		if err := a.sendInitialInput(task); err != nil {
			a.appendOutput("error", fmt.Sprintf("Failed to send initial task: %v", err))
		}
	}()

	return nil
}

// buildTask prepends any context file contents to the agent's task string.
func (a *Agent) buildTask() string {
	if len(a.contextFiles) == 0 {
		return a.Task
	}

	var context strings.Builder
	for _, path := range a.contextFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			a.appendOutput("system", fmt.Sprintf("Warning: could not read context file %s: %v", path, err))
			continue
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		context.WriteString(fmt.Sprintf("<context file=%q>\n%s\n</context>\n\n", path, content))
	}

	if context.Len() == 0 {
		return a.Task
	}
	return context.String() + a.Task
}

// sendInitialInput sends the first task over stdin using backend.InitialInput.
func (a *Agent) sendInitialInput(task string) error {
	a.mu.Lock()
	sid := a.sessionID
	a.mu.Unlock()

	data, err := a.backend.InitialInput(task, sid)
	if err != nil {
		return fmt.Errorf("backend InitialInput: %w", err)
	}
	a.stdinMu.Lock()
	defer a.stdinMu.Unlock()
	if a.stdin == nil {
		return fmt.Errorf("stdin closed before initial task could be sent")
	}
	_, err = a.stdin.Write(data)
	return err
}

// streamOutput reads JSONL from the backend subprocess stdout, normalises each
// line via backend.ParseEvent, and dispatches the results.
func (a *Agent) streamOutput(r io.ReadCloser) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		events, err := a.backend.ParseEvent(line)
		if err != nil {
			a.appendOutput("error", fmt.Sprintf("parse error: %v", err))
			continue
		}
		for _, ev := range events {
			a.handleBackendEvent(ev)
		}
	}
}

// handleBackendEvent dispatches a normalised BackendEvent to the output buffer
// and triggers state transitions.
func (a *Agent) handleBackendEvent(ev *BackendEvent) {
	switch ev.Kind {
	case BackendText:
		if ev.Content != "" {
			a.appendOutput("text", ev.Content)
		}

	case BackendToolUse:
		summary := formatToolUseSummary(ev.ToolName, ev.ToolInput)
		entry := OutputEntry{
			Timestamp: time.Now(),
			Source:    "tool_use",
			Content:   summary,
			ToolName:  ev.ToolName,
			ToolID:    ev.ToolID,
		}
		a.outputMu.Lock()
		a.output = append(a.output, entry)
		a.outputMu.Unlock()
		if a.notify != nil {
			a.notify()
		}

	case BackendToolResult:
		entry := OutputEntry{
			Timestamp: time.Now(),
			Source:    "tool_result",
			Content:   ev.Content,
			ToolID:    ev.ToolID,
			IsError:   ev.IsError,
		}
		a.outputMu.Lock()
		a.output = append(a.output, entry)
		a.outputMu.Unlock()
		if a.notify != nil {
			a.notify()
		}

	case BackendSessionInit:
		if ev.Model != "" {
			a.appendOutput("system", fmt.Sprintf("Model: %s", ev.Model))
		}
		if ev.SessionID != "" {
			a.mu.Lock()
			a.sessionID = ev.SessionID
			a.mu.Unlock()
		}

	case BackendResult:
		a.handleResult(ev)

	case BackendError:
		if ev.Content != "" {
			a.appendOutput("error", fmt.Sprintf("Error: %s", ev.Content))
		}

	case BackendIgnore:
		// nothing to do
	}
}

// handleResult processes a BackendResult event: updates cost, transitions state,
// and triggers worktree merge-back on successful completion.
func (a *Agent) handleResult(ev *BackendEvent) {
	a.mu.Lock()
	if ev.CostUSD > 0 {
		a.TotalCostUSD = ev.CostUSD
	}
	if a.State == AgentRunning || a.State == AgentStarting {
		if ev.IsResultError {
			now := time.Now()
			a.EndedAt = &now
			a.State = AgentError
			a.Error = ev.Content
		} else if !a.useWorktree {
			now := time.Now()
			a.EndedAt = &now
			a.State = AgentComplete
		}
		// useWorktree && !isError: stay AgentRunning until merge finishes below
	}
	a.mu.Unlock()

	playSound(a.soundCfg)

	if ev.IsResultError {
		errMsg := ev.Content
		if errMsg == "" {
			errMsg = "agent exited with error (no message provided)"
		}
		a.appendOutput("error", fmt.Sprintf("Error: %s", errMsg))
	} else {
		status := "completed"
		if ev.Subtype != "" && ev.Subtype != "success" {
			status = ev.Subtype
		}
		costStr := ""
		if ev.CostUSD > 0 {
			costStr = fmt.Sprintf(" ($%.4f)", ev.CostUSD)
		}
		a.appendOutput("result", fmt.Sprintf("Agent %s%s", status, costStr))
	}

	if a.useWorktree && !ev.IsResultError {
		mergeResult := a.mergeWorktreeBack()
		a.mu.Lock()
		a.MergeResult = mergeResult
		now := time.Now()
		a.EndedAt = &now
		a.State = AgentComplete
		a.mu.Unlock()
	} else if a.useWorktree && ev.IsResultError {
		a.appendOutput("system", "Worktree preserved (agent errored) — merge manually or clean up later")
	}
}

// streamStderr reads stderr and appends as error entries
func (a *Agent) streamStderr(r io.ReadCloser) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			a.appendOutput("error", line)
		}
	}
}

// waitForExit waits for the subprocess to finish
func (a *Agent) waitForExit() {
	err := a.cmd.Wait()

	a.mu.Lock()
	if a.EndedAt == nil {
		now := time.Now()
		a.EndedAt = &now
	}

	var exitErrMsg string
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			a.ExitCode = exitErr.ExitCode()
		}
		if a.State == AgentRunning || a.State == AgentStarting {
			a.State = AgentError
			a.Error = err.Error()
			exitErrMsg = err.Error()
		} else if a.State == AgentError && a.Error == "" {
			a.Error = err.Error()
			exitErrMsg = err.Error()
		}
	} else {
		if a.State == AgentRunning {
			a.State = AgentComplete
		}
	}
	a.mu.Unlock()

	if exitErrMsg != "" {
		a.appendOutput("error", fmt.Sprintf("Process exit: %s", exitErrMsg))
	} else if a.notify != nil {
		a.notify()
	}

	close(a.done)
}

// sendInput sends a follow-up message to the agent's stdin.
// Accepts messages to running, completed, or soft-closed agents (process stays
// alive until explicitly removed via RemoveAgent).
func (a *Agent) sendInput(message string) error {
	a.mu.Lock()
	if a.State != AgentRunning && a.State != AgentComplete && a.State != AgentKilled {
		a.mu.Unlock()
		return fmt.Errorf("agent %s is in state %s, cannot send input", a.ID, a.State)
	}
	if a.cmd == nil || a.cmd.Process == nil {
		a.mu.Unlock()
		return fmt.Errorf("agent %s process is no longer running", a.ID)
	}

	prevState := a.State
	prevEndedAt := a.EndedAt

	// Resume agent back to running when sending a follow-up
	if a.State == AgentComplete || a.State == AgentKilled {
		a.State = AgentRunning
		a.EndedAt = nil
	}

	// isStreaming: was the agent already in a running (mid-response) state
	// before this call? Used by some backends to choose the input message type.
	isStreaming := prevState == AgentRunning

	sid := a.sessionID
	a.mu.Unlock()

	data, err := a.backend.FollowUpInput(message, sid, isStreaming)
	if err != nil {
		a.mu.Lock()
		a.State = prevState
		a.EndedAt = prevEndedAt
		a.mu.Unlock()
		return fmt.Errorf("backend FollowUpInput: %w", err)
	}

	a.stdinMu.Lock()
	defer a.stdinMu.Unlock()

	if a.stdin == nil {
		a.mu.Lock()
		a.State = prevState
		a.EndedAt = prevEndedAt
		a.mu.Unlock()
		return fmt.Errorf("agent %s stdin not available (process exited)", a.ID)
	}

	_, err = a.stdin.Write(data)
	if err != nil {
		a.mu.Lock()
		a.State = prevState
		a.EndedAt = prevEndedAt
		a.mu.Unlock()
		return fmt.Errorf("write to stdin: %w (process may have exited)", err)
	}

	a.appendOutput("user_input", message)
	return nil
}

// softClose marks the agent as killed without terminating the subprocess.
// The process stays alive and can still receive messages until RemoveAgent.
func (a *Agent) softClose() {
	a.mu.Lock()
	changed := false
	if a.State == AgentRunning || a.State == AgentStarting || a.State == AgentComplete {
		a.State = AgentKilled
		now := time.Now()
		if a.EndedAt == nil {
			a.EndedAt = &now
		}
		changed = true
	}
	a.mu.Unlock()

	if changed && a.notify != nil {
		a.notify()
	}
}

// kill terminates the agent subprocess and cleans up any worktree.
func (a *Agent) kill() error {
	a.mu.Lock()

	wtPath := a.worktreePath
	sourceRepoPath := a.sourceRepoPath
	wtBranch := a.worktreeBranch

	if a.cmd == nil || a.cmd.Process == nil {
		a.mu.Unlock()
		if wtPath != "" {
			cleanupWorktree(sourceRepoPath, wtPath, wtBranch)
		}
		return nil
	}

	a.stdinMu.Lock()
	if a.stdin != nil {
		a.stdin.Close()
		a.stdin = nil
	}
	a.stdinMu.Unlock()

	if a.State != AgentKilled {
		a.State = AgentKilled
	}
	a.appendOutputLocked("system", "Agent killed")

	err := a.cmd.Process.Kill()
	a.mu.Unlock()

	if wtPath != "" {
		cleanupWorktree(sourceRepoPath, wtPath, wtBranch)
	}

	if err != nil {
		return fmt.Errorf("kill agent %s: %w", a.ID, err)
	}
	return nil
}

// getOutput returns output entries from the given offset.
func (a *Agent) getOutput(offset int) []OutputEntry {
	a.outputMu.Lock()
	defer a.outputMu.Unlock()

	if offset >= len(a.output) {
		return nil
	}
	if offset < 0 {
		offset = 0
	}

	result := make([]OutputEntry, len(a.output)-offset)
	copy(result, a.output[offset:])
	return result
}

// getFullOutput returns all text content joined as a single string.
func (a *Agent) getFullOutput() string {
	a.outputMu.Lock()
	defer a.outputMu.Unlock()

	var parts []string
	for _, entry := range a.output {
		if entry.Source == "text" || entry.Source == "tool_use" || entry.Source == "tool_result" {
			parts = append(parts, entry.Content)
		}
	}
	return strings.Join(parts, "\n")
}

// appendOutput appends an output entry (thread-safe).
// Output is suppressed after the agent has been killed.
func (a *Agent) appendOutput(source, content string) {
	a.mu.Lock()
	killed := a.State == AgentKilled
	a.mu.Unlock()
	if killed {
		return
	}

	a.outputMu.Lock()
	a.output = append(a.output, OutputEntry{
		Timestamp: time.Now(),
		Source:    source,
		Content:   content,
	})
	a.outputMu.Unlock()

	if a.notify != nil {
		a.notify()
	}
}

// appendOutputLocked appends output when mu is already held (uses outputMu only).
func (a *Agent) appendOutputLocked(source, content string) {
	a.outputMu.Lock()
	a.output = append(a.output, OutputEntry{
		Timestamp: time.Now(),
		Source:    source,
		Content:   content,
	})
	a.outputMu.Unlock()

	if a.notify != nil {
		a.notify()
	}
}

// setState changes state (caller must hold mu).
func (a *Agent) setState(state AgentState) {
	a.State = state
}

// Snapshot returns a point-in-time copy of the agent's mutable fields (thread-safe).
func (a *Agent) Snapshot() AgentSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return AgentSnapshot{
		ID:           a.ID,
		WorkDir:      a.WorkDir,
		Task:         a.Task,
		Summary:      a.Summary,
		State:        a.State,
		CreatedAt:    a.CreatedAt,
		StartedAt:    a.StartedAt,
		EndedAt:      a.EndedAt,
		ExitCode:     a.ExitCode,
		Error:        a.Error,
		TotalCostUSD: a.TotalCostUSD,
		RouteResult:  a.RouteResult,
		MergeResult:  a.MergeResult,
		BackendName:  a.backend.Name(),
	}
}

// AgentSnapshot is a point-in-time copy of an agent's state, safe to read without locks.
type AgentSnapshot struct {
	ID           string
	WorkDir      string
	Task         string
	Summary      string
	State        AgentState
	CreatedAt    time.Time
	StartedAt    *time.Time
	EndedAt      *time.Time
	ExitCode     int
	Error        string
	TotalCostUSD float64
	RouteResult  *ClassificationResult
	MergeResult  string
	BackendName  string
}

// GetConversationHistory returns the agent's conversation formatted as a transcript
// suitable for injecting into a new agent's prompt to resume the conversation.
func (a *Agent) GetConversationHistory() string {
	a.outputMu.Lock()
	defer a.outputMu.Unlock()

	var parts []string
	for _, entry := range a.output {
		switch entry.Source {
		case "text":
			parts = append(parts, entry.Content)
		case "tool_use":
			parts = append(parts, fmt.Sprintf("[Tool: %s] %s", entry.ToolName, entry.Content))
		case "tool_result":
			if entry.Content != "" {
				prefix := "[Tool Result]"
				if entry.IsError {
					prefix = "[Tool Error]"
				}
				parts = append(parts, fmt.Sprintf("%s %s", prefix, entry.Content))
			}
		case "user_input":
			parts = append(parts, fmt.Sprintf("[User Message] %s", entry.Content))
		case "error":
			parts = append(parts, fmt.Sprintf("[Error] %s", entry.Content))
		}
	}
	return strings.Join(parts, "\n")
}

// Done returns a channel that closes when the agent subprocess exits.
func (a *Agent) Done() <-chan struct{} {
	return a.done
}

// IsActive returns true if the agent is still running.
func (a *Agent) IsActive() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.State == AgentRunning || a.State == AgentStarting || a.State == AgentRouting
}

// formatToolUseSummary creates a concise summary of a tool use event.
func formatToolUseSummary(name string, input map[string]interface{}) string {
	switch name {
	case "Read", "read":
		path, _ := input["file_path"].(string)
		return fmt.Sprintf("Read %s", path)
	case "Edit", "edit":
		path, _ := input["file_path"].(string)
		return fmt.Sprintf("Edit %s", path)
	case "Write", "write":
		path, _ := input["file_path"].(string)
		return fmt.Sprintf("Write %s", path)
	case "Bash", "bash":
		cmd, _ := input["command"].(string)
		return fmt.Sprintf("Bash: %s", truncate(cmd, 120))
	case "Grep", "grep":
		pattern, _ := input["pattern"].(string)
		return fmt.Sprintf("Grep: %s", truncate(pattern, 80))
	case "Glob", "glob":
		pattern, _ := input["pattern"].(string)
		return fmt.Sprintf("Glob: %s", pattern)
	case "WebSearch":
		query, _ := input["query"].(string)
		return fmt.Sprintf("WebSearch: %s", truncate(query, 80))
	case "WebFetch":
		url, _ := input["url"].(string)
		return fmt.Sprintf("WebFetch: %s", truncate(url, 80))
	default:
		return name
	}
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
