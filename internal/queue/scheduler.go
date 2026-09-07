package queue

import (
	"log"
	"time"
)

// Start launches the scheduler goroutine. Idempotent: repeated calls are
// ignored so daemon wiring can call it unconditionally.
func (m *Manager) Start() {
	m.runOnce.Do(func() {
		m.started.Store(true)
		go m.loop()
	})
}

// stopWarnAfter is how long Stop waits before telling the operator why the
// daemon is taking its time; stopDrainTimeout is how long it waits before
// giving up. See Stop for where the number comes from.
const (
	stopWarnAfter = 2 * time.Second

	// stopDrainTimeout has to stay under the caller's own grace period, and
	// under what is left of it by the time Stop is reached: `singularity
	// daemon stop` SIGTERMs and SIGKILLs 10s later, and daemon.Run spends up
	// to 3s draining HTTP before this. Those two add up to 6s, leaving
	// eng.Shutdown roughly 4s before the external SIGKILL lands — and
	// eng.Shutdown is not free: it kills every agent under one lock and, for
	// each worktree-isolated one, runs `git worktree remove`, `git worktree
	// prune` and `git branch -D` synchronously (cleanupWorktree). Four
	// seconds is enough for an ordinary number of agents, not a guarantee
	// under an unbounded number of worktree-isolated ones — daemon.Run's 2s
	// post-eng.Shutdown listener wait doesn't add to that risk, since by
	// then eng.Shutdown has already run and the only thing left to lose is
	// socket cleanup, which the next startup sweeps regardless. Three
	// seconds here is deliberately shorter than a slow `git worktree add`
	// can take: the point is not to outwait the spawn, it is to leave
	// eng.Shutdown as much of the remaining budget as this bound can spare.
	stopDrainTimeout = 3 * time.Second
)

// Stop shuts the scheduler down and waits for the goroutine to exit. It
// reports whether the wait completed. Running agents are left alone — they
// belong to the engine, which has its own shutdown path; the queue only
// stops making new decisions.
//
// Cancelling runCtx before closing stop is what makes the wait short in
// practice: every spawn not yet started is refused immediately, so the only
// thing Stop can be waiting on is the single spawn already in flight. That
// ordering also fixes what the timeout used to break — returning early let
// the caller run eng.Shutdown() while the dispatch goroutine was still
// inside StartTask, two goroutines tearing down and building up the same
// engine agent.
//
// The bound is a backstop for the one case cancellation cannot reach: a
// spawn already past its context check and stuck in `git worktree add` on a
// large repo, or in os.Stat on a wedged mount. Waiting for that
// unconditionally trades a narrow race for a much worse failure — the
// daemon is SIGKILLed at the end of its grace period, so eng.Shutdown never
// runs (every agent orphaned) and the socket file is left behind. Giving up
// keeps the ordered shutdown at the cost of that narrow window, and says so
// in the log, which is the trade the project prefers.
func (m *Manager) Stop() bool {
	// Order matters: cancel before closing stop. The dispatch loop reads
	// runCtx to decide what to do with a spawn that raced the shutdown, and
	// a claim released via the stop channel while runCtx still looked live
	// would be re-dispatched by the tick that is already under way.
	m.stopOnce.Do(func() {
		m.runCancel()
		close(m.stop)
	})
	if !m.started.Load() {
		// Never started, so nothing will ever close m.stopped. Waiting
		// would block until the deadline for no reason.
		return true
	}
	warn := time.NewTimer(stopWarnAfter)
	defer warn.Stop()
	deadline := time.NewTimer(m.stopTimeout())
	defer deadline.Stop()
	for {
		select {
		case <-m.stopped:
			return true
		case <-warn.C:
			log.Printf("queue: still waiting for an in-flight agent spawn to return before shutdown")
		case <-deadline.C:
			log.Printf("queue: gave up after %s waiting for an in-flight agent spawn; "+
				"engine shutdown may overlap it — a spawn this slow is usually `git worktree add` on a large repo",
				m.stopTimeout())
			return false
		}
	}
}

