package queue

import (
	"log"
	"time"
)

// Start launches the scheduler goroutine. Idempotent: repeated calls are
// ignored so daemon wiring can call it unconditionally.
func (m *Manager) Start() {
	m.runOnce.Do(func() {
		go m.loop()
	})
}

// Stop shuts the scheduler down and waits for the goroutine to exit.
// Running agents are left alone — they belong to the engine, which has its
// own shutdown path; the queue only stops making new decisions.
func (m *Manager) Stop() {
	m.stopOnce.Do(func() {
		close(m.stop)
	})
	select {
	case <-m.stopped:
	case <-time.After(2 * time.Second):
		log.Printf("queue: scheduler did not stop within 2s")
	}
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
	changed = append(changed, m.dispatch()...)
	m.emit(changed)
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
			m.failLocked(t, "agent is no longer known to the engine")
		case o.state == agentStateComplete:
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
	changed = append(changed, m.refreshBlockedLocked()...)
	m.flushLocked()
	m.mu.Unlock()

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
				if err := m.runner.KillAgent(id); err != nil {
					log.Printf("queue: kill agent %s during queue abort: %v", id, err)
				}
			}(agentID)
		}
	}
	return changed
}

// dispatch starts as many ready tasks as capacity allows.
func (m *Manager) dispatch() []Task {
	if m.runner == nil {
		return nil
	}

	active, max := m.runner.Capacity()
	if max > 0 && active >= max {
		// No slot free. Not an error: capacity is backpressure here, which
		// is the whole reason queued tasks exist.
		return nil
	}

	m.mu.Lock()
	var candidates []*Task
	for _, t := range m.tasks {
		if t.State != StateReady {
			continue
		}
		if q := m.queues[t.QueueID]; q != nil && q.paused {
			continue
		}
		candidates = append(candidates, t)
	}
	ordered := dispatchOrder(candidates)

	// Claim the tasks to start under the lock, marking them running before
	// releasing it, so a concurrent tick cannot dispatch the same task
	// twice. StartTask itself runs unlocked.
	type claim struct {
		task *Task
		copy Task
	}
	var claims []claim
	slots := max - active
	if max <= 0 {
		slots = len(ordered)
	}
	// Working directories claimed within this tick: WorkDirBusy only sees
	// agents the engine already knows about, so two tasks on the same
	// directory in one tick would both pass the check.
	//
	// Worktree-isolated tasks are exempt from both checks. The engine gives
	// each such agent its own worktree and rewrites the agent's WorkDir to
	// it, so several of them can share a nominal repo path without ever
	// touching the same files — serialising them would be pure loss.
	claimedDirs := make(map[string]bool)
	for _, t := range ordered {
		if slots <= 0 {
			break
		}
		if !t.Opts.UseWorktree {
			if claimedDirs[t.WorkDir] || m.runner.WorkDirBusy(t.WorkDir) {
				continue
			}
			claimedDirs[t.WorkDir] = true
		}
		t.Attempts++
		t.State = StateRunning
		t.Error = ""
		now := time.Now()
		t.StartedAt = &now
		t.EndedAt = nil
		if q := m.queues[t.QueueID]; q != nil {
			q.dirty = true
		}
		claims = append(claims, claim{task: t, copy: t.Clone()})
		slots--
	}
	m.flushLocked()
	m.mu.Unlock()

	if len(claims) == 0 {
		return nil
	}

	var changed []Task
	for _, c := range claims {
		agentID, err := m.runner.StartTask(c.copy)

		m.mu.Lock()
		t := m.tasks[c.task.ID]
		if t == nil {
			m.mu.Unlock()
			continue
		}
		if err != nil {
			// The engine refused the spawn. The attempt still counts: a
			// task whose workdir no longer exists fails every time, and
			// refunding the attempt would loop on it forever.
			t.StartedAt = nil
			m.failLocked(t, "spawn failed: "+err.Error())
		} else {
			t.AgentID = agentID
		}
		if q := m.queues[t.QueueID]; q != nil {
			q.dirty = true
		}
		changed = append(changed, t.Clone())
		m.flushLocked()
		m.mu.Unlock()
	}

	m.mu.Lock()
	changed = append(changed, m.refreshBlockedLocked()...)
	m.flushLocked()
	m.mu.Unlock()

	return changed
}
