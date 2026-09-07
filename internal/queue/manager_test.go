package queue

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeRunner is an in-memory AgentRunner. Tests drive agent lifecycles by
// calling setState, so no subprocess is ever spawned.
type fakeRunner struct {
	mu      sync.Mutex
	states  map[string]string // agentID -> state name
	errs    map[string]string
	byTask  map[string]string // taskID -> agentID
	started []Task
	// alive models the OS process behind each agent the fake started,
	// separately from the state the engine reports. The two really do come
	// apart: engine.KillAgent soft-closes, moving the state to killed while
	// the subprocess keeps running (and keeps editing its working
	// directory). alive is what a test checks to assert a process was
	// genuinely ended (killedIDs, TerminateAgent) — it is deliberately NOT
	// what WorkDirBusy answers from, because production's WorkDirBusy does
	// not know it either: it derives busy from ActiveAgents, i.e. from
	// state, so it goes false the instant softClose runs even though the
	// process above is still very much alive. A fake that answered
	// WorkDirBusy from `alive` would be safer than production and could
	// never reproduce that gap — which is exactly why it survived five
	// review cycles at this level.
	alive map[string]bool
	// agentDirs and agentWorktree record where each started agent works, so
	// WorkDirBusy answers from the fake's own agents' state the way the
	// engine answers from ActiveAgents.
	agentDirs     map[string]string
	agentWorktree map[string]bool
	// busyDirs marks directories occupied by an agent the queue did not
	// start — the one case the fake cannot derive from its own state.
	busyDirs  map[string]bool
	maxAgents int
	startErr  error
	seq       int
	// beforeStart, when set, runs at the top of StartTask without f.mu
	// held, so a test can act on the manager while a spawn is in flight.
	beforeStart func(Task)
	// capacityActive, when non-nil, overrides what Capacity reports while
	// leaving StartTask's own cap check on the honest count. That is the
	// engine's real bug shape: it gates on one number and reports another.
	capacityActive *int
	inputs         []string
	killed         []string
	// refused records the tasks whose spawn was declined because the
	// manager's run context was already cancelled.
	refused []string
	// spawnCtx is the context of the most recent StartTask call, kept so a
	// test can assert dispatch handed over the manager's cancellable one
	// and not context.Background().
	spawnCtx context.Context
}

func newFakeRunner(max int) *fakeRunner {
	return &fakeRunner{
		states:        map[string]string{},
		errs:          map[string]string{},
		byTask:        map[string]string{},
		alive:         map[string]bool{},
		agentDirs:     map[string]string{},
		agentWorktree: map[string]bool{},
		busyDirs:      map[string]bool{},
		maxAgents:     max,
	}
}

func (f *fakeRunner) StartTask(ctx context.Context, t Task) (string, error) {
	// Checked first, exactly like EngineRunner: cancellation is honoured by
	// refusing to begin a spawn, never by abandoning one. beforeStart is
	// therefore work that is already past the point of no return, which is
	// what makes it a faithful stand-in for a slow `git worktree add`.
	f.mu.Lock()
	f.spawnCtx = ctx
	f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		f.mu.Lock()
		f.refused = append(f.refused, t.ID)
		f.mu.Unlock()
		return "", fmt.Errorf("queue is shutting down: %w", err)
	}
	if f.beforeStart != nil {
		f.beforeStart(t)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return "", f.startErr
	}
	// Enforce the cap the way the real engine does, counting the same
	// agents Capacity reports. Without this the fake is more permissive
	// than the engine and the whole class of "the scheduler claimed a slot
	// the engine then refused" defects cannot be reproduced.
	if f.maxAgents > 0 && f.activeLocked() >= f.maxAgents {
		return "", fmt.Errorf("%w: agent limit reached (%d/%d active)", ErrNoCapacity, f.activeLocked(), f.maxAgents)
	}
	f.seq++
	id := fmt.Sprintf("a%d", f.seq)
	f.states[id] = "running"
	f.alive[id] = true
	f.agentDirs[id] = filepath.Clean(t.WorkDir)
	f.agentWorktree[id] = t.Opts.UseWorktree
	f.byTask[t.ID] = id
	f.started = append(f.started, t)
	return id, nil
}

func (f *fakeRunner) AgentState(agentID string) (string, string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.states[agentID]
	if !ok {
		return "", "", false
	}
	return st, f.errs[agentID], true
}

func (f *fakeRunner) Capacity() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.capacityActive != nil {
		return *f.capacityActive, f.maxAgents
	}
	return f.activeLocked(), f.maxAgents
}

// activeLocked is the one active-agent count the fake has, shared by
// Capacity and StartTask's cap check. The real engine's divergence between
// those two counts is finding 1; the fake must not be able to hide it.
func (f *fakeRunner) activeLocked() int {
	active := 0
	for _, st := range f.states {
		switch st {
		case "complete", "error", "killed":
		default:
			active++
		}
	}
	return active
}

// WorkDirBusy answers the way EngineRunner's production implementation does:
// from the agent's reported STATE (busy unless complete/error/killed), not
// from whether its process is actually still alive. Those two really do come
// apart — engine.KillAgent soft-closes, moving the state to killed while the
// subprocess keeps running and keeps editing its directory — and a fake that
// answers from `alive` instead reports the safe (wrong) answer, which is
// exactly how this class of defect survived five review cycles at the queue
// level: the fake never disagreed with what the scheduler assumed. Worktree-
// isolated agents never match, because the engine rewrites their WorkDir to
// the private worktree it created for them.
func (f *fakeRunner) WorkDirBusy(dir string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	want := filepath.Clean(dir)
	if f.busyDirs[want] {
		return true
	}
	for id, agentDir := range f.agentDirs {
		if f.agentWorktree[id] || agentDir != want {
			continue
		}
		switch f.states[id] {
		case "complete", "error", "killed":
			continue
		}
		return true
	}
	return false
}

func (f *fakeRunner) SendInput(agentID, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.states[agentID]; !ok {
		return errors.New("no such agent")
	}
	f.inputs = append(f.inputs, agentID+":"+message)
	f.states[agentID] = "running"
	return nil
}

// TerminateAgent models the contract AgentRunner documents: the process is
// gone when it returns, so the directory is free again.
func (f *fakeRunner) TerminateAgent(agentID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed = append(f.killed, agentID)
	f.states[agentID] = "killed"
	f.alive[agentID] = false
	return nil
}

// softClose models engine.KillAgent — the TUI's kill — for the tests that
// need an agent whose record says killed while its process is still there.
func (f *fakeRunner) softClose(agentID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[agentID] = "killed"
}

// removeAgent models `agents remove`: the record is gone and so is the
// process.
func (f *fakeRunner) removeAgent(agentID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.states, agentID)
	delete(f.alive, agentID)
	delete(f.agentDirs, agentID)
}

// setTaskState marks the agent dispatched for taskID as being in state.
func (f *fakeRunner) setTaskState(t *testing.T, taskID, state, errText string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.byTask[taskID]
	if !ok {
		t.Fatalf("no agent dispatched for task %s", taskID)
	}
	f.states[id] = state
	f.errs[id] = errText
	switch state {
	case "complete", "error":
		// The engine only reports these once cmd.Wait returned, so the
		// process really has exited and the directory really is free.
		f.alive[id] = false
	}
}

func (f *fakeRunner) startedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.started))
	for _, t := range f.started {
		out = append(out, t.ID)
	}
	return out
}

