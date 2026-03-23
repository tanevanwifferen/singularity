package engine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
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

// Agent wraps a Claude Code subprocess with structured output streaming
type Agent struct {
	ID        string     `json:"id"`
	WorkDir   string     `json:"work_dir"`
	Task      string     `json:"task"`
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
	cmd    *exec.Cmd
	stdin   io.WriteCloser
	stdinMu sync.Mutex
	stdout io.ReadCloser
	stderr io.ReadCloser
	done   chan struct{}
	mu     sync.Mutex

	// Configuration
	model        string
	allowedTools []string
	maxTurns     int
	contextFiles []string

	// Cost tracking (from result event)
	TotalCostUSD float64 `json:"total_cost_usd,omitempty"`

	// Smart routing result (nil if not routed)
	RouteResult *RouteResult `json:"route_result,omitempty"`

	// sessionID is the actual session ID assigned by Claude (from system/init event)
	sessionID string

	// Worktree isolation fields
	useWorktree    bool   // whether this agent runs in a worktree
	worktreePath   string // path to the created worktree
	worktreeBranch string // temporary branch name for the worktree
	sourceRepoPath string // original repo path (for merge-back)
	sourceBranch   string // branch to merge back into
	MergeResult    string `json:"merge_result,omitempty"` // result of merge-back ("merged", "conflict", "no-changes", "")
}

