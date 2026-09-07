package service

import "context"

// FlowService covers adversarial review flows: repeated implement → review →
// fix rounds over one working directory, driven daemon-side until a reviewer
// accepts the work or the round cap is hit.
//
// It is its own capability rather than extra QueueService methods for the
// reason a flow is not a queue: a flow's round count depends on a verdict
// that does not exist when the work is submitted, so it cannot be expressed
// as a DAG handed over in one call. The flow owns a queue ("flow-<id>") and
// appends one round's two tasks at a time; the queue never learns what a
// flow is.
//
// Nothing here streams, the same argument QueueService makes: a flow
// outlives the connection that started it. Progress reaches every connected
// client as "flow_updated" WS frames carrying the whole flow, plus the
// ordinary "queue_task_changed" frames its tasks keep producing.
//
// The reconciler itself is daemon-only. Start/Stop/Wake/Restore on
// flow.Manager are startup wiring, not operations a client may perform, and
// deliberately do not appear here.
//
// Every method returns ErrUnavailable when the daemon has no flow manager,
// mirroring how QueueService degrades without a queue.
type FlowService interface {
	// Start validates a request and records a new flow, returning it in
	// FlowPending with no rounds: submitting round 1 is the reconciler's
	// job, so the path a restart takes is the path a fresh flow takes. A
	// caller that wants to wait for work to be under way watches the
	// flow's state rather than this call.
	//
	// Returns ErrInvalidRequest for an empty goal, a work_dir that is not
	// an existing directory, a max_rounds outside 1..20, or opts asking
	// for worktree isolation — which a flow refuses rather than silently
	// clears, because every round must see the same tree.
	Start(ctx context.Context, req FlowStartRequest) (*Flow, error)

	// List returns flows oldest first, optionally narrowed to a set of
	// states. An empty states slice means every state; an unknown state is
	// ErrInvalidRequest rather than an empty result, exactly as
	// QueueService.List treats one.
	List(ctx context.Context, states []FlowState) ([]Flow, error)

	// Get returns one flow by ID, rounds and verdicts included, or
	// ErrNotFound.
	Get(ctx context.Context, flowID string) (*Flow, error)

	// Tree returns the flow's flat, parent-linked node list: the root, one
	// node per round, and one node per step carrying its task's live state
	// and the agent that ran it. Separate from Get because a step's state
	// is deliberately not stored on the round — it is read from the queue
	// on demand, so a task an operator cancelled or retried behind the
	// flow's back shows up as what it is.
	//
	// Returns ErrNotFound for an unknown flowID.
	Tree(ctx context.Context, flowID string) (*FlowTree, error)

	// Cancel marks the flow cancelled and stops the tasks it created, one
	// recorded task ID at a time — never the whole queue, which may hold
	// tasks the flow did not create. Idempotent: cancelling an already
	// terminal flow succeeds. Returns ErrNotFound for an unknown flowID.
	Cancel(ctx context.Context, flowID string) error

	// Remove deletes a flow's record and its verdict directory, leaving the
	// underlying queue alone (`queue remove` owns that). Refused with
	// ErrConflict while the flow is non-terminal — a running flow's record
	// is the only index of its tasks, so dropping it orphans them. Cancel
	// first. Returns ErrNotFound for an unknown flowID.
	Remove(ctx context.Context, flowID string) error
}