// newTestManager builds a Manager with a no-op store and a synchronous
// tick (the scheduler goroutine is not started; tests call tick directly so
// there is nothing to race against).
func newTestManager(runner AgentRunner) *Manager {
	return NewManager(runner, &Store{})
}

func mustAdd(t *testing.T, m *Manager, specs []TaskSpec) []Task {
	t.Helper()
	tasks, err := m.Add(specs)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return tasks
}

func stateOf(t *testing.T, m *Manager, id string) State {
	t.Helper()
	task, err := m.Get(id)
	if err != nil {
		t.Fatalf("Get(%s): %v", id, err)
	}
	return task.State
}

func TestAddResolvesLocalNamesIntoDependencies(t *testing.T) {
	m := newTestManager(newFakeRunner(4))
	tasks := mustAdd(t, m, []TaskSpec{
		{Name: "impl", Prompt: "p", WorkDir: "/w"},
		{Name: "review", Prompt: "p", WorkDir: "/w", After: []string{"impl"}},
		{Name: "fix", Prompt: "p", WorkDir: "/w", After: []string{"review"}},
	})
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}
	if got := tasks[1].DependsOn; len(got) != 1 || got[0] != tasks[0].ID {
		t.Errorf("review deps = %v, want [%s]", got, tasks[0].ID)
	}
	if got := tasks[2].DependsOn; len(got) != 1 || got[0] != tasks[1].ID {
		t.Errorf("fix deps = %v, want [%s]", got, tasks[1].ID)
	}
	// The head of the chain is immediately dispatchable; the rest are not.
	if tasks[0].State != StateReady {
		t.Errorf("impl state = %s, want ready", tasks[0].State)
	}
	if tasks[1].State != StateBlocked || tasks[2].State != StateBlocked {
		t.Errorf("downstream states = %s/%s, want blocked", tasks[1].State, tasks[2].State)
	}
	// All three share the batch's auto-created queue.
	if tasks[0].QueueID == "" || tasks[1].QueueID != tasks[0].QueueID || tasks[2].QueueID != tasks[0].QueueID {
		t.Errorf("expected one shared queue, got %q/%q/%q", tasks[0].QueueID, tasks[1].QueueID, tasks[2].QueueID)
	}
}

