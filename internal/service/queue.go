package service

import "context"

// QueueService covers the daemon-side task queue: submitting dependency-
// ordered agent work, inspecting it, and steering it. It exists as its own
// capability rather than as extra AgentService methods because the two have
// opposite lifetimes — an agent is a live subprocess the caller babysits,
// while a queued task outlives the connection that submitted it.
//
// That difference is also why nothing here streams. The queue's progress is
// broadcast to every connected client as "queue_task_changed" WS frames (the
// daemon wires Manager.OnChange to them), so a view renders live state
// without holding a per-caller subscription open — which is exactly what a
// caller that submits a DAG and disconnects needs.
//
// The scheduler itself is daemon-only: Start/Stop/Wake/Restore and the
// engine observer hook do NOT appear here. They are startup wiring, not
// operations a client may perform.
type QueueService interface {
	// Add submits a batch of tasks as one atomic unit: either every spec is
	// accepted or none is. Specs may reference each other by their
	// batch-local Name in After, which is what lets a caller declare a
	// whole DAG in one round trip instead of chaining calls through
	// assigned IDs. Specs with no QueueID land in a queue created for the
	// batch. Returns ErrInvalidRequest for an unknown dependency, a cycle,
	// or a missing prompt/work dir.
	Add(ctx context.Context, specs []TaskSpec) ([]Task, error)

	// List returns tasks, optionally narrowed to one queue and to a set of
	// states. An empty queueID means every queue; an empty states slice
	// means every state. Returns ErrNotFound for an unknown queueID and
	// ErrInvalidRequest for an unknown state.
	List(ctx context.Context, queueID string, states []TaskState) ([]Task, error)

	// Get returns one task by ID or ErrNotFound.
	Get(ctx context.Context, taskID string) (*Task, error)

	// Queues returns a summary of every queue, newest first. Cheap enough
	// to poll: the counts are tallied in memory, not from disk.
	Queues(ctx context.Context) ([]QueueInfo, error)

	// QueueInfo returns the summary for one queue or ErrNotFound.
	QueueInfo(ctx context.Context, queueID string) (*QueueInfo, error)

	// Graph returns one queue's dependency DAG, topologically ordered where
	// possible. Separate from List because rendering the shape of a queue
	// needs edges, and carrying them on every task listing would repeat the
	// same data once per node.
	Graph(ctx context.Context, queueID string) (*QueueGraph, error)

	// Cancel stops one task: a running task's agent is killed, a pending
	// one simply never starts. Dependents follow the task's OnFailure
	// policy. Idempotent — cancelling an already-finished task succeeds.
	Cancel(ctx context.Context, taskID string) error

	// CancelQueue cancels every unfinished task in a queue.
	CancelQueue(ctx context.Context, queueID string) error

	// Retry puts a failed, cancelled or skipped task back in line with a
	// fresh retry budget, and un-skips everything that was skipped because
	// of it. Returns ErrConflict when the task is in any other state.
	Retry(ctx context.Context, taskID string) error

	// Answer delivers the operator's reply to a task whose agent stopped to
	// ask a question; the agent resumes. Returns ErrConflict when the task
	// is not waiting for input.
	//
	// Inert in this build: the agent engine cannot report that an agent
	// stopped to ask something, so no task reaches queue.StateWaitingHuman
	// and every call returns ErrConflict. Callers must not branch on it.
	Answer(ctx context.Context, taskID, message string) error

	// Pause stops a queue from dispatching anything new. Tasks already
	// running are left alone: pausing is about not starting more work, not
	// about interrupting work in flight.
	Pause(ctx context.Context, queueID string) error

	// Resume lifts a pause.
	Resume(ctx context.Context, queueID string) error

	// RemoveQueue forgets a queue entirely and deletes its persisted state.
	// Refused with ErrInvalidRequest while any task is still active: a
	// running agent would keep reporting into a queue that no longer
	// exists. Cancel or wait first. Returns ErrNotFound for an unknown
	// queueID.
	RemoveQueue(ctx context.Context, queueID string) error
}
