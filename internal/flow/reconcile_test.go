package flow

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/queue"
)

// These tests drive the reconciler by calling tick() directly, which is what
// makes every assertion deterministic: the pass is synchronous, the fake
// queue moves only when a test moves it, and the verdict files are written by
// the test instead of by a reviewer. The goroutine that normally calls tick()
// is exercised separately, at the bottom.

// The observer hook must fit queue.Manager.AddChangeObserver with no adapter:
// the daemon registers it directly, and this is the line that fails if either
// signature drifts.
var _ = func(m *Manager, q *queue.Manager) func() { return q.AddChangeObserver(m.Notify) }

const acceptVerdict = `{"verdict": "accept", "summary": "does what was asked"}`

// rejectVerdict is a well-formed rejection carrying one blocker, whose detail
// is the string a fix prompt must be shown to contain.
func rejectVerdict(detail string) string {
	return `{"verdict": "reject", "summary": "not yet",
	  "findings": [{"severity": "blocker", "file": "retry.go", "line": 88,
	                "detail": "` + detail + `"}]}`
}

// newDriven builds a manager over a fake queue and a real store: the store is
// not optional here, because the verdict path is where the whole accept path
// lives.
func newDriven(t *testing.T) (*Manager, *fakeQueue) {
	t.Helper()
	q := newFakeQueue()
	return NewManager(q, newStore(t)), q
}

// startFlow records a flow with the given cap, or the default when rounds is
// zero.
func startFlow(t *testing.T, m *Manager, rounds int) Flow {
	t.Helper()
	req := startReq(t)
	req.MaxRounds = rounds
	f, err := m.Start(req)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return f
}

// recordChanges installs a change listener that also calls back into the
// manager. Re-entering Get from the callback is the assertion that matters:
// if the reconciler ever notified while holding m.mu, this deadlocks and the
// test times out rather than passing quietly.
func recordChanges(t *testing.T, m *Manager) func() []Flow {
	t.Helper()
	var (
		mu   sync.Mutex
		seen []Flow
	)
	m.OnFlowChange(func(f Flow) {
		if _, err := m.Get(f.ID); err != nil {
			t.Errorf("Get from the change callback: %v", err)
		}
		mu.Lock()
		seen = append(seen, f)
		mu.Unlock()
	})
	return func() []Flow {
		mu.Lock()
		defer mu.Unlock()
		return append([]Flow(nil), seen...)
	}
}

func writeVerdict(t *testing.T, m *Manager, flowID string, round, attempt int, body string) {
	t.Helper()
	path := m.verdictPath(flowID, round, attempt)
	if path == "" {
		t.Fatal("manager has no verdict directory")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir verdict dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write verdict: %v", err)
	}
}

// currentRound returns a copy of the flow's last round.
func currentRound(t *testing.T, m *Manager, flowID string) Round {
	t.Helper()
	f, err := m.Get(flowID)
	if err != nil {
		t.Fatalf("Get %s: %v", flowID, err)
	}
	r := f.CurrentRound()
	if r == nil {
		t.Fatalf("flow %s has no rounds", flowID)
	}
	return *r
}

// finishSteps marks the current round's work and review tasks done, which is
// the liveness signal that sends the reconciler looking for a verdict.
func finishSteps(t *testing.T, m *Manager, q *fakeQueue, flowID string) Round {
	t.Helper()
	r := currentRound(t, m, flowID)
	q.setState(t, r.WorkTaskID, queue.StateDone)
	q.setState(t, r.ReviewTaskID, queue.StateDone)
	return r
}