// stopTimeout is stopDrainTimeout unless a test overrode it.
func (m *Manager) stopTimeout() time.Duration {
	if d := m.stopTimeoutOverride; d > 0 {
		return d
	}
	return stopDrainTimeout
}

// Wake asks the scheduler to run a tick as soon as it can. Non-blocking:
// the wake channel has capacity 1, so a burst of wakes coalesces into one
// tick rather than queueing up redundant work.
func (m *Manager) Wake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Notify is the engine observer hook: the daemon registers it via
// engine.AddAgentObserver so any agent state change wakes the scheduler.
// The agent ID is deliberately ignored — the tick reconciles every active
// task, which is both simpler and immune to the ID arriving for an agent
// the queue does not own.
func (m *Manager) Notify(string) { m.Wake() }

// loop is the scheduler goroutine.
func (m *Manager) loop() {
	defer close(m.stopped)
	ticker := time.NewTicker(m.tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stop:
			return
		case <-m.wake:
		case <-ticker.C:
		}
		m.tick()
	}
}

// tick is one scheduling pass: reconcile what is running, propagate any
// failures, then dispatch whatever is ready and fits.
//
// The order matters. Reconciling first means a slot freed by an agent that
// just finished is available to the dispatch step in the same tick, so a
// chain of dependent tasks advances one link per tick instead of stalling
// for a whole tick interval between links.
func (m *Manager) tick() {
	changed := m.reconcile()
	changed = append(changed, m.settle()...)
	changed = append(changed, m.dispatch()...)
	m.emit(changed)
}

// settle recomputes dependency-derived state once per tick, unconditionally.
//
// It has to be unconditional. Both reconcile and dispatch return early on an
// idle daemon — no active agents to probe, nothing ready to start — so a
// dependency graph that changed without a task transition (a queue removed
// out from under its dependents, say) would otherwise stay wedged until some
// unrelated operator action happened to run the pass.
func (m *Manager) settle() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	changed := m.refreshBlockedLocked()
	m.flushLocked()
	return changed
}

// reconcile reads the engine's view of every dispatched agent and advances
// the owning task's state to match.
func (m *Manager) reconcile() []Task {
	if m.runner == nil {
		return nil
	}

	// Collect the work to do under the lock, then query the runner without
	// it: AgentState reaches into the engine, which takes its own locks.
	type probe struct {
		taskID  string
		agentID string
	}
	m.mu.Lock()
	var probes []probe
	for _, t := range m.tasks {
		if t.State.Active() && t.AgentID != "" {
			probes = append(probes, probe{taskID: t.ID, agentID: t.AgentID})
		}
	}
	m.mu.Unlock()

	if len(probes) == 0 {
		return nil
	}

	type observation struct {
		taskID string
		state  string
		errTxt string
		known  bool
	}
	obs := make([]observation, 0, len(probes))
	for _, p := range probes {
		state, errTxt, ok := m.runner.AgentState(p.agentID)
		obs = append(obs, observation{taskID: p.taskID, state: state, errTxt: errTxt, known: ok})
	}

	m.mu.Lock()
	var changed []Task
	abortQueues := make(map[string]bool)
	// terminateAgents collects agents the queue is about to stop tracking:
	// every branch below that moves a task out of State.Active() — done,
	// failed or killed alike — releases its agent explicitly rather than
	// inferring the engine already ended it. A terminal state label is not
	// proof the process is gone: engine.KillAgent soft-closes (so the TUI
	// can keep talking to a killed agent), and a backend can emit its result
	// while its process stays resident on stdin waiting for a follow-up
	// (agent.sendInput), which is what `complete` looks like too. Collected
	// under the lock, acted on after it is released. TerminateAgent is a
	// no-op on a process that has genuinely already exited, so calling it
	// here for every case costs nothing extra.
	var terminateAgents []string
	for _, o := range obs {
		t := m.tasks[o.taskID]
		if t == nil || !t.State.Active() {
			continue
		}
		before := t.State

		switch {
		case !o.known:
			// The agent vanished from the engine (removed, or the engine
			// was restarted underneath us). Treat as a failure so the retry
			// policy decides, rather than leaving the task running forever.
			// Nothing to terminate: the engine has no record of it at all.
			m.failLocked(t, "agent is no longer known to the engine")
		case o.state == agentStateComplete:
			if t.AgentID != "" {
				terminateAgents = append(terminateAgents, t.AgentID)
			}
			t.State = StateDone
			t.Error = ""
			now := time.Now()
			t.EndedAt = &now
			if q := m.queues[t.QueueID]; q != nil {
				q.dirty = true
			}
		case o.state == agentStateError || o.state == agentStateKilled:
			msg := o.errTxt
			if msg == "" {
				msg = "agent " + o.state
			}
			if t.AgentID != "" {
				terminateAgents = append(terminateAgents, t.AgentID)
			}
			m.failLocked(t, msg)
		case o.state == agentStateWaitingHuman:
			t.State = StateWaitingHuman
			if q := m.queues[t.QueueID]; q != nil {
				q.dirty = true
			}
		default:
			// routing / starting / running / idle: still in flight. A task
			// that was waiting for a human and is running again lands here.
			if t.State == StateWaitingHuman {
				t.State = StateRunning
				t.Question = ""
				if q := m.queues[t.QueueID]; q != nil {
					q.dirty = true
				}
			}
		}

		if t.State != before {
			changed = append(changed, t.Clone())
			if t.State == StateFailed && t.OnFailure == FailAbortQueue {
				abortQueues[t.QueueID] = true
			}
		}
	}

	for queueID := range abortQueues {
		changed = append(changed, m.abortQueueLocked(queueID)...)
	}
	m.flushLocked()
	m.mu.Unlock()

	// Terminated synchronously, not fire-and-forget: tick() runs dispatch
	// immediately after reconcile returns, and dispatch decides whether a
	// directory is free from the same engine state TerminateAgent changes.
	// A goroutine here would let dispatch run before the kill actually
	// lands, dispatching a second agent while the first's process is still
	// alive in the directory it was just declared to have left.
	for _, agentID := range terminateAgents {
		if err := m.runner.TerminateAgent(agentID); err != nil {
			log.Printf("queue: terminate agent %s observed dead in the engine: %v", agentID, err)
		}
	}

	// Dependency-derived state is not recomputed here: tick's settle pass
	// runs immediately after and does it for every tick, idle or not.
	return changed
}

