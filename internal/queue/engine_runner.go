package queue

import (
	"context"
	"errors"
	"fmt"
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
//
// ctx is honoured by refusing to begin a spawn, not by interrupting one.
// engine.StartAgent takes no context and is not cancellable: it stats the
// project path, may run `git worktree add`, and forks a subprocess, none of
// which unwind cleanly halfway. Adding a context to it would mean threading
// cancellation through the agent lifecycle for the benefit of one caller,
// which is out of proportion to the risk. Refusing at this boundary is
// enough for what the queue needs — Stop cancels, every remaining spawn in
// the batch returns here immediately, and dispatch cleans up the one that
// was already under way.
func (r *EngineRunner) StartTask(ctx context.Context, t Task) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("queue is shutting down: %w", err)
	}
	opts := engine.AgentOptions{
		Model:        t.Opts.Model,
		Effort:       t.Opts.Effort,
		AllowedTools: t.Opts.AllowedTools,
		MaxTurns:     t.Opts.MaxTurns,
		ContextFiles: t.Opts.ContextFiles,
		SmartRoute:   t.Opts.RouteEnabled(),
		UseWorktree:  t.Opts.UseWorktree,
		Summary:      t.Title,
		WorkflowID:   t.QueueID,
		BackendName:  t.Opts.Backend,
	}
	if t.Opts.TimeoutSecs > 0 {
		opts.Timeout = time.Duration(t.Opts.TimeoutSecs) * time.Second
	}
	id, err := r.eng.StartAgent(t.WorkDir, t.Prompt, opts)
	if errors.Is(err, engine.ErrAgentLimit) {
		// Translate into the queue's own sentinel so the scheduler can
		// treat a cap refusal as backpressure without importing the
		// engine — and so the test fake can produce the same condition.
		return "", fmt.Errorf("%w: %s", ErrNoCapacity, err.Error())
	}
	return id, err
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
//
// The active count comes from ActiveCount, not EngineStats.Active: the
// scheduler has to size its dispatch on exactly the number StartAgent gates
// on, or it claims slots the engine then refuses. The two differ for agents
// in the routing state.
func (r *EngineRunner) Capacity() (int, int) {
	return r.eng.ActiveCount(), r.eng.MaxAgents()
}

// WorkDirBusy reports whether any agent's process is still resident in
// workDir, whatever that agent's state label says and whoever started it.
//
// This is a question about processes (Engine.WorkDirOccupied), not about
// which agents the engine currently counts as active: an agent the queue
// does not own — a bare `agents spawn`, a TUI-killed agent, a Jira AI agent
// — can sit soft-closed or complete with its process still editing workDir,
// invisible to ActiveAgents but not to this. Comparison is on the cleaned
// path, so "/w/api" and "/w/api/" are the same directory. Worktree-isolated
// agents never match a repo path here: the engine rewrites their WorkDir to
// the private worktree it created (setupWorktree), so their directory is
// unique per agent by construction.
func (r *EngineRunner) WorkDirBusy(workDir string) bool {
	return r.eng.WorkDirOccupied(workDir)
}

// SendInput delivers a message to a running agent's stdin.
func (r *EngineRunner) SendInput(agentID, message string) error {
	return r.eng.SendInput(agentID, message)
}

// TerminateAgent really ends the agent: the subprocess is killed and any
// worktree cleaned up, leaving the record (and its transcript) in place.
//
// engine.KillAgent is deliberately not used here even though it is what the
// TUI's kill action calls. It soft-closes — State becomes killed while the
// process keeps running so an operator can carry on talking to the agent —
// which is right for a human at a terminal and wrong for the queue: the
// agent stops counting toward ActiveCount the moment it is soft-closed, but
// its process keeps editing its directory (WorkDirOccupied honestly reports
// that, but nothing else ever ends the process). Leaving it running would
// hold that directory busy forever, with no future task ever able to
// dispatch into it. engine.RemoveAgent terminates too but drops the record,
// which would take the cancelled task's output with it and make reconcile
// see the agent vanish rather than be killed.
func (r *EngineRunner) TerminateAgent(agentID string) error {
	return r.eng.TerminateAgent(agentID)
}
