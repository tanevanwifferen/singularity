package engine

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/config"
)

// AgentOptions configures an agent's behavior
type AgentOptions struct {
	Model        string        // Claude model to use (empty = default)
	Effort       string        // Effort level: "low", "medium", "high" (empty = default)
	AllowedTools []string      // Restrict available tools
	MaxTurns     int           // Max conversation turns (0 = unlimited)
	Timeout      time.Duration // Kill agent after this duration (0 = no timeout)
	ContextFiles []string      // Files to read and inject into the prompt on startup
	SmartRoute   bool          // Use Haiku to classify prompt and pick model (opus for planning, sonnet for implementation)
	UseWorktree  bool          // Create a git worktree for isolation; merge back on completion
	Summary      string        // One-line summary for display in agent list (auto-generated if empty)
	WorkflowID   string        // Optional workflow ID (branch name) this agent belongs to
}

// Engine manages a pool of Claude Code agent subprocesses
type Engine struct {
	agents    map[string]*Agent
	mu        sync.RWMutex
	idSeq     atomic.Int64
	maxAgents int
	soundCfg  config.SoundConfig
}

// New creates a new agent engine
func New(maxAgents int) *Engine {
	if maxAgents <= 0 {
		maxAgents = 10
	}
	return &Engine{
		agents:    make(map[string]*Agent),
		maxAgents: maxAgents,
	}
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
	agent := newAgent(id, projectPath, task, opts)
	agent.soundCfg = e.soundCfg
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
			route, err := RoutePrompt(task)
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
