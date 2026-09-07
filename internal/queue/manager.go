package queue

import (
	"context"
	"errors"
	"log"
	"regexp"
	"sync"
	"sync/atomic"
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
	// ErrNoCapacity is what a runner returns from StartTask when the agent
	// cap refused the spawn. It is never a task failure: capacity is
	// backpressure, so the scheduler puts the task back in line with its
	// attempt refunded. Runners must wrap it so errors.Is matches.
	ErrNoCapacity = errors.New("no agent capacity")
)

// AgentRunner is the queue's view of the agent engine. Keeping it an
// interface is what lets the scheduler be tested without spawning
// subprocesses; engine_runner.go holds the production adapter.
type AgentRunner interface {
	// StartTask dispatches a task and returns the new agent's ID.
	//
	// ctx is the manager's run context, cancelled by Stop. An implementation
	// must not begin a spawn once ctx is done: that refusal is what lets
	// Stop wait for the dispatch loop to actually finish instead of racing
	// it on a timeout. Returning ctx.Err() (wrapped or bare) is enough —
	// dispatch decides what to do from the manager's own context, not from
	// the error value.
	//
	// A refusal caused by the agent cap must be reported as an error
	// wrapping ErrNoCapacity: the scheduler distinguishes that from a
	// genuine spawn failure and does not consume an attempt for it.
	StartTask(ctx context.Context, t Task) (agentID string, err error)

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

	// TerminateAgent stops a dispatched agent for good: the OS process
	// must be gone and the working directory released once it returns.
	//
	// Soft-close semantics — end the turn, keep the process addressable —
	// are deliberately not enough here. The queue calls this when the work
	// itself is over (cancelled, queue aborted, or spawned for a claim that
	// is no longer ours), and an agent whose process outlives the call is
	// invisible to Capacity and WorkDirBusy while it is still editing
	// files, which is exactly how a second agent gets dispatched into an
	// occupied directory.
	TerminateAgent(agentID string) error
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

	// emitCh decouples change broadcasts from the goroutine that produced
	// them. The daemon's OnChange hook writes one WS frame per connected
	// client synchronously, each bounded only by a 10s write deadline, so
	// calling it inline put a wedged WS peer directly in the scheduler's
	// dispatch path — ready tasks sat idle with free engine slots while
	// tick() blocked in a broadcast. Frames are drained by one goroutine so
	// order is preserved, and dropped rather than queued without bound when
	// the consumer cannot keep up: a task change is a hint to re-read, and
	// stalling the scheduler to guarantee delivery is the worse trade.
	emitCh       chan Task
	emitOnce     sync.Once
	emitDropping atomic.Bool

	// runCtx is cancelled by Stop, before the stop channel is closed. It
	// exists for the one thing the scheduler does that it cannot otherwise
	// interrupt: AgentRunner.StartTask, which reaches into the engine and
	// may sit in `git worktree add` for seconds. Handing it to the runner
	// lets an in-flight batch refuse every remaining spawn immediately, so
	// Stop waits on at most the single spawn already under way rather than
	// timing out and letting eng.Shutdown race the dispatch goroutine.
	runCtx    context.Context
	runCancel context.CancelFunc

	// stopTimeoutOverride shortens Stop's drain deadline for tests. Zero
	// means stopDrainTimeout. Set before Start and never mutated after.
	stopTimeoutOverride time.Duration

	wake     chan struct{}
	stop     chan struct{}
	stopped  chan struct{}
	started  atomic.Bool
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
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		queues:       make(map[string]*queueState),
		tasks:        make(map[string]*Task),
		runner:       runner,
		store:        store,
		tickInterval: time.Second,
		emitCh:       make(chan Task, emitBuffer),
		runCtx:       ctx,
		runCancel:    cancel,
		wake:         make(chan struct{}, 1),
		stop:         make(chan struct{}),
		stopped:      make(chan struct{}),
	}
}

// emitBuffer is how many change frames may be in flight to the OnChange
// consumer before emit starts dropping. Sized for a busy tick's worth of
// transitions with room to spare, so only a genuinely stuck consumer — not
// an ordinary burst — ever loses a frame.
const emitBuffer = 256

// OnChange registers the task-change callback. Replaces any previous one.
func (m *Manager) OnChange(fn func(Task)) {
	m.onChangeMu.Lock()
	m.onChange = fn
	m.onChangeMu.Unlock()
}

// emit hands the change frames to the broadcast goroutine. Called outside
// m.mu, and — the point of the indirection — it never blocks: see emitCh.
func (m *Manager) emit(tasks []Task) {
	if len(tasks) == 0 || !m.hasOnChange() {
		return
	}
	m.emitOnce.Do(func() { go m.emitLoop() })
	for _, t := range tasks {
		select {
		case m.emitCh <- t:
		default:
			// One line per burst rather than one per frame: a stuck
			// consumer would otherwise drown the log in the same message.
			if m.emitDropping.CompareAndSwap(false, true) {
				log.Printf("queue: change broadcast consumer is not keeping up, dropping frames (task %s)", t.ID)
			}
		}
	}
}

// hasOnChange reports whether a change callback is registered.
func (m *Manager) hasOnChange() bool {
	m.onChangeMu.RLock()
	defer m.onChangeMu.RUnlock()
	return m.onChange != nil
}

// emitLoop drains emitCh into the change callback. One goroutine, so frames
// reach the consumer in the order emit produced them. The hook is re-read
// per frame because OnChange may replace it at any time.
func (m *Manager) emitLoop() {
	for {
		select {
		case <-m.stop:
			return
		case t := <-m.emitCh:
			m.emitDropping.Store(false)
			m.onChangeMu.RLock()
			fn := m.onChange
			m.onChangeMu.RUnlock()
			if fn != nil {
				fn(t)
			}
		}
	}
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
				// Clear the derived text only when leaving skipped, whose
				// Error this pass wrote itself. Other pending tasks may
				// carry an informational note that has to survive — Restore
				// records why a task was requeued after a daemon restart,
				// and blanket-clearing here erased it before an operator
				// could ever see it.
				if t.State == StateSkipped {
					t.Error = ""
					t.EndedAt = nil
				}
				t.State = next
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
