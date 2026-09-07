package flow

import (
	"errors"
	"fmt"
	"log"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/queue"
)

// The flow manager: the record keeper. It owns the set of flows, mints their
// IDs, validates a start request and persists every mutation — and it does
// not drive them. Round submission and verdict reading belong to the
// reconciler (reconcile.go), for the reason queue.Manager.tick exists:
// re-deriving a flow's position from its tasks is what makes a daemon
// restart uneventful, and a handler that advanced a flow inline would lose
// every transition the daemon was not running for.
//
// Consequently Start leaves a new flow in StatePending. That state is one
// reconciler pass wide (§4), so a flow is never observed stateless, and the
// pass that picks it up is the same code path a restart takes.

// Sentinel errors returned by Manager. The service layer maps these onto its
// own sentinels the way local.mapQueueErr maps the queue's; callers should
// match with errors.Is rather than on message text.
var (
	// ErrNotFound means no flow with the given ID exists. Maps to NOT_FOUND.
	ErrNotFound = errors.New("not found")
	// ErrInvalid means the request was malformed: an empty goal, a work_dir
	// that is not a directory, a max_rounds out of range, an unknown list
	// state, or the worktree isolation a flow refuses (§2). Maps to
	// BAD_REQUEST.
	ErrInvalid = errors.New("invalid flow request")
	// ErrActive means Remove was called on a flow that has not finished.
	// Maps to CONFLICT, the same rule and reason as
	// queue.Manager.RemoveQueue: dropping the record of a running flow
	// would orphan the tasks it is the only index of.
	ErrActive = errors.New("flow is still active")
)

// maxRounds is the upper bound on a flow's round cap, and defaultRounds the
// value a request that says nothing gets (§4).
const (
	defaultRounds = 3
	maxRoundsCap  = 20
)

// TaskQueue is the flow's view of the task queue: the four verbs a flow
// actually needs, declared here rather than imported as a concrete type.
//
// This is queue.AgentRunner's discipline applied one layer up. It buys two
// things. The manager is testable against a fake queue, with no scheduler
// goroutine and no agent processes — which is the only way the round
// bookkeeping is assertable at all. And internal/queue stays ignorant of
// flows: the dependency runs one way, so the queue never grows a special
// case for a consumer it cannot see.
//
// The signatures are queue.Manager's own, so the concrete manager satisfies
// this without an adapter (manager_test.go asserts that at compile time).
// Notably absent is CancelQueue: a flow cancels the task IDs it recorded
// itself, one at a time, because Manager.Add adopts a pre-existing queue
// named flow-<id> rather than refusing it, and cancelling a whole queue
// could therefore reach tasks the flow did not create (§4).
type TaskQueue interface {
	// Add submits a batch atomically; specs may reference each other by
	// batch-local Name in After, which is how a round's two tasks declare
	// their edge in one call.
	Add(specs []queue.TaskSpec) ([]queue.Task, error)
	// Get returns one task by ID, or an error wrapping queue.ErrNotFound.
	Get(taskID string) (queue.Task, error)
	// List returns tasks filtered by queue and state; empty means "any".
	List(queueID string, states []queue.State) ([]queue.Task, error)
	// Cancel stops one task, terminating its agent if it has one.
	Cancel(taskID string) error
}

// StartRequest is everything needed to create a flow. It is the wire shape
// too: internal/api aliases these types rather than re-projecting them, so
// the tags are the request's field names (§6).
//
// Opts deliberately has no worktree escape hatch that works — UseWorktree is
// refused, not ignored, see Start.
type StartRequest struct {
	Title string `json:"title,omitempty"`
	// Goal is the implementer's task and is repeated verbatim to every
	// later fixer and to the reviewer. Required.
	Goal string `json:"goal"`
	// ReviewGoal narrows the reviewer's attention without replacing the
	// rest of its review.
	ReviewGoal string `json:"review_goal,omitempty"`
	// WorkDir must exist: every round of the flow runs in it.
	WorkDir string `json:"work_dir"`
	// MaxRounds defaults to 3 and is validated to 1..20.
	MaxRounds int         `json:"max_rounds,omitempty"`
	Opts      TaskOptions `json:"opts,omitempty"`
	// ReviewOpts defaults to Opts when left entirely unset. Partial
	// overrides are the caller's to compose (the CLI's --reviewer-* flags
	// override only what they name); an all-zero value here means "the
	// reviewer runs like the implementer".
	ReviewOpts TaskOptions `json:"review_opts,omitempty"`
}

