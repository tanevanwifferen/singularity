package engine

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/config"
)

// AgentOptions configures an agent's behavior
type AgentOptions struct {
	Model        string        // Model to use (empty = backend default)
	Effort       string        // Effort level: "low", "medium", "high" (empty = default)
	AllowedTools []string      // Restrict available tools (pi maps these onto its own tool names)
	MaxTurns     int           // Max conversation turns (0 = unlimited; claude only, pi warns)
	Timeout      time.Duration // Kill agent after this duration (0 = no timeout)
	ContextFiles []string      // Files to read and inject into the prompt on startup
	SmartRoute   bool          // Use cheap model to classify prompt and pick model/effort
	UseWorktree  bool          // Create a git worktree for isolation; merge back on completion
	Summary      string        // One-line summary for display in agent list (auto-generated if empty)
	WorkflowID   string        // Optional workflow ID (branch name) this agent belongs to
	// Backend overrides the engine's default backend for this agent.
	// nil means use the engine default (or resolve from BackendName).
	Backend Backend
	// BackendName is a string alternative to Backend ("claude" or "pi").
	// Used when the caller comes from a CLI/HTTP path that carries strings.
	// Ignored when Backend is non-nil.
	BackendName string
}

// Engine manages a pool of coding-agent subprocesses
type Engine struct {
	agents         map[string]*Agent
	mu             sync.RWMutex
	idSeq          atomic.Int64
	maxAgents      int
	soundCfg       config.SoundConfig
	defaultBackend Backend // used when AgentOptions.Backend is nil

	// Observer callback: fired when any agent's state or output changes.
	// Called from agent goroutines -- must be non-blocking.
	onUpdate     func(agentID string)
	updateMu     sync.RWMutex
	updateTimers map[string]*time.Timer
	timerMu      sync.Mutex
}

// New creates a new agent engine using the claude backend by default.
func New(maxAgents int) *Engine {
	if maxAgents <= 0 {
		maxAgents = 10
	}
	return &Engine{
		agents:         make(map[string]*Agent),
		maxAgents:      maxAgents,
		defaultBackend: NewPiBackend(""),
		updateTimers:   make(map[string]*time.Timer),
	}
}

// SetMaxAgents overrides the concurrent-agent cap. Call before starting any
// agents (daemon startup); values <= 0 are ignored.
func (e *Engine) SetMaxAgents(n int) {
	if n <= 0 {
		return
	}
	e.mu.Lock()
	e.maxAgents = n
	e.mu.Unlock()
}

// SetDefaultBackend replaces the engine's default backend.
// Call before starting any agents.
func (e *Engine) SetDefaultBackend(b Backend) {
	e.mu.Lock()
	e.defaultBackend = b
	e.mu.Unlock()
}

// DefaultBackend returns the currently configured default backend.
func (e *Engine) DefaultBackend() Backend {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.defaultBackend
}

// SetSoundConfig configures sound notifications for agent completion.
func (e *Engine) SetSoundConfig(cfg config.SoundConfig) {
	e.mu.Lock()
	e.soundCfg = cfg
	e.mu.Unlock()
}

