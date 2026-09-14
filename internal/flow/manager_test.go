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
	req.IssueKey = "  PROJ-123  "

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
	if f.IssueKey != "PROJ-123" {
		t.Errorf("IssueKey = %q, want it trimmed", f.IssueKey)
	}
}

// TestStartIssueKeyOptional pins IssueKey as purely informational: a flow
// started without one — the ordinary --prompt path — records no key and
// Start does not require or fetch one.
func TestStartIssueKeyOptional(t *testing.T) {
	m, _ := newManager(t)
	f, err := m.Start(startReq(t))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if f.IssueKey != "" {
		t.Errorf("IssueKey = %q, want empty when the request carries none", f.IssueKey)
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

// setError sets a task's error text, which is what the reconciler quotes when
// a step fails.
func (q *fakeQueue) setError(t *testing.T, id, msg string) {
	t.Helper()
	q.mu.Lock()
	defer q.mu.Unlock()
	task, ok := q.tasks[id]
	if !ok {
		t.Fatalf("fakeQueue has no task %s", id)
	}
	task.Error = msg
}

// RetryStep is the operation queue.Retry alone cannot be: these tests prove
// the flow actually notices a retried step, which is what TestRetryRestartsADoneTask
// in internal/queue's own manager_test.go cannot show — that test only
// proves the queue will re-run a done task, never that anything reads the
// re-run's result.

// TestRetryStepRedoesTheWorkOfATerminalFlow retries a done work step of a
// flow that already ended rejected at its cap, and shows the flow does not
// just re-run the step but re-reviews it and reads the new verdict: the
// thing queue.Retry alone cannot do, because rounds.go settles a round
// within one tick of its review going done and apply's terminal guard then
// refuses every further transition on it.
func TestRetryStepRedoesTheWorkOfATerminalFlow(t *testing.T) {
	m, q := newDriven(t)
	f := startFlow(t, m, 1)
	m.tick()

	r1 := finishSteps(t, m, q, f.ID)
	writeVerdict(t, m, f.ID, 1, 1, rejectVerdict("still wrong"))
	m.tick()

	got, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateRejected {
		t.Fatalf("flow state = %s, want rejected at the cap before the retry", got.State)
	}

	out, err := m.RetryStep(f.ID, r1.WorkTaskID)
	if err != nil {
		t.Fatalf("RetryStep(work): %v", err)
	}
	if out.State != StateRunning || out.Error != "" || out.EndedAt != nil {
		t.Fatalf("flow after retry = %s / %q / ended %v, want running with no error and not ended",
			out.State, out.Error, out.EndedAt)
	}
	r := out.CurrentRound()
	if r.State != RoundRunning || r.Verdict != nil || r.N != 1 {
		t.Fatalf("round after retry = %+v, want round 1 running with its verdict cleared", r)
	}
	if r.WorkTaskID == r1.WorkTaskID || r.ReviewTaskID == r1.ReviewTaskID {
		t.Fatalf("round after retry = %+v, want fresh task IDs, not the old %s/%s",
			r, r1.WorkTaskID, r1.ReviewTaskID)
	}
	if len(q.batches) != 2 {
		t.Fatalf("submitted %d batches, want the original round plus the retry's", len(q.batches))
	}
	retryBatch := q.batches[1]
	if len(retryBatch) != 2 || retryBatch[1].After[0] != retryBatch[0].Name {
		t.Fatalf("retry batch = %+v, want a fresh work task and a review depending on it", retryBatch)
	}

	// The crux: a fresh accept on the retried work now actually lands. Under
	// queue.Retry alone this verdict would never be read — the flow settled
	// on the old rejection and apply's terminal guard would refuse to move
	// it again.
	finishSteps(t, m, q, f.ID)
	writeVerdict(t, m, f.ID, 1, 1, acceptVerdict)
	m.tick()

	final, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.State != StateAccepted {
		t.Fatalf("flow state after the retried round's accept = %s, want accepted", final.State)
	}
	if rr := final.CurrentRound(); rr.Verdict == nil || rr.Verdict.Decision != DecisionAccept {
		t.Fatalf("round verdict = %+v, want the retried review's accept", rr.Verdict)
	}
}

// TestRetryStepRedoesTheReviewAlone retries only a done round's review step,
// leaving the work step exactly as it was, and shows the flow reads the
// fresh verdict from the same per-attempt path the first review wrote to.
func TestRetryStepRedoesTheReviewAlone(t *testing.T) {
	m, q := newDriven(t)
	f := startFlow(t, m, 1)
	m.tick()

	r1 := finishSteps(t, m, q, f.ID)
	writeVerdict(t, m, f.ID, 1, 1, rejectVerdict("wrong verdict"))
	m.tick()

	out, err := m.RetryStep(f.ID, r1.ReviewTaskID)
	if err != nil {
		t.Fatalf("RetryStep(review): %v", err)
	}
	r := out.CurrentRound()
	if r.WorkTaskID != r1.WorkTaskID {
		t.Fatalf("work task = %s, want the untouched original %s", r.WorkTaskID, r1.WorkTaskID)
	}
	if r.ReviewTaskID == r1.ReviewTaskID || r.ReviewAttempt != 1 {
		t.Fatalf("round after retry = %+v, want a fresh review task still at attempt 1", r)
	}

	q.setState(t, r.ReviewTaskID, queue.StateDone)
	writeVerdict(t, m, f.ID, 1, 1, acceptVerdict)
	m.tick()

	final, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.State != StateAccepted {
		t.Fatalf("flow state = %s, want accepted from the retried review's fresh verdict", final.State)
	}
}

// TestRetryStepRefusesWhileAnotherFlowTaskIsLive is the concurrency-safety
// finding: a flow's rounds all run in one WorkDir with worktree isolation
// forced off, so retrying a done step while any other task the flow owns
// could still run risks two agents editing that tree at once.
func TestRetryStepRefusesWhileAnotherFlowTaskIsLive(t *testing.T) {
	m, q := newDriven(t)
	f := startFlow(t, m, 3)
	m.tick()

	r1 := currentRound(t, m, f.ID)
	// Work done, review still blocked — the fake queue never advances a
	// dependent on its own, which stands in for "review is about to run".
	q.setState(t, r1.WorkTaskID, queue.StateDone)

	_, err := m.RetryStep(f.ID, r1.WorkTaskID)
	if !errors.Is(err, ErrNotRetryable) {
		t.Fatalf("RetryStep while the review can still run: err = %v, want ErrNotRetryable", err)
	}
	if len(q.batches) != 1 {
		t.Fatalf("submitted %d batches, want the retry refused before anything was queued", len(q.batches))
	}
}

// TestRetryStepRefusesAnEarlierRound is the other half of the finding: only
// the flow's current round is retryable. An earlier round's fix prompts and
// findings are already baked into every round after it, so reopening it
// would rewrite history those later rounds depend on.
func TestRetryStepRefusesAnEarlierRound(t *testing.T) {
	m, q := newDriven(t)
	f := startFlow(t, m, 3)
	m.tick()

	r1 := finishSteps(t, m, q, f.ID)
	writeVerdict(t, m, f.ID, 1, 1, rejectVerdict("round 1 findings"))
	m.tick() // submits round 2

	if got, err := m.Get(f.ID); err != nil || len(got.Rounds) != 2 {
		t.Fatalf("Get: %v, rounds=%+v, want round 2 submitted", err, got.Rounds)
	}

	_, err := m.RetryStep(f.ID, r1.WorkTaskID)
	if !errors.Is(err, ErrNotRetryable) {
		t.Fatalf("RetryStep on round 1 while round 2 is current: err = %v, want ErrNotRetryable", err)
	}
	if !strings.Contains(err.Error(), "not its current round") {
		t.Errorf("error = %q, want it to say round 1 is not current", err)
	}
}

// TestRetryStepRefusesWhileTheCommitTaskIsLive is the hole in the liveness
// check the record alone cannot close: an accept fires a commit task that
// is never recorded on the flow (submitCommitTask is fire-and-forget), and
// RetryStep is the one operation that acts on an accepted flow. The TUI
// shows the round done the instant the verdict lands, while the committer
// is still running in the same tree — so the check must read the flow's
// queue, not just recordedTaskIDs.
func TestRetryStepRefusesWhileTheCommitTaskIsLive(t *testing.T) {
	m, q := newDriven(t)
	f := startFlow(t, m, 1)
	m.tick()

	r1 := finishSteps(t, m, q, f.ID)
	writeVerdict(t, m, f.ID, 1, 1, acceptVerdict)
	m.tick()

	got, err := m.Get(f.ID)
	if err != nil || got.State != StateAccepted {
		t.Fatalf("Get: %v, state = %s, want accepted", err, got.State)
	}
	if len(q.batches) != 2 || len(q.batches[1]) != 1 {
		t.Fatalf("submitted batches = %+v, want the round and its commit task", q.batches)
	}
	// The fake queue leaves a fresh task pending, which for a commit task
	// is exactly the "still to run in this tree" the TUI cannot see.
	commitID := "t3"
	if _, err := q.Get(commitID); err != nil {
		t.Fatalf("commit task %s not in the queue: %v", commitID, err)
	}

	for _, step := range []string{r1.WorkTaskID, r1.ReviewTaskID} {
		_, err := m.RetryStep(f.ID, step)
		if !errors.Is(err, ErrNotRetryable) {
			t.Fatalf("RetryStep(%s) with the commit task pending: err = %v, want ErrNotRetryable", step, err)
		}
		if !strings.Contains(err.Error(), commitID) {
			t.Errorf("error = %q, want it to name the live commit task %s", err, commitID)
		}
	}
	if len(q.batches) != 2 {
		t.Fatalf("submitted %d batches, want the retry refused before anything was queued", len(q.batches))
	}

	// Once the committer has finished, the same retry goes through.
	q.setState(t, commitID, queue.StateDone)
	out, err := m.RetryStep(f.ID, r1.WorkTaskID)
	if err != nil {
		t.Fatalf("RetryStep after the commit task settled: %v", err)
	}
	if out.State != StateRunning || out.CurrentRound().WorkTaskID == r1.WorkTaskID {
		t.Fatalf("flow after retry = %+v, want running on a fresh work task", out)
	}
}

// TestRetryStepRefusesAnyLiveTaskInTheFlowQueue: a task an operator queued
// into flow-<id> by hand is in the same tree for the same reason the commit
// task is, and the check reads the queue, so it is refused too.
func TestRetryStepRefusesAnyLiveTaskInTheFlowQueue(t *testing.T) {
	m, q := newDriven(t)
	f := startFlow(t, m, 1)
	m.tick()

	r1 := finishSteps(t, m, q, f.ID)
	writeVerdict(t, m, f.ID, 1, 1, rejectVerdict("nope"))
	m.tick()

	q.seed("stray", f.QueueID, queue.StateRunning)
	if _, err := m.RetryStep(f.ID, r1.WorkTaskID); !errors.Is(err, ErrNotRetryable) {
		t.Fatalf("RetryStep beside a stray running task in the flow's queue: err = %v, want ErrNotRetryable", err)
	}
	// A live task in some OTHER queue is somebody else's tree.
	q.setState(t, "stray", queue.StateDone)
	q.seed("elsewhere", "flow-other", queue.StateRunning)
	if _, err := m.RetryStep(f.ID, r1.WorkTaskID); err != nil {
		t.Fatalf("RetryStep with only another queue's task live: %v", err)
	}
}

// continuedAtRejection drives a one-round flow to its rejection and continues
// it: the record then has round 1 settled rejected under a RUNNING flow with
// round 2 not yet submitted — the exact window in which the reconciler's
// submitRound(2) and an operator's RetryStep can race.
func continuedAtRejection(t *testing.T, m *Manager, q *fakeQueue) Flow {
	t.Helper()
	f := startFlow(t, m, 1)
	m.tick()
	finishSteps(t, m, q, f.ID)
	writeVerdict(t, m, f.ID, 1, 1, rejectVerdict("round 1 findings"))
	m.tick()
	if _, err := m.Continue(f.ID, 1); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	snap, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if snap.State != StateRunning || len(snap.Rounds) != 1 || snap.Rounds[0].State != RoundRejected {
		t.Fatalf("flow after continue = %+v, want running with round 1 settled rejected", snap)
	}
	return snap
}

// TestRetryStepLosesTheRaceToTheNextRound is the round-count hole: retrying
// round N while it is a settled rejection under a running flow, with the
// reconciler's submitRound(N+1) already past its snapshot. Round 1 is
// unchanged by value in that window, so a guard that compared only the round
// would let both writes land — round 1 reopened AND round 2 appended, two
// implementers in one tree. Whichever lands second must lose and cancel the
// tasks it just submitted. Both orderings are driven here through the two
// halves' own entry points with stale snapshots, which is what the race is.
func TestRetryStepLosesTheRaceToTheNextRound(t *testing.T) {
	t.Run("submitRound lands first", func(t *testing.T) {
		m, q := newDriven(t)
		snap := continuedAtRejection(t, m, q)
		stale := snap.Clone()

		// The reconciler's half lands: round 2 is now on the record.
		if _, changed := m.submitRound(&snap, 2); !changed {
			t.Fatal("submitRound(2) from the continued snapshot did not land")
		}
		before := len(q.cancelledIDs())

		// The retry's half runs from the snapshot it took before that.
		_, err := m.resubmitRound(&stale, stale.Rounds[0])
		if !errors.Is(err, errRetryRaced) {
			t.Fatalf("resubmitRound with round 2 already appended: err = %v, want errRetryRaced", err)
		}
		got, gerr := m.Get(snap.ID)
		if gerr != nil {
			t.Fatalf("Get: %v", gerr)
		}
		if len(got.Rounds) != 2 || got.Rounds[0].State != RoundRejected || got.Rounds[0].WorkTaskID != stale.Rounds[0].WorkTaskID {
			t.Fatalf("rounds after the lost retry = %+v, want round 1 untouched and round 2 current", got.Rounds)
		}
		cancelled := q.cancelledIDs()[before:]
		if len(cancelled) != 2 {
			t.Fatalf("cancelled %v after the lost retry, want its two fresh tasks", cancelled)
		}
		for _, id := range cancelled {
			if id == got.Rounds[1].WorkTaskID || id == got.Rounds[1].ReviewTaskID {
				t.Fatalf("the lost retry cancelled round 2's own task %s", id)
			}
		}
	})

	t.Run("retry lands first", func(t *testing.T) {
		m, q := newDriven(t)
		snap := continuedAtRejection(t, m, q)
		stale := snap.Clone()

		out, err := m.RetryStep(snap.ID, snap.Rounds[0].WorkTaskID)
		if err != nil {
			t.Fatalf("RetryStep: %v", err)
		}
		if r := out.CurrentRound(); r.State != RoundRunning || r.N != 1 {
			t.Fatalf("round after retry = %+v, want round 1 reopened", r)
		}
		before := len(q.cancelledIDs())

		// The reconciler's half, from its stale snapshot: len(Rounds) is
		// still 1, so only the last-round-terminal check can refuse it.
		if _, changed := m.submitRound(&stale, 2); changed {
			t.Fatal("submitRound(2) landed on a flow whose round 1 was just reopened")
		}
		got, gerr := m.Get(snap.ID)
		if gerr != nil {
			t.Fatalf("Get: %v", gerr)
		}
		if len(got.Rounds) != 1 || got.Rounds[0].State != RoundRunning {
			t.Fatalf("rounds after the lost submit = %+v, want only the reopened round 1", got.Rounds)
		}
		if cancelled := q.cancelledIDs()[before:]; len(cancelled) != 2 {
			t.Fatalf("cancelled %v after the lost submit, want its two orphaned tasks", cancelled)
		}

		// And the reconciler proper, re-deriving from the record, now sees
		// a running round and waits for it rather than opening round 2.
		batches := len(q.batches)
		m.tick()
		if len(q.batches) != batches {
			t.Fatalf("a tick after the retry submitted %d more batches, want none", len(q.batches)-batches)
		}
	})
}

// writePlanFile puts a plan document where the planner was told to write it.
func writePlanFile(t *testing.T, m *Manager, flowID, body string) {
	t.Helper()
	path := m.planPath(flowID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir plan dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write plan: %v", err)
	}
}

// TestRetryStepRedoesThePlan retries the plan step of a flow whose planner
// failed, and shows the flow reads the new plan and opens round 1 against
// it: the pre-round-1 branch of applyRetry.
func TestRetryStepRedoesThePlan(t *testing.T) {
	m, q := newDriven(t)
	req := startReq(t)
	req.EnablePlanning = true
	f, err := m.Start(req)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	m.tick()

	got, err := m.Get(f.ID)
	if err != nil || got.PlanTaskID == "" {
		t.Fatalf("Get: %v, flow = %+v, want a plan task submitted", err, got)
	}
	planID := got.PlanTaskID
	q.setState(t, planID, queue.StateFailed)
	m.tick()
	if got, _ = m.Get(f.ID); got.State != StateErrored {
		t.Fatalf("flow after the planner failed = %s, want errored", got.State)
	}

	out, err := m.RetryStep(f.ID, planID)
	if err != nil {
		t.Fatalf("RetryStep(plan): %v", err)
	}
	if out.State != StateRunning || out.Error != "" || out.EndedAt != nil {
		t.Fatalf("flow after plan retry = %s / %q / ended %v, want running, no error, not ended",
			out.State, out.Error, out.EndedAt)
	}
	if out.PlanTaskID == planID || out.PlanTaskID == "" || out.Plan != "" || len(out.Rounds) != 0 {
		t.Fatalf("flow after plan retry = %+v, want a fresh plan task, no plan text and no rounds", out)
	}
	if len(q.batches) != 2 || len(q.batches[1]) != 1 || q.batches[1][0].Title != "f1 plan (retry)" {
		t.Fatalf("submitted batches = %+v, want a second one-task plan batch", q.batches)
	}

	writePlanFile(t, m, f.ID, "1. Parse the date form.\n")
	q.setState(t, out.PlanTaskID, queue.StateDone)
	m.tick()

	final, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.Plan != "1. Parse the date form." || len(final.Rounds) != 1 {
		t.Fatalf("flow after the retried plan settled = %+v, want the plan read and round 1 opened", final)
	}
	if !strings.Contains(q.batches[2][0].Prompt, "1. Parse the date form.") {
		t.Errorf("round 1 work prompt does not carry the retried plan:\n%s", q.batches[2][0].Prompt)
	}

	// Now that round 1 exists, the plan is history.
	if _, err := m.RetryStep(f.ID, final.PlanTaskID); !errors.Is(err, ErrNotRetryable) {
		t.Fatalf("RetryStep(plan) after round 1 opened: err = %v, want ErrNotRetryable", err)
	}
}

// TestRetryStepPlanLosesTheRace drives applyRetry's pre-round-1 guard from a
// stale snapshot: the flow read its plan and opened round 1 while the plan
// retry's Add was in flight, so the fresh plan task must be cancelled rather
// than recorded over a plan an implementer has already been given.
func TestRetryStepPlanLosesTheRace(t *testing.T) {
	m, q := newDriven(t)
	req := startReq(t)
	req.EnablePlanning = true
	f, err := m.Start(req)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	m.tick()
	stale, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// The flow moves on: plan lands, round 1 opens.
	writePlanFile(t, m, f.ID, "the plan")
	q.setState(t, stale.PlanTaskID, queue.StateDone)
	m.tick()
	if got, _ := m.Get(f.ID); len(got.Rounds) != 1 {
		t.Fatalf("flow = %+v, want round 1 opened", got)
	}
	before := len(q.cancelledIDs())

	_, err = m.resubmitPlan(&stale)
	if !errors.Is(err, errRetryRaced) {
		t.Fatalf("resubmitPlan from a pre-round-1 snapshot after round 1 opened: err = %v, want errRetryRaced", err)
	}
	got, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.PlanTaskID != stale.PlanTaskID || got.Plan != "the plan" || len(got.Rounds) != 1 {
		t.Fatalf("flow after the lost plan retry = %+v, want it untouched", got)
	}
	cancelled := q.cancelledIDs()[before:]
	if len(cancelled) != 1 || cancelled[0] == stale.PlanTaskID {
		t.Fatalf("cancelled %v after the lost plan retry, want exactly its fresh plan task", cancelled)
	}
}

// TestRetryStepKeepsTheSyntheticVerdictAtAttemptTwo: at review attempt 2 the
// round's verdict is attempt 1's synthetic reject, which readRoundVerdict
// re-records if attempt 2 is unparseable too. A review retry at attempt 2
// must leave it in place, or a second unparseable verdict after the retry
// errors the round with no explanation at all.
func TestRetryStepKeepsTheSyntheticVerdictAtAttemptTwo(t *testing.T) {
	m, q := newDriven(t)
	f := startFlow(t, m, 1)
	m.tick()

	finishSteps(t, m, q, f.ID) // no verdict written: attempt 1 is unparseable
	m.tick()
	r := currentRound(t, m, f.ID)
	if r.ReviewAttempt != 2 || r.Verdict == nil {
		t.Fatalf("round = %+v, want attempt 2 with the synthetic reject recorded", r)
	}
	synthetic := *r.Verdict

	q.setState(t, r.ReviewTaskID, queue.StateDone) // attempt 2 unparseable too
	m.tick()
	if got, _ := m.Get(f.ID); got.State != StateErrored || got.Error != unparseableAfterTwo {
		t.Fatalf("flow = %s / %q, want errored after two unparseable verdicts", got.State, got.Error)
	}

	out, err := m.RetryStep(f.ID, r.ReviewTaskID)
	if err != nil {
		t.Fatalf("RetryStep(review at attempt 2): %v", err)
	}
	rr := out.CurrentRound()
	if rr.ReviewAttempt != 2 || rr.State != RoundRunning {
		t.Fatalf("round after retry = %+v, want still attempt 2 and running", rr)
	}
	if rr.Verdict == nil || !reflect.DeepEqual(*rr.Verdict, synthetic) {
		t.Fatalf("round verdict after retry = %+v, want attempt 1's synthetic reject kept", rr.Verdict)
	}

	// The retried attempt 2 is unparseable again: the operator still has
	// the explanation.
	q.setState(t, rr.ReviewTaskID, queue.StateDone)
	m.tick()
	final, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.State != StateErrored || final.Error != unparseableAfterTwo {
		t.Fatalf("flow = %s / %q, want errored again", final.State, final.Error)
	}
	if v := final.CurrentRound().Verdict; v == nil || !reflect.DeepEqual(*v, synthetic) {
		t.Fatalf("round verdict after the second failure = %+v, want the synthetic reject still on record", v)
	}

	// Whereas a retry at attempt 1 starts clean, as before.
	m2, q2 := newDriven(t)
	f2 := startFlow(t, m2, 1)
	m2.tick()
	r2 := finishSteps(t, m2, q2, f2.ID)
	writeVerdict(t, m2, f2.ID, 1, 1, rejectVerdict("wrong"))
	m2.tick()
	out2, err := m2.RetryStep(f2.ID, r2.ReviewTaskID)
	if err != nil {
		t.Fatalf("RetryStep(review at attempt 1): %v", err)
	}
	if v := out2.CurrentRound().Verdict; v != nil {
		t.Fatalf("round verdict after an attempt-1 review retry = %+v, want cleared", v)
	}
}

// TestRetryStepReportsAQueueRefusalAsItself: a queue that refuses the
// retry's batch is not a race, and the operator must get the queue's own
// complaint rather than be sent looking for one.
func TestRetryStepReportsAQueueRefusalAsItself(t *testing.T) {
	m, q := newDriven(t)
	f := startFlow(t, m, 1)
	m.tick()
	r1 := finishSteps(t, m, q, f.ID)
	writeVerdict(t, m, f.ID, 1, 1, rejectVerdict("wrong"))
	m.tick()

	refusal := fmt.Errorf("%w: no such backend", queue.ErrInvalid)
	q.mu.Lock()
	q.addErr = refusal
	q.mu.Unlock()

	for _, step := range []string{r1.WorkTaskID, r1.ReviewTaskID} {
		_, err := m.RetryStep(f.ID, step)
		if !errors.Is(err, queue.ErrInvalid) {
			t.Fatalf("RetryStep(%s) with Add refusing: err = %v, want the queue's own error", step, err)
		}
		if errors.Is(err, ErrNotRetryable) || strings.Contains(err.Error(), "changed while") {
			t.Errorf("error = %q, reported as a race", err)
		}
	}
	got, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateRejected || got.CurrentRound().WorkTaskID != r1.WorkTaskID {
		t.Fatalf("flow after a refused retry = %+v, want it untouched", got)
	}
}