func TestPendingFlowSubmitsRoundOne(t *testing.T) {
	m, q := newDriven(t)
	changes := recordChanges(t, m)
	f := startFlow(t, m, 0)

	m.tick()

	if len(q.batches) != 1 {
		t.Fatalf("submitted %d batches, want exactly 1: %+v", len(q.batches), q.batches)
	}
	batch := q.batches[0]
	if len(batch) != 2 {
		t.Fatalf("round 1 batch has %d specs, want the work task and its review: %+v", len(batch), batch)
	}
	work, review := batch[0], batch[1]

	if work.Title != "f1 r1 implement" || review.Title != "f1 r1 review" {
		t.Errorf("titles = %q / %q, want \"f1 r1 implement\" / \"f1 r1 review\"", work.Title, review.Title)
	}
	for _, s := range batch {
		if s.QueueID != "flow-f1" {
			t.Errorf("spec %q queue = %q, want flow-f1", s.Title, s.QueueID)
		}
		if s.WorkDir != f.WorkDir {
			t.Errorf("spec %q work_dir = %q, want the flow's %q", s.Title, s.WorkDir, f.WorkDir)
		}
	}
	// The review depends on the work by batch-local name, which is what
	// makes the two an atomic round rather than two independent tasks.
	if len(review.After) != 1 || review.After[0] != work.Name || work.Name != "implement" {
		t.Errorf("review.After = %v over work name %q, want [implement]", review.After, work.Name)
	}
	if len(work.After) != 0 {
		t.Errorf("work.After = %v, want the work task to depend on nothing", work.After)
	}
	if work.Prompt != ImplementPrompt(&f) {
		t.Errorf("round 1 work prompt is not ImplementPrompt:\n%s", work.Prompt)
	}
	wantPath := m.verdictPath(f.ID, 1, 1)
	if !strings.HasSuffix(wantPath, "r1-a1-verdict.json") {
		t.Fatalf("verdict path = %q, want it to end r1-a1-verdict.json", wantPath)
	}
	if review.Prompt != ReviewPrompt(&f, wantPath) {
		t.Errorf("review prompt is not ReviewPrompt with the round's verdict path:\n%s", review.Prompt)
	}
	// The reviewer writes the file with a shell redirect, which will not
	// create the directory for it.
	if info, err := os.Stat(filepath.Dir(wantPath)); err != nil || !info.IsDir() {
		t.Errorf("verdict dir %s: %v", filepath.Dir(wantPath), err)
	}

	got, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateRunning {
		t.Errorf("flow state = %q, want running", got.State)
	}
	if len(got.Rounds) != 1 {
		t.Fatalf("flow has %d rounds, want 1", len(got.Rounds))
	}
	r := got.Rounds[0]
	if r.N != 1 || r.WorkTaskID != "t1" || r.ReviewTaskID != "t2" ||
		r.ReviewAttempt != 1 || r.State != RoundRunning || r.StartedAt.IsZero() {
		t.Errorf("round = %+v, want round 1 running over t1/t2 at attempt 1", r)
	}

	if seen := changes(); len(seen) != 1 || seen[0].State != StateRunning {
		t.Errorf("emitted %d changes (%+v), want one running flow", len(seen), seen)
	}

	// A round already submitted is not submitted again: the pass re-derives
	// from the record rather than from what it did last time.
	m.tick()
	if len(q.batches) != 1 {
		t.Errorf("a second pass submitted %d batches, want the first one only", len(q.batches))
	}
}

