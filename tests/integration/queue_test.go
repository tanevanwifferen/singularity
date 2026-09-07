//go:build integration

package integration

import (
	"errors"
	"testing"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// waitForTaskState polls the daemon over HTTP until taskID reports want.
// Polling (rather than a WS subscription) is deliberate: it is the same
// thing `singl queue wait` does, so the read path under test is the one an
// orchestrating agent actually uses.
func waitForTaskState(t *testing.T, d *testDaemon, taskID string, want api.TaskState) api.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last api.TaskState
	for time.Now().Before(deadline) {
		ctx, cancel := shortCtx(t)
		task, err := d.Client.QueueGet(ctx, taskID)
		cancel()
		if err != nil {
			t.Fatalf("QueueGet(%s): %v", taskID, err)
		}
		if task.State == want {
			return *task
		}
		last = task.State
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s never reached %s (last seen %s)", taskID, want, last)
	return api.Task{}
}

// assertTaskState reads one task over the wire and checks its state now.
func assertTaskState(t *testing.T, d *testDaemon, taskID string, want api.TaskState) {
	t.Helper()
	ctx, cancel := shortCtx(t)
	defer cancel()
	task, err := d.Client.QueueGet(ctx, taskID)
	if err != nil {
		t.Fatalf("QueueGet(%s): %v", taskID, err)
	}
	if task.State != want {
		t.Errorf("task %s state = %s, want %s", taskID, task.State, want)
	}
}

// TestQueueDAGRoundTrip submits a diamond DAG through the remote client and
// asserts the dependency ordering the daemon reports back.
//
// This is the join the suite was missing. internal/client/queue_test.go
// drives the client against a hand-written HTTP stub and
// internal/server/queue_handlers_test.go drives the handlers against a fake
// service; neither notices if the two envelopes stop agreeing — handleQueueGet
// writes a bare task where every sibling route wraps its payload, and only a
// real round trip pins that.
func TestQueueDAGRoundTrip(t *testing.T) {
	d := startTestDaemon(t)
	repo := repoFixture(t)

	ctx, cancel := shortCtx(t)
	defer cancel()

	// The two middle tasks are concurrent, so they need a directory each:
	// one agent per working directory is enforced all the way through the
	// daemon, and two siblings sharing a path would serialise rather than
	// run together.
	leftRepo, rightRepo := repoFixture(t), repoFixture(t)

	added, err := d.Client.QueueAdd(ctx, []api.TaskSpec{
		{Name: "root", Title: "root", Prompt: "p", WorkDir: repo},
		{Name: "left", Title: "left", Prompt: "p", WorkDir: leftRepo, After: []string{"root"}},
		{Name: "right", Title: "right", Prompt: "p", WorkDir: rightRepo, After: []string{"root"}},
		{Name: "join", Title: "join", Prompt: "p", WorkDir: repo, After: []string{"left", "right"}},
	})
	if err != nil {
		t.Fatalf("QueueAdd: %v", err)
	}
	if len(added) != 4 {
		t.Fatalf("QueueAdd returned %d tasks, want 4", len(added))
	}
	root, left, right, join := added[0].ID, added[1].ID, added[2].ID, added[3].ID
	queueID := added[0].QueueID
	if queueID == "" {
		t.Fatal("QueueAdd returned a task with no queue ID")
	}

	// Level 0: only the root may run; everything downstream stays blocked
	// for as long as it is unfinished.
	waitForTaskState(t, d, root, service.TaskRunning)
	for _, id := range []string{left, right, join} {
		assertTaskState(t, d, id, service.TaskBlocked)
	}

	// Level 1: finishing the root releases both of its dependents, and
	// only those — the join still has an unfinished parent.
	d.Runner.complete(root)
	waitForTaskState(t, d, left, service.TaskRunning)
	waitForTaskState(t, d, right, service.TaskRunning)
	assertTaskState(t, d, root, service.TaskDone)
	assertTaskState(t, d, join, service.TaskBlocked)

	// Level 2: one parent is not enough for a join.
	d.Runner.complete(left)
	waitForTaskState(t, d, left, service.TaskDone)
	assertTaskState(t, d, join, service.TaskBlocked)

	d.Runner.complete(right)
	waitForTaskState(t, d, join, service.TaskRunning)
	d.Runner.complete(join)
	waitForTaskState(t, d, join, service.TaskDone)

	// The order the scheduler actually dispatched in must match the order
	// the daemon reported. left/right are concurrent, so only their
	// position relative to root and join is defined.
	order := d.Runner.dispatchOrder()
	if len(order) != 4 {
		t.Fatalf("dispatch order = %v, want 4 entries", order)
	}
	if order[0] != root {
		t.Errorf("dispatch order = %v, want the root first", order)
	}
	if order[3] != join {
		t.Errorf("dispatch order = %v, want the join last", order)
	}

	// The state filter's wire spelling: `?state=done` has to round-trip
	// through the query string, the handler's parser and the manager.
	done, err := d.Client.QueueList(ctx, queueID, []api.TaskState{service.TaskDone})
	if err != nil {
		t.Fatalf("QueueList: %v", err)
	}
	if len(done) != 4 {
		t.Errorf("QueueList(state=done) returned %d tasks, want 4", len(done))
	}

	// The graph route, over the same wire.
	graph, err := d.Client.QueueGraph(ctx, queueID)
	if err != nil {
		t.Fatalf("QueueGraph: %v", err)
	}
	if len(graph.Nodes) != 4 {
		t.Errorf("graph has %d nodes, want 4", len(graph.Nodes))
	}

	// And the tallies, which the CLI's `queue wait` decides on.
	info, err := d.Client.QueueInfo(ctx, queueID)
	if err != nil {
		t.Fatalf("QueueInfo: %v", err)
	}
	if info.Done != 4 || info.Total != 4 {
		t.Errorf("queue info done/total = %d/%d, want 4/4", info.Done, info.Total)
	}
}

// TestQueueSerialisesTasksSharingAWorkDir pins the one-agent-per-directory
// invariant end to end, and that a cancel really releases the directory
// rather than only marking the task terminal. Two independent tasks, one
// path: the second must wait, and it must be waiting on the agent, not on a
// dependency it does not have.
func TestQueueSerialisesTasksSharingAWorkDir(t *testing.T) {
	d := startTestDaemon(t)
	repo := repoFixture(t)

	ctx, cancel := shortCtx(t)
	defer cancel()

	added, err := d.Client.QueueAdd(ctx, []api.TaskSpec{
		{Name: "first", Prompt: "p", WorkDir: repo},
		{Name: "second", Prompt: "p", WorkDir: repo},
	})
	if err != nil {
		t.Fatalf("QueueAdd: %v", err)
	}
	first, second := added[0].ID, added[1].ID

	waitForTaskState(t, d, first, service.TaskRunning)
	assertTaskState(t, d, second, service.TaskReady)
	if d.Runner.started(second) {
		t.Fatal("the second task was dispatched into a directory another agent holds")
	}

	// Cancelling the first task has to terminate its agent, which is what
	// frees the directory. A soft close would leave the process there and
	// the queue would dispatch on top of it.
	if err := d.Client.QueueCancel(ctx, first); err != nil {
		t.Fatalf("QueueCancel: %v", err)
	}
	waitForTaskState(t, d, first, service.TaskCancelled)
	waitForTaskState(t, d, second, service.TaskRunning)
}

// TestQueueCancelSkipsDependentsOverTheWire pins the other half of the
// dependency contract end to end: cancelling a task that others depend on
// settles them to skipped, and that transition has to be visible through
// the client rather than only in the manager's own tests.
func TestQueueCancelSkipsDependentsOverTheWire(t *testing.T) {
	d := startTestDaemon(t)
	repo := repoFixture(t)

	ctx, cancel := shortCtx(t)
	defer cancel()

	added, err := d.Client.QueueAdd(ctx, []api.TaskSpec{
		{Name: "root", Prompt: "p", WorkDir: repo},
		{Name: "leaf", Prompt: "p", WorkDir: repo, After: []string{"root"}},
	})
	if err != nil {
		t.Fatalf("QueueAdd: %v", err)
	}
	root, leaf := added[0].ID, added[1].ID

	waitForTaskState(t, d, root, service.TaskRunning)
	assertTaskState(t, d, leaf, service.TaskBlocked)

	if err := d.Client.QueueCancel(ctx, root); err != nil {
		t.Fatalf("QueueCancel: %v", err)
	}
	assertTaskState(t, d, root, service.TaskCancelled)
	// on_failure defaults to block, so the dependent is skipped rather than
	// left blocked forever.
	waitForTaskState(t, d, leaf, service.TaskSkipped)

	// A skipped task is retryable, and the retry has to come back over the
	// wire as a real state change too.
	if err := d.Client.QueueRetry(ctx, root); err != nil {
		t.Fatalf("QueueRetry: %v", err)
	}
	waitForTaskState(t, d, root, service.TaskRunning)
	waitForTaskState(t, d, leaf, service.TaskBlocked)
}

// TestQueueErrorsRoundTripAsSentinels: the queue's error sentinels have to
// survive the trip through codeForServiceErr and back through mapError, or
// a client cannot tell a bad request from a missing task.
func TestQueueErrorsRoundTripAsSentinels(t *testing.T) {
	d := startTestDaemon(t)

	ctx, cancel := shortCtx(t)
	defer cancel()

	if _, err := d.Client.QueueGet(ctx, "nope"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("QueueGet(unknown): got %v, want ErrNotFound", err)
	}
	if err := d.Client.QueueCancel(ctx, "nope"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("QueueCancel(unknown): got %v, want ErrNotFound", err)
	}
	if _, err := d.Client.QueueAdd(ctx, []api.TaskSpec{{Prompt: "", WorkDir: ""}}); !errors.Is(err, service.ErrInvalidRequest) {
		t.Errorf("QueueAdd(empty spec): got %v, want ErrInvalidRequest", err)
	}
	// Answer is wired but inert — no task reaches waiting_human in this
	// build — so it must report a conflict rather than appearing to work.
	added, err := d.Client.QueueAdd(ctx, []api.TaskSpec{{Prompt: "p", WorkDir: repoFixture(t)}})
	if err != nil {
		t.Fatalf("QueueAdd: %v", err)
	}
	if err := d.Client.QueueAnswer(ctx, added[0].ID, "hello"); !errors.Is(err, service.ErrConflict) {
		t.Errorf("QueueAnswer: got %v, want ErrConflict — the verb is inert in this build", err)
	}
}
