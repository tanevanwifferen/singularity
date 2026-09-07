package queue

import (
	"path/filepath"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
)

// EngineRunner adapts *engine.Engine to the AgentRunner interface. It is the
// only file in this package that knows the engine exists, which keeps the
// scheduler unit-testable against a fake.
type EngineRunner struct {
	eng *engine.Engine
}

// NewEngineRunner wraps an engine for use by a Manager.
func NewEngineRunner(eng *engine.Engine) *EngineRunner {
	return &EngineRunner{eng: eng}
}

// StartTask spawns an agent for the task and returns its ID.
//
// The task's Title becomes the agent's Summary so a queued task is
// identifiable in the agent list without reading its whole prompt. The
// queue ID travels as WorkflowID, which the engine logs to the agent's
// output stream — enough to trace an agent back to the queue that made it.
func (r *EngineRunner) StartTask(t Task) (string, error) {
	opts := engine.AgentOptions{
		Model:        t.Opts.Model,
		Effort:       t.Opts.Effort,
		AllowedTools: t.Opts.AllowedTools,
		MaxTurns:     t.Opts.MaxTurns,
		ContextFiles: t.Opts.ContextFiles,
		SmartRoute:   t.Opts.SmartRoute,
		UseWorktree:  t.Opts.UseWorktree,
		Summary:      t.Title,
		WorkflowID:   t.QueueID,
		BackendName:  t.Opts.Backend,
	}
	if t.Opts.TimeoutSecs > 0 {
		opts.Timeout = time.Duration(t.Opts.TimeoutSecs) * time.Second
	}
	return r.eng.StartAgent(t.WorkDir, t.Prompt, opts)
}

// AgentState reports one agent's state name and error text.
func (r *EngineRunner) AgentState(agentID string) (string, string, bool) {
	a := r.eng.GetAgent(agentID)
	if a == nil {
		return "", "", false
	}
	snap := a.Snapshot()
	return snap.State.String(), snap.Error, true
}

// Capacity reports active agents and the engine's cap.
func (r *EngineRunner) Capacity() (int, int) {
	stats := r.eng.Stats()
	return stats.Active, stats.MaxAgents
}

// WorkDirBusy reports whether any active agent is working in workDir.
//
// Comparison is on the cleaned path, so "/w/api" and "/w/api/" are the same
// directory. Worktree-isolated agents never match a repo path here: the
// engine rewrites their WorkDir to the private worktree it created
// (setupWorktree), so their directory is unique per agent by construction.
func (r *EngineRunner) WorkDirBusy(workDir string) bool {
	want := filepath.Clean(workDir)
	for _, a := range r.eng.ActiveAgents() {
		snap := a.Snapshot()
		if filepath.Clean(snap.WorkDir) == want {
			return true
		}
	}
	return false
}

// SendInput delivers a message to a running agent's stdin.
func (r *EngineRunner) SendInput(agentID, message string) error {
	return r.eng.SendInput(agentID, message)
}

// KillAgent soft-closes the agent: the turn ends but the process stays
// addressable, matching what the agent view's kill action does.
func (r *EngineRunner) KillAgent(agentID string) error {
	return r.eng.KillAgent(agentID)
}