// TestPlanningSubmitsPlanBeforeRoundOne drives a flow with EnablePlanning
// set through the plan task, in place of the immediate round-1 submission
// TestPendingFlowSubmitsRoundOne covers, and checks round 1 only opens once
// the plan is on record and folded into the implement prompt.
func TestPlanningSubmitsPlanBeforeRoundOne(t *testing.T) {
	m, q := newDriven(t)
	req := startReq(t)
	req.EnablePlanning = true
	req.PlanOpts = TaskOptions{Model: "opus"}
	f, err := m.Start(req)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	m.tick()

	if len(q.batches) != 1 || len(q.batches[0]) != 1 {
		t.Fatalf("submitted batches = %+v, want one batch of one plan task", q.batches)
	}
	planSpec := q.batches[0][0]
	if planSpec.Title != "f1 plan" {
		t.Errorf("plan task title = %q, want \"f1 plan\"", planSpec.Title)
	}
	if planSpec.Opts.Model != "opus" {
		t.Errorf("plan task model = %q, want opus", planSpec.Opts.Model)
	}

	got, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateRunning || got.PlanTaskID != "t1" || len(got.Rounds) != 0 {
		t.Fatalf("flow after plan submitted = %+v, want running with plan_task_id t1 and no rounds", got)
	}

	// Round 1 does not open while the plan task is still running.
	q.setState(t, "t1", queue.StateRunning)
	m.tick()
	if len(q.batches) != 1 {
		t.Fatalf("submitted %d batches while the plan task was running, want 1", len(q.batches))
	}

	planPath := m.planPath(f.ID)
	if err := os.MkdirAll(filepath.Dir(planPath), 0o700); err != nil {
		t.Fatalf("mkdir plan dir: %v", err)
	}
	if err := os.WriteFile(planPath, []byte("1. Parse Retry-After.\n2. Retry once it elapses.\n"), 0o600); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	q.setState(t, "t1", queue.StateDone)
	m.tick()

	got, err = m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Plan != "1. Parse Retry-After.\n2. Retry once it elapses." {
		t.Errorf("flow.Plan = %q, want the trimmed plan file contents", got.Plan)
	}
	if len(got.Rounds) != 1 {
		t.Fatalf("flow has %d rounds after the plan settled, want round 1 opened", len(got.Rounds))
	}
	if len(q.batches) != 2 {
		t.Fatalf("submitted %d batches, want a second batch for round 1", len(q.batches))
	}
	work := q.batches[1][0]
	if work.Prompt != ImplementPrompt(&got) {
		t.Errorf("round 1 work prompt does not include the plan:\n%s", work.Prompt)
	}
	if !strings.Contains(work.Prompt, "## Plan") {
		t.Errorf("round 1 work prompt has no ## Plan section:\n%s", work.Prompt)
	}
}

func TestWorkTaskDoneAloneChangesNothing(t *testing.T) {
	m, q := newDriven(t)
	f := startFlow(t, m, 0)
	m.tick()

	r := currentRound(t, m, f.ID)
	q.setState(t, r.WorkTaskID, queue.StateDone)
	changes := recordChanges(t, m)
	m.tick()

	if got := currentRound(t, m, f.ID); got.State != RoundRunning || got.Verdict != nil {
		t.Errorf("round = %+v, want it still running with no verdict: the queue's own after "+
			"edge releases the review task", got)
	}
	if seen := changes(); len(seen) != 0 {
		t.Errorf("emitted %d changes, want none: a settled work task is not a flow transition", len(seen))
	}
	if len(q.batches) != 1 {
		t.Errorf("submitted %d batches, want no new work", len(q.batches))
	}
}

func TestAcceptVerdictAcceptsTheFlow(t *testing.T) {
	m, q := newDriven(t)
	changes := recordChanges(t, m)
	f := startFlow(t, m, 0)
	m.tick()

	finishSteps(t, m, q, f.ID)
	writeVerdict(t, m, f.ID, 1, 1, acceptVerdict)
	m.tick()

	got, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateAccepted || got.Error != "" || got.EndedAt == nil {
		t.Fatalf("flow = %s / %q / ended %v, want accepted with no error", got.State, got.Error, got.EndedAt)
	}
	r := got.Rounds[0]
	if r.State != RoundAccepted || r.EndedAt == nil {
		t.Errorf("round = %+v, want accepted and ended", r)
	}
	if r.Verdict == nil || r.Verdict.Decision != DecisionAccept || r.Verdict.Summary != "does what was asked" {
		t.Errorf("round verdict = %+v, want the parsed accept", r.Verdict)
	}
	if len(q.batches) != 2 {
		t.Errorf("submitted %d batches, want the round and its commit task but no round 2 after an accept", len(q.batches))
	}
	commit := q.batches[1]
	if len(commit) != 1 || commit[0].Prompt != CommitPrompt(&got) {
		t.Errorf("second batch = %+v, want the one-task commit batch", commit)
	}
	// running, then accepted: two transitions, and nothing observes an
	// accepted round under a running flow in between.
	seen := changes()
	if len(seen) != 2 || seen[1].State != StateAccepted {
		t.Errorf("emitted %+v, want [running, accepted]", statesOf(seen))
	}
	if len(seen) == 2 && seen[1].Rounds[0].State != RoundAccepted {
		t.Errorf("the accepted frame carries round state %q, want accepted", seen[1].Rounds[0].State)
	}
}

