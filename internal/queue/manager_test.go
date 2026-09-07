package queue

import (
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
	mu        sync.Mutex
	states    map[string]string // agentID -> state name
	errs      map[string]string
	byTask    map[string]string // taskID -> agentID
	started   []Task
	busyDirs  map[string]bool
	maxAgents int
	startErr  error
	seq       int
	inputs    []string
	killed    []string
}

func newFakeRunner(max int) *fakeRunner {
	return &fakeRunner{
		states:    map[string]string{},
		errs:      map[string]string{},
		byTask:    map[string]string{},
		busyDirs:  map[string]bool{},
		maxAgents: max,
	}
}

func (f *fakeRunner) StartTask(t Task) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return "", f.startErr
	}
	f.seq++
	id := fmt.Sprintf("a%d", f.seq)
	f.states[id] = "running"
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
	active := 0
	for _, st := range f.states {
		switch st {
		case "complete", "error", "killed":
		default:
			active++
		}
	}
	return active, f.maxAgents
}

func (f *fakeRunner) WorkDirBusy(dir string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.busyDirs[filepath.Clean(dir)]
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

func (f *fakeRunner) KillAgent(agentID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed = append(f.killed, agentID)
	f.states[agentID] = "killed"
	return nil
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
	runner.mu.Lock()
	delete(runner.states, runner.byTask[id])
	runner.mu.Unlock()
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
