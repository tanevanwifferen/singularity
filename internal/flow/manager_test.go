package flow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/queue"
)

// The concrete queue must satisfy the narrow interface with no adapter — that
// is the whole point of declaring TaskQueue with queue.Manager's own
// signatures, and this line is what fails if one of them drifts.
var _ TaskQueue = (*queue.Manager)(nil)

// fakeQueue is a TaskQueue with no scheduler and no agents: Add records
// tasks and resolves batch-local names, and states only move when a test
// moves them. Everything the manager does to the queue is therefore
// assertable, which a real queue.Manager — whose scheduler would dispatch
// the tasks and reach for an engine — could not offer.
type fakeQueue struct {
	mu    sync.Mutex
	tasks map[string]*queue.Task
	seq   int

	// batches records every Add, so a test can assert what a flow submitted
	// as well as what it ended up with.
	batches [][]queue.TaskSpec
	// cancelled records Cancel calls in order, including repeats: the point
	// of the cancel test is that a flow touches exactly its own task IDs.
	cancelled []string
	addErr    error
}

func newFakeQueue() *fakeQueue {
	return &fakeQueue{tasks: make(map[string]*queue.Task)}
}

func (q *fakeQueue) Add(specs []queue.TaskSpec) ([]queue.Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.addErr != nil {
		return nil, q.addErr
	}
	q.batches = append(q.batches, specs)

	byName := make(map[string]string, len(specs))
	ids := make([]string, len(specs))
	for i := range specs {
		q.seq++
		ids[i] = fmt.Sprintf("t%d", q.seq)
		if specs[i].Name != "" {
			byName[specs[i].Name] = ids[i]
		}
	}

	out := make([]queue.Task, 0, len(specs))
	for i, s := range specs {
		t := &queue.Task{
			ID:        ids[i],
			QueueID:   s.QueueID,
			Title:     s.Title,
			Prompt:    s.Prompt,
			WorkDir:   s.WorkDir,
			Opts:      s.Opts,
			State:     queue.StateReady,
			CreatedAt: time.Now(),
		}
		for _, after := range s.After {
			dep, ok := byName[after]
			if !ok {
				dep = after
			}
			t.DependsOn = append(t.DependsOn, dep)
			t.State = queue.StateBlocked
		}
		q.tasks[t.ID] = t
		out = append(out, t.Clone())
	}
	return out, nil
}

func (q *fakeQueue) Get(taskID string) (queue.Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.tasks[taskID]
	if !ok {
		return queue.Task{}, queue.ErrNotFound
	}
	return t.Clone(), nil
}

func (q *fakeQueue) List(queueID string, states []queue.State) ([]queue.Task, error) {
	want := make(map[queue.State]bool, len(states))
	for _, s := range states {
		want[s] = true
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []queue.Task
	for _, t := range q.tasks {
		if queueID != "" && t.QueueID != queueID {
			continue
		}
		if len(want) > 0 && !want[t.State] {
			continue
		}
		out = append(out, t.Clone())
	}
	return out, nil
}

func (q *fakeQueue) Cancel(taskID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.cancelled = append(q.cancelled, taskID)
	t, ok := q.tasks[taskID]
	if !ok {
		return queue.ErrNotFound
	}
	if !t.State.Terminal() {
		t.State = queue.StateCancelled
	}
	return nil
}

// seed inserts a task the flow did not submit, so a test can prove the flow
// leaves it alone.
func (q *fakeQueue) seed(id, queueID string, state queue.State) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.tasks[id] = &queue.Task{ID: id, QueueID: queueID, State: state}
}

func (q *fakeQueue) setState(t *testing.T, id string, state queue.State) {
	t.Helper()
	q.mu.Lock()
	defer q.mu.Unlock()
	task, ok := q.tasks[id]
	if !ok {
		t.Fatalf("fakeQueue has no task %s", id)
	}
	task.State = state
}

