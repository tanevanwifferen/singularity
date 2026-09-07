package queue

import (
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Sentinel errors returned by Manager. The service layer maps these onto
// its own sentinels; callers should match with errors.Is rather than on
// message text.
var (
	// ErrNotFound means no task or queue with the given ID exists.
	ErrNotFound = errors.New("not found")
	// ErrInvalid means the request was malformed: an unknown dependency, a
	// dependency cycle, a bad ID, a missing prompt or workdir.
	ErrInvalid = errors.New("invalid queue request")
	// ErrNotRetryable means Retry was called on a task that is not in a
	// failed-like state.
	ErrNotRetryable = errors.New("task is not in a retryable state")
	// ErrNotWaiting means Answer was called on a task that is not waiting
	// for a human.
	ErrNotWaiting = errors.New("task is not waiting for input")
)

// AgentRunner is the queue's view of the agent engine. Keeping it an
// interface is what lets the scheduler be tested without spawning
// subprocesses; engine_runner.go holds the production adapter.
type AgentRunner interface {
	// StartTask dispatches a task and returns the new agent's ID.
	StartTask(t Task) (agentID string, err error)

	// AgentState returns the agent's current state name (the same strings
	// engine.AgentState.String() produces), its error text, and whether
	// the agent is still known to the engine at all.
	AgentState(agentID string) (state string, errText string, ok bool)

	// Capacity reports the number of active agents and the configured cap.
	Capacity() (active int, max int)

	// WorkDirBusy reports whether a live agent already occupies workDir.
	// The scheduler refuses to dispatch into an occupied directory: two
	// agents editing one worktree corrupt each other's work.
	WorkDirBusy(workDir string) bool

	// SendInput delivers an operator answer to a waiting agent.
	SendInput(agentID, message string) error

	// KillAgent terminates a dispatched agent. Used when a task is
	// cancelled while its agent is still running.
	KillAgent(agentID string) error
}

// queueState is one queue's in-memory state. tasks is kept in creation
// order and is also the slice that gets persisted.
type queueState struct {
	id        string
	paused    bool
	createdAt time.Time
	tasks     []*Task
	// dirty marks the queue as needing a Save on the next flush.
	dirty bool
}

// Manager owns every queue and the scheduler goroutine that drains them.
//
// All exported methods are safe for concurrent use and return copies of
// task state, never the pointers the scheduler mutates.
type Manager struct {
	mu     sync.Mutex
	queues map[string]*queueState
	tasks  map[string]*Task // taskID -> task, across all queues

	runner AgentRunner
	store  *Store

	taskSeq  int64
	queueSeq int64

	// onChange, when set, is called (outside the lock) for every task whose
	// state the scheduler advanced. Used by the daemon for WS broadcasts.
	onChange   func(Task)
	onChangeMu sync.RWMutex

	// tickInterval is the scheduler's fallback poll period. Agent updates
	// normally wake it immediately via Notify; the tick covers the cases
	// where nothing pushes — a task retried by an operator, capacity freed
	// by an agent the queue did not start.
	tickInterval time.Duration

	wake     chan struct{}
	stop     chan struct{}
	stopped  chan struct{}
	runOnce  sync.Once
	stopOnce sync.Once
}

// idPattern constrains queue IDs: they become filenames in the state
// directory, so anything path-like is rejected outright.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// taskIDPattern matches the IDs this package assigns, used to recover the
// sequence counter after a restart.
var taskIDPattern = regexp.MustCompile(`^t(\d+)$`)

// NewManager builds a Manager over the given runner and store. Either may
// be nil-ish: a nil store disables persistence, and a nil runner leaves the
// scheduler able to track state but unable to dispatch (used by tests that
// only exercise dependency logic).
func NewManager(runner AgentRunner, store *Store) *Manager {
	return &Manager{
		queues:       make(map[string]*queueState),
		tasks:        make(map[string]*Task),
		runner:       runner,
		store:        store,
		tickInterval: time.Second,
		wake:         make(chan struct{}, 1),
		stop:         make(chan struct{}),
		stopped:      make(chan struct{}),
	}
}

// OnChange registers the task-change callback. Replaces any previous one.
func (m *Manager) OnChange(fn func(Task)) {
	m.onChangeMu.Lock()
	m.onChange = fn
	m.onChangeMu.Unlock()
}

// emit fires the change callback. Called outside m.mu.
func (m *Manager) emit(tasks []Task) {
	m.onChangeMu.RLock()
	fn := m.onChange
	m.onChangeMu.RUnlock()
	if fn == nil {
		return
	}
	for _, t := range tasks {
		fn(t)
	}
}

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
		if s.QueueID == "" {
			if autoQueue == "" {
				m.queueSeq++
				autoQueue = "q" + strconv.FormatInt(m.queueSeq, 10)
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

	m.refreshBlockedLocked()
	for _, id := range assigned {
		out = append(out, m.tasks[id].Clone())
	}
	m.flushLocked()
	m.mu.Unlock()

	m.Wake()
	return out, nil
}

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

// Cancel stops one task. A running task's agent is killed; a pending task
// simply never starts. Dependents follow the task's OnFailure policy, so a
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
		if err := m.runner.KillAgent(agentID); err != nil {
			log.Printf("queue: kill agent %s for cancelled task %s: %v", agentID, taskID, err)
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
	for _, t := range q.tasks {
		if t.State.Terminal() {
			continue
		}
		m.markLocked(t, StateCancelled, "queue cancelled by operator")
		changed = append(changed, t.Clone())
	}
	m.flushLocked()
	m.mu.Unlock()

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
	m.mu.Unlock()

	if err := m.store.Delete(queueID); err != nil {
		log.Printf("queue: delete state for %s: %v", queueID, err)
	}
	return nil
}

// markLocked applies a terminal state to t. Caller holds m.mu.
func (m *Manager) markLocked(t *Task, state State, errText string) {
	t.State = state
	if errText != "" {
		t.Error = errText
	}
	if state.Terminal() && t.EndedAt == nil {
		now := time.Now()
		t.EndedAt = &now
	}
	if q := m.queues[t.QueueID]; q != nil {
		q.dirty = true
	}
}

// refreshBlockedLocked recomputes blocked/ready/skipped for every task whose
// state is derived from its dependencies, and returns the ones that changed.
// Caller holds m.mu.
//
// Skipped is included because it is purely derived — "a dependency did not
// complete" — so retrying that dependency has to un-skip everything
// downstream of it. Cancelled and failed are NOT derived: an operator
// cancelled the one, the agent failed the other, and neither should be
// revived by a change elsewhere in the graph.
//
// The pass loops to a fixed point: both skipping and un-skipping cascade
// along a chain, and a single pass would leave the far end stuck behind a
// predecessor it had already looked at.
func (m *Manager) refreshBlockedLocked() []Task {
	var changed []Task
	for {
		progressed := false
		for _, t := range m.tasks {
			switch t.State {
			case StateBlocked, StateReady, StateSkipped:
			default:
				continue
			}
			ready, skip := depsSatisfied(t, m.tasks)
			var next State
			switch {
			case skip:
				next = StateSkipped
			case ready:
				next = StateReady
			default:
				next = StateBlocked
			}
			if next == t.State {
				continue
			}
			if next == StateSkipped {
				m.markLocked(t, StateSkipped, "skipped: a dependency did not complete")
			} else {
				// Coming back from skipped: clear the derived failure text
				// and the end timestamp so the task looks pending again.
				t.State = next
				t.Error = ""
				t.EndedAt = nil
				if q := m.queues[t.QueueID]; q != nil {
					q.dirty = true
				}
			}
			changed = append(changed, t.Clone())
			progressed = true
		}
		if !progressed {
			return changed
		}
	}
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