func TestAddRejectsCycle(t *testing.T) {
	m := newTestManager(newFakeRunner(4))
	// A batch cannot express a cycle via local names alone unless two specs
	// reference each other, which is exactly what this checks.
	_, err := m.Add([]TaskSpec{
		{Name: "a", Prompt: "p", WorkDir: "/w", After: []string{"b"}},
		{Name: "b", Prompt: "p", WorkDir: "/w", After: []string{"a"}},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for a cycle, got %v", err)
	}
}

func TestAddRejectsCycleAgainstExistingTasks(t *testing.T) {
	m := newTestManager(newFakeRunner(4))
	first := mustAdd(t, m, []TaskSpec{{Name: "a", Prompt: "p", WorkDir: "/w"}})
	// Make the existing task depend on the new one, and the new one on it.
	m.mu.Lock()
	m.tasks[first[0].ID].DependsOn = []string{"t2"}
	m.mu.Unlock()
	_, err := m.Add([]TaskSpec{{Name: "b", Prompt: "p", WorkDir: "/w", After: []string{first[0].ID}}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}

func TestAddRejectsUnknownDependency(t *testing.T) {
	m := newTestManager(newFakeRunner(4))
	_, err := m.Add([]TaskSpec{{Name: "a", Prompt: "p", WorkDir: "/w", After: []string{"nope"}}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}

// TestAddRejectsCaseInsensitiveQueueIDCollision is finding 4: Store.path
// derives a queue's state filename from the raw ID, and idPattern accepts
// both cases, so "Prod" and "prod" are two queues in m.queues but one file
// on a case-insensitive filesystem (macOS/APFS) — whichever queue flushes
// last silently overwrites the other's tasks on disk.
func TestAddRejectsCaseInsensitiveQueueIDCollision(t *testing.T) {
	m := newTestManager(newFakeRunner(4))
	if _, err := m.Add([]TaskSpec{{Name: "a", Prompt: "p", WorkDir: "/w", QueueID: "Prod"}}); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	_, err := m.Add([]TaskSpec{{Name: "b", Prompt: "p", WorkDir: "/w", QueueID: "prod"}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for a case-folded queue id collision, got %v", err)
	}
}

// TestAddRejectsCaseInsensitiveQueueIDCollisionWithinOneBatch covers the
// half finding 4's fix would otherwise miss: two specs in the very same
// batch that both mint a new, colliding queue id before either has landed
// in m.queues.
func TestAddRejectsCaseInsensitiveQueueIDCollisionWithinOneBatch(t *testing.T) {
	m := newTestManager(newFakeRunner(4))
	_, err := m.Add([]TaskSpec{
		{Name: "a", Prompt: "p", WorkDir: "/w", QueueID: "Prod"},
		{Name: "b", Prompt: "p", WorkDir: "/w", QueueID: "prod"},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for a same-batch queue id collision, got %v", err)
	}
	if _, ok := m.tasks["t1"]; ok {
		t.Fatal("Add must be atomic: a rejected batch must not leave task t1 committed")
	}
}

func TestAddIsAtomic(t *testing.T) {
	m := newTestManager(newFakeRunner(4))
	_, err := m.Add([]TaskSpec{
		{Name: "ok", Prompt: "p", WorkDir: "/w"},
		{Name: "bad", Prompt: "", WorkDir: "/w"},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
	if got, _ := m.List("", nil); len(got) != 0 {
		t.Fatalf("a rejected batch must add nothing, found %d tasks", len(got))
	}
}

func TestChainAdvancesOneLinkPerCompletion(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{
		{Name: "impl", Prompt: "p", WorkDir: "/w1"},
		{Name: "review", Prompt: "p", WorkDir: "/w2", After: []string{"impl"}},
	})
	impl, review := tasks[0].ID, tasks[1].ID

	m.tick()
	if got := stateOf(t, m, impl); got != StateRunning {
		t.Fatalf("impl state = %s, want running", got)
	}
	if got := stateOf(t, m, review); got != StateBlocked {
		t.Fatalf("review state = %s, want blocked", got)
	}
	if ids := runner.startedIDs(); len(ids) != 1 || ids[0] != impl {
		t.Fatalf("started = %v, want only %s", ids, impl)
	}

	runner.setTaskState(t, impl, "complete", "")
	m.tick()

	if got := stateOf(t, m, impl); got != StateDone {
		t.Fatalf("impl state = %s, want done", got)
	}
	// Reconcile-then-dispatch in one tick means the dependent starts
	// immediately rather than waiting for the next interval.
	if got := stateOf(t, m, review); got != StateRunning {
		t.Fatalf("review state = %s, want running in the same tick", got)
	}
}

func TestFailureBlocksDependentsBySkipping(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{
		{Name: "a", Prompt: "p", WorkDir: "/w1"},
		{Name: "b", Prompt: "p", WorkDir: "/w2", After: []string{"a"}},
		{Name: "c", Prompt: "p", WorkDir: "/w3", After: []string{"b"}},
	})

	m.tick()
	runner.setTaskState(t, tasks[0].ID, "error", "boom")
	m.tick()

	if got := stateOf(t, m, tasks[0].ID); got != StateFailed {
		t.Fatalf("a state = %s, want failed", got)
	}
	// The skip must cascade the whole chain, not just the direct dependent.
	if got := stateOf(t, m, tasks[1].ID); got != StateSkipped {
		t.Errorf("b state = %s, want skipped", got)
	}
	if got := stateOf(t, m, tasks[2].ID); got != StateSkipped {
		t.Errorf("c state = %s, want skipped (cascade past b)", got)
	}
}

func TestFailureWithContinuePolicyLetsDependentsRun(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{
		{Name: "a", Prompt: "p", WorkDir: "/w1", OnFailure: FailContinue},
		{Name: "b", Prompt: "p", WorkDir: "/w2", After: []string{"a"}},
	})

	m.tick()
	runner.setTaskState(t, tasks[0].ID, "error", "boom")
	m.tick()

	if got := stateOf(t, m, tasks[1].ID); got != StateRunning {
		t.Fatalf("b state = %s, want running under on_failure=continue", got)
	}
}

func TestAbortQueuePolicyCancelsEverythingPending(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{
		{Name: "a", Prompt: "p", WorkDir: "/w1", OnFailure: FailAbortQueue},
		{Name: "b", Prompt: "p", WorkDir: "/w2"},
		{Name: "c", Prompt: "p", WorkDir: "/w3", After: []string{"a"}},
	})

	m.tick()
	bAgent := runner.agentFor(t, tasks[1].ID)
	runner.setTaskState(t, tasks[0].ID, "error", "boom")
	m.tick()

	if got := stateOf(t, m, tasks[0].ID); got != StateFailed {
		t.Fatalf("a state = %s, want failed", got)
	}
	// b is independent of a, but abort-queue is queue-wide by definition.
	if got := stateOf(t, m, tasks[1].ID); got != StateCancelled {
		t.Errorf("b state = %s, want cancelled", got)
	}
	if got := stateOf(t, m, tasks[2].ID); got != StateCancelled {
		t.Errorf("c state = %s, want cancelled", got)
	}

	// The invariant, not just the call: abortQueueLocked's terminate runs
	// fire-and-forget, so assert on its effect (agent killed, directory
	// freed) rather than racing its goroutine directly. Deleting the
	// TerminateAgent call from abortQueueLocked must fail this.
	waitFor(t, func() bool {
		for _, id := range runner.killedIDs() {
			if id == bAgent {
				return true
			}
		}
		return false
	}, "b's agent to be terminated by the queue abort")
	waitFor(t, func() bool {
		return !runner.WorkDirBusy("/w2")
	}, "/w2 to be released after the queue abort")

	next := mustAdd(t, m, []TaskSpec{{Name: "d", Prompt: "p", WorkDir: "/w2"}})
	m.tick()
	if got := stateOf(t, m, next[0].ID); got != StateRunning {
		t.Fatalf("d state = %s, want running once b's agent released /w2", got)
	}
}

func TestRetryBudgetIsConsumedThenTaskFails(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{{Name: "a", Prompt: "p", WorkDir: "/w", MaxRetries: 1}})
	id := tasks[0].ID

	m.tick()
	runner.setTaskState(t, id, "error", "first")
	m.tick()
	// One retry left, so the task goes back around instead of failing.
	if got := stateOf(t, m, id); got != StateRunning {
		t.Fatalf("after first failure state = %s, want running (retry)", got)
	}

	runner.setTaskState(t, id, "error", "second")
	m.tick()
	if got := stateOf(t, m, id); got != StateFailed {
		t.Fatalf("after second failure state = %s, want failed", got)
	}
	task, _ := m.Get(id)
	if task.Attempts != 2 {
		t.Errorf("attempts = %d, want 2", task.Attempts)
	}
}

func TestSpawnFailureDoesNotLoopForever(t *testing.T) {
	runner := newFakeRunner(4)
	runner.startErr = errors.New("workdir gone")
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{{Name: "a", Prompt: "p", WorkDir: "/gone"}})
	id := tasks[0].ID

	// Several ticks must not keep re-attempting a task with no retry budget.
	for i := 0; i < 5; i++ {
		m.tick()
	}
	if got := stateOf(t, m, id); got != StateFailed {
		t.Fatalf("state = %s, want failed", got)
	}
	task, _ := m.Get(id)
	if task.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 — a refunded attempt would spin", task.Attempts)
	}
}

func TestCapacityHoldsTasksReadyInsteadOfFailing(t *testing.T) {
	runner := newFakeRunner(1)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{
		{Name: "a", Prompt: "p", WorkDir: "/w1"},
		{Name: "b", Prompt: "p", WorkDir: "/w2"},
	})

	m.tick()
	running, ready := 0, 0
	for _, id := range []string{tasks[0].ID, tasks[1].ID} {
		switch stateOf(t, m, id) {
		case StateRunning:
			running++
		case StateReady:
			ready++
		}
	}
	if running != 1 || ready != 1 {
		t.Fatalf("running=%d ready=%d, want exactly one of each at max_agents=1", running, ready)
	}

	// Freeing the slot lets the queued task through — backpressure, not error.
	runner.setTaskState(t, tasks[0].ID, "complete", "")
	m.tick()
	if got := stateOf(t, m, tasks[1].ID); got != StateRunning {
		t.Fatalf("b state = %s, want running once a slot freed", got)
	}
}

func TestWorkDirBusyBlocksSecondTaskInSameDirectory(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{
		{Name: "a", Prompt: "p", WorkDir: "/shared"},
		{Name: "b", Prompt: "p", WorkDir: "/shared"},
	})

	m.tick()
	running := 0
	for _, id := range []string{tasks[0].ID, tasks[1].ID} {
		if stateOf(t, m, id) == StateRunning {
			running++
		}
	}
	if running != 1 {
		t.Fatalf("running=%d, want 1 — two agents must never share a workdir", running)
	}
}

// TestRunningAgentBlocksItsDirectoryAcrossTicks is the invariant a
// hand-set busyDirs map could never test: the agent the runner itself
// started has to keep its directory occupied on later ticks, not just
// within the tick that claimed it.
func TestRunningAgentBlocksItsDirectoryAcrossTicks(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	first := mustAdd(t, m, []TaskSpec{{Name: "a", Prompt: "p", WorkDir: "/shared"}})
	m.tick()
	if got := stateOf(t, m, first[0].ID); got != StateRunning {
		t.Fatalf("first task state = %s, want running", got)
	}

	// Submitted after the dispatch, so the in-tick claimedDirs set cannot
	// be what stops it: only the live agent can.
	second := mustAdd(t, m, []TaskSpec{{Name: "b", Prompt: "p", WorkDir: "/shared/"}})
	m.tick()
	if got := stateOf(t, m, second[0].ID); got != StateReady {
		t.Fatalf("second task state = %s, want ready while an agent holds /shared", got)
	}

	// And it goes as soon as the directory is genuinely free.
	runner.setTaskState(t, first[0].ID, "complete", "")
	m.tick()
	if got := stateOf(t, m, second[0].ID); got != StateRunning {
		t.Fatalf("second task state = %s, want running once the directory freed", got)
	}
}