// StartAgent creates and starts a new agent working on the given task
func (e *Engine) StartAgent(projectPath string, task string, opts AgentOptions) (string, error) {
	// Validate project path
	info, err := os.Stat(projectPath)
	if err != nil {
		return "", fmt.Errorf("invalid project path %q: %w", projectPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project path %q is not a directory", projectPath)
	}

	e.mu.Lock()
	// Check capacity
	activeCount := 0
	for _, a := range e.agents {
		if a.IsActive() {
			activeCount++
		}
	}
	if activeCount >= e.maxAgents {
		e.mu.Unlock()
		return "", fmt.Errorf("agent limit reached (%d/%d active)", activeCount, e.maxAgents)
	}

	id := e.generateID()
	backend := opts.Backend
	if backend == nil && opts.BackendName != "" {
		backend = BackendByName(opts.BackendName)
	}
	if backend == nil {
		backend = e.defaultBackend
	}
	agent := newAgent(id, projectPath, task, opts, backend)
	agent.soundCfg = e.soundCfg
	agent.notify = func() { e.notifyUpdate(id) }
	e.agents[id] = agent
	e.mu.Unlock()

	if opts.WorkflowID != "" {
		agent.appendOutput("system", fmt.Sprintf("Workflow: %s", opts.WorkflowID))
	}

	// Set up worktree isolation if requested
	if opts.UseWorktree {
		if err := agent.setupWorktree(); err != nil {
			agent.setState(AgentError)
			agent.Error = fmt.Sprintf("worktree setup: %v", err)
			agent.appendOutput("error", fmt.Sprintf("Failed to create worktree: %v", err))
			return id, fmt.Errorf("worktree setup: %w", err)
		}
		agent.appendOutput("system", fmt.Sprintf("Worktree created at %s (branch: %s)", agent.worktreePath, agent.worktreeBranch))
	}

	if opts.SmartRoute && opts.Model == "" {
		// Route async: show agent immediately, classify in background, then start
		agent.setState(AgentRouting)
		agent.appendOutput("system", "Routing via Haiku...")
		go func() {
			route, err := RoutePrompt(task, backend)
			if err == nil {
				agent.mu.Lock()
				agent.model = route.Model
				agent.effort = route.Effort
				agent.RouteResult = route
				if route.Summary != "" {
					agent.Summary = route.Summary
				}
				agent.mu.Unlock()
			}
			if startErr := agent.start(); startErr != nil {
				agent.appendOutput("error", fmt.Sprintf("Failed to start agent: %v", startErr))
			}
		}()
	} else {
		if err := agent.start(); err != nil {
			return "", fmt.Errorf("failed to start agent: %w", err)
		}
	}

	// Set up timeout if configured
	if opts.Timeout > 0 {
		go func() {
			select {
			case <-time.After(opts.Timeout):
				agent.kill()
				agent.appendOutput("system", fmt.Sprintf("Agent killed: timeout after %s", opts.Timeout))
			case <-agent.Done():
				// Agent finished before timeout
			}
		}()
	}

	return id, nil
}

// GetStatus returns the current state of an agent
func (e *Engine) GetStatus(sessionID string) (AgentState, error) {
	agent := e.getAgent(sessionID)
	if agent == nil {
		return AgentError, fmt.Errorf("agent not found: %s", sessionID)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	return agent.State, nil
}

// GetOutput returns the agent's output content as a string
func (e *Engine) GetOutput(sessionID string) (string, error) {
	agent := e.getAgent(sessionID)
	if agent == nil {
		return "", fmt.Errorf("agent not found: %s", sessionID)
	}
	return agent.getFullOutput(), nil
}

// GetOutputEntries returns structured output entries from a given offset
func (e *Engine) GetOutputEntries(sessionID string, offset int) ([]OutputEntry, error) {
	agent := e.getAgent(sessionID)
	if agent == nil {
		return nil, fmt.Errorf("agent not found: %s", sessionID)
	}
	return agent.getOutput(offset), nil
}

// KillAgent soft-closes an agent: marks it as killed but leaves the subprocess alive
// so follow-up messages can still be sent. The process is only terminated when
// RemoveAgent is called (i.e., during cleanup).
func (e *Engine) KillAgent(sessionID string) error {
	agent := e.getAgent(sessionID)
	if agent == nil {
		return fmt.Errorf("agent not found: %s", sessionID)
	}
	agent.softClose()
	return nil
}

// SendInput sends a follow-up message to a running agent's stdin
func (e *Engine) SendInput(sessionID string, message string) error {
	agent := e.getAgent(sessionID)
	if agent == nil {
		return fmt.Errorf("agent not found: %s", sessionID)
	}
	return agent.sendInput(message)
}

// ResumeWithHistory creates a new agent that includes the conversation history
// from a crashed/errored agent, plus an optional new user message.
// Returns the new agent ID.
func (e *Engine) ResumeWithHistory(oldAgentID string, userMessage string, opts AgentOptions) (string, error) {
	oldAgent := e.getAgent(oldAgentID)
	if oldAgent == nil {
		return "", fmt.Errorf("agent not found: %s", oldAgentID)
	}

	history := oldAgent.GetConversationHistory()
	oldAgent.mu.Lock()
	originalTask := oldAgent.Task
	workDir := oldAgent.WorkDir
	oldAgent.mu.Unlock()

	// Build the resumed task with history context
	var task strings.Builder
	task.WriteString("You are resuming a conversation that was interrupted by a crash. ")
	task.WriteString("Below is the original task and the conversation history from before the crash.\n\n")
	task.WriteString("=== ORIGINAL TASK ===\n")
	task.WriteString(originalTask)
	task.WriteString("\n\n=== CONVERSATION HISTORY (before crash) ===\n")
	task.WriteString(history)
	task.WriteString("\n\n=== END OF HISTORY ===\n\n")
	if userMessage != "" {
		task.WriteString("The user says: ")
		task.WriteString(userMessage)
	} else {
		task.WriteString("Please continue where you left off.")
	}

	if opts.Summary == "" {
		opts.Summary = "[resumed] " + extractSummary(originalTask)
	}

	return e.StartAgent(workDir, task.String(), opts)
}

// RemoveAgent kills the subprocess and removes the agent from the engine.
// This is the point at which deferred kills (from KillAgent) actually terminate the process.
func (e *Engine) RemoveAgent(sessionID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	agent, exists := e.agents[sessionID]
	if !exists {
		return fmt.Errorf("agent not found: %s", sessionID)
	}

	agent.kill()

	delete(e.agents, sessionID)
	return nil
}

// GetAgent returns the full agent info
func (e *Engine) GetAgent(sessionID string) *Agent {
	return e.getAgent(sessionID)
}

// ListAgents returns all agents sorted by ID
func (e *Engine) ListAgents() []*Agent {
	e.mu.RLock()
	defer e.mu.RUnlock()

	agents := make([]*Agent, 0, len(e.agents))
	for _, a := range e.agents {
		agents = append(agents, a)
	}
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].ID < agents[j].ID
	})
	return agents
}