// Agent state names as produced by engine.AgentState.String(). Compared as
// strings so internal/queue does not import internal/engine.
//
// agentStateWaitingHuman is not emitted by the engine yet — it arrives with
// the human-escalation feature. Handling it now means the queue picks up
// escalations the moment the engine can report them, with no change here.
const (
	agentStateComplete     = "complete"
	agentStateError        = "error"
	agentStateKilled       = "killed"
	agentStateWaitingHuman = "waiting_human"
)

// failLocked applies a failure to t, consuming a retry if one is left.
// Caller holds m.mu.
func (m *Manager) failLocked(t *Task, msg string) {
	t.Error = msg
	if t.Attempts <= t.MaxRetries {
		// Retry: back to blocked so dependencies are re-checked, and drop
		// the dead agent reference.
		t.State = StateBlocked
		t.AgentID = ""
		t.Question = ""
		t.StartedAt = nil
		if q := m.queues[t.QueueID]; q != nil {
			q.dirty = true
		}
		return
	}
	m.markLocked(t, StateFailed, msg)
}

// abortQueueLocked cancels every unfinished task in a queue after a task
// with the abort-queue policy failed. Caller holds m.mu.
func (m *Manager) abortQueueLocked(queueID string) []Task {
	q := m.queues[queueID]
	if q == nil {
		return nil
	}
	var changed []Task
	for _, t := range q.tasks {
		if t.State.Terminal() {
			continue
		}
		agentID := t.AgentID
		m.markLocked(t, StateCancelled, "queue aborted: a task with on_failure=abort-queue failed")
		changed = append(changed, t.Clone())
		if agentID != "" && m.runner != nil {
			// Fire-and-forget so the kill does not run under m.mu.
			go func(id string) {
				if err := m.runner.TerminateAgent(id); err != nil {
					log.Printf("queue: terminate agent %s during queue abort: %v", id, err)
				}
			}(agentID)
		}
	}
	return changed
}