// TestCancelledTaskReleasesItsWorkingDirectory is finding 1 as a test: a
// cancel has to end the agent's process, because the scheduler treats the
// directory as free the moment the agent stops counting as active. Against
// a soft-closing runner the second task dispatches into a directory the
// first agent is still editing.
func TestCancelledTaskReleasesItsWorkingDirectory(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{{Name: "a", Prompt: "p", WorkDir: "/shared"}})
	m.tick()
	agentID := runner.agentFor(t, tasks[0].ID)

	if err := m.Cancel(tasks[0].ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if runner.WorkDirBusy("/shared") {
		t.Fatal("/shared is still occupied after the cancel: the agent's process outlived it, so the next dispatch collides with it")
	}
	if killed := runner.killedIDs(); len(killed) != 1 || killed[0] != agentID {
		t.Errorf("terminated = %v, want [%s]", killed, agentID)
	}

	// Only now is a second task on the same directory safe.
	second := mustAdd(t, m, []TaskSpec{{Name: "b", Prompt: "p", WorkDir: "/shared"}})
	m.tick()
	if got := stateOf(t, m, second[0].ID); got != StateRunning {
		t.Fatalf("second task state = %s, want running once the cancelled agent is gone", got)
	}
}

// TestSoftClosedAgentDoesNotHoldItsDirectory pins the actual engine
// behaviour (review cycle 5, finding 3): WorkDirBusy is derived from state,
// not from whether the process is alive, so the moment engine.KillAgent
// soft-closes an agent, WorkDirBusy reports its directory free — while the
// subprocess above keeps editing it. An earlier version of this test
// asserted the opposite, on the assumption that "still holds its directory"
// was what the fake ought to model; it was not what production does, and a
// fake asserting the safer, wrong answer is why this whole class of defect
// went unfalsifiable at the queue level for five cycles. The trap this
// leaves — the scheduler has no way to see the directory is still occupied —
// is exactly why reconcile has to terminate a killed agent itself rather
// than rely on the accounting to catch up (TestReconcileTerminatesAKilledAgentBeforeDispatch).
func TestSoftClosedAgentDoesNotHoldItsDirectory(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{{Name: "a", Prompt: "p", WorkDir: "/shared"}})
	m.tick()

	agentID := runner.agentFor(t, tasks[0].ID)
	runner.softClose(agentID)
	if runner.WorkDirBusy("/shared") {
		t.Fatal("WorkDirBusy = true after softClose: production derives busy from ActiveAgents (state), which excludes AgentKilled immediately — the fake must reproduce that, not paper over it")
	}
	if active, _ := runner.Capacity(); active != 0 {
		t.Errorf("active = %d, want 0 — a soft-closed agent stops counting toward the cap, which is exactly the trap", active)
	}
	if !runner.alive[agentID] {
		t.Fatal("test setup: softClose must not itself end the process — that is what TerminateAgent is for")
	}
}

// TestReconcileTerminatesAKilledAgentBeforeDispatch is finding 1 as a test:
// an operator running `singl agents kill` (or the TUI's kill key) soft-closes
// the agent — the record says killed, the process does not. Before this fix,
// reconcile's `killed` branch only failed the task; nothing ever terminated
// the agent, so its process outlived the task that owned it, invisible to
// WorkDirBusy and Capacity alike. The fix has to land before dispatch runs in
// the same tick, or the freed-up directory is claimed by a second agent while
// the first one's process is still alive in it.
func TestReconcileTerminatesAKilledAgentBeforeDispatch(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	first := mustAdd(t, m, []TaskSpec{{Name: "a", Prompt: "p", WorkDir: "/shared"}})
	m.tick()
	agentID := runner.agentFor(t, first[0].ID)

	// Simulate `singl agents kill`: engine.KillAgent soft-closes, so the
	// record says killed while runner.alive stays true.
	runner.softClose(agentID)

	second := mustAdd(t, m, []TaskSpec{{Name: "b", Prompt: "p", WorkDir: "/shared"}})
	m.tick()

	if got := stateOf(t, m, first[0].ID); got != StateFailed {
		t.Fatalf("first task state = %s, want failed once the engine reports its agent killed", got)
	}
	if killed := runner.killedIDs(); len(killed) != 1 || killed[0] != agentID {
		t.Fatalf("terminated = %v, want [%s] — reconcile must end a soft-closed agent's process itself, not just fail the task that owned it", killed, agentID)
	}
	if runner.alive[agentID] {
		t.Fatal("agent is still marked alive after reconcile observed it killed")
	}
	if got := stateOf(t, m, second[0].ID); got != StateRunning {
		t.Fatalf("second task state = %s, want running: reconcile's termination has to complete before dispatch runs in the same tick", got)
	}
}

func TestWorktreeTasksShareANominalWorkDir(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{
		{Name: "a", Prompt: "p", WorkDir: "/repo", Opts: TaskOptions{UseWorktree: true}},
		{Name: "b", Prompt: "p", WorkDir: "/repo", Opts: TaskOptions{UseWorktree: true}},
	})

	m.tick()
	for _, id := range []string{tasks[0].ID, tasks[1].ID} {
		if got := stateOf(t, m, id); got != StateRunning {
			t.Fatalf("task %s state = %s, want running: worktree isolation makes the shared path safe", id, got)
		}
	}
}

func TestPauseStopsDispatchButNotRunningWork(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{
		{Name: "a", Prompt: "p", WorkDir: "/w1"},
		{Name: "b", Prompt: "p", WorkDir: "/w2", After: []string{"a"}},
	})
	queueID := tasks[0].QueueID

	m.tick()
	if err := m.Pause(queueID); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	runner.setTaskState(t, tasks[0].ID, "complete", "")
	m.tick()

	if got := stateOf(t, m, tasks[0].ID); got != StateDone {
		t.Errorf("a state = %s, want done — pausing must not disturb work in flight", got)
	}
	if got := stateOf(t, m, tasks[1].ID); got != StateReady {
		t.Fatalf("b state = %s, want ready (held by the pause)", got)
	}

	if err := m.Resume(queueID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	m.tick()
	if got := stateOf(t, m, tasks[1].ID); got != StateRunning {
		t.Fatalf("b state = %s, want running after resume", got)
	}
}

func TestCancelKillsAgentAndSkipsDependents(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{
		{Name: "a", Prompt: "p", WorkDir: "/w1"},
		{Name: "b", Prompt: "p", WorkDir: "/w2", After: []string{"a"}},
	})

	m.tick()
	if err := m.Cancel(tasks[0].ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got := stateOf(t, m, tasks[0].ID); got != StateCancelled {
		t.Fatalf("a state = %s, want cancelled", got)
	}
	if got := stateOf(t, m, tasks[1].ID); got != StateSkipped {
		t.Errorf("b state = %s, want skipped", got)
	}
	runner.mu.Lock()
	killed := len(runner.killed)
	runner.mu.Unlock()
	if killed != 1 {
		t.Errorf("killed %d agents, want 1", killed)
	}
}