func TestRejectSubmitsTheNextRoundWithTheFindings(t *testing.T) {
	m, q := newDriven(t)
	changes := recordChanges(t, m)
	f := startFlow(t, m, 0)
	m.tick()

	const detail = "Atoi on a Retry-After that may be an HTTP-date"
	finishSteps(t, m, q, f.ID)
	writeVerdict(t, m, f.ID, 1, 1, rejectVerdict(detail))
	m.tick()

	got, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateRunning {
		t.Fatalf("flow state = %q, want it still running below the cap", got.State)
	}
	if len(got.Rounds) != 2 {
		t.Fatalf("flow has %d rounds, want round 2 submitted: %+v", len(got.Rounds), got.Rounds)
	}
	if r1 := got.Rounds[0]; r1.State != RoundRejected || r1.Verdict == nil ||
		r1.Verdict.Decision != DecisionReject || r1.EndedAt == nil {
		t.Errorf("round 1 = %+v (verdict %+v), want rejected and settled", r1, r1.Verdict)
	}
	r2 := got.Rounds[1]
	if r2.N != 2 || r2.State != RoundRunning || r2.ReviewAttempt != 1 ||
		r2.WorkTaskID != "t3" || r2.ReviewTaskID != "t4" {
		t.Errorf("round 2 = %+v, want a fresh running round over t3/t4", r2)
	}

	if len(q.batches) != 2 {
		t.Fatalf("submitted %d batches, want round 2's: %+v", len(q.batches), q.batches)
	}
	batch := q.batches[1]
	if len(batch) != 2 || batch[0].Name != "fix" || batch[0].Title != "f1 r2 fix" ||
		batch[1].Title != "f1 r2 review" || len(batch[1].After) != 1 || batch[1].After[0] != "fix" {
		t.Fatalf("round 2 batch = %+v, want a fix task and its review", batch)
	}
	// The findings reach the fixer by prompt text; nothing else carries them.
	if !strings.Contains(batch[0].Prompt, detail) {
		t.Errorf("fix prompt does not carry the finding %q:\n%s", detail, batch[0].Prompt)
	}
	if !strings.Contains(batch[0].Prompt, "This is round 2") {
		t.Errorf("fix prompt does not say which round it is:\n%s", batch[0].Prompt)
	}
	// Round 2's reviewer writes to round 2's own path.
	if want := m.verdictPath(f.ID, 2, 1); !strings.Contains(batch[1].Prompt, want) {
		t.Errorf("round 2 review prompt does not name %s", want)
	}

	if seen := changes(); len(seen) != 3 {
		t.Errorf("emitted %+v, want [running, round-1-rejected, round-2-submitted]", statesOf(seen))
	}
}

func TestRejectAtTheCapRejectsTheFlow(t *testing.T) {
	m, q := newDriven(t)
	f := startFlow(t, m, 1)
	m.tick()

	finishSteps(t, m, q, f.ID)
	writeVerdict(t, m, f.ID, 1, 1, rejectVerdict("still wrong"))
	m.tick()

	got, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateRejected || got.EndedAt == nil {
		t.Fatalf("flow = %s (ended %v), want rejected at the cap", got.State, got.EndedAt)
	}
	if got.Error != "" {
		t.Errorf("flow error = %q, want none: a rejection is an outcome, not a failure", got.Error)
	}
	if len(got.Rounds) != 1 || got.Rounds[0].Verdict == nil {
		t.Errorf("rounds = %+v, want round 1 alone with its findings on the record", got.Rounds)
	}
	if len(q.batches) != 1 {
		t.Errorf("submitted %d batches, want nothing past the cap", len(q.batches))
	}
}