// OutputEntry represents a single output chunk from the agent.
// Source is one of: "text", "tool_use", "tool_result", "system", "error", "result"
type OutputEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"`
	Content   string    `json:"content"`

	// Structured fields for tool events
	ToolName  string `json:"tool_name,omitempty"`
	ToolID    string `json:"tool_id,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// newAgent creates a new agent instance
func newAgent(id, workDir, task string, opts AgentOptions) *Agent {
	return &Agent{
		ID:           id,
		WorkDir:      workDir,
		Task:         task,
		State:        AgentIdle,
		CreatedAt:    time.Now(),
		output:       make([]OutputEntry, 0),
		done:         make(chan struct{}),
		model:        opts.Model,
		allowedTools: opts.AllowedTools,
		maxTurns:     opts.MaxTurns,
		contextFiles: opts.ContextFiles,
		useWorktree:  opts.UseWorktree,
	}
}

// start launches the Claude Code subprocess
func (a *Agent) start() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.State != AgentIdle && a.State != AgentRouting {
		return fmt.Errorf("agent %s is in state %s, expected idle or routing", a.ID, a.State)
	}

	a.State = AgentStarting

	args := a.buildArgs()

	a.cmd = exec.Command("claude", args...)
	a.cmd.Dir = a.WorkDir
	a.cmd.Env = a.buildEnv()

	var err error
	a.stdin, err = a.cmd.StdinPipe()
	if err != nil {
		a.setState(AgentError)
		a.Error = fmt.Sprintf("stdin pipe: %v", err)
		return err
	}
	a.stdout, err = a.cmd.StdoutPipe()
	if err != nil {
		a.setState(AgentError)
		a.Error = fmt.Sprintf("stdout pipe: %v", err)
		return err
	}

	a.stderr, err = a.cmd.StderrPipe()
	if err != nil {
		a.setState(AgentError)
		a.Error = fmt.Sprintf("stderr pipe: %v", err)
		return err
	}

	if err := a.cmd.Start(); err != nil {
		a.setState(AgentError)
		a.Error = fmt.Sprintf("start: %v", err)
		return err
	}

	now := time.Now()
	a.StartedAt = &now
	a.State = AgentRunning

	if a.RouteResult != nil {
		a.appendOutput("system", fmt.Sprintf("Routed → %s (%s: %s)", a.RouteResult.Model, a.RouteResult.Category, a.RouteResult.Reason))
	}
	a.appendOutput("system", fmt.Sprintf("Agent %s started with task: %s", a.ID, a.Task))

	// Stream structured JSON output
	go a.streamJSON(a.stdout)
	go a.streamStderr(a.stderr)
	go a.waitForExit()

	// Send the initial task via stdin (required when using --input-format stream-json)
	go func() {
		task := a.buildTask()
		if err := a.sendInput(task); err != nil {
			a.appendOutput("error", fmt.Sprintf("Failed to send initial task: %v", err))
		}
	}()

	return nil
}

// buildArgs constructs the claude CLI arguments for stream-json mode.
// Note: when --input-format stream-json is used, the initial prompt must be
// sent via stdin as JSON, not as a positional argument.
func (a *Agent) buildArgs() []string {
	args := []string{
		"--print",
		"--verbose",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--permission-mode", "bypassPermissions",
	}

	if a.model != "" {
		args = append(args, "--model", a.model)
	}

	if a.maxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", a.maxTurns))
	}

	for _, tool := range a.allowedTools {
		args = append(args, "--allowedTools", tool)
	}

	return args
}

// buildEnv constructs the environment for the subprocess
func (a *Agent) buildEnv() []string {
	env := os.Environ()
	env = append(env,
		"CLAUDE_NO_ANALYTICS=true",
	)
	return env
}

// buildTask constructs the full task string by prepending context file contents.
// Context files specified in AgentOptions are read and injected before the user's task.
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

// streamJSON parses newline-delimited JSON events from Claude's stream-json output
func (a *Agent) streamJSON(r io.ReadCloser) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			a.appendOutput("error", fmt.Sprintf("JSON parse error: %v", err))
			continue
		}

		a.processEvent(event)
	}
}

// processEvent handles a single stream-json event
func (a *Agent) processEvent(event map[string]interface{}) {
	eventType, _ := event["type"].(string)

	switch eventType {
	case "assistant":
		a.processAssistantEvent(event)

	case "system":
		subtype, _ := event["subtype"].(string)
		switch subtype {
		case "init":
			model, _ := event["model"].(string)
			if model != "" {
				a.appendOutput("system", fmt.Sprintf("Model: %s", model))
			}
			if sid, _ := event["session_id"].(string); sid != "" {
				a.mu.Lock()
				a.sessionID = sid
				a.mu.Unlock()
			}
		// Skip hook_started, hook_response — noisy
		}

	case "result":
		a.processResultEvent(event)

	case "rate_limit_event":
		// Skip — not useful for display
	}
}

// processAssistantEvent handles assistant message events containing text and tool_use
func (a *Agent) processAssistantEvent(event map[string]interface{}) {
	msg, ok := event["message"].(map[string]interface{})
	if !ok {
		return
	}

	content, ok := msg["content"].([]interface{})
	if !ok {
		return
	}

	for _, block := range content {
		blockMap, ok := block.(map[string]interface{})
		if !ok {
			continue
		}

		blockType, _ := blockMap["type"].(string)
		switch blockType {
		case "text":
			text, _ := blockMap["text"].(string)
			if text != "" {
				a.appendOutput("text", text)
			}

		case "tool_use":
			name, _ := blockMap["name"].(string)
			id, _ := blockMap["id"].(string)
			input, _ := blockMap["input"].(map[string]interface{})

			summary := formatToolUseSummary(name, input)
			entry := OutputEntry{
				Timestamp: time.Now(),
				Source:    "tool_use",
				Content:   summary,
				ToolName:  name,
				ToolID:    id,
			}
			a.outputMu.Lock()
			a.output = append(a.output, entry)
			a.outputMu.Unlock()

		case "tool_result":
			content, _ := blockMap["content"].(string)
			toolID, _ := blockMap["tool_use_id"].(string)
			isError, _ := blockMap["is_error"].(bool)

			entry := OutputEntry{
				Timestamp: time.Now(),
				Source:    "tool_result",
				Content:   content,
				ToolID:    toolID,
				IsError:   isError,
			}
			a.outputMu.Lock()
			a.output = append(a.output, entry)
			a.outputMu.Unlock()
		}
	}
}

// processResultEvent handles the final result event.
// With --input-format stream-json the process stays alive waiting for follow-up
// messages, so we transition the agent state here rather than waiting for the
// process to exit.
func (a *Agent) processResultEvent(event map[string]interface{}) {
	subtype, _ := event["subtype"].(string)
	isError, _ := event["is_error"].(bool)
	result, _ := event["result"].(string)
	costUSD, _ := event["total_cost_usd"].(float64)

	a.mu.Lock()
	if costUSD > 0 {
		a.TotalCostUSD = costUSD
	}
	// Transition state on result event since the process may not exit
	// (stream-json input mode keeps it alive for follow-ups)
	if a.State == AgentRunning || a.State == AgentStarting {
		now := time.Now()
		a.EndedAt = &now
		if isError {
			a.State = AgentError
			a.Error = result
		} else {
			a.State = AgentComplete
		}
	}
	a.mu.Unlock()

	if isError {
		a.appendOutput("error", fmt.Sprintf("Error: %s", result))
	} else {
		status := "completed"
		if subtype != "" {
			status = subtype
		}
		costStr := ""
		if costUSD > 0 {
			costStr = fmt.Sprintf(" ($%.4f)", costUSD)
		}
		a.appendOutput("result", fmt.Sprintf("Agent %s%s", status, costStr))
	}

	// Merge worktree back on completion (not error)
	if a.useWorktree && !isError {
		go func() {
			mergeResult := a.mergeWorktreeBack()
			a.mu.Lock()
			a.MergeResult = mergeResult
			a.mu.Unlock()
		}()
	} else if a.useWorktree && isError {
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
	// Only update EndedAt if not already set by processResultEvent
	if a.EndedAt == nil {
		now := time.Now()
		a.EndedAt = &now
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			a.ExitCode = exitErr.ExitCode()
		}
		// Only set error state if not already completed or killed
		if a.State == AgentRunning || a.State == AgentStarting {
			a.State = AgentError
			a.Error = err.Error()
		}
	} else {
		if a.State == AgentRunning {
			a.State = AgentComplete
		}
	}

	a.mu.Unlock()

	close(a.done)
}

// sendInput sends a follow-up message to the agent's stdin via stream-json protocol.
// Accepts messages to running, completed, or soft-closed agents (process stays alive
// in stream-json mode until explicitly removed via RemoveAgent).
func (a *Agent) sendInput(message string) error {
	a.mu.Lock()
	if a.State != AgentRunning && a.State != AgentComplete && a.State != AgentKilled {
		a.mu.Unlock()
		return fmt.Errorf("agent %s is in state %s, cannot send input", a.ID, a.State)
	}

	// Check if the subprocess is still alive before resuming
	if a.cmd == nil || a.cmd.Process == nil {
		a.mu.Unlock()
		return fmt.Errorf("agent %s process is no longer running", a.ID)
	}

	// Remember previous state so we can revert on failure
	prevState := a.State
	prevEndedAt := a.EndedAt

	// Resume agent back to running when sending a follow-up
	if a.State == AgentComplete || a.State == AgentKilled {
		a.State = AgentRunning
		a.EndedAt = nil
	}

	sid := a.sessionID
	a.mu.Unlock()
	if sid == "" {
		sid = a.ID
	}

	a.stdinMu.Lock()
	defer a.stdinMu.Unlock()

	if a.stdin == nil {
		// Revert state — stdin was closed (process exited or was killed)
		a.mu.Lock()
		a.State = prevState
		a.EndedAt = prevEndedAt
		a.mu.Unlock()
		return fmt.Errorf("agent %s stdin not available (process exited)", a.ID)
	}

	// stream-json input format requires a structured message envelope
	msg := map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{
			"role":    "user",
			"content": message,
		},
		"session_id":         sid,
		"parent_tool_use_id": nil,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal input: %w", err)
	}
	data = append(data, '\n')

	_, err = a.stdin.Write(data)
	if err != nil {
		// Revert state — write failed (broken pipe, process exited, etc.)
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
// The process stays alive and can still receive messages until RemoveAgent is called.
func (a *Agent) softClose() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.State == AgentRunning || a.State == AgentStarting || a.State == AgentComplete {
		a.State = AgentKilled
		now := time.Now()
		if a.EndedAt == nil {
			a.EndedAt = &now
		}
	}
}

// kill terminates the agent subprocess
func (a *Agent) kill() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cmd == nil || a.cmd.Process == nil {
		return nil
	}

	// Close stdin before killing
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

	return a.cmd.Process.Kill()
}

// getOutput returns all output entries, optionally from a given offset
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

// getFullOutput returns all text content as a single string
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

// appendOutput appends an output entry (thread-safe)
func (a *Agent) appendOutput(source, content string) {
	a.outputMu.Lock()
	defer a.outputMu.Unlock()
	a.output = append(a.output, OutputEntry{
		Timestamp: time.Now(),
		Source:    source,
		Content:   content,
	})
}

// appendOutputLocked appends output when mu is already held (uses outputMu only)
func (a *Agent) appendOutputLocked(source, content string) {
	a.outputMu.Lock()
	defer a.outputMu.Unlock()
	a.output = append(a.output, OutputEntry{
		Timestamp: time.Now(),
		Source:    source,
		Content:   content,
	})
}

// setState changes state (caller must hold mu)
func (a *Agent) setState(state AgentState) {
	a.State = state
}

// Snapshot returns a point-in-time copy of the agent's mutable fields (thread-safe)
func (a *Agent) Snapshot() AgentSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return AgentSnapshot{
		ID:           a.ID,
		WorkDir:      a.WorkDir,
		Task:         a.Task,
		State:        a.State,
		CreatedAt:    a.CreatedAt,
		StartedAt:    a.StartedAt,
		EndedAt:      a.EndedAt,
		ExitCode:     a.ExitCode,
		Error:        a.Error,
		TotalCostUSD: a.TotalCostUSD,
		RouteResult:  a.RouteResult,
		MergeResult:  a.MergeResult,
	}
}

// AgentSnapshot is a point-in-time copy of an agent's state, safe to read without locks
type AgentSnapshot struct {
	ID           string
	WorkDir      string
	Task         string
	State        AgentState
	CreatedAt    time.Time
	StartedAt    *time.Time
	EndedAt      *time.Time
	ExitCode     int
	Error        string
	TotalCostUSD float64
	RouteResult  *RouteResult
	MergeResult  string
}

// Done returns a channel that closes when the agent exits
func (a *Agent) Done() <-chan struct{} {
	return a.done
}

// IsActive returns true if the agent is still running
func (a *Agent) IsActive() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.State == AgentRunning || a.State == AgentStarting || a.State == AgentRouting
}

// formatToolUseSummary creates a concise summary of a tool use
func formatToolUseSummary(name string, input map[string]interface{}) string {
	switch name {
	case "Read":
		path, _ := input["file_path"].(string)
		return fmt.Sprintf("Read %s", path)
	case "Edit":
		path, _ := input["file_path"].(string)
		return fmt.Sprintf("Edit %s", path)
	case "Write":
		path, _ := input["file_path"].(string)
		return fmt.Sprintf("Write %s", path)
	case "Bash":
		cmd, _ := input["command"].(string)
		return fmt.Sprintf("Bash: %s", truncate(cmd, 120))
	case "Grep":
		pattern, _ := input["pattern"].(string)
		return fmt.Sprintf("Grep: %s", truncate(pattern, 80))
	case "Glob":
		pattern, _ := input["pattern"].(string)
		return fmt.Sprintf("Glob: %s", pattern)
	case "WebSearch":
		query, _ := input["query"].(string)
		return fmt.Sprintf("WebSearch: %s", truncate(query, 80))
	case "WebFetch":
		url, _ := input["url"].(string)
		return fmt.Sprintf("WebFetch: %s", truncate(url, 80))
	default:
		return fmt.Sprintf("%s", name)
	}
}

// truncate shortens a string to maxLen, adding "..." if truncated
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
