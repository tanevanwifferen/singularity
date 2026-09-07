package queue

import (
	"errors"
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
	m.flushLocked()
	m.mu.Unlock()

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
				if err := m.runner.KillAgent(id); err != nil {
					log.Printf("queue: kill agent %s during queue abort: %v", id, err)
				}
			}(agentID)
		}
	}
	return changed
}

// claim is a task dispatch marked running and is about to spawn. copy is
// the snapshot taken at claim time, which stillOurClaim compares against
// once StartTask returns.
type claim struct {
	id   string
	copy Task
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

	// Pass 1: pick the eligible tasks and the working directories that need
	// checking, under the lock.
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
	wantDirs := make([]string, 0, len(ordered))
	seenDir := make(map[string]bool, len(ordered))
	for _, t := range ordered {
		if t.Opts.UseWorktree || seenDir[t.WorkDir] {
			continue
		}
		seenDir[t.WorkDir] = true
		wantDirs = append(wantDirs, t.WorkDir)
	}
	m.mu.Unlock()

	if len(ordered) == 0 {
		return nil
	}

	// Pass 2: ask the runner which directories are occupied, with the lock
	// released. WorkDirBusy walks the engine and snapshots every active
	// agent — one agent mutex each — so calling it inside the claim loop
	// would park every concurrent queue read behind up to one traversal per
	// candidate. Same discipline as reconcile's AgentState calls.
	busy := make(map[string]bool, len(wantDirs))
	for _, dir := range wantDirs {
		busy[dir] = m.runner.WorkDirBusy(dir)
	}

	// Pass 3: claim the tasks to start under the lock, marking them running
	// before releasing it, so a concurrent tick cannot dispatch the same
	// task twice. StartTask itself runs unlocked.
	var claims []claim
	m.mu.Lock()
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
	for _, cand := range ordered {
		if slots <= 0 {
			break
		}
		// Re-verify: the lock was released for the busy-directory probe, so
		// the task may have been cancelled or retried in the meantime.
		t := m.tasks[cand.ID]
		if t == nil || t.State != StateReady {
			continue
		}
		if q := m.queues[t.QueueID]; q != nil && q.paused {
			continue
		}
		if !t.Opts.UseWorktree {
			if claimedDirs[t.WorkDir] || busy[t.WorkDir] {
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
		claims = append(claims, claim{id: t.ID, copy: t.Clone()})
		slots--
	}
	m.flushLocked()
	m.mu.Unlock()

	if len(claims) == 0 {
		return nil
	}

	var changed []Task
	// Agents spawned for a task that stopped being ours mid-spawn. Killed
	// after the loop, outside the lock: leaving one alive would hold an
	// engine slot and a working directory that nothing tracks any more.
	var orphans []string

spawn:
	for i, c := range claims {
		select {
		case <-m.stop:
			// Shutting down. Everything not yet spawned goes back to ready
			// so Stop waits on at most one in-flight StartTask instead of
			// the whole batch, and the engine is not handed work it is
			// about to tear down anyway.
			changed = append(changed, m.releaseClaims(claims[i:])...)
			break spawn
		default:
		}

		agentID, err := m.runner.StartTask(c.copy)

		m.mu.Lock()
		t := m.tasks[c.id]
		if t == nil || !m.stillOurClaim(t, c.copy) {
			// The task was cancelled, retried or removed while StartTask
			// was in flight. Its state belongs to whoever changed it; the
			// only thing left to do is not to leak the agent.
			m.mu.Unlock()
			if err == nil {
				orphans = append(orphans, agentID)
			}
			continue
		}
		switch {
		case errors.Is(err, ErrNoCapacity):
			// The cap refused the spawn — the scheduler's own capacity read
			// was stale (an agent started outside the queue, or one was
			// still being routed). That is backpressure, not a failure: the
			// task goes back in line with its attempt refunded.
			m.releaseClaimLocked(t)
		case err != nil:
			// The engine refused the spawn for a real reason. The attempt
			// still counts: a task whose workdir no longer exists fails
			// every time, and refunding the attempt would loop on it
			// forever.
			t.StartedAt = nil
			m.failLocked(t, "spawn failed: "+err.Error())
		default:
			t.AgentID = agentID
		}
		if q := m.queues[t.QueueID]; q != nil {
			q.dirty = true
		}
		changed = append(changed, t.Clone())
		m.flushLocked()
		m.mu.Unlock()
	}

	for _, id := range orphans {
		if err := m.runner.KillAgent(id); err != nil {
			log.Printf("queue: kill orphaned agent %s: %v", id, err)
		}
	}

	m.mu.Lock()
	changed = append(changed, m.refreshBlockedLocked()...)
	m.flushLocked()
	m.mu.Unlock()

	return changed
}

// stillOurClaim reports whether t is the same claim dispatch made, i.e. no
// operator action landed while StartTask was unlocked. Attempts and
// StartedAt together identify the claim: a cancel, a retry, a failure or a
// re-dispatch all move at least one of them. Caller holds m.mu.
func (m *Manager) stillOurClaim(t *Task, claimed Task) bool {
	if t.State != StateRunning || t.AgentID != "" || t.Attempts != claimed.Attempts {
		return false
	}
	return t.StartedAt != nil && claimed.StartedAt != nil && t.StartedAt.Equal(*claimed.StartedAt)
}

// releaseClaimLocked returns a claimed task to ready with its attempt
// refunded. For the cases where the spawn never happened at all: the agent
// cap refused it, or the scheduler is stopping. Neither is the task's
// doing, so spending a retry on it would eventually fail a task that never
// ran. Caller holds m.mu.
func (m *Manager) releaseClaimLocked(t *Task) {
	t.State = StateReady
	t.Error = ""
	t.StartedAt = nil
	if t.Attempts > 0 {
		t.Attempts--
	}
	if q := m.queues[t.QueueID]; q != nil {
		q.dirty = true
	}
}

// releaseClaims releases a batch of unspawned claims, skipping any that are
// no longer ours.
func (m *Manager) releaseClaims(claims []claim) []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	var changed []Task
	for _, c := range claims {
		t := m.tasks[c.id]
		if t == nil || !m.stillOurClaim(t, c.copy) {
			continue
		}
		m.releaseClaimLocked(t)
		changed = append(changed, t.Clone())
	}
	m.flushLocked()
	return changed
}