func TestMissingVerdictSubmitsOneMoreReview(t *testing.T) {
	m, q := newDriven(t)
	f := startFlow(t, m, 3)
	m.tick()

	first := finishSteps(t, m, q, f.ID)
	// No verdict file at all, which §3.2 treats exactly as an unparseable one.
	m.tick()

	got := currentRound(t, m, f.ID)
	if got.N != 1 || got.State != RoundRunning {
		t.Fatalf("round = %+v, want round 1 still running: a re-review is not a new round", got)
	}
	if got.ReviewAttempt != 2 {
		t.Fatalf("review attempt = %d, want 2", got.ReviewAttempt)
	}
	if got.ReviewTaskID == first.ReviewTaskID || got.ReviewTaskID != "t3" {
		t.Errorf("review task = %q, want the newly submitted t3 (was %q)", got.ReviewTaskID, first.ReviewTaskID)
	}
	if got.WorkTaskID != first.WorkTaskID {
		t.Errorf("work task = %q, want the original %q: the work is already done", got.WorkTaskID, first.WorkTaskID)
	}
	// The synthetic verdict is what tells an operator what happened.
	if got.Verdict == nil || got.Verdict.Decision != DecisionReject || len(got.Verdict.Findings) != 1 {
		t.Fatalf("round verdict = %+v, want a synthetic reject with one finding", got.Verdict)
	}
	fd := got.Verdict.Findings[0]
	if fd.Severity != SeverityMajor {
		t.Errorf("synthetic finding severity = %q, want major", fd.Severity)
	}
	if path := m.verdictPath(f.ID, 1, 1); !strings.Contains(fd.Detail, path) {
		t.Errorf("synthetic finding %q does not name the path %s", fd.Detail, path)
	}

	if len(q.batches) != 2 {
		t.Fatalf("submitted %d batches, want the re-review: %+v", len(q.batches), q.batches)
	}
	batch := q.batches[1]
	if len(batch) != 1 {
		t.Fatalf("re-review batch = %+v, want one review task and no new work", batch)
	}
	if len(batch[0].After) != 0 {
		t.Errorf("re-review depends on %v, want nothing: the work is already done", batch[0].After)
	}
	if batch[0].Title != "f1 r1 review a2" {
		t.Errorf("re-review title = %q, want it to name the attempt", batch[0].Title)
	}
	if want := m.verdictPath(f.ID, 1, 2); !strings.Contains(batch[0].Prompt, want) {
		t.Errorf("re-review prompt does not name %s, so it could read the dead attempt's file", want)
	}

	// The second attempt is not re-submitted while it is still running.
	m.tick()
	if len(q.batches) != 2 {
		t.Errorf("submitted %d batches, want no third", len(q.batches))
	}
}

func TestTwoUnparseableVerdictsErrorTheFlow(t *testing.T) {
	m, q := newDriven(t)
	f := startFlow(t, m, 3)
	m.tick()

	finishSteps(t, m, q, f.ID)
	// Attempt 1: present but not a verdict. Attempt 2: never written.
	writeVerdict(t, m, f.ID, 1, 1, `{"verdict": "looks good to me"}`)
	m.tick()

	second := currentRound(t, m, f.ID)
	if second.ReviewAttempt != 2 {
		t.Fatalf("review attempt = %d, want the re-review submitted", second.ReviewAttempt)
	}
	q.setState(t, second.ReviewTaskID, queue.StateDone)
	changes := recordChanges(t, m)
	m.tick()

	got, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateErrored || got.EndedAt == nil {
		t.Fatalf("flow = %s (ended %v), want errored", got.State, got.EndedAt)
	}
	if got.Error != unparseableAfterTwo {
		t.Errorf("flow error = %q, want %q", got.Error, unparseableAfterTwo)
	}
	if r := got.Rounds[0]; r.State != RoundErrored || r.Verdict == nil {
		t.Errorf("round = %+v (verdict %+v), want errored with the synthetic verdict kept", r, r.Verdict)
	}
	if len(q.batches) != 2 {
		t.Errorf("submitted %d batches, want no third review: attempts are capped at 2", len(q.batches))
	}
	// Nothing more happens, ever: the flow is terminal, so later passes skip it.
	m.tick()
	m.tick()
	if seen := changes(); len(seen) != 1 {
		t.Errorf("emitted %+v after the flow errored, want the single errored frame", statesOf(seen))
	}
}