func (q *fakeQueue) setAgent(t *testing.T, id, agentID string) {
	t.Helper()
	q.mu.Lock()
	defer q.mu.Unlock()
	task, ok := q.tasks[id]
	if !ok {
		t.Fatalf("fakeQueue has no task %s", id)
	}
	task.AgentID = agentID
}

func (q *fakeQueue) drop(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.tasks, id)
}

func (q *fakeQueue) cancelledIDs() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]string(nil), q.cancelled...)
}

// newManager builds a manager over a fake queue and a persistence-free
// store, which is what most of these tests want.
func newManager(t *testing.T) (*Manager, *fakeQueue) {
	t.Helper()
	q := newFakeQueue()
	return NewManager(q, &Store{}), q
}

// startReq is a valid request against a real temp directory, so work_dir
// validation passes for the tests that are about something else.
func startReq(t *testing.T) StartRequest {
	t.Helper()
	return StartRequest{
		Title:   "retry parsing",
		Goal:    "handle the HTTP-date form of Retry-After",
		WorkDir: t.TempDir(),
	}
}

func TestStartRefusals(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cases := []struct {
		name string
		req  StartRequest
		want string
	}{
		{"empty goal", StartRequest{WorkDir: dir}, "no goal"},
		{"whitespace goal", StartRequest{Goal: "  \n ", WorkDir: dir}, "no goal"},
		{"empty work_dir", StartRequest{Goal: "g"}, "no work_dir"},
		{"missing work_dir", StartRequest{Goal: "g", WorkDir: filepath.Join(dir, "nope")}, "work_dir"},
		{"work_dir is a file", StartRequest{Goal: "g", WorkDir: file}, "not a directory"},
		{"negative max_rounds", StartRequest{Goal: "g", WorkDir: dir, MaxRounds: -1}, "max_rounds"},
		{"max_rounds over cap", StartRequest{Goal: "g", WorkDir: dir, MaxRounds: 21}, "max_rounds"},
		{"use_worktree", StartRequest{Goal: "g", WorkDir: dir,
			Opts: TaskOptions{UseWorktree: true}}, "use_worktree"},
		{"reviewer use_worktree", StartRequest{Goal: "g", WorkDir: dir,
			ReviewOpts: TaskOptions{UseWorktree: true}}, "use_worktree"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newManager(t)
			_, err := m.Start(tc.req)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Start error = %v, want ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Start error = %q, want it to mention %q", err, tc.want)
			}
			if flows, _ := m.List(nil); len(flows) != 0 {
				t.Errorf("a refused start recorded %d flows, want 0", len(flows))
			}
		})
	}
}

func TestStartDefaultsAndAssignedIDs(t *testing.T) {
	m, _ := newManager(t)

	for i, want := range []string{"f1", "f2", "f3"} {
		f, err := m.Start(startReq(t))
		if err != nil {
			t.Fatalf("Start %d: %v", i, err)
		}
		if f.ID != want {
			t.Errorf("flow %d ID = %q, want %q", i, f.ID, want)
		}
		if f.QueueID != "flow-"+want {
			t.Errorf("flow %d QueueID = %q, want %q", i, f.QueueID, "flow-"+want)
		}
		if f.State != StatePending {
			t.Errorf("flow %d State = %q, want %q — round 1 is the reconciler's to submit",
				i, f.State, StatePending)
		}
		if len(f.Rounds) != 0 {
			t.Errorf("flow %d has %d rounds, want none before the reconciler runs", i, len(f.Rounds))
		}
		if f.MaxRounds != defaultRounds {
			t.Errorf("flow %d MaxRounds = %d, want the default %d", i, f.MaxRounds, defaultRounds)
		}
		if f.CreatedAt.IsZero() {
			t.Errorf("flow %d has no CreatedAt", i)
		}
	}
}

func TestStartAcceptsRoundCapBounds(t *testing.T) {
	m, _ := newManager(t)
	for _, rounds := range []int{1, 3, maxRoundsCap} {
		req := startReq(t)
		req.MaxRounds = rounds
		f, err := m.Start(req)
		if err != nil {
			t.Fatalf("Start with max_rounds %d: %v", rounds, err)
		}
		if f.MaxRounds != rounds {
			t.Errorf("MaxRounds = %d, want %d", f.MaxRounds, rounds)
		}
	}
}