// TestCancelQueueTerminatesRunningAgents pins the bulk-cancel path that
// shipped without a single test through three review cycles:
// Manager.CancelQueue marked every unfinished task cancelled but never
// terminated the agent behind it, so the operator was told the queue was
// cancelled while a live agent kept editing its directory and holding an
// engine slot nothing tracked any more. This must fail before the fix
// (CancelQueue never calls TerminateAgent) and pass after (it does, mirroring
// Cancel).
func TestCancelQueueTerminatesRunningAgents(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	q1 := mustAdd(t, m, []TaskSpec{{QueueID: "q1", Name: "a", Prompt: "p", WorkDir: "/shared"}})
	m.tick()
	agentID := runner.agentFor(t, q1[0].ID)

	// A task in an unrelated queue, on the same directory: it cannot dispatch
	// while a1 is alive, cancelled or not.
	q2 := mustAdd(t, m, []TaskSpec{{QueueID: "q2", Name: "b", Prompt: "p", WorkDir: "/shared"}})

	if err := m.CancelQueue("q1"); err != nil {
		t.Fatalf("CancelQueue: %v", err)
	}
	if got := stateOf(t, m, q1[0].ID); got != StateCancelled {
		t.Fatalf("a state = %s, want cancelled", got)
	}
	if killed := runner.killedIDs(); len(killed) != 1 || killed[0] != agentID {
		t.Fatalf("terminated = %v, want [%s]", killed, agentID)
	}
	if runner.WorkDirBusy("/shared") {
		t.Fatal("/shared is still occupied after CancelQueue: the agent's process outlived it")
	}

	// Only now is the unrelated queue's task free to dispatch into /shared.
	m.tick()
	if got := stateOf(t, m, q2[0].ID); got != StateRunning {
		t.Fatalf("b state = %s, want running once the cancelled queue's agent released /shared", got)
	}
}

func TestRetryReopensASkippedDependent(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{
		{Name: "a", Prompt: "p", WorkDir: "/w1"},
		{Name: "b", Prompt: "p", WorkDir: "/w2", After: []string{"a"}},
	})

	m.tick()
	runner.setTaskState(t, tasks[0].ID, "error", "boom")
	m.tick()
	if got := stateOf(t, m, tasks[1].ID); got != StateSkipped {
		t.Fatalf("b state = %s, want skipped", got)
	}

	if err := m.Retry(tasks[0].ID); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	// Retrying the failed dependency must un-skip what it blocked.
	if got := stateOf(t, m, tasks[1].ID); got != StateBlocked {
		t.Fatalf("b state = %s, want blocked again after retrying a", got)
	}
	m.tick()
	runner.setTaskState(t, tasks[0].ID, "complete", "")
	m.tick()
	if got := stateOf(t, m, tasks[1].ID); got != StateRunning {
		t.Fatalf("b state = %s, want running", got)
	}
}

func TestRetryRejectsRunningTask(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{{Name: "a", Prompt: "p", WorkDir: "/w"}})
	m.tick()
	if err := m.Retry(tasks[0].ID); !errors.Is(err, ErrNotRetryable) {
		t.Fatalf("Retry on a running task = %v, want ErrNotRetryable", err)
	}
}

func TestAnswerRequiresWaitingState(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{{Name: "a", Prompt: "p", WorkDir: "/w"}})
	m.tick()
	if err := m.Answer(tasks[0].ID, "hi"); !errors.Is(err, ErrNotWaiting) {
		t.Fatalf("Answer on a running task = %v, want ErrNotWaiting", err)
	}
}

func TestWaitingHumanRoundTrip(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{{Name: "a", Prompt: "p", WorkDir: "/w"}})
	id := tasks[0].ID

	m.tick()
	runner.setTaskState(t, id, "waiting_human", "")
	m.tick()
	if got := stateOf(t, m, id); got != StateWaitingHuman {
		t.Fatalf("state = %s, want waiting_human", got)
	}
	// A waiting task still holds its slot: the subprocess is alive.
	if !stateOf(t, m, id).Active() {
		t.Error("waiting_human must count as active")
	}

	if err := m.Answer(id, "use option B"); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if got := stateOf(t, m, id); got != StateRunning {
		t.Fatalf("state = %s, want running after an answer", got)
	}
	runner.mu.Lock()
	inputs := append([]string(nil), runner.inputs...)
	runner.mu.Unlock()
	if len(inputs) != 1 {
		t.Fatalf("inputs = %v, want exactly one", inputs)
	}
}

func TestVanishedAgentFailsItsTask(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{{Name: "a", Prompt: "p", WorkDir: "/w"}})
	id := tasks[0].ID

	m.tick()
	// Simulate `agents remove` on a queue-owned agent.
	runner.removeAgent(runner.agentFor(t, id))
	m.tick()

	if got := stateOf(t, m, id); got != StateFailed {
		t.Fatalf("state = %s, want failed when the agent disappears", got)
	}
}

func TestPriorityOrdersDispatch(t *testing.T) {
	runner := newFakeRunner(1)
	m := newTestManager(runner)
	mustAdd(t, m, []TaskSpec{
		{Name: "low", Title: "low", Prompt: "p", WorkDir: "/w1", Priority: 0},
		{Name: "high", Title: "high", Prompt: "p", WorkDir: "/w2", Priority: 10},
	})

	m.tick()
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.started) != 1 {
		t.Fatalf("started %d tasks, want 1", len(runner.started))
	}
	if runner.started[0].Title != "high" {
		t.Errorf("dispatched %q first, want the higher-priority task", runner.started[0].Title)
	}
}

func TestListFiltersByQueueAndState(t *testing.T) {
	m := newTestManager(newFakeRunner(4))
	mustAdd(t, m, []TaskSpec{{QueueID: "alpha", Name: "a", Prompt: "p", WorkDir: "/w"}})
	mustAdd(t, m, []TaskSpec{{QueueID: "beta", Name: "b", Prompt: "p", WorkDir: "/w"}})

	got, err := m.List("alpha", nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].QueueID != "alpha" {
		t.Fatalf("List(alpha) = %+v, want one alpha task", got)
	}

	if _, err := m.List("", []State{"nonsense"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("List with a bogus state = %v, want ErrInvalid", err)
	}
	if _, err := m.List("missing", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("List of an unknown queue = %v, want ErrNotFound", err)
	}
}

func TestQueueInfoDrainedIgnoresFinishedButNotWaiting(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{{QueueID: "q", Name: "a", Prompt: "p", WorkDir: "/w"}})

	m.tick()
	info, err := m.QueueInfo("q")
	if err != nil {
		t.Fatalf("QueueInfo: %v", err)
	}
	if info.Drained() {
		t.Error("a queue with a running task is not drained")
	}

	runner.setTaskState(t, tasks[0].ID, "waiting_human", "")
	m.tick()
	info, _ = m.QueueInfo("q")
	if info.Drained() {
		t.Error("a queue waiting on a human is not drained — it needs an answer")
	}

	runner.setTaskState(t, tasks[0].ID, "complete", "")
	m.tick()
	info, _ = m.QueueInfo("q")
	if !info.Drained() {
		t.Errorf("queue with only finished tasks should be drained: %+v", info)
	}
	if info.Done != 1 {
		t.Errorf("done = %d, want 1", info.Done)
	}
}

func TestGraphIsTopologicallyOrdered(t *testing.T) {
	m := newTestManager(newFakeRunner(4))
	tasks := mustAdd(t, m, []TaskSpec{
		{QueueID: "g", Name: "c", Prompt: "p", WorkDir: "/w", After: []string{"b"}},
		{QueueID: "g", Name: "b", Prompt: "p", WorkDir: "/w", After: []string{"a"}},
		{QueueID: "g", Name: "a", Prompt: "p", WorkDir: "/w"},
	})
	// Specs were submitted leaf-first on purpose; the graph must still come
	// back root-first.
	a, b, c := tasks[2].ID, tasks[1].ID, tasks[0].ID

	g, err := m.Graph("g")
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if len(g.Nodes) != 3 {
		t.Fatalf("nodes = %d, want 3", len(g.Nodes))
	}
	order := map[string]int{}
	for i, n := range g.Nodes {
		order[n.ID] = i
	}
	if !(order[a] < order[b] && order[b] < order[c]) {
		t.Errorf("node order %v is not topological (want %s < %s < %s)", order, a, b, c)
	}
	if len(g.Edges) != 2 {
		t.Errorf("edges = %+v, want 2", g.Edges)
	}
}

