package queue

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Task submission: the one entry point that creates tasks, and the
// validation that decides whether a batch is admissible at all.

// Add submits a batch of tasks as one atomic unit: either every spec is
// accepted or none is. Specs may reference each other by their batch-local
// Name in After, which is what lets a whole DAG be declared in one call
// without round-tripping through assigned IDs.
//
// Specs with no QueueID land in a single queue created for the batch.
func (m *Manager) Add(specs []TaskSpec) ([]Task, error) {
	if len(specs) == 0 {
		return nil, fmt.Errorf("%w: no tasks supplied", ErrInvalid)
	}

	m.mu.Lock()

	// Pass 1: validate, assign IDs, remember local names.
	byName := make(map[string]string, len(specs))
	assigned := make([]string, len(specs))
	autoQueue := ""
	// seenQueueIDs guards against two specs in this same batch introducing
	// colliding new queue IDs before either has landed in m.queues.
	seenQueueIDs := make(map[string]string, len(specs))
	for i := range specs {
		s := &specs[i]
		if s.Prompt == "" {
			m.mu.Unlock()
			return nil, fmt.Errorf("%w: task %d has no prompt", ErrInvalid, i)
		}
		if s.WorkDir == "" {
			m.mu.Unlock()
			return nil, fmt.Errorf("%w: task %d has no work_dir", ErrInvalid, i)
		}
		if s.OnFailure == "" {
			s.OnFailure = FailBlock
		}
		if !s.OnFailure.Valid() {
			m.mu.Unlock()
			return nil, fmt.Errorf("%w: task %d has unknown on_failure %q", ErrInvalid, i, s.OnFailure)
		}
		if s.MaxRetries < 0 {
			m.mu.Unlock()
			return nil, fmt.Errorf("%w: task %d has negative max_retries", ErrInvalid, i)
		}
		if s.QueueID != "" && !idPattern.MatchString(s.QueueID) {
			m.mu.Unlock()
			return nil, fmt.Errorf("%w: queue id %q must be alphanumeric with . _ - (max 64 chars)", ErrInvalid, s.QueueID)
		}
		// Queue IDs become filenames (store.path), and idPattern accepts
		// both cases, so "Prod" and "prod" are two queues in m.queues but
		// one file on a case-insensitive filesystem (macOS/APFS) — each
		// Save silently overwrites the other's tasks. Reject the clash
		// against both already-existing queues and ones this same batch is
		// about to create, rather than let two queues fight over one file.
		if s.QueueID != "" {
			fold := strings.ToLower(s.QueueID)
			if other, ok := seenQueueIDs[fold]; ok && other != s.QueueID {
				m.mu.Unlock()
				return nil, fmt.Errorf("%w: queue id %q collides with %q earlier in this batch on a case-insensitive filesystem", ErrInvalid, s.QueueID, other)
			}
			seenQueueIDs[fold] = s.QueueID
			for existing := range m.queues {
				if existing != s.QueueID && strings.EqualFold(existing, s.QueueID) {
					m.mu.Unlock()
					return nil, fmt.Errorf("%w: queue id %q collides with existing queue %q on a case-insensitive filesystem", ErrInvalid, s.QueueID, existing)
				}
			}
		}
		if s.QueueID == "" {
			if autoQueue == "" {
				autoQueue = m.mintQueueIDLocked()
			}
			s.QueueID = autoQueue
		}

		m.taskSeq++
		id := "t" + strconv.FormatInt(m.taskSeq, 10)
		assigned[i] = id
		if s.Name != "" {
			if _, dup := byName[s.Name]; dup {
				m.mu.Unlock()
				return nil, fmt.Errorf("%w: duplicate task name %q in batch", ErrInvalid, s.Name)
			}
			byName[s.Name] = id
		}
	}

	// Pass 2: resolve dependencies. A local Name wins over a task ID so a
	// batch stays self-consistent even if a name happens to look like an ID.
	incoming := make(map[string][]string, len(specs))
	for i := range specs {
		var deps []string
		for _, ref := range specs[i].After {
			if ref == "" {
				continue
			}
			if id, ok := byName[ref]; ok {
				deps = append(deps, id)
				continue
			}
			if _, ok := m.tasks[ref]; ok {
				deps = append(deps, ref)
				continue
			}
			m.mu.Unlock()
			return nil, fmt.Errorf("%w: task %q depends on unknown task %q", ErrInvalid, specs[i].Name, ref)
		}
		incoming[assigned[i]] = deps
	}

	if err := validateDeps(m.tasks, incoming); err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}

	// Pass 3: commit.
	now := time.Now()
	out := make([]Task, 0, len(specs))
	for i := range specs {
		s := specs[i]
		t := &Task{
			ID:         assigned[i],
			QueueID:    s.QueueID,
			Title:      s.Title,
			Prompt:     s.Prompt,
			WorkDir:    s.WorkDir,
			DependsOn:  incoming[assigned[i]],
			Opts:       s.Opts,
			State:      StateBlocked,
			MaxRetries: s.MaxRetries,
			OnFailure:  s.OnFailure,
			Priority:   s.Priority,
			CreatedAt:  now,
		}
		q := m.queues[s.QueueID]
		if q == nil {
			q = &queueState{id: s.QueueID, createdAt: now}
			m.queues[s.QueueID] = q
		}
		q.tasks = append(q.tasks, t)
		q.dirty = true
		m.tasks[t.ID] = t
	}

	// The blocked->ready promotion refreshBlockedLocked performs here is a
	// real state change and has to be broadcast like any other. Nothing
	// else will: the next tick's settle finds these tasks already correct,
	// so dispatch is the earliest frame a subscriber would otherwise see —
	// which for a task behind a long dependency chain or a paused queue is
	// minutes away, or never.
	promoted := m.refreshBlockedLocked()
	for _, id := range assigned {
		out = append(out, m.tasks[id].Clone())
	}
	// out already carries the post-refresh state of every new task, so a
	// promoted task must not be emitted twice. Copied rather than appended
	// to in place: out is the caller's return value.
	changed := make([]Task, len(out), len(out)+len(promoted))
	copy(changed, out)
	for _, t := range promoted {
		if _, isNew := incoming[t.ID]; !isNew {
			changed = append(changed, t)
		}
	}
	m.flushLocked()
	m.mu.Unlock()

	m.emit(changed)
	m.Wake()
	return out, nil
}

// mintQueueIDLocked allocates an unused auto queue ID. The sequence alone is
// not enough: an operator may name a queue "q1" outright (idPattern allows
// it) without ever touching the counter, and reusing that ID would silently
// merge an unrelated batch into their queue — where a single `queue cancel`
// or `queue pause` would then hit both. The check has to be case-fold, not
// exact, for the same reason the explicit-ID path above folds: "Q1" and the
// auto-minted "q1" are two queues in m.queues but one file on a
// case-insensitive filesystem (macOS/APFS), so an exact-only match here would
// let the auto path silently collide with an operator-named queue the
// explicit path already refuses to create. Caller holds m.mu.
func (m *Manager) mintQueueIDLocked() string {
	for {
		m.queueSeq++
		id := "q" + strconv.FormatInt(m.queueSeq, 10)
		taken := false
		for existing := range m.queues {
			if strings.EqualFold(existing, id) {
				taken = true
				break
			}
		}
		if !taken {
			return id
		}
	}
}