// Manager owns every flow. All exported methods are safe for concurrent use
// and return copies of flow state, never the pointers the reconciler
// mutates.
type Manager struct {
	mu    sync.Mutex
	flows map[string]*Flow

	queue TaskQueue
	store *Store

	seq int64

	// The driver's state. The reconciler itself is reconcile.go; the fields
	// live here because they are the manager's, and because NewManager is
	// the one place the channels can be made before anything can Wake.
	//
	// tickInterval is the reconciler's fallback poll period. A queue task
	// change normally wakes it immediately via Notify; the tick covers what
	// nothing pushes — a verdict file that landed after its review task was
	// already observed done, and every transition a daemon restart has to
	// re-derive.
	tickInterval time.Duration
	wake         chan struct{}
	stop         chan struct{}
	stopped      chan struct{}
	started      atomic.Bool
	runOnce      sync.Once
	stopOnce     sync.Once
	// stopTimeoutOverride shortens Stop's drain deadline for tests. Zero
	// means stopDrainTimeout. Set before Start and never mutated after.
	stopTimeoutOverride time.Duration

	// onFlowChange is called, outside every lock, once per flow transition
	// the reconciler makes. The daemon fills this slot with its WS
	// broadcast hook. Guarded by its own mutex so a listener may call back
	// into the manager.
	onFlowChange func(Flow)
	onChangeMu   sync.RWMutex
}

// NewManager builds a Manager over the given queue and store. Either may be
// nil-ish: a nil store disables persistence (Store's zero value is a valid
// no-op), and a nil queue leaves the manager able to keep records but unable
// to submit or cancel tasks — which is what a test that only exercises
// validation wants.
func NewManager(q TaskQueue, store *Store) *Manager {
	return &Manager{
		flows:        make(map[string]*Flow),
		queue:        q,
		store:        store,
		tickInterval: defaultTickInterval,
		wake:         make(chan struct{}, 1),
		stop:         make(chan struct{}),
		stopped:      make(chan struct{}),
	}
}

// Restore adopts every persisted flow and continues the f<n> sequence from
// the highest ID seen, so a new flow cannot reuse a live one's ID.
//
// It replays nothing: see the package-level note above and Restore's own doc
// comment. A restored non-terminal flow is picked up by the next reconciler
// pass, which re-derives its position from its tasks.
func (m *Manager) Restore() {
	flows, seq := Restore(m.store)

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, f := range flows {
		if _, clash := m.flows[f.ID]; clash {
			continue
		}
		m.flows[f.ID] = f
	}
	if seq > m.seq {
		m.seq = seq
	}
}