func TestFailedTaskErrorsTheFlow(t *testing.T) {
	for _, tc := range []struct {
		name string
		// step picks which of the round's tasks fails.
		step func(r Round) string
	}{
		{"work task", func(r Round) string { return r.WorkTaskID }},
		{"review task", func(r Round) string { return r.ReviewTaskID }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, q := newDriven(t)
			f := startFlow(t, m, 3)
			m.tick()

			r := currentRound(t, m, f.ID)
			// The work task is done in both cases, so the review branch is
			// actually reached in the second.
			q.setState(t, r.WorkTaskID, queue.StateDone)
			failed := tc.step(r)
			q.setState(t, failed, queue.StateFailed)
			q.setError(t, failed, "agent timed out")
			m.tick()

			got, err := m.Get(f.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.State != StateErrored || got.EndedAt == nil {
				t.Fatalf("flow = %s (ended %v), want errored", got.State, got.EndedAt)
			}
			if !strings.Contains(got.Error, failed) || !strings.Contains(got.Error, "agent timed out") {
				t.Errorf("flow error = %q, want it to name task %s and the reason", got.Error, failed)
			}
			if r := got.Rounds[0]; r.State != RoundErrored || r.EndedAt == nil {
				t.Errorf("round = %+v, want it graded errored under a finished flow", r)
			}
			if len(q.batches) != 1 {
				t.Errorf("submitted %d batches, want nothing after the failure", len(q.batches))
			}
		})
	}
}

func TestExternallyCancelledTaskCancelsTheFlow(t *testing.T) {
	m, q := newDriven(t)
	f := startFlow(t, m, 3)
	m.tick()

	r := currentRound(t, m, f.ID)
	q.setState(t, r.WorkTaskID, queue.StateDone)
	// `singl queue cancel --id t2` behind the flow's back.
	q.setState(t, r.ReviewTaskID, queue.StateCancelled)
	m.tick()

	got, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateCancelled || got.EndedAt == nil {
		t.Fatalf("flow = %s (ended %v), want cancelled: continuing would review work "+
			"someone deliberately stopped", got.State, got.EndedAt)
	}
	if !strings.Contains(got.Error, r.ReviewTaskID) {
		t.Errorf("flow error = %q, want it to name the cancelled task %s", got.Error, r.ReviewTaskID)
	}
	if len(q.batches) != 1 {
		t.Errorf("submitted %d batches, want nothing after the cancellation", len(q.batches))
	}
}

func TestMissingTaskErrorsTheFlow(t *testing.T) {
	m, q := newDriven(t)
	f := startFlow(t, m, 3)
	m.tick()

	// `queue remove --queue flow-f1` on a live flow: the round can never
	// settle, so polling it forever is the one thing not to do.
	r := currentRound(t, m, f.ID)
	q.drop(r.WorkTaskID)
	m.tick()

	got, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateErrored || !strings.Contains(got.Error, r.WorkTaskID) {
		t.Errorf("flow = %s / %q, want errored naming %s", got.State, got.Error, r.WorkTaskID)
	}
}

func TestSubmitForcesWorktreeIsolationOff(t *testing.T) {
	m, q := newDriven(t)
	f := startFlow(t, m, 3)
	// Start refuses use_worktree, so the only way a flow can carry it is a
	// hand-edited state file — which is exactly what this guards.
	m.mu.Lock()
	m.flows[f.ID].Opts.UseWorktree = true
	m.flows[f.ID].ReviewOpts.UseWorktree = true
	m.mu.Unlock()

	m.tick()

	for _, s := range q.batches[0] {
		if s.Opts.UseWorktree {
			t.Errorf("spec %q kept use_worktree: the reviewer would read a different tree "+
				"than the implementer wrote", s.Title)
		}
	}
}

