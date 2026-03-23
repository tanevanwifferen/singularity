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
)

// AgentState represents the lifecycle state of an agent
type AgentState int

const (
	AgentIdle AgentState = iota
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

// Agent wraps a Claude Code subprocess with stdin/stdout management
type Agent struct {
	ID         string     `json:"id"`
	WorkDir    string     `json:"work_dir"`
	Task       string     `json:"task"`
	State      AgentState `json:"state"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
	Error      string     `json:"error,omitempty"`
	ExitCode   int        `json:"exit_code"`

	// Output buffer
	output     []OutputEntry
	outputMu   sync.Mutex

	// Process management
	cmd        *exec.Cmd
	stdout     io.ReadCloser
	stderr     io.ReadCloser
	done       chan struct{}
	mu         sync.Mutex

	// Configuration
	model      string
	allowedTools []string
	maxTurns   int
}

// OutputEntry represents a single output chunk from the agent
type OutputEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"` // "stdout", "stderr", "system"
	Content  string    `json:"content"`
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
	}
}

// start launches the Claude Code subprocess
func (a *Agent) start() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.State != AgentIdle {
		return fmt.Errorf("agent %s is in state %s, expected idle", a.ID, a.State)
	}

	a.State = AgentStarting

	args := a.buildArgs()

	a.cmd = exec.Command("claude", args...)
	a.cmd.Dir = a.WorkDir
	a.cmd.Env = a.buildEnv()

	// No stdin needed - task is passed as CLI argument
	// Close stdin immediately to avoid "no stdin data received" warning
	a.cmd.Stdin = nil

	// Set up pipes
	var err error
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

	a.appendOutput("system", fmt.Sprintf("Agent %s started with task: %s", a.ID, a.Task))

	// Stream output in background
	go a.streamOutput(a.stdout, "stdout")
	go a.streamOutput(a.stderr, "stderr")
	go a.waitForExit()

	return nil
}

// buildArgs constructs the claude CLI arguments
func (a *Agent) buildArgs() []string {
	args := []string{
		"--print",
		"--verbose",
		"--output-format", "text",
		"--dangerously-skip-permissions",
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

	// The task/prompt goes last
	args = append(args, a.Task)

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

// streamOutput reads from a pipe and appends to the output buffer
func (a *Agent) streamOutput(r io.ReadCloser, source string) {
	scanner := bufio.NewScanner(r)
	// Increase buffer size for long lines
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		a.appendOutput(source, line)
	}
}

// waitForExit waits for the subprocess to finish
func (a *Agent) waitForExit() {
	err := a.cmd.Wait()

	a.mu.Lock()
	now := time.Now()
	a.EndedAt = &now

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			a.ExitCode = exitErr.ExitCode()
		}
		// Only set error state if not already killed
		if a.State != AgentKilled {
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

// sendMessage writes a message to the agent's stdin
func (a *Agent) sendMessage(msg string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.State != AgentRunning {
		return fmt.Errorf("agent %s is not running (state: %s)", a.ID, a.State)
	}

	// Agents run in --print mode (non-interactive), stdin is not available
	return fmt.Errorf("agent %s does not support stdin in print mode", a.ID)
}

// kill terminates the agent subprocess
func (a *Agent) kill() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cmd == nil || a.cmd.Process == nil {
		return nil
	}

	a.State = AgentKilled
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

// getFullOutput returns all stdout content as a single string
func (a *Agent) getFullOutput() string {
	a.outputMu.Lock()
	defer a.outputMu.Unlock()

	var parts []string
	for _, entry := range a.output {
		if entry.Source == "stdout" {
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

// Done returns a channel that closes when the agent exits
func (a *Agent) Done() <-chan struct{} {
	return a.done
}

// IsActive returns true if the agent is still running
func (a *Agent) IsActive() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.State == AgentRunning || a.State == AgentStarting
}