// Start validates a request, records a new flow and returns it.
//
// The flow comes back StatePending with no rounds: submitting round 1 is the
// reconciler's, so that the submit path a restart takes is the same one a
// fresh flow takes. A caller that wants to block until the work is under way
// waits on the flow's state, not on this call.
func (m *Manager) Start(req StartRequest) (Flow, error) {
	goal := strings.TrimSpace(req.Goal)
	if goal == "" {
		return Flow{}, fmt.Errorf("%w: no goal supplied", ErrInvalid)
	}
	workDir := strings.TrimSpace(req.WorkDir)
	if workDir == "" {
		return Flow{}, fmt.Errorf("%w: no work_dir supplied", ErrInvalid)
	}
	// The directory has to exist now, not when the first agent is
	// dispatched: a flow that starts against a missing path would error one
	// task failure later, with the message buried in an agent's output.
	info, err := os.Stat(workDir)
	if err != nil {
		return Flow{}, fmt.Errorf("%w: work_dir %s: %v", ErrInvalid, workDir, err)
	}
	if !info.IsDir() {
		return Flow{}, fmt.Errorf("%w: work_dir %s is not a directory", ErrInvalid, workDir)
	}

	rounds := req.MaxRounds
	if rounds == 0 {
		rounds = defaultRounds
	}
	if rounds < 1 || rounds > maxRoundsCap {
		return Flow{}, fmt.Errorf("%w: max_rounds must be between 1 and %d, got %d",
			ErrInvalid, maxRoundsCap, req.MaxRounds)
	}

	// Refused rather than silently cleared, because the caller who asked
	// for isolation would otherwise get a flow that quietly did not have
	// it. Worktree isolation is wrong here twice over: the engine rewrites
	// an isolated agent's WorkDir to a private checkout, so the reviewer
	// would review a different tree than the implementer wrote, and each
	// isolated agent merges back on its own, so a rejected round's work
	// would already be merged (§2). Isolation is the caller's job, done
	// before the flow starts — point the flow at a workflow's worktree.
	if req.Opts.UseWorktree || req.ReviewOpts.UseWorktree {
		return Flow{}, fmt.Errorf("%w: use_worktree is not supported by flows: "+
			"every round must see the same tree, and an isolated agent both reviews a "+
			"private checkout and merges it back independently. Create the worktree first "+
			"and pass it as work_dir", ErrInvalid)
	}

	reviewOpts := req.ReviewOpts
	if isZeroOpts(reviewOpts) {
		reviewOpts = req.Opts
	}

	m.mu.Lock()
	m.seq++
	id := "f" + strconv.FormatInt(m.seq, 10)
	f := &Flow{
		ID:         id,
		QueueID:    "flow-" + id,
		Title:      strings.TrimSpace(req.Title),
		Goal:       goal,
		ReviewGoal: strings.TrimSpace(req.ReviewGoal),
		WorkDir:    workDir,
		MaxRounds:  rounds,
		Opts:       req.Opts,
		ReviewOpts: reviewOpts,
		State:      StatePending,
		Rounds:     []*Round{},
		CreatedAt:  time.Now(),
	}
	m.flows[id] = f
	m.saveLocked(f)
	out := f.Clone()
	m.mu.Unlock()

	return out, nil
}

// Get returns one flow by ID.
func (m *Manager) Get(flowID string) (Flow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.flows[flowID]
	if !ok {
		return Flow{}, ErrNotFound
	}
	return f.Clone(), nil
}

// List returns every flow, optionally filtered by state, oldest first. An
// empty states slice means every state; an unknown state is a request error
// rather than an empty result, exactly as queue.Manager.List treats one.
func (m *Manager) List(states []State) ([]Flow, error) {
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

	out := make([]Flow, 0, len(m.flows))
	for _, f := range m.flows {
		if len(want) > 0 && !want[f.State] {
			continue
		}
		out = append(out, f.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return lessFlowID(out[i].ID, out[j].ID) })
	return out, nil
}

