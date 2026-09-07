package queue

import (
	"fmt"
	"sort"
)

// Read paths. Every one of these returns copies, never the pointers the
// scheduler mutates.

// Get returns one task by ID.
func (m *Manager) Get(taskID string) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[taskID]
	if !ok {
		return Task{}, ErrNotFound
	}
	return t.Clone(), nil
}

// List returns tasks, optionally filtered by queue and by state. An empty
// queueID means every queue; an empty states slice means every state.
// Results are ordered by queue then creation.
func (m *Manager) List(queueID string, states []State) ([]Task, error) {
	for _, s := range states {
		if !s.Valid() {
			return nil, fmt.Errorf("%w: unknown state %q", ErrInvalid, s)
		}
	}
	want := make(map[State]bool, len(states))
	for _, s := range states {
		want[s] = true
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if queueID != "" {
		if _, ok := m.queues[queueID]; !ok {
			return nil, ErrNotFound
		}
	}

	ids := make([]string, 0, len(m.queues))
	for id := range m.queues {
		if queueID != "" && id != queueID {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var out []Task
	for _, id := range ids {
		for _, t := range m.queues[id].tasks {
			if len(want) > 0 && !want[t.State] {
				continue
			}
			out = append(out, t.Clone())
		}
	}
	return out, nil
}

// Queues returns a summary of every queue, newest first.
func (m *Manager) Queues() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Info, 0, len(m.queues))
	for _, q := range m.queues {
		out = append(out, q.info())
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// QueueInfo returns the summary for one queue.
func (m *Manager) QueueInfo(queueID string) (Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q, ok := m.queues[queueID]
	if !ok {
		return Info{}, ErrNotFound
	}
	return q.info(), nil
}

// info tallies the queue's task states. Caller holds m.mu.
func (q *queueState) info() Info {
	out := Info{ID: q.id, Paused: q.paused, CreatedAt: q.createdAt, Total: len(q.tasks)}
	for _, t := range q.tasks {
		switch t.State {
		case StateBlocked:
			out.Blocked++
		case StateReady:
			out.Ready++
		case StateRunning:
			out.Running++
		case StateWaitingHuman:
			out.Waiting++
		case StateDone:
			out.Done++
		case StateFailed:
			out.Failed++
		case StateCancelled:
			out.Cancelled++
		case StateSkipped:
			out.Skipped++
		}
	}
	return out
}

// Graph returns one queue's dependency DAG.
func (m *Manager) Graph(queueID string) (Graph, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q, ok := m.queues[queueID]
	if !ok {
		return Graph{}, ErrNotFound
	}
	return buildGraph(queueID, q.tasks), nil
}
