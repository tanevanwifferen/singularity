package queue

import (
	"log"
	"strconv"
	"time"
)

// Persistence: loading queues back at startup and writing them out again.
// The scheduler and the operator verbs both leave queues marked dirty and
// call flushLocked; nothing else in the package touches the store.

// Restore loads persisted queues from the store. Tasks left mid-flight by a
// daemon restart are reconciled: the engine keeps agents in memory only, so
// a task recorded as running has no surviving agent and goes back to ready
// (or failed, when its retries are spent).
//
// Parse failures are logged and skipped rather than returned — one corrupt
// state file must not stop the daemon from serving the other queues.
func (m *Manager) Restore() {
	records, problems := m.store.Load()
	for _, p := range problems {
		log.Printf("queue: %v", p)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range records {
		rec := records[i]
		if rec.ID == "" {
			continue
		}
		q := &queueState{id: rec.ID, paused: rec.Paused, createdAt: rec.CreatedAt}
		if q.createdAt.IsZero() {
			q.createdAt = time.Now()
		}
		for _, t := range rec.Tasks {
			if t == nil || t.ID == "" {
				continue
			}
			t.QueueID = rec.ID
			if t.State.Active() {
				// The agent running this task died with the old daemon
				// process: the engine holds agents in memory only. That is
				// an external interruption rather than the task failing, so
				// the attempt is refunded and the task goes back in line
				// with its full retry budget.
				//
				// The trade-off is a task that crashes the daemon every
				// time it runs will be retried after every restart. Bounding
				// that with the retry counter would instead penalise every
				// task for an unrelated restart, which is the worse of the
				// two failures; a crash-looping daemon is visible on its own.
				t.AgentID = ""
				t.Question = ""
				t.State = StateBlocked
				t.StartedAt = nil
				t.Error = "requeued: the daemon restarted while this task was running"
				if t.Attempts > 0 {
					t.Attempts--
				}
				q.dirty = true
			}
			q.tasks = append(q.tasks, t)
			m.tasks[t.ID] = t
			if match := taskIDPattern.FindStringSubmatch(t.ID); match != nil {
				if n, err := strconv.ParseInt(match[1], 10, 64); err == nil && n > m.taskSeq {
					m.taskSeq = n
				}
			}
		}
		m.queues[rec.ID] = q
		if n := autoQueueSeq(rec.ID); n > m.queueSeq {
			m.queueSeq = n
		}
	}

	// Recompute blocked/ready now that every task is loaded: a dependency
	// may have finished in a previous daemon lifetime.
	m.refreshBlockedLocked()
	m.flushLocked()
}

// autoQueueSeq extracts the numeric suffix of an auto-generated queue ID
// ("q7" -> 7), returning 0 for operator-supplied names.
func autoQueueSeq(id string) int64 {
	if len(id) < 2 || id[0] != 'q' {
		return 0
	}
	n, err := strconv.ParseInt(id[1:], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// flushLocked persists every dirty queue. Caller holds m.mu.
func (m *Manager) flushLocked() {
	for _, q := range m.queues {
		if !q.dirty {
			continue
		}
		if err := m.store.Save(q); err != nil {
			log.Printf("queue: save %s: %v", q.id, err)
			continue
		}
		q.dirty = false
	}
}
