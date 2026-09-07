package engine

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/config"
)

// ErrAgentLimit is returned by StartAgent when the concurrent-agent cap is
// already reached. It is a sentinel rather than a bare message because
// callers have to tell it apart from a genuine spawn failure: the task
// queue treats a cap refusal as backpressure (the task waits for a slot)
// and anything else as a failed attempt.
var ErrAgentLimit = errors.New("agent limit reached")

// AgentOptions configures an agent's behavior
type AgentOptions struct {
	Model        string        // Model to use (empty = backend default)
	Effort       string        // Effort level: "low", "medium", "high" (empty = default)
	AllowedTools []string      // Restrict available tools (pi maps these onto its own tool names)
	MaxTurns     int           // Max conversation turns (0 = unlimited; claude only, pi warns)
	Timeout      time.Duration // Kill agent after this duration (0 = no timeout)
	ContextFiles []string      // Files to read and inject into the prompt on startup
	SmartRoute   bool          // Use cheap model to classify prompt and pick model/effort/summary (skipped when both Model and Effort are pinned)
	UseWorktree  bool          // Create a git worktree for isolation; merge back on completion
	Summary      string        // One-line summary for display in agent list (auto-generated if empty)
	WorkflowID   string        // Optional workflow ID (branch name) this agent belongs to
	// PromptLogNote, when non-empty, is logged to the output stream instead of
	// the full task text. Used by ResumeWithHistory so the seeded history is
	// not replayed as a giant prompt entry. Engine-internal: the HTTP request
	// DTOs (api.AgentStartRequest etc.) do not carry it.
	PromptLogNote string
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

	// summarize titles an agent whose caller supplied no summary. It is a
	// field so tests can inject a stub instead of shelling out to a model.
	summarize Summarizer

	// Observer callbacks: fired when any agent's state or output changes.
	// Called from agent goroutines -- must be non-blocking.
	//
	// primaryObserver is the single slot owned by OnAgentUpdate (the
	// daemon's WS broadcast hook). extraObservers holds any number of
	// additional listeners registered via AddAgentObserver -- the task
	// queue scheduler is one. Both are notified for every update; a
	// single-slot design would have let whichever component registered
	// last silently starve the other.
	primaryObserver func(agentID string)
	extraObservers  []observerEntry
	observerSeq     int64
	updateMu        sync.RWMutex
	updateTimers    map[string]*time.Timer
	timerMu         sync.Mutex
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
		summarize:      defaultSummarizer,
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
	activeCount := e.activeCountLocked()
	if activeCount >= e.maxAgents {
		e.mu.Unlock()
		return "", fmt.Errorf("%w (%d/%d active)", ErrAgentLimit, activeCount, e.maxAgents)
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
			// No subprocess was ever created, and start() will never run for
			// this agent — without this, processExited() (and so
			// WorkDirOccupied) would report it occupying projectPath forever.
			agent.closeDone()
			return id, fmt.Errorf("worktree setup: %w", err)
		}
		agent.appendOutput("system", fmt.Sprintf("Worktree created at %s (branch: %s)", agent.worktreePath, agent.worktreeBranch))
	}

	// Route whenever the classifier still has something to contribute. Pinning
	// the model suppresses only the classifier's model choice — not its effort
	// choice, and not the summary, which used to disappear with it.
	if opts.SmartRoute && (opts.Model == "" || opts.Effort == "") {
		// Route async: show agent immediately, classify in background, then start
		agent.setState(AgentRouting)
		agent.appendOutput("system", "Routing via Haiku...")
		go func() {
			routed := false
			route, err := RoutePrompt(task, backend)
			if err != nil {
				agent.appendOutput("error", fmt.Sprintf("Smart routing failed (%v); falling back to backend defaults", err))
			} else {
				agent.mu.Lock()
				// An explicit --model / --effort from the user beats the
				// classifier, one field at a time.
				if opts.Model == "" {
					agent.model = route.Model
				}
				if opts.Effort == "" {
					agent.effort = route.Effort
				}
				agent.RouteResult = route
				if route.Summary != "" {
					agent.Summary = route.Summary
					routed = true
				}
				agent.mu.Unlock()
				if routed {
					// The list shows Summary, so the routed title is an
					// observable change like any other.
					agent.notifySummary()
				}
			}
			// The classifier already returns a title, so only pay for a
			// second call when routing produced none.
			if opts.Summary == "" && !routed {
				agent.summarizeAsync(e.summarize, backend)
			}
			if startErr := agent.start(); startErr != nil {
				agent.appendOutput("error", fmt.Sprintf("Failed to start agent: %v", startErr))
			}
		}()
	} else {
		// Summarisation used to be a side effect of the routing decision, so
		// pinning --model silently left the agent titled with the first line
		// of its own prompt. It is now independent of routing: this branch is
		// reached when routing is off or has nothing left to decide.
		if opts.Summary == "" {
			agent.summarizeAsync(e.summarize, backend)
		}
		if err := agent.start(); err != nil {
			return "", fmt.Errorf("failed to start agent: %w", err)
		}
	}

	// Set up timeout if configured
	if opts.Timeout > 0 {
		go func() {
			select {
			case <-time.After(opts.Timeout):
				// terminate(), not a bare kill(false): a timeout firing while
				// the agent is still being smart-routed has no subprocess yet,
				// so kill's nil-cmd branch would leave the state untouched and
				// the classifier would go on to start the agent unbounded —
				// silently voiding the timeout it was just declared to enforce.
				agent.terminate()
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
// TerminateAgent or RemoveAgent is called (i.e., during cleanup).
func (e *Engine) KillAgent(sessionID string) error {
	agent := e.getAgent(sessionID)
	if agent == nil {
		return fmt.Errorf("agent not found: %s", sessionID)
	}
	agent.softClose()
	return nil
}

// TerminateAgent kills the agent's subprocess and cleans up its worktree,
// keeping the agent record so its transcript stays readable.
//
// This is the call for a caller that means "stop doing this work now": unlike
// KillAgent the process really is gone afterwards, so the agent stops holding
// a slot and its working directory, and unlike RemoveAgent the record — and
// with it the output the caller may still want to explain what happened —
// survives. An agent still doing work reports state killed afterwards; one
// that already reached complete or error is left exactly as it finished.
func (e *Engine) TerminateAgent(sessionID string) error {
	agent := e.getAgent(sessionID)
	if agent == nil {
		return fmt.Errorf("agent not found: %s", sessionID)
	}
	return agent.terminate()
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

	// Log a compact note instead of the composite task: replaying the seeded
	// history as one giant prompt entry would drown the new agent's log.
	note := fmt.Sprintf("Resumed from %s with seeded conversation history (%d chars).", oldAgentID, len(history))
	if userMessage != "" {
		note += "\nThe user says: " + userMessage
	} else {
		note += "\nPlease continue where you left off."
	}
	opts.PromptLogNote = note

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

	// terminate(), not a bare kill(false): an agent still being smart-routed
	// has no subprocess yet, so kill's nil-cmd branch would neither force the
	// state terminal nor close done, and the pending classifier would go on
	// to start a real process in a directory this deletion just stopped
	// tracking. terminate() forces the state first so the late start() call
	// refuses.
	agent.terminate()

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

// ActiveAgents returns only agents occupying a pool slot, sorted by ID
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

	// terminate(), not a bare kill(false) — see RemoveAgent's comment: a
	// routing agent has no subprocess yet, and a bare kill leaves it able to
	// spawn one after the daemon believes everything is torn down.
	for _, agent := range e.agents {
		agent.terminate()
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