func TestRemoveQueueRefusesWhileActive(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	mustAdd(t, m, []TaskSpec{{QueueID: "q", Name: "a", Prompt: "p", WorkDir: "/w"}})
	m.tick()
	if err := m.RemoveQueue("q"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("RemoveQueue with a running task = %v, want ErrInvalid", err)
	}
}

func TestPersistenceRoundTripRequeuesInterruptedWork(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	runner := newFakeRunner(4)
	m := NewManager(runner, store)
	tasks := mustAdd(t, m, []TaskSpec{
		{QueueID: "keep", Name: "a", Prompt: "first", WorkDir: "/w1"},
		{QueueID: "keep", Name: "b", Prompt: "second", WorkDir: "/w2", After: []string{"a"}},
	})
	m.tick()
	runner.setTaskState(t, tasks[0].ID, "complete", "")
	m.tick()

	// Fresh manager over the same directory: the daemon restarting.
	m2 := NewManager(newFakeRunner(4), store)
	m2.Restore()

	got, err := m2.List("keep", nil)
	if err != nil {
		t.Fatalf("List after restore: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("restored %d tasks, want 2", len(got))
	}
	byID := map[string]Task{}
	for _, tk := range got {
		byID[tk.ID] = tk
	}
	if s := byID[tasks[0].ID].State; s != StateDone {
		t.Errorf("finished task restored as %s, want done", s)
	}
	// The second task was mid-flight; its agent died with the old process,
	// so it must be dispatchable again rather than stuck in running.
	if s := byID[tasks[1].ID].State; s != StateReady {
		t.Errorf("interrupted task restored as %s, want ready", s)
	}
	if byID[tasks[1].ID].AgentID != "" {
		t.Error("restored task kept a dead agent reference")
	}

	// ID sequence must continue past the restored tasks.
	next := mustAdd(t, m2, []TaskSpec{{QueueID: "keep", Name: "c", Prompt: "third", WorkDir: "/w3"}})
	for _, existing := range got {
		if next[0].ID == existing.ID {
			t.Fatalf("new task reused restored ID %s", next[0].ID)
		}
	}
}

func TestSchedulerGoroutineDrainsAChain(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	m.tickInterval = 5 * time.Millisecond
	tasks := mustAdd(t, m, []TaskSpec{
		{Name: "a", Prompt: "p", WorkDir: "/w1"},
		{Name: "b", Prompt: "p", WorkDir: "/w2", After: []string{"a"}},
	})
	m.Start()
	defer m.Stop()

	waitFor(t, func() bool { return stateOf(t, m, tasks[0].ID) == StateRunning }, "a to start")
	runner.setTaskState(t, tasks[0].ID, "complete", "")
	m.Wake()
	waitFor(t, func() bool { return stateOf(t, m, tasks[1].ID) == StateRunning }, "b to start")
	runner.setTaskState(t, tasks[1].ID, "complete", "")
	m.Wake()
	waitFor(t, func() bool {
		info, err := m.QueueInfo(tasks[0].QueueID)
		return err == nil && info.Drained()
	}, "queue to drain")
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestRequeueNoteSurvivesTheBlockedToReadyPass(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	runner := newFakeRunner(4)
	m := NewManager(runner, store)
	tasks := mustAdd(t, m, []TaskSpec{{QueueID: "k", Name: "a", Prompt: "p", WorkDir: "/w"}})
	m.tick()

	m2 := NewManager(newFakeRunner(4), store)
	m2.Restore()
	restored, err := m2.Get(tasks[0].ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if restored.State != StateReady {
		t.Fatalf("state = %s, want ready", restored.State)
	}
	// The task has no dependencies, so it goes blocked->ready inside
	// Restore itself. The note explaining the restart must outlive that.
	if restored.Error == "" {
		t.Error("requeue note was cleared by the blocked->ready transition")
	}
}

// TestAgentLimitFromRunnerReturnsTaskToReady pins the invariant the queue
// exists for: the cap is backpressure. Even when the scheduler's capacity
// read is stale — the engine reports fewer active agents than it gates on,
// which is exactly what a smart-route agent stuck in routing does — the
// refused task must go back in line, not fail.
func TestAgentLimitFromRunnerReturnsTaskToReady(t *testing.T) {
	runner := newFakeRunner(1)
	// An agent the queue did not start already holds the only slot, but
	// Capacity under-reports it.
	runner.states["outsider"] = "routing"
	stale := 0
	runner.capacityActive = &stale

	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{{Name: "a", Prompt: "p", WorkDir: "/w"}})
	id := tasks[0].ID

	m.tick()
	task, _ := m.Get(id)
	if task.State != StateReady {
		t.Fatalf("state = %s, want ready — an agent-limit refusal is backpressure, not a failure", task.State)
	}
	if task.Attempts != 0 {
		t.Errorf("attempts = %d, want 0 — a refused spawn must not spend a retry", task.Attempts)
	}
	if task.Error != "" {
		t.Errorf("error = %q, want empty", task.Error)
	}

	// The slot frees up: the same task now starts, with its full budget.
	runner.mu.Lock()
	runner.states["outsider"] = "complete"
	runner.capacityActive = nil
	runner.mu.Unlock()

	m.tick()
	if got := stateOf(t, m, id); got != StateRunning {
		t.Fatalf("state = %s, want running once the slot freed", got)
	}
}

// TestCancelDuringSpawnKillsTheAgentAndStaysCancelled covers the unlocked
// StartTask window: a cancel that lands there used to leave the spawned
// agent live and untracked (nothing probes a cancelled task), holding an
// engine slot and a working directory forever.
func TestCancelDuringSpawnKillsTheAgentAndStaysCancelled(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{{Name: "a", Prompt: "p", WorkDir: "/w"}})
	id := tasks[0].ID

	runner.beforeStart = func(Task) {
		if err := m.Cancel(id); err != nil {
			t.Errorf("Cancel: %v", err)
		}
	}

	m.tick()
	if got := stateOf(t, m, id); got != StateCancelled {
		t.Fatalf("state = %s, want cancelled — dispatch must not overwrite an operator cancel", got)
	}
	runner.mu.Lock()
	killed := append([]string(nil), runner.killed...)
	runner.mu.Unlock()
	if len(killed) != 1 {
		t.Fatalf("killed = %v, want exactly the orphaned agent", killed)
	}
}

// TestSpawnFailureDuringCancelDoesNotResurrectTask is the other half of the
// same window: a failing spawn must not drag a cancelled task back into
// blocked, where the next tick would re-dispatch work the operator stopped.
func TestSpawnFailureDuringCancelDoesNotResurrectTask(t *testing.T) {
	runner := newFakeRunner(4)
	runner.startErr = errors.New("workdir gone")
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{{Name: "a", Prompt: "p", WorkDir: "/w"}})
	id := tasks[0].ID

	runner.beforeStart = func(Task) {
		if err := m.Cancel(id); err != nil {
			t.Errorf("Cancel: %v", err)
		}
	}

	m.tick()
	m.tick()
	if got := stateOf(t, m, id); got != StateCancelled {
		t.Fatalf("state = %s, want cancelled", got)
	}
}