func TestStartReviewOptsDefaultToOpts(t *testing.T) {
	m, _ := newManager(t)

	req := startReq(t)
	req.Opts = TaskOptions{Model: "opus", Effort: "high", TimeoutSecs: 900}
	f, err := m.Start(req)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !reflect.DeepEqual(f.ReviewOpts, f.Opts) {
		t.Errorf("ReviewOpts = %+v, want a copy of Opts %+v", f.ReviewOpts, f.Opts)
	}

	req2 := startReq(t)
	req2.Opts = TaskOptions{Model: "opus"}
	req2.ReviewOpts = TaskOptions{Model: "sonnet"}
	f2, err := m.Start(req2)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if f2.ReviewOpts.Model != "sonnet" {
		t.Errorf("ReviewOpts.Model = %q, want the reviewer's own %q", f2.ReviewOpts.Model, "sonnet")
	}
}

func TestStartTrimsAndKeepsGoalVerbatimOtherwise(t *testing.T) {
	m, _ := newManager(t)
	req := startReq(t)
	req.Goal = "  fix the parser\n\nit is wrong  "
	req.Title = "  parser  "
	req.ReviewGoal = "  be adversarial  "

	f, err := m.Start(req)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if f.Goal != "fix the parser\n\nit is wrong" {
		t.Errorf("Goal = %q, want it trimmed at the ends only", f.Goal)
	}
	if f.Title != "parser" || f.ReviewGoal != "be adversarial" {
		t.Errorf("Title = %q, ReviewGoal = %q, want both trimmed", f.Title, f.ReviewGoal)
	}
}

func TestGetReturnsACopy(t *testing.T) {
	m, _ := newManager(t)
	f, err := m.Start(startReq(t))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	got, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got.Goal = "mutated"
	got.Rounds = append(got.Rounds, &Round{N: 99})

	again, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if again.Goal == "mutated" || len(again.Rounds) != 0 {
		t.Error("Get handed out a pointer into manager state")
	}

	if _, err := m.Get("f404"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get of a missing flow = %v, want ErrNotFound", err)
	}
}

