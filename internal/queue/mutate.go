package queue

import (
	"fmt"
	"log"
)

// Operator mutations: the verbs an operator drives from the CLI or the TUI,
// as opposed to the transitions the scheduler makes on its own. They share a
// shape — take the lock, move the task, settle the graph, persist, then do
// the engine-facing work with the lock released.

// Cancel stops one task. A running task's agent is terminated — process and
// all, so the working directory is genuinely released; a pending task simply
// never starts. Dependents follow the task's OnFailure policy, so a
// cancelled task under the default "block" policy skips everything
// downstream of it.
func (m *Manager) Cancel(taskID string) error {
	m.mu.Lock()
	t, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	if t.State.Terminal() {
		m.mu.Unlock()
		return nil
	}
	agentID := t.AgentID
	m.markLocked(t, StateCancelled, "cancelled by operator")
	changed := m.refreshBlockedLocked()
	changed = append(changed, t.Clone())
	m.flushLocked()
	m.mu.Unlock()

	if agentID != "" && m.runner != nil {
		// Best-effort: an agent that already exited is not an error here,
		// the task is cancelled either way.
		if err := m.runner.TerminateAgent(agentID); err != nil {
			log.Printf("queue: terminate agent %s for cancelled task %s: %v", agentID, taskID, err)
		}
	}
	m.emit(changed)
	m.Wake()
	return nil
}

// CancelQueue cancels every unfinished task in a queue.
func (m *Manager) CancelQueue(queueID string) error {
	m.mu.Lock()
	q, ok := m.queues[queueID]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	var changed []Task
	var agentIDs []string
	for _, t := range q.tasks {
		if t.State.Terminal() {
			continue
		}
		if t.AgentID != "" {
			agentIDs = append(agentIDs, t.AgentID)
		}
		m.markLocked(t, StateCancelled, "queue cancelled by operator")
		changed = append(changed, t.Clone())
	}
	m.flushLocked()
	m.mu.Unlock()

	// Terminate outside the lock, same shape as Cancel: a running agent's
	// process must be gone before the queue can call itself cancelled, or
	// nothing tracks the slot and directory it keeps holding.
	for _, agentID := range agentIDs {
		if m.runner == nil {
			break
		}
		if err := m.runner.TerminateAgent(agentID); err != nil {
			log.Printf("queue: terminate agent %s during queue cancel: %v", agentID, err)
		}
	}
	m.emit(changed)
	m.Wake()
	return nil
}

// Retry puts a failed, cancelled or skipped task back in line. Its attempt
// counter is reset so the task gets its full retry budget again, and any
// dependents that were skipped because of it are reconsidered.
func (m *Manager) Retry(taskID string) error {
	m.mu.Lock()
	t, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	switch t.State {
	case StateFailed, StateCancelled, StateSkipped:
	default:
		m.mu.Unlock()
		return ErrNotRetryable
	}
	// Captured before it is cleared below. Every path that lands a task here
	// already terminates its agent (Cancel, CancelQueue, reconcile's
	// error/killed branch), so this is normally a no-op — but Retry is
	// itself a point where the queue stops tracking a task's agent, and that
	// release must be made explicit rather than assumed true by
	// construction of every other caller.
	agentID := t.AgentID
	t.State = StateBlocked
	t.Error = ""
	t.Question = ""
	t.Attempts = 0
	t.StartedAt = nil
	t.EndedAt = nil
	t.AgentID = ""
	if q := m.queues[t.QueueID]; q != nil {
		q.dirty = true
	}
	changed := m.refreshBlockedLocked()
	changed = append(changed, t.Clone())
	m.flushLocked()
	m.mu.Unlock()

	if agentID != "" && m.runner != nil {
		if err := m.runner.TerminateAgent(agentID); err != nil {
			log.Printf("queue: terminate agent %s while retrying task %s: %v", agentID, taskID, err)
		}
	}

	m.emit(changed)
	m.Wake()
	return nil
}

// Answer delivers the operator's reply to a task waiting for human input.
// The agent resumes; the scheduler moves the task back to running once the
// engine reports it running again.
func (m *Manager) Answer(taskID, message string) error {
	if message == "" {
		return fmt.Errorf("%w: message required", ErrInvalid)
	}
	m.mu.Lock()
	t, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	if t.State != StateWaitingHuman {
		m.mu.Unlock()
		return ErrNotWaiting
	}
	agentID := t.AgentID
	m.mu.Unlock()

	if m.runner == nil || agentID == "" {
		return fmt.Errorf("%w: task has no live agent", ErrInvalid)
	}
	if err := m.runner.SendInput(agentID, message); err != nil {
		return err
	}

	m.mu.Lock()
	if t, ok := m.tasks[taskID]; ok && t.State == StateWaitingHuman {
		t.State = StateRunning
		t.Question = ""
		if q := m.queues[t.QueueID]; q != nil {
			q.dirty = true
		}
	}
	m.flushLocked()
	m.mu.Unlock()

	m.Wake()
	return nil
}

// Pause stops the queue from dispatching anything new. Tasks already
// running are left alone — pausing is about not starting more work, not
// about interrupting work in flight.
func (m *Manager) Pause(queueID string) error { return m.setPaused(queueID, true) }

// Resume lifts a pause.
func (m *Manager) Resume(queueID string) error { return m.setPaused(queueID, false) }

func (m *Manager) setPaused(queueID string, paused bool) error {
	m.mu.Lock()
	q, ok := m.queues[queueID]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	if q.paused != paused {
		q.paused = paused
		q.dirty = true
		m.flushLocked()
	}
	m.mu.Unlock()
	if !paused {
		m.Wake()
	}
	return nil
}

// RemoveQueue drops a queue and its tasks entirely. Refuses while any task
// is still active: dropping the record of a running agent would orphan it.
func (m *Manager) RemoveQueue(queueID string) error {
	m.mu.Lock()
	q, ok := m.queues[queueID]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	for _, t := range q.tasks {
		if t.State.Active() {
			m.mu.Unlock()
			return fmt.Errorf("%w: task %s is still %s", ErrInvalid, t.ID, t.State)
		}
	}
	for _, t := range q.tasks {
		delete(m.tasks, t.ID)
	}
	delete(m.queues, queueID)
	// Tasks in other queues may have depended on the ones just deleted. A
	// missing dependency counts as satisfied, so they can run now; settling
	// here rather than waiting for the next scheduler pass keeps the state
	// an operator reads back immediately after the removal honest.
	changed := m.refreshBlockedLocked()
	m.flushLocked()
	m.mu.Unlock()

	if err := m.store.Delete(queueID); err != nil {
		log.Printf("queue: delete state for %s: %v", queueID, err)
	}
	m.emit(changed)
	m.Wake()
	return nil
}