// TestRemoveQueueSettlesDependentsInOtherQueues: a missing dependency counts
// as satisfied, so dropping the queue that held it has to release its
// dependents right away rather than at whatever unrelated operator action
// next happens to recompute the graph.
func TestRemoveQueueSettlesDependentsInOtherQueues(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	first := mustAdd(t, m, []TaskSpec{{QueueID: "a", Name: "a", Prompt: "p", WorkDir: "/w1"}})
	second := mustAdd(t, m, []TaskSpec{{QueueID: "b", Name: "b", Prompt: "p", WorkDir: "/w2", After: []string{first[0].ID}}})

	if err := m.Cancel(first[0].ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got := stateOf(t, m, second[0].ID); got != StateSkipped {
		t.Fatalf("dependent state = %s, want skipped", got)
	}

	if err := m.RemoveQueue("a"); err != nil {
		t.Fatalf("RemoveQueue: %v", err)
	}
	if got := stateOf(t, m, second[0].ID); got != StateReady {
		t.Fatalf("dependent state = %s, want ready immediately after the dependency was removed", got)
	}
}

// TestIdleTickSettlesDerivedState pins the unconditional settle pass. Both
// reconcile and dispatch bail out early on an idle daemon, so a graph that
// changed without any task transition has to be recomputed by the tick
// itself or it stays wedged indefinitely.
func TestIdleTickSettlesDerivedState(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{
		{Name: "a", Prompt: "p", WorkDir: "/w1"},
		{Name: "b", Prompt: "p", WorkDir: "/w2", After: []string{"a"}},
	})

	// The queue is paused so dispatch cannot mask the result by starting
	// the task in the same tick; ready is the state under test.
	if err := m.Pause(tasks[1].QueueID); err != nil {
		t.Fatalf("Pause: %v", err)
	}

	// Wedge the dependent: its dependency is skipped-out and then vanishes
	// from the index without anything recomputing the graph.
	m.mu.Lock()
	dep := m.tasks[tasks[0].ID]
	dep.State = StateCancelled
	m.tasks[tasks[1].ID].State = StateBlocked
	delete(m.tasks, dep.ID)
	m.mu.Unlock()

	// Nothing is active and nothing is ready, so reconcile and dispatch
	// both return early — only the settle pass can move this task.
	m.tick()
	if got := stateOf(t, m, tasks[1].ID); got != StateReady {
		t.Fatalf("state = %s, want ready — an idle tick must still settle derived state", got)
	}
}

// TestStopDuringDispatchReleasesUnspawnedClaims: Stop used to wait on the
// whole claimed batch, one subprocess spawn at a time, and then time out
// while the daemon tore the engine down underneath the dispatch goroutine.
func TestStopDuringDispatchReleasesUnspawnedClaims(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{
		{Name: "a", Prompt: "p", WorkDir: "/w1"},
		{Name: "b", Prompt: "p", WorkDir: "/w2"},
		{Name: "c", Prompt: "p", WorkDir: "/w3"},
	})

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	runner.beforeStart = func(Task) {
		once.Do(func() {
			entered <- struct{}{}
			<-release
		})
	}

	m.Start()
	m.Wake()
	<-entered

	stopped := make(chan struct{})
	go func() {
		m.Stop()
		close(stopped)
	}()
	<-m.stop // Stop has signalled; the dispatch loop can now observe it.
	close(release)

	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not return while dispatch was mid-spawn")
	}

	started := runner.startedIDs()
	if len(started) != 1 {
		t.Fatalf("started = %v, want only the in-flight spawn", started)
	}
	for _, task := range tasks {
		if task.ID == started[0] {
			continue
		}
		got, _ := m.Get(task.ID)
		if got.State != StateReady {
			t.Errorf("task %s state = %s, want ready — an unspawned claim must be released on stop", task.ID, got.State)
		}
		if got.Attempts != 0 {
			t.Errorf("task %s attempts = %d, want 0", task.ID, got.Attempts)
		}
	}
}

// TestAutoQueueIDSkipsAnOperatorNamedQueue: idPattern accepts "q1" as a
// queue name, and naming a queue never touches the auto-ID counter. Minting
// straight from the counter therefore merged an unrelated batch into the
// operator's queue, where one `queue cancel --queue q1` or `queue pause`
// would then hit both.
func TestAutoQueueIDSkipsAnOperatorNamedQueue(t *testing.T) {
	m := newTestManager(newFakeRunner(4))
	named := mustAdd(t, m, []TaskSpec{{QueueID: "q1", Name: "a", Prompt: "p", WorkDir: "/w1"}})

	auto := mustAdd(t, m, []TaskSpec{{Name: "b", Prompt: "p", WorkDir: "/w2"}})
	if auto[0].QueueID == "q1" {
		t.Fatalf("auto-minted queue = q1, want a free ID — the operator already owns that name")
	}

	// The two queues stay separate: each holds exactly its own task.
	for _, tc := range []struct {
		queueID string
		wantID  string
	}{
		{"q1", named[0].ID},
		{auto[0].QueueID, auto[0].ID},
	} {
		got, err := m.List(tc.queueID, nil)
		if err != nil {
			t.Fatalf("List(%s): %v", tc.queueID, err)
		}
		if len(got) != 1 || got[0].ID != tc.wantID {
			t.Errorf("queue %s holds %v, want just %s", tc.queueID, taskIDsOf(got), tc.wantID)
		}
	}
}

// TestAutoQueueIDSkipsARestoredOperatorNamedQueue is the same collision
// across a daemon restart. This one already passed before the mint loop
// existed, and pinning that is the point: autoQueueSeq parses any "q<n>",
// so an operator-named "q1" happens to advance the counter on Restore and
// the restart path was protected by accident rather than by design. It is
// the only path that was — a queue named during this daemon's lifetime
// never touches the counter, which is the case above.
func TestAutoQueueIDSkipsARestoredOperatorNamedQueue(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	m := NewManager(newFakeRunner(4), store)
	named := mustAdd(t, m, []TaskSpec{{QueueID: "q1", Name: "a", Prompt: "p", WorkDir: "/w1"}})

	m2 := NewManager(newFakeRunner(4), store)
	m2.Restore()

	auto := mustAdd(t, m2, []TaskSpec{{Name: "b", Prompt: "p", WorkDir: "/w2"}})
	if auto[0].QueueID == "q1" {
		t.Fatalf("auto-minted queue = q1 after restore, want a free ID")
	}
	got, err := m2.List("q1", nil)
	if err != nil {
		t.Fatalf("List(q1): %v", err)
	}
	if len(got) != 1 || got[0].ID != named[0].ID {
		t.Errorf("restored queue q1 holds %v, want just %s", taskIDsOf(got), named[0].ID)
	}
}

// taskIDsOf is a readable form for assertion failures.
func taskIDsOf(tasks []Task) []string {
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}

// agentFor returns the agent the fake dispatched for taskID.
func (f *fakeRunner) agentFor(t *testing.T, taskID string) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.byTask[taskID]
	if !ok {
		t.Fatalf("no agent dispatched for task %s", taskID)
	}
	return id
}

// killedIDs snapshots the agents the fake was asked to kill.
func (f *fakeRunner) killedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.killed...)
}

// lastSpawnCtx returns the context of the most recent StartTask call.
func (f *fakeRunner) lastSpawnCtx() context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.spawnCtx
}

