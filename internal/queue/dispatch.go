package queue

import (
	"errors"
	"log"
	"time"
)

// The dispatch half of the scheduler: choosing what to start, claiming it
// under the lock, spawning it with the lock released, and reconciling the
// claim against whatever happened in between.

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
			// so the engine is not handed work it is about to tear down.
			// This is the fast path only: runCtx makes the guarantee, since
			// the runner refuses a spawn begun after cancellation whether
			// or not this check happened to observe the close in time.
			changed = append(changed, m.releaseClaims(claims[i:])...)
			break spawn
		default:
		}

		agentID, err := m.runner.StartTask(m.runCtx, c.copy)

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
		if m.runCtx.Err() != nil {
			// Stop landed while this spawn was in flight. Handled exactly
			// like a stale claim, and for the same reason: the claim is no
			// longer ours to complete. Adopting the agent would record an
			// AgentID that eng.Shutdown is about to invalidate, and failing
			// the task would blame it for a shutdown. Back to ready with
			// the attempt refunded, and kill whatever did get spawned.
			m.releaseClaimLocked(t)
			changed = append(changed, t.Clone())
			m.flushLocked()
			m.mu.Unlock()
			if err == nil && agentID != "" {
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
		if err := m.runner.TerminateAgent(id); err != nil {
			log.Printf("queue: terminate orphaned agent %s: %v", id, err)
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