func TestRestartRederivesFromPersistedState(t *testing.T) {
	store := newStore(t)
	q := newFakeQueue()
	first := NewManager(q, store)
	f := startFlow(t, first, 3)
	first.tick()

	// The review finished and its verdict is on disk, but the daemon dies
	// before reacting to it. An event-handler design has lost that edge; a
	// reconciler just reads `done` on the next pass.
	finishSteps(t, first, q, f.ID)
	writeVerdict(t, first, f.ID, 1, 1, rejectVerdict("HTTP-date form still unhandled"))

	restarted := NewManager(q, store)
	restarted.Restore()
	restarted.tick()

	got, err := restarted.Get(f.ID)
	if err != nil {
		t.Fatalf("Get after Restore: %v", err)
	}
	if len(got.Rounds) != 2 || got.Rounds[0].State != RoundRejected || got.State != StateRunning {
		t.Fatalf("restored flow = %s with %d rounds (round 1 %s), want round 1 rejected and round 2 running",
			got.State, len(got.Rounds), got.Rounds[0].State)
	}
	if !strings.Contains(q.batches[1][0].Prompt, "HTTP-date form still unhandled") {
		t.Errorf("round 2's fix prompt does not carry the verdict read after the restart")
	}

	// The other half of §5: the daemon died after recording the verdict but
	// before submitting the next round. advanceOnce makes exactly one
	// transition, so this reproduces that record precisely.
	store2 := newStore(t)
	q2 := newFakeQueue()
	mid := NewManager(q2, store2)
	g := startFlow(t, mid, 3)
	mid.tick()
	finishSteps(t, mid, q2, g.ID)
	writeVerdict(t, mid, g.ID, 1, 1, rejectVerdict("one more thing"))
	if _, changed := mid.advanceOnce(g.ID); !changed {
		t.Fatal("advanceOnce recorded no transition for a settled review")
	}
	if n := len(q2.batches); n != 1 {
		t.Fatalf("submitted %d batches, want round 2 not yet submitted", n)
	}

	resumed := NewManager(q2, store2)
	resumed.Restore()
	resumed.tick()
	if got, err := resumed.Get(g.ID); err != nil || len(got.Rounds) != 2 {
		t.Errorf("resumed flow = %+v, %v; want round 2 submitted from the settled record", got, err)
	}
}

func TestCapCountsRoundsNotReviewAttempts(t *testing.T) {
	m, q := newDriven(t)
	f := startFlow(t, m, 2)
	m.tick()

	// Round 1 burns both review attempts: the first produces nothing, the
	// second a genuine rejection. That still counts as one round.
	finishSteps(t, m, q, f.ID)
	m.tick()
	second := currentRound(t, m, f.ID)
	if second.ReviewAttempt != 2 || second.N != 1 {
		t.Fatalf("round = %+v, want round 1 on attempt 2", second)
	}
	q.setState(t, second.ReviewTaskID, queue.StateDone)
	writeVerdict(t, m, f.ID, 1, 2, rejectVerdict("first round findings"))
	m.tick()

	got, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateRunning || len(got.Rounds) != 2 {
		t.Fatalf("flow = %s with %d rounds, want round 2 running: a re-review does not "+
			"consume a round", got.State, len(got.Rounds))
	}

	// Round 2 is the cap.
	finishSteps(t, m, q, f.ID)
	writeVerdict(t, m, f.ID, 2, 1, rejectVerdict("second round findings"))
	m.tick()

	got, err = m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateRejected {
		t.Fatalf("flow = %s, want rejected at the cap", got.State)
	}
	if len(got.Rounds) != 2 || got.Rounds[1].N != 2 {
		t.Errorf("rounds = %+v, want exactly two", got.Rounds)
	}
	// Three submissions in all — round 1, its re-review, round 2 — and the
	// rejection at the cap adds nothing.
	if len(q.batches) != 3 {
		t.Errorf("submitted %d batches, want round 1 + its re-review + round 2", len(q.batches))
	}
}