// Cancel marks the flow cancelled and stops the tasks it created.
//
// The two halves are deliberate. The flow record moves first, under the
// lock, so nothing observes a cancelled flow still calling itself running;
// the queue calls then happen with the lock released, one recorded task ID at
// a time. Never CancelQueue: Manager.Add adopts a queue named flow-<id> that
// somehow already exists, so the queue may hold tasks this flow did not
// create, and cancelling those is not the flow's to do (§4).
//
// Cancelling an already-terminal flow is a no-op, not an error — the same
// idempotence queue.Manager.Cancel offers, and what makes a double-click on
// a TUI confirm harmless. A task that refuses to cancel is logged rather than
// returned: the flow is cancelled either way, and the reconciler is what
// notices a task that outlived the call.
func (m *Manager) Cancel(flowID string) error {
	m.mu.Lock()
	f, ok := m.flows[flowID]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	if f.State.Terminal() {
		m.mu.Unlock()
		return nil
	}
	taskIDs := recordedTaskIDs(f)
	now := time.Now()
	f.State = StateCancelled
	f.EndedAt = &now
	// A round the operator stopped concluded nothing, which is what
	// RoundErrored means. RoundState has no cancelled member on purpose —
	// a round's grade describes the review, and there was none — so
	// leaving the round "running" under a terminal flow would be the only
	// other option, and it would make the tree lie about a step that is
	// about to be cancelled.
	for _, r := range f.Rounds {
		if r.State.Terminal() {
			continue
		}
		r.State = RoundErrored
		if r.EndedAt == nil {
			r.EndedAt = &now
		}
	}
	m.saveLocked(f)
	m.mu.Unlock()

	if m.queue == nil {
		return nil
	}
	for _, id := range taskIDs {
		t, err := m.queue.Get(id)
		if err != nil {
			// Gone from the queue (queue remove) — nothing left to stop.
			continue
		}
		if t.State.Terminal() {
			continue
		}
		if err := m.queue.Cancel(id); err != nil {
			log.Printf("flow: cancel task %s of flow %s: %v", id, flowID, err)
		}
	}
	return nil
}

// Remove deletes a flow's record and its verdict directory. It is refused
// while the flow is non-terminal, the same rule and reason as
// queue.Manager.RemoveQueue: a running flow's record is the only index of
// the tasks it created, so dropping it orphans them. Cancel first.
//
// The underlying queue is left alone — `queue remove` owns that, and a
// flow's tasks stay inspectable after the flow record is gone.
func (m *Manager) Remove(flowID string) error {
	m.mu.Lock()
	f, ok := m.flows[flowID]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	if !f.State.Terminal() {
		m.mu.Unlock()
		return fmt.Errorf("%w: flow %s is %s", ErrActive, flowID, f.State)
	}
	delete(m.flows, flowID)
	m.mu.Unlock()

	if err := m.store.Delete(flowID); err != nil {
		// The record is already out of the map, so reporting this as a
		// failure would leave the caller unable to retry anything useful.
		log.Printf("flow: delete state for %s: %v", flowID, err)
	}
	return nil
}

// saveLocked persists one flow. Caller holds m.mu — the flow pointer is
// manager state, and marshalling it while the reconciler could be mutating
// it is exactly the race the lock exists for. A persistence failure is
// logged, not returned: the flow is live in memory either way, and refusing
// a start because the state directory is full would be the worse outcome.
func (m *Manager) saveLocked(f *Flow) {
	if err := m.store.Save(f); err != nil {
		log.Printf("flow: save %s: %v", f.ID, err)
	}
}

// recordedTaskIDs returns every task ID the flow created, in round order.
// This is the whole of the flow's authority over the queue: an ID that is
// not in here is not the flow's to touch.
func recordedTaskIDs(f *Flow) []string {
	out := make([]string, 0, len(f.Rounds)*2)
	for _, r := range f.Rounds {
		if r.WorkTaskID != "" {
			out = append(out, r.WorkTaskID)
		}
		if r.ReviewTaskID != "" {
			out = append(out, r.ReviewTaskID)
		}
	}
	return out
}

// isZeroOpts reports whether o carries no instruction at all, which is what
// makes ReviewOpts fall back to Opts. reflect rather than == because
// TaskOptions holds slices and is therefore not comparable; a hand-written
// field-by-field check is the thing that silently stops being true when
// queue.TaskOptions gains a field.
func isZeroOpts(o TaskOptions) bool {
	return reflect.DeepEqual(o, TaskOptions{})
}

// lessFlowID orders flow IDs by their numeric suffix so f2 precedes f10,
// which plain string ordering gets wrong. IDs this package did not mint sort
// after the ones it did, by string.
func lessFlowID(a, b string) bool {
	an, bn := flowIDSeq(a), flowIDSeq(b)
	if an != bn && an != 0 && bn != 0 {
		return an < bn
	}
	if (an == 0) != (bn == 0) {
		return bn == 0
	}
	return a < b
}