func TestListFiltersByStateAndOrdersNumerically(t *testing.T) {
	m, _ := newManager(t)
	// Eleven flows, so lexical ordering would put f10 before f2.
	for i := 0; i < 11; i++ {
		if _, err := m.Start(startReq(t)); err != nil {
			t.Fatalf("Start %d: %v", i, err)
		}
	}
	setState(t, m, "f2", StateAccepted)
	setState(t, m, "f10", StateAccepted)
	setState(t, m, "f11", StateRejected)

	all, err := m.List(nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 11 {
		t.Fatalf("List returned %d flows, want 11", len(all))
	}
	if all[0].ID != "f1" || all[1].ID != "f2" || all[9].ID != "f10" || all[10].ID != "f11" {
		t.Errorf("List order = %s, want f1, f2, … f10, f11", flowIDs(all))
	}

	accepted, err := m.List([]State{StateAccepted})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := flowIDs(accepted); got != "f2, f10" {
		t.Errorf("accepted filter = %s, want f2, f10", got)
	}

	both, err := m.List([]State{StateAccepted, StateRejected})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := flowIDs(both); got != "f2, f10, f11" {
		t.Errorf("two-state filter = %s, want f2, f10, f11", got)
	}

	pending, err := m.List([]State{StatePending})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(pending) != 8 {
		t.Errorf("pending filter returned %d flows, want 8", len(pending))
	}
}

func TestListRejectsUnknownState(t *testing.T) {
	m, _ := newManager(t)
	_, err := m.List([]State{StatePending, "finished"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("List error = %v, want ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "finished") {
		t.Errorf("List error = %q, want it to name the bad state", err)
	}
}

func TestCancelTouchesOnlyItsOwnNonTerminalTasks(t *testing.T) {
	m, q := newManager(t)
	f, err := m.Start(startReq(t))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Two rounds' worth of tasks the flow created, plus one task in the
	// flow's own queue that it did not create — the case §4 is about, since
	// queue.Manager.Add adopts a pre-existing queue named flow-<id>.
	submitRound(t, m, q, f.ID, 1)
	submitRound(t, m, q, f.ID, 2)
	// Round 2 has not settled — the helper records settled rounds, and a
	// cancel is only interesting while something is still live.
	makeRoundLive(t, m, f.ID, 2)
	q.seed("t99", "flow-"+f.ID, queue.StateRunning)
	q.seed("t100", "some-other-queue", queue.StateRunning)

	// Round 1 has settled; round 2 is live.
	q.setState(t, "t1", queue.StateDone)
	q.setState(t, "t2", queue.StateDone)

	if err := m.Cancel(f.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	got := q.cancelledIDs()
	if len(got) != 2 || got[0] != "t3" || got[1] != "t4" {
		t.Errorf("cancelled tasks = %v, want only the flow's own live ones [t3 t4]", got)
	}
	for _, id := range []string{"t99", "t100"} {
		task, err := q.Get(id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if task.State != queue.StateRunning {
			t.Errorf("task %s is %s: the flow cancelled a task it did not create", id, task.State)
		}
	}

	after, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.State != StateCancelled {
		t.Errorf("flow state = %q, want %q", after.State, StateCancelled)
	}
	if after.EndedAt == nil {
		t.Error("a cancelled flow has no EndedAt")
	}
	if after.Rounds[0].State != RoundRejected {
		t.Errorf("settled round 1 = %q, want it left alone", after.Rounds[0].State)
	}
	if after.Rounds[1].State != RoundErrored || after.Rounds[1].EndedAt == nil {
		t.Errorf("live round 2 = %q (ended %v), want it concluded as errored",
			after.Rounds[1].State, after.Rounds[1].EndedAt)
	}
}

func TestCancelIsIdempotentAndReportsMissingFlows(t *testing.T) {
	m, q := newManager(t)
	f, err := m.Start(startReq(t))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	submitRound(t, m, q, f.ID, 1)

	if err := m.Cancel(f.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	first := len(q.cancelledIDs())
	if err := m.Cancel(f.ID); err != nil {
		t.Fatalf("second Cancel: %v", err)
	}
	if got := len(q.cancelledIDs()); got != first {
		t.Errorf("a second Cancel issued %d more queue cancels, want none", got-first)
	}

	if err := m.Cancel("f404"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Cancel of a missing flow = %v, want ErrNotFound", err)
	}
}

func TestRemoveRefusedWhileNonTerminal(t *testing.T) {
	store := newStore(t)
	q := newFakeQueue()
	m := NewManager(q, store)

	f, err := m.Start(startReq(t))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	submitRound(t, m, q, f.ID, 1)

	// Both non-terminal states are refused, not just the running one.
	for _, state := range []State{StatePending, StateRunning} {
		setState(t, m, f.ID, state)
		err := m.Remove(f.ID)
		if !errors.Is(err, ErrActive) {
			t.Fatalf("Remove of a %s flow = %v, want ErrActive", state, err)
		}
		if !strings.Contains(err.Error(), string(state)) {
			t.Errorf("Remove error = %q, want it to name the state", err)
		}
		if _, err := m.Get(f.ID); err != nil {
			t.Fatalf("a refused Remove dropped the flow: %v", err)
		}
	}

	// A verdict file stands in for what a reviewer would have written.
	verdictDir := store.VerdictDir(f.ID)
	if err := os.MkdirAll(verdictDir, 0o700); err != nil {
		t.Fatalf("mkdir verdict dir: %v", err)
	}
	verdict := filepath.Join(verdictDir, "r1-a1-verdict.json")
	if err := os.WriteFile(verdict, []byte(`{"verdict":"reject"}`), 0o600); err != nil {
		t.Fatalf("write verdict: %v", err)
	}

	if err := m.Cancel(f.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := m.Remove(f.ID); err != nil {
		t.Fatalf("Remove of a cancelled flow: %v", err)
	}
	if _, err := m.Get(f.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Remove = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(filepath.Join(store.Dir, f.ID+".json")); !os.IsNotExist(err) {
		t.Errorf("record still on disk after Remove: %v", err)
	}
	if _, err := os.Stat(verdictDir); !os.IsNotExist(err) {
		t.Errorf("verdict dir still on disk after Remove: %v", err)
	}
	if err := m.Remove("f404"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Remove of a missing flow = %v, want ErrNotFound", err)
	}
	// The flow's tasks survive: `queue remove` owns the queue, not Remove.
	if _, err := q.Get("t1"); err != nil {
		t.Errorf("Remove deleted the flow's tasks: %v", err)
	}
}

func TestTreeForTwoRoundFlow(t *testing.T) {
	m, q := newManager(t)
	req := startReq(t)
	req.Title = "retry-after handling"
	f, err := m.Start(req)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	submitRound(t, m, q, f.ID, 1)
	submitRound(t, m, q, f.ID, 2)
	setState(t, m, f.ID, StateRunning)

	q.setState(t, "t1", queue.StateDone)
	q.setAgent(t, "t1", "agent-1712-4")
	q.setState(t, "t2", queue.StateDone)
	q.setAgent(t, "t2", "agent-1712-5")
	q.setState(t, "t3", queue.StateRunning)
	q.setAgent(t, "t3", "agent-1712-9")

	tree, err := m.Tree(f.ID)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if tree.FlowID != f.ID {
		t.Errorf("FlowID = %q, want %q", tree.FlowID, f.ID)
	}

	want := []TreeNode{
		{ID: "f1", Kind: NodeFlow, Label: "retry-after handling", State: "running"},
		{ID: "f1/r1", ParentID: "f1", Kind: NodeRound, Label: "round 1", State: "rejected", Round: 1},
		{ID: "f1/r1/implement", ParentID: "f1/r1", Kind: NodeStep, Label: "implement",
			State: "done", Round: 1, TaskID: "t1", AgentID: "agent-1712-4"},
		{ID: "f1/r1/review", ParentID: "f1/r1", Kind: NodeStep, Label: "review",
			State: "done", Round: 1, TaskID: "t2", AgentID: "agent-1712-5"},
		{ID: "f1/r2", ParentID: "f1", Kind: NodeRound, Label: "round 2", State: "rejected", Round: 2},
		{ID: "f1/r2/fix", ParentID: "f1/r2", Kind: NodeStep, Label: "fix",
			State: "running", Round: 2, TaskID: "t3", AgentID: "agent-1712-9"},
		{ID: "f1/r2/review", ParentID: "f1/r2", Kind: NodeStep, Label: "review",
			State: "blocked", Round: 2, TaskID: "t4"},
	}
	if len(tree.Nodes) != len(want) {
		t.Fatalf("tree has %d nodes, want %d: %+v", len(tree.Nodes), len(want), tree.Nodes)
	}
	for i, w := range want {
		got := tree.Nodes[i]
		// Verdicts are asserted separately; compare the rest field by field.
		gotVerdict := got.Verdict
		got.Verdict = nil
		if got != w {
			t.Errorf("node %d = %+v, want %+v", i, got, w)
		}
		switch got.Kind {
		case NodeRound:
			if gotVerdict == nil {
				t.Errorf("round node %s carries no verdict", got.ID)
			}
		default:
			if gotVerdict != nil {
				t.Errorf("%s node %s carries a verdict; only rounds should", got.Kind, got.ID)
			}
		}
	}

	if _, err := m.Tree("f404"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Tree of a missing flow = %v, want ErrNotFound", err)
	}
}

func TestTreeStepStateIsReadFromTheQueue(t *testing.T) {
	m, q := newManager(t)
	f, err := m.Start(startReq(t))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	submitRound(t, m, q, f.ID, 1)

	// An operator cancelling a flow's task behind its back is a real event
	// (§4); the tree must report the task, not the flow's memory of it.
	q.setState(t, "t1", queue.StateCancelled)
	tree, err := m.Tree(f.ID)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if got := nodeByID(t, tree, "f1/r1/implement").State; got != "cancelled" {
		t.Errorf("step state = %q, want the task's own %q", got, "cancelled")
	}

	// A task the queue no longer has leaves the step in place with its
	// state unrecoverable, rather than silently dropping the node.
	q.drop("t1")
	tree, err = m.Tree(f.ID)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	node := nodeByID(t, tree, "f1/r1/implement")
	if node.State != StepStateUnknown || node.TaskID != "t1" {
		t.Errorf("orphaned step = %+v, want state %q and its task id kept", node, StepStateUnknown)
	}
}

func TestTreeOfAPendingFlowIsJustTheRoot(t *testing.T) {
	m, _ := newManager(t)
	f, err := m.Start(startReq(t))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	tree, err := m.Tree(f.ID)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if len(tree.Nodes) != 1 {
		t.Fatalf("pending flow tree has %d nodes, want 1: %+v", len(tree.Nodes), tree.Nodes)
	}
	root := tree.Nodes[0]
	if root.Kind != NodeFlow || root.State != string(StatePending) || root.ParentID != "" {
		t.Errorf("root = %+v, want a parentless pending flow node", root)
	}
}

func TestTreeRootFallsBackToTheGoal(t *testing.T) {
	m, _ := newManager(t)
	req := startReq(t)
	req.Title = ""
	req.Goal = strings.Repeat("a", 80) + "\nsecond line"
	f, err := m.Start(req)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	tree, err := m.Tree(f.ID)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	label := tree.Nodes[0].Label
	if strings.Contains(label, "second line") {
		t.Errorf("label = %q, want the first line of the goal only", label)
	}
	if !strings.HasSuffix(label, "…") || len([]rune(label)) != 61 {
		t.Errorf("label = %q (%d runes), want it shortened to 60 plus an ellipsis",
			label, len([]rune(label)))
	}
}

func TestPersistenceRoundTripThroughAStore(t *testing.T) {
	store := newStore(t)
	q := newFakeQueue()
	m := NewManager(q, store)

	req := startReq(t)
	req.MaxRounds = 5
	req.Opts = TaskOptions{Model: "opus", ContextFiles: []string{"internal/http/retry.go"}}
	f, err := m.Start(req)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	submitRound(t, m, q, f.ID, 1)
	if _, err := m.Start(startReq(t)); err != nil {
		t.Fatalf("Start second flow: %v", err)
	}

	// A fresh manager over the same directory: what a daemon restart is.
	restored := NewManager(q, store)
	restored.Restore()

	got, err := restored.Get(f.ID)
	if err != nil {
		t.Fatalf("Get after Restore: %v", err)
	}
	if got.QueueID != "flow-f1" || got.MaxRounds != 5 || got.Goal != f.Goal {
		t.Errorf("restored flow = %+v, want the record Start wrote", got)
	}
	if got.Opts.Model != "opus" || len(got.Opts.ContextFiles) != 1 {
		t.Errorf("restored Opts = %+v, want them preserved", got.Opts)
	}
	if len(got.Rounds) != 1 {
		t.Fatalf("restored flow has %d rounds, want 1", len(got.Rounds))
	}
	r := got.Rounds[0]
	if r.WorkTaskID != "t1" || r.ReviewTaskID != "t2" || r.Verdict == nil ||
		len(r.Verdict.Findings) != 1 {
		t.Errorf("restored round = %+v (verdict %+v), want the mapping and verdict intact",
			r, r.Verdict)
	}

	if flows, _ := restored.List(nil); len(flows) != 2 {
		t.Errorf("restored %d flows, want 2", len(flows))
	}

	// The sequence continues past the restored IDs, so a new flow cannot
	// take a live one's ID.
	next, err := restored.Start(startReq(t))
	if err != nil {
		t.Fatalf("Start after Restore: %v", err)
	}
	if next.ID != "f3" {
		t.Errorf("post-restore flow ID = %q, want f3", next.ID)
	}

	// Restore is idempotent: a second pass must not duplicate or reset.
	restored.Restore()
	if flows, _ := restored.List(nil); len(flows) != 3 {
		t.Errorf("after a second Restore there are %d flows, want 3", len(flows))
	}
	if again, err := restored.Get(next.ID); err != nil || again.ID != "f3" {
		t.Errorf("Get %s after a second Restore = %+v, %v", next.ID, again, err)
	}

	// The tree survives the round trip, which is the shape a client reads.
	tree, err := restored.Tree(f.ID)
	if err != nil {
		t.Fatalf("Tree after Restore: %v", err)
	}
	if len(tree.Nodes) != 4 {
		t.Errorf("restored tree has %d nodes, want root + round + 2 steps: %+v",
			len(tree.Nodes), tree.Nodes)
	}
}

// --- helpers ------------------------------------------------------------

// setState moves a flow's state the way the reconciler will, since this unit
// deliberately has no advancement of its own.
func setState(t *testing.T, m *Manager, flowID string, state State) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.flows[flowID]
	if !ok {
		t.Fatalf("manager has no flow %s", flowID)
	}
	f.State = state
	m.saveLocked(f)
}

// makeRoundLive rewinds a recorded round to the state the reconciler leaves
// it in while its tasks are still running: no verdict yet.
func makeRoundLive(t *testing.T, m *Manager, flowID string, n int) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.flows[flowID]
	if !ok {
		t.Fatalf("manager has no flow %s", flowID)
	}
	for _, r := range f.Rounds {
		if r.N != n {
			continue
		}
		r.State = RoundRunning
		r.Verdict = nil
		r.EndedAt = nil
		m.saveLocked(f)
		return
	}
	t.Fatalf("flow %s has no round %d", flowID, n)
}

// submitRound stands in for the reconciler: it submits a round's two tasks
// through the same TaskQueue the manager holds and records the round with a
// settled reject verdict, so the read paths have something to report.
func submitRound(t *testing.T, m *Manager, q *fakeQueue, flowID string, n int) {
	t.Helper()
	f, err := m.Get(flowID)
	if err != nil {
		t.Fatalf("Get %s: %v", flowID, err)
	}
	work := workStepLabel(n)
	tasks, err := q.Add([]queue.TaskSpec{
		{Name: work, QueueID: f.QueueID, Title: fmt.Sprintf("%s r%d %s", flowID, n, work),
			Prompt: "work", WorkDir: f.WorkDir},
		{Name: "review", QueueID: f.QueueID, Title: fmt.Sprintf("%s r%d review", flowID, n),
			Prompt: "review", WorkDir: f.WorkDir, After: []string{work}},
	})
	if err != nil {
		t.Fatalf("queue Add: %v", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.flows[flowID]
	ended := time.Now()
	rec.Rounds = append(rec.Rounds, &Round{
		N:             n,
		WorkTaskID:    tasks[0].ID,
		ReviewTaskID:  tasks[1].ID,
		ReviewAttempt: 1,
		State:         RoundRejected,
		StartedAt:     ended.Add(-time.Minute),
		EndedAt:       &ended,
		Verdict: &Verdict{
			Decision: DecisionReject,
			Summary:  "date form ignored",
			Findings: []Finding{{Severity: SeverityBlocker, File: "retry.go", Line: 88,
				Detail: "Atoi on a value that may be an HTTP-date"}},
		},
	})
	m.saveLocked(rec)
}

func nodeByID(t *testing.T, tree Tree, id string) TreeNode {
	t.Helper()
	for _, n := range tree.Nodes {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("tree has no node %s: %+v", id, tree.Nodes)
	return TreeNode{}
}

func flowIDs(flows []Flow) string {
	ids := make([]string, 0, len(flows))
	for _, f := range flows {
		ids = append(ids, f.ID)
	}
	return strings.Join(ids, ", ")
}