// ActiveAgents returns only running/starting agents sorted by ID
func (e *Engine) ActiveAgents() []*Agent {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var active []*Agent
	for _, a := range e.agents {
		if a.IsActive() {
			active = append(active, a)
		}
	}
	sort.Slice(active, func(i, j int) bool {
		return active[i].ID < active[j].ID
	})
	return active
}

// WaitFor blocks until the given agent completes or the timeout expires
func (e *Engine) WaitFor(sessionID string, timeout time.Duration) (AgentState, error) {
	agent := e.getAgent(sessionID)
	if agent == nil {
		return AgentError, fmt.Errorf("agent not found: %s", sessionID)
	}

	if timeout <= 0 {
		<-agent.Done()
		agent.mu.Lock()
		defer agent.mu.Unlock()
		return agent.State, nil
	}

	select {
	case <-agent.Done():
		agent.mu.Lock()
		defer agent.mu.Unlock()
		return agent.State, nil
	case <-time.After(timeout):
		return AgentRunning, fmt.Errorf("timeout waiting for agent %s", sessionID)
	}
}

// Shutdown kills all agents and cleans up their worktrees.
func (e *Engine) Shutdown() {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, agent := range e.agents {
		agent.kill()
	}
	e.agents = make(map[string]*Agent)
}

// PruneStaleWorktrees cleans up agent worktrees from previous sessions that
// don't correspond to any currently active agent. Safe to call on startup.
func (e *Engine) PruneStaleWorktrees(repoPath string) {
	e.mu.RLock()
	active := make(map[string]bool, len(e.agents))
	for id := range e.agents {
		active[id] = true
	}
	e.mu.RUnlock()

	go CleanupStaleWorktrees(repoPath, active)
}

// MaxAgents returns the maximum number of concurrent agents allowed.
// This value is set at construction and never changes.
func (e *Engine) MaxAgents() int {
	return e.maxAgents
}

// Stats returns engine statistics
func (e *Engine) Stats() EngineStats {
	e.mu.RLock()
	defer e.mu.RUnlock()

	stats := EngineStats{
		MaxAgents: e.maxAgents,
	}
	for _, a := range e.agents {
		stats.Total++
		snap := a.Snapshot()
		switch snap.State {
		case AgentRunning, AgentStarting:
			stats.Active++
		case AgentComplete:
			stats.Completed++
		case AgentError:
			stats.Errored++
		case AgentKilled:
			stats.Killed++
		}
	}
	return stats
}

// EngineStats holds summary statistics about the engine
type EngineStats struct {
	Total     int `json:"total"`
	Active    int `json:"active"`
	Completed int `json:"completed"`
	Errored   int `json:"errored"`
	Killed    int `json:"killed"`
	MaxAgents int `json:"max_agents"`
}

// getAgent retrieves an agent by ID (thread-safe)
func (e *Engine) getAgent(id string) *Agent {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.agents[id]
}

// generateID creates a unique agent ID
func (e *Engine) generateID() string {
	seq := e.idSeq.Add(1)
	return fmt.Sprintf("agent-%d-%d", time.Now().Unix(), seq)
}

// OnAgentUpdate registers a callback that fires whenever an agent's state
// or output changes. The callback receives the agent ID and must be
// non-blocking. Only one callback is supported; subsequent calls replace
// the previous one.
func (e *Engine) OnAgentUpdate(fn func(agentID string)) {
	e.updateMu.Lock()
	e.onUpdate = fn
	e.updateMu.Unlock()
}

// notifyUpdate fires the registered observer callback, debounced PER AGENT to
// coalesce rapid bursts of output into a single notification every 50ms. The
// debounce must be per agent: a shared timer would let a chatty agent B
// swallow agent A's pending notification — including A's terminal
// complete/error transition, which then never reaches WS subscribers.
func (e *Engine) notifyUpdate(agentID string) {
	e.updateMu.RLock()
	fn := e.onUpdate
	e.updateMu.RUnlock()
	if fn == nil {
		return
	}

	e.timerMu.Lock()
	if t := e.updateTimers[agentID]; t != nil {
		t.Stop()
	}
	e.updateTimers[agentID] = time.AfterFunc(50*time.Millisecond, func() {
		e.timerMu.Lock()
		delete(e.updateTimers, agentID)
		e.timerMu.Unlock()
		fn(agentID)
	})
	e.timerMu.Unlock()
}