// TestStopWaitsForTheSpawnItCannotCancel is the hole cycle 1 bounded but did
// not close. Releasing the unspawned claims capped Stop's exposure at one
// StartTask, but one StartTask doing `git worktree add` can outlast any
// timeout — and when the timeout expired Stop returned anyway, so the
// caller went on to eng.Shutdown() while the dispatch goroutine was still
// inside the engine.
//
// The spawn here deliberately takes longer than stopWarnAfter. Stop must
// still be waiting when it completes, not gone.
func TestStopWaitsForTheSpawnItCannotCancel(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	tasks := mustAdd(t, m, []TaskSpec{{Name: "a", Prompt: "p", WorkDir: "/w1"}})

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	runner.beforeStart = func(Task) {
		once.Do(func() {
			entered <- struct{}{}
			<-release
		})
	}

	m.Start()
	m.Wake()
	<-entered

	stopped := make(chan struct{})
	go func() {
		m.Stop()
		close(stopped)
	}()

	// Outlast the point at which Stop used to give up and return.
	select {
	case <-stopped:
		t.Fatal("Stop returned while a spawn was still in flight — eng.Shutdown would race it")
	case <-time.After(stopWarnAfter + 250*time.Millisecond):
	}
	close(release)

	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop never returned after the in-flight spawn completed")
	}

	// The spawn landed after cancellation, so its agent is killed rather
	// than adopted and the task goes back to ready with its attempt
	// refunded — the same treatment a claim that stopped being ours gets.
	got, err := m.Get(tasks[0].ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateReady {
		t.Errorf("task state = %s, want ready — a spawn that raced shutdown must not be adopted", got.State)
	}
	if got.AgentID != "" {
		t.Errorf("task agent = %q, want empty — eng.Shutdown is about to invalidate it", got.AgentID)
	}
	if got.Attempts != 0 {
		t.Errorf("attempts = %d, want 0 — a shutdown is not the task's failure", got.Attempts)
	}
	if killed := runner.killedIDs(); len(killed) != 1 {
		t.Errorf("killed = %v, want exactly the agent spawned into the shutdown", killed)
	}
}

// TestStartTaskContextIsCancelledByStop pins the plumbing the unbounded
// wait rests on. The stop-channel poll at the top of the spawn loop is a
// fast path that can lose its race; the context cannot, because it travels
// into the runner itself. If dispatch ever passed context.Background(), or
// Stop stopped cancelling, Stop would be back to waiting on a whole batch
// of spawns with nothing to shorten it.
func TestStartTaskContextIsCancelledByStop(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)
	mustAdd(t, m, []TaskSpec{{Name: "a", Prompt: "p", WorkDir: "/w1"}})
	m.tick()

	ctx := runner.lastSpawnCtx()
	if ctx == nil {
		t.Fatal("dispatch never called StartTask")
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("spawn context already cancelled before Stop: %v", err)
	}
	m.Stop()
	if err := ctx.Err(); err == nil {
		t.Error("Stop did not cancel the context dispatch hands to StartTask")
	}
}

// TestEngineRunnerRefusesSpawnAfterCancellation asserts the production
// runner — not just the fake — declines to reach into the engine once the
// manager's run context is done. The nil engine is the assertion: a runner
// that got as far as StartAgent would panic.
func TestEngineRunnerRefusesSpawnAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	id, err := NewEngineRunner(nil).StartTask(ctx, Task{ID: "t1", Prompt: "p", WorkDir: "/w1"})
	if err == nil {
		t.Fatal("StartTask spawned an agent after cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want it to wrap context.Canceled", err)
	}
	if id != "" {
		t.Errorf("agent ID = %q, want empty", id)
	}
	// A refused spawn is backpressure-shaped, not a task failure: dispatch
	// decides that from the manager's context rather than the error, so the
	// one thing this must not do is claim the cap refused it.
	if errors.Is(err, ErrNoCapacity) {
		t.Error("a cancelled spawn must not masquerade as a capacity refusal")
	}
}

// TestStopWithoutStartReturnsImmediately: Stop's wait is now unbounded, so
// a Manager that never ran its scheduler must not be waited on at all —
// nothing would ever close m.stopped.
func TestStopWithoutStartReturnsImmediately(t *testing.T) {
	m := newTestManager(newFakeRunner(1))
	done := make(chan struct{})
	go func() {
		m.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop on an unstarted Manager blocked")
	}
}

// TestSlowChangeConsumerDoesNotStallDispatch: the daemon wires OnChange to a
// WS broadcast that writes to every connected client synchronously, bounded
// only by a 10s deadline each. Calling that inline from tick() put a wedged
// peer directly in the dispatch path — ready tasks sat idle with free engine
// slots, which is precisely the stall the queue exists to remove.
func TestSlowChangeConsumerDoesNotStallDispatch(t *testing.T) {
	runner := newFakeRunner(4)
	m := newTestManager(runner)

	blocked := make(chan struct{})
	firstFrame := make(chan struct{}, 1)
	var once sync.Once
	m.OnChange(func(Task) {
		once.Do(func() {
			select {
			case firstFrame <- struct{}{}:
			default:
			}
			<-blocked
		})
	})
	defer close(blocked)

	first := mustAdd(t, m, []TaskSpec{{Name: "a", Prompt: "p", WorkDir: "/w1"}})
	select {
	case <-firstFrame:
	case <-time.After(2 * time.Second):
		t.Fatal("no change frame reached the consumer")
	}

	// The consumer is now wedged and will stay wedged. Dispatch must be
	// entirely unaffected by that.
	second := mustAdd(t, m, []TaskSpec{{Name: "b", Prompt: "p", WorkDir: "/w2"}})
	done := make(chan struct{})
	go func() {
		m.tick()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("tick blocked behind a wedged change consumer")
	}

	for _, task := range []Task{first[0], second[0]} {
		if got := stateOf(t, m, task.ID); got != StateRunning {
			t.Errorf("task %s state = %s, want running — dispatch must not wait on a broadcast", task.ID, got)
		}
	}
}

// TestAddEmitsTheReadyPromotion: Add was the only mutator that never called
// emit, so a dependency-free task's blocked->ready transition — a real state
// change — reached no subscriber. Nor could the scheduler recover it: the
// next tick's settle finds the task already correct, so the earliest frame
// would be dispatch, which for a task behind a paused queue never comes.
func TestAddEmitsTheReadyPromotion(t *testing.T) {
	m := newTestManager(newFakeRunner(4))

	frames := make(chan Task, 16)
	m.OnChange(func(t Task) { frames <- t })
	defer m.Stop()

	added := mustAdd(t, m, []TaskSpec{
		{Name: "root", Prompt: "p", WorkDir: "/w1"},
		{Name: "leaf", Prompt: "p", WorkDir: "/w2", After: []string{"root"}},
	})

	// Exactly one frame per added task, each carrying its settled state:
	// the root promoted to ready, the dependent still blocked.
	want := map[string]State{added[0].ID: StateReady, added[1].ID: StateBlocked}
	got := map[string]State{}
	deadline := time.After(2 * time.Second)
	for len(got) < len(want) {
		select {
		case f := <-frames:
			if prev, dup := got[f.ID]; dup {
				t.Fatalf("task %s emitted twice (%s then %s)", f.ID, prev, f.State)
			}
			got[f.ID] = f.State
		case <-deadline:
			t.Fatalf("only %d of %d frames arrived: %v", len(got), len(want), got)
		}
	}
	for id, wantState := range want {
		if got[id] != wantState {
			t.Errorf("task %s emitted in state %s, want %s", id, got[id], wantState)
		}
	}
}