func TestTickIsRaceFreeAgainstCancel(t *testing.T) {
	m, q := newDriven(t)
	recordChanges(t, m)

	var ids []string
	for i := 0; i < 4; i++ {
		f := startFlow(t, m, 5)
		ids = append(ids, f.ID)
	}
	m.tick()
	// Give every pass something real to do: each flow's review has landed a
	// rejection, so a tick settles a round and submits the next one.
	for _, id := range ids {
		finishSteps(t, m, q, id)
		writeVerdict(t, m, id, 1, 1, rejectVerdict("concurrent findings"))
	}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			m.tick()
		}
	}()
	go func() {
		defer wg.Done()
		for _, id := range ids {
			if err := m.Cancel(id); err != nil {
				t.Errorf("Cancel %s: %v", id, err)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			for _, id := range ids {
				if _, err := m.Get(id); err != nil {
					t.Errorf("Get %s: %v", id, err)
				}
				if _, err := m.Tree(id); err != nil {
					t.Errorf("Tree %s: %v", id, err)
				}
			}
		}
	}()
	wg.Wait()

	for _, id := range ids {
		got, err := m.Get(id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if got.State != StateCancelled {
			t.Errorf("flow %s = %s, want cancelled: Cancel wins over a concurrent pass", id, got.State)
		}
		for _, r := range got.Rounds {
			if !r.State.Terminal() {
				t.Errorf("flow %s round %d = %s under a cancelled flow, want it settled", id, r.N, r.State)
			}
		}
	}
	// Whatever the interleaving, a task the flow recorded is not left
	// running, and no task it never recorded was touched.
	recorded := make(map[string]bool)
	for _, id := range ids {
		f, _ := m.Get(id)
		for _, tid := range recordedTaskIDs(&f) {
			recorded[tid] = true
		}
	}
	for _, tid := range q.cancelledIDs() {
		if !recorded[tid] {
			t.Errorf("cancelled task %s is not one the flows recorded", tid)
		}
	}
}

func TestReconcilerLifecycle(t *testing.T) {
	m, _ := newDriven(t)
	// Never started: Stop must not wait for a goroutine that will never
	// close its done channel, and Wake must not block on a buffered
	// channel nobody drains.
	m.Wake()
	if !m.StopReconciler() {
		t.Error("StopReconciler on a manager that never started returned false")
	}
	if !m.StopReconciler() {
		t.Error("StopReconciler is not idempotent")
	}

	m2, _ := newDriven(t)
	// Long enough that only a Wake can drive the pass under test.
	m2.tickInterval = time.Hour
	f := startFlow(t, m2, 3)
	m2.StartReconciler()
	m2.StartReconciler() // idempotent
	t.Cleanup(func() { m2.StopReconciler() })

	m2.Notify(queue.Task{QueueID: f.QueueID})
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, err := m2.Get(f.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.State == StateRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("flow is still %s two seconds after a Notify", got.State)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !m2.StopReconciler() {
		t.Error("StopReconciler did not drain within its deadline")
	}
}

func TestNotifyIgnoresTasksOutsideAFlowQueue(t *testing.T) {
	m, _ := newDriven(t)
	m.Notify(queue.Task{QueueID: "release-prep"})
	if len(m.wake) != 0 {
		t.Error("a task change in an unrelated queue woke the reconciler")
	}
	m.Notify(queue.Task{QueueID: "flow-f1"})
	if len(m.wake) != 1 {
		t.Error("a flow queue's task change did not wake the reconciler")
	}
	// Capacity 1: a burst coalesces rather than queueing redundant passes.
	m.Notify(queue.Task{QueueID: "flow-f1"})
	m.Wake()
	if len(m.wake) != 1 {
		t.Errorf("wake channel holds %d, want a single coalesced wake", len(m.wake))
	}
}

// statesOf renders a change sequence for a failure message.
func statesOf(flows []Flow) []State {
	out := make([]State, 0, len(flows))
	for _, f := range flows {
		out = append(out, f.State)
	}
	return out
}
