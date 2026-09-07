//go:build integration

package integration

import (
	"context"
	"fmt"
	"sync"

	"gitlab.com/tanevanwifferen1/singularity/internal/queue"
)

// scriptedRunner is a queue.AgentRunner that never forks anything. It exists
// so the integration daemon can carry a *real* queue.Manager: the wire path
// under test is client -> HTTP handler -> service -> manager, and spawning
// actual agent subprocesses to exercise it would make the test slow, need a
// backend, and prove nothing extra.
//
// A dispatched task stays in the running state until the test calls
// complete, so dependency ordering is observable rather than raced: while a
// task is unfinished its dependents must stay blocked, and that is the
// assertion.
type scriptedRunner struct {
	mu     sync.Mutex
	seq    int
	max    int
	states map[string]string // agentID -> engine state name
	byTask map[string]string // taskID  -> agentID
	order  []string          // task IDs in dispatch order
	// notify wakes the scheduler so a released task advances immediately
	// instead of waiting out the fallback tick.
	notify func()
}

func newScriptedRunner(max int) *scriptedRunner {
	return &scriptedRunner{
		max:    max,
		states: map[string]string{},
		byTask: map[string]string{},
	}
}

func (r *scriptedRunner) StartTask(ctx context.Context, t queue.Task) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("queue is shutting down: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.max > 0 && r.activeLocked() >= r.max {
		return "", fmt.Errorf("%w: agent limit reached", queue.ErrNoCapacity)
	}
	r.seq++
	id := fmt.Sprintf("a%d", r.seq)
	r.states[id] = "running"
	r.byTask[t.ID] = id
	r.order = append(r.order, t.ID)
	return id, nil
}

func (r *scriptedRunner) AgentState(agentID string) (string, string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.states[agentID]
	return st, "", ok
}

func (r *scriptedRunner) Capacity() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.activeLocked(), r.max
}

func (r *scriptedRunner) activeLocked() int {
	n := 0
	for _, st := range r.states {
		switch st {
		case "complete", "error", "killed":
		default:
			n++
		}
	}
	return n
}

// WorkDirBusy always reports free: the integration DAGs share one fixture
// repo on purpose, and serialising them on the directory would hide the
// dependency ordering the tests are there to observe.
func (r *scriptedRunner) WorkDirBusy(string) bool { return false }

func (r *scriptedRunner) SendInput(string, string) error { return nil }

func (r *scriptedRunner) KillAgent(agentID string) error {
	r.mu.Lock()
	r.states[agentID] = "killed"
	r.mu.Unlock()
	if r.notify != nil {
		r.notify()
	}
	return nil
}

// dispatchOrder snapshots the task IDs in the order the scheduler started
// them.
func (r *scriptedRunner) dispatchOrder() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

// started reports whether the scheduler has dispatched taskID.
func (r *scriptedRunner) started(taskID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.byTask[taskID]
	return ok
}

// complete finishes a dispatched task's agent, unblocking its dependents.
func (r *scriptedRunner) complete(taskID string) {
	r.mu.Lock()
	if id, ok := r.byTask[taskID]; ok {
		r.states[id] = "complete"
	}
	r.mu.Unlock()
	if r.notify != nil {
		r.notify()
	}
}

// releaseAll completes everything still in flight. Called on teardown so a
// held task cannot keep the scheduler busy while the daemon shuts down.
func (r *scriptedRunner) releaseAll() {
	r.mu.Lock()
	for _, id := range r.byTask {
		if st := r.states[id]; st == "running" {
			r.states[id] = "complete"
		}
	}
	r.mu.Unlock()
	if r.notify != nil {
		r.notify()
	}
}

// compile-time check that the fake still satisfies the interface the real
// EngineRunner implements.
var _ queue.AgentRunner = (*scriptedRunner)(nil)
