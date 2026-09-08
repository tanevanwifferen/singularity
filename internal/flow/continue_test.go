package flow

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/queue"
)

// These tests drive a flow to each of the three continuable endings with the
// same synchronous tick() the other reconciler tests use, then continue it and
// assert what the *next pass* derives from the record — because that is the
// contract a continue actually has to keep. Continue itself submits nothing.

// runToRejection drives a flow with the given cap to a rejection in every
// round, leaving it rejected at the cap. Returns the flow ID.
func runToRejection(t *testing.T, m *Manager, q *fakeQueue, cap int) string {
	t.Helper()
	f := startFlow(t, m, cap)
	for n := 1; n <= cap; n++ {
		m.tick()
		r := currentRound(t, m, f.ID)
		if r.N != n {
			t.Fatalf("round = %d, want %d", r.N, n)
		}
		q.setState(t, r.WorkTaskID, queue.StateDone)
		q.setState(t, r.ReviewTaskID, queue.StateDone)
		writeVerdict(t, m, f.ID, n, 1, rejectVerdict(roundFinding(n)))
		m.tick()
	}
	got, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateRejected {
		t.Fatalf("flow = %s, want rejected at the cap", got.State)
	}
	return f.ID
}

// roundFinding is a per-round finding detail, so a fix prompt can be shown to
// carry the right round's findings and not merely some round's.
func roundFinding(n int) string {
	return "round " + string(rune('0'+n)) + " finding text"
}

func TestContinueExtendsTheSameFlow(t *testing.T) {
	m, q := newDriven(t)
	id := runToRejection(t, m, q, 2)
	before, _ := m.Get(id)
	// Installed only now, so the frames counted below are the continue's.
	changes := recordChanges(t, m)

	got, err := m.Continue(id, 3)
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if got.ID != id {
		t.Errorf("Continue returned flow %q, want the same flow %q: a continue extends, it does not fork",
			got.ID, id)
	}
	if got.State != StateRunning {
		t.Errorf("state = %q, want running", got.State)
	}
	if got.MaxRounds != before.MaxRounds+3 {
		t.Errorf("max rounds = %d, want the old %d raised by 3", got.MaxRounds, before.MaxRounds)
	}
	if got.Error != "" || got.EndedAt != nil {
		t.Errorf("continued flow still carries error %q / ended %v, want both cleared", got.Error, got.EndedAt)
	}
	if len(got.Rounds) != 2 {
		t.Fatalf("rounds = %d, want the two already run kept exactly as they were", len(got.Rounds))
	}
	if got.Goal != before.Goal || got.ReviewGoal != before.ReviewGoal || got.WorkDir != before.WorkDir {
		t.Errorf("continue changed the goal/review goal/work dir; none of them is re-specifiable")
	}
	for i, r := range got.Rounds {
		if r.State != RoundRejected || r.Verdict == nil || r.EndedAt == nil {
			t.Errorf("round %d = %+v, want the settled rejection untouched", i+1, r)
		}
	}
	// Continue is an API transition, not the driver's: the change slot
	// reports the reconciler's work, as it does for Start and Cancel.
	if seen := changes(); len(seen) != 0 {
		t.Errorf("Continue emitted %v, want no flow-change frames", statesOf(seen))
	}

	// The next pass is what opens round 3 — and it does so from the record
	// alone, with the whole history behind it.
	batches := len(q.batches)
	m.tick()
	if len(q.batches) != batches+1 {
		t.Fatalf("submitted %d batches, want exactly one more for round 3", len(q.batches)-batches)
	}
	batch := q.batches[batches]
	if len(batch) != 2 || batch[0].Title != "f1 r3 fix" || batch[1].Title != "f1 r3 review" {
		t.Fatalf("round 3 batch = %+v, want a fix task and its review", batch)
	}
	if !strings.Contains(batch[0].Prompt, roundFinding(2)) {
		t.Errorf("round 3's fix prompt does not carry round 2's findings:\n%s", batch[0].Prompt)
	}
	if !strings.Contains(batch[0].Prompt, "Round 1:") {
		t.Errorf("round 3's fix prompt does not summarise round 1; the history is what a continue is for:\n%s",
			batch[0].Prompt)
	}
	after, _ := m.Get(id)
	if len(after.Rounds) != 3 || after.Rounds[2].N != 3 || after.Rounds[2].ReviewAttempt != 1 {
		t.Errorf("rounds = %+v, want round 3 appended at attempt 1", after.Rounds)
	}
}

func TestContinueDefaultsToThreeRounds(t *testing.T) {
	m, q := newDriven(t)
	id := runToRejection(t, m, q, 1)

	got, err := m.Continue(id, 0)
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if got.MaxRounds != 1+defaultExtraRounds {
		t.Errorf("max rounds = %d, want 1 raised by the default %d", got.MaxRounds, defaultExtraRounds)
	}
}

func TestContinueRefusals(t *testing.T) {
	t.Run("unknown flow", func(t *testing.T) {
		m, _ := newDriven(t)
		if _, err := m.Continue("f99", 3); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("accepted", func(t *testing.T) {
		m, q := newDriven(t)
		f := startFlow(t, m, 3)
		m.tick()
		finishSteps(t, m, q, f.ID)
		writeVerdict(t, m, f.ID, 1, 1, acceptVerdict)
		m.tick()
		if got, _ := m.Get(f.ID); got.State != StateAccepted {
			t.Fatalf("flow = %s, want accepted", got.State)
		}

		_, err := m.Continue(f.ID, 3)
		if !errors.Is(err, ErrNotContinuable) {
			t.Fatalf("err = %v, want ErrNotContinuable", err)
		}
		if !strings.Contains(err.Error(), "nothing to fix") {
			t.Errorf("err = %q, want it to say why an accepted flow is not continuable", err)
		}
		if got, _ := m.Get(f.ID); got.State != StateAccepted {
			t.Errorf("a refused continue moved the flow to %s", got.State)
		}
	})

	for _, tc := range []struct {
		name string
		// tick says whether to submit round 1, which is the difference
		// between a pending flow and a running one.
		tick bool
		want State
	}{
		{"pending", false, StatePending},
		{"running", true, StateRunning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, q := newDriven(t)
			f := startFlow(t, m, 3)
			if tc.tick {
				m.tick()
			}
			if got, _ := m.Get(f.ID); got.State != tc.want {
				t.Fatalf("flow = %s, want %s", got.State, tc.want)
			}

			_, err := m.Continue(f.ID, 3)
			if !errors.Is(err, ErrNotContinuable) {
				t.Fatalf("err = %v, want ErrNotContinuable: a live flow is waited on, not continued", err)
			}
			if !strings.Contains(err.Error(), string(tc.want)) {
				t.Errorf("err = %q, want it to name the state %q", err, tc.want)
			}
			if n := len(q.batches); tc.tick && n != 1 {
				t.Errorf("submitted %d batches, want the refusal to have queued nothing", n)
			}
		})
	}

	t.Run("negative rounds", func(t *testing.T) {
		m, q := newDriven(t)
		id := runToRejection(t, m, q, 1)
		_, err := m.Continue(id, -1)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("already at the ceiling", func(t *testing.T) {
		m, q := newDriven(t)
		id := runToRejection(t, m, q, 1)
		m.mu.Lock()
		m.flows[id].MaxRounds = maxRoundsCap
		m.mu.Unlock()

		_, err := m.Continue(id, 1)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
		// Actionable, not a bare range complaint: no round count would
		// have worked, and the message has to say so.
		if !strings.Contains(err.Error(), "20") || !strings.Contains(err.Error(), "ceiling") {
			t.Errorf("err = %q, want it to name the %d-round ceiling", err, maxRoundsCap)
		}
		if !strings.Contains(err.Error(), "new flow") {
			t.Errorf("err = %q, want it to name what the operator can do instead", err)
		}
		if got, _ := m.Get(id); got.State != StateRejected {
			t.Errorf("a refused continue moved the flow to %s", got.State)
		}
	})

	t.Run("raised cap would pass the ceiling", func(t *testing.T) {
		m, q := newDriven(t)
		id := runToRejection(t, m, q, 1)
		m.mu.Lock()
		m.flows[id].MaxRounds = maxRoundsCap - 1
		m.mu.Unlock()

		_, err := m.Continue(id, 3)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
		if !strings.Contains(err.Error(), "at most 1 more") {
			t.Errorf("err = %q, want it to name the number that would fit", err)
		}
		// One more is exactly what fits.
		if _, err := m.Continue(id, 1); err != nil {
			t.Fatalf("Continue by 1 at %d: %v", maxRoundsCap-1, err)
		}
	})

	t.Run("work dir is gone", func(t *testing.T) {
		m, q := newDriven(t)
		id := runToRejection(t, m, q, 1)
		got, _ := m.Get(id)
		if err := os.RemoveAll(got.WorkDir); err != nil {
			t.Fatalf("remove work dir: %v", err)
		}

		_, err := m.Continue(id, 3)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
		if !strings.Contains(err.Error(), got.WorkDir) {
			t.Errorf("err = %q, want it to name the missing path %s", err, got.WorkDir)
		}
		if after, _ := m.Get(id); after.State != StateRejected || after.MaxRounds != 1 {
			t.Errorf("flow = %s at %d rounds, want the refusal to have changed nothing",
				after.State, after.MaxRounds)
		}
	})

	t.Run("work dir is a file", func(t *testing.T) {
		m, q := newDriven(t)
		id := runToRejection(t, m, q, 1)
		got, _ := m.Get(id)
		if err := os.RemoveAll(got.WorkDir); err != nil {
			t.Fatalf("remove work dir: %v", err)
		}
		if err := os.WriteFile(got.WorkDir, []byte("x"), 0o600); err != nil {
			t.Fatalf("write file: %v", err)
		}
		if _, err := m.Continue(id, 3); !errors.Is(err, ErrInvalid) ||
			!strings.Contains(err.Error(), "not a directory") {
			t.Fatalf("err = %v, want ErrInvalid about a non-directory", err)
		}
	})
}

// Hard case 1: a cancelled flow's trailing round is half run — its work task
// done and its review cancelled. Continuing must settle that round explicitly
// and open a fresh one, never resume the half-finished one.
func TestContinueSettlesACancelledHalfRunRound(t *testing.T) {
	m, q := newDriven(t)
	f := startFlow(t, m, 3)
	m.tick()
	// Round 1 rejects, round 2 opens, and the operator cancels mid-round.
	finishSteps(t, m, q, f.ID)
	writeVerdict(t, m, f.ID, 1, 1, rejectVerdict(roundFinding(1)))
	m.tick()
	r2 := currentRound(t, m, f.ID)
	if r2.N != 2 {
		t.Fatalf("round = %d, want round 2 open", r2.N)
	}
	q.setState(t, r2.WorkTaskID, queue.StateDone)
	if err := m.Cancel(f.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	got, err := m.Continue(f.ID, 2)
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if got.State != StateRunning || got.MaxRounds != 5 || got.Error != "" || got.EndedAt != nil {
		t.Fatalf("flow = %s at %d rounds (err %q, ended %v), want running at 5 with the cancellation cleared",
			got.State, got.MaxRounds, got.Error, got.EndedAt)
	}
	if len(got.Rounds) != 2 {
		t.Fatalf("rounds = %d, want the abandoned round 2 kept, not dropped and not duplicated", len(got.Rounds))
	}
	abandoned := got.Rounds[1]
	if abandoned.State != RoundErrored || abandoned.EndedAt == nil {
		t.Errorf("round 2 = %+v, want it settled errored with an end time", abandoned)
	}
	if abandoned.WorkTaskID != r2.WorkTaskID || abandoned.ReviewTaskID != r2.ReviewTaskID {
		t.Errorf("round 2's task IDs changed to %s/%s, want the abandoned round's own %s/%s",
			abandoned.WorkTaskID, abandoned.ReviewTaskID, r2.WorkTaskID, r2.ReviewTaskID)
	}
	// The explicit reason: a Round has nowhere but its verdict to record
	// one, so the abandonment is written as §3.3's synthetic reject.
	if abandoned.Verdict == nil || abandoned.Verdict.Decision != DecisionReject ||
		len(abandoned.Verdict.Findings) != 1 {
		t.Fatalf("round 2 verdict = %+v, want a synthetic reject saying why it never concluded",
			abandoned.Verdict)
	}
	if d := abandoned.Verdict.Findings[0].Detail; !strings.Contains(d, "never reached a verdict") ||
		!strings.Contains(d, string(StateCancelled)) {
		t.Errorf("abandonment finding = %q, want it to say the round never concluded and why", d)
	}

	// One pass: round 3, with fresh tasks. Nothing is resubmitted into
	// round 2 and nothing waits on its cancelled review.
	m.tick()
	after, _ := m.Get(f.ID)
	if after.State != StateRunning {
		t.Fatalf("flow = %s after one pass, want it still running: the reconciler must not "+
			"re-derive the ending it was continued out of (%q)", after.State, after.Error)
	}
	if len(after.Rounds) != 3 {
		t.Fatalf("rounds = %d, want round 3 opened", len(after.Rounds))
	}
	r3 := after.Rounds[2]
	if r3.N != 3 || r3.State != RoundRunning || r3.ReviewAttempt != 1 {
		t.Errorf("round 3 = %+v, want a fresh running round at attempt 1", r3)
	}
	if r3.WorkTaskID == r2.WorkTaskID || r3.ReviewTaskID == r2.ReviewTaskID {
		t.Errorf("round 3 reuses round 2's tasks (%s/%s)", r3.WorkTaskID, r3.ReviewTaskID)
	}
	if !strings.Contains(q.batches[len(q.batches)-1][0].Prompt, "never reached a verdict") {
		t.Errorf("round 3's fix prompt does not mention the abandoned round, so the fixer does not " +
			"know the tree may be half-done")
	}
	// And it settles from there like any other flow.
	finishSteps(t, m, q, f.ID)
	writeVerdict(t, m, f.ID, 3, 1, acceptVerdict)
	m.tick()
	if end, _ := m.Get(f.ID); end.State != StateAccepted {
		t.Errorf("flow = %s (%q), want the continued flow to accept normally", end.State, end.Error)
	}
}

// The other shapes of a half-run round: both steps cancelled, and a step still
// recorded non-terminal because the cancel never reached the queue. Neither
// may leave a task the queue could still dispatch into the flow's work dir.
func TestContinueStopsLeftoverTasksOfAnAbandonedRound(t *testing.T) {
	for _, tc := range []struct {
		name string
		// live picks the step left running behind the flow's back.
		live func(r Round) string
	}{
		{"work task never stopped", func(r Round) string { return r.WorkTaskID }},
		{"review task never stopped", func(r Round) string { return r.ReviewTaskID }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, q := newDriven(t)
			f := startFlow(t, m, 3)
			m.tick()
			r1 := currentRound(t, m, f.ID)
			if err := m.Cancel(f.ID); err != nil {
				t.Fatalf("Cancel: %v", err)
			}
			// The daemon died before Cancel's queue call landed, so a
			// step is still live under a flow that says it is not.
			stray := tc.live(r1)
			q.setState(t, stray, queue.StateRunning)

			if _, err := m.Continue(f.ID, 1); err != nil {
				t.Fatalf("Continue: %v", err)
			}
			task, err := q.Get(stray)
			if err != nil {
				t.Fatalf("Get %s: %v", stray, err)
			}
			if task.State != queue.StateCancelled {
				t.Errorf("stray task %s = %s, want cancelled before the new round opens: it would "+
					"otherwise be writing into the tree the new round is fixing", stray, task.State)
			}
			// Only the flow's own tasks, and only the live ones.
			for _, id := range q.cancelledIDs() {
				if id != r1.WorkTaskID && id != r1.ReviewTaskID {
					t.Errorf("cancelled %s, which this flow did not create", id)
				}
			}
		})
	}
}

// Hard case 2: §3.3 fired twice, so the flow errored with a synthetic reject
// on its last round. Continuing opens a fresh round that uses those findings
// like any other and gets its own two review attempts.
func TestContinueAfterTwoUnparseableVerdicts(t *testing.T) {
	m, q := newDriven(t)
	f := startFlow(t, m, 1)
	m.tick()
	finishSteps(t, m, q, f.ID)
	m.tick() // attempt 1 produced nothing: synthetic reject, re-review
	second := currentRound(t, m, f.ID)
	if second.ReviewAttempt != 2 {
		t.Fatalf("review attempt = %d, want the re-review submitted", second.ReviewAttempt)
	}
	q.setState(t, second.ReviewTaskID, queue.StateDone)
	m.tick() // attempt 2 produced nothing either: errored
	errored, _ := m.Get(f.ID)
	if errored.State != StateErrored || errored.Error != unparseableAfterTwo {
		t.Fatalf("flow = %s / %q, want errored after two attempts", errored.State, errored.Error)
	}
	synthetic := errored.Rounds[0].Verdict
	if synthetic == nil {
		t.Fatal("round 1 has no synthetic verdict to continue from")
	}

	got, err := m.Continue(f.ID, 2)
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if got.State != StateRunning || got.Error != "" {
		t.Fatalf("flow = %s / %q, want running with the terminal reason cleared", got.State, got.Error)
	}
	// The synthetic verdict is the round's record of what happened and is
	// not rewritten: it is exactly what the next round works from.
	if kept := got.Rounds[0].Verdict; kept == nil || kept.Findings[0].Detail != synthetic.Findings[0].Detail {
		t.Errorf("round 1 verdict = %+v, want the synthetic reject kept verbatim", kept)
	}
	if got.Rounds[0].ReviewAttempt != 2 {
		t.Errorf("round 1 review attempt = %d, want the 2 it actually used", got.Rounds[0].ReviewAttempt)
	}

	m.tick()
	after, _ := m.Get(f.ID)
	if len(after.Rounds) != 2 {
		t.Fatalf("rounds = %d, want round 2 opened", len(after.Rounds))
	}
	fresh := after.Rounds[1]
	if fresh.ReviewAttempt != 1 {
		t.Errorf("round 2 review attempt = %d, want its own two attempts starting at 1", fresh.ReviewAttempt)
	}
	fix := q.batches[len(q.batches)-1][0]
	if !strings.Contains(fix.Prompt, synthetic.Findings[0].Detail) {
		t.Errorf("round 2's fix prompt does not carry round 1's findings:\n%s", fix.Prompt)
	}
	// And §3.3 is available to it in full: its own first attempt failing
	// buys one more review, not an immediate error.
	q.setState(t, fresh.WorkTaskID, queue.StateDone)
	q.setState(t, fresh.ReviewTaskID, queue.StateDone)
	m.tick()
	retry := currentRound(t, m, f.ID)
	if retry.N != 2 || retry.ReviewAttempt != 2 {
		t.Fatalf("round = %+v, want round 2 on its second attempt", retry)
	}
	if got, _ := m.Get(f.ID); got.State != StateRunning {
		t.Errorf("flow = %s, want it still running: the new round's attempts are its own", got.State)
	}
}

// Hard case 3: the reconciler derives the next action from the persisted
// record alone, so a continued flow survives a daemon restart in either half
// of the continuation — and neither half resubmits a round that already
// exists.
func TestContinuedFlowSurvivesARestart(t *testing.T) {
	t.Run("restart before the round is submitted", func(t *testing.T) {
		store := newStore(t)
		q := newFakeQueue()
		first := NewManager(q, store)
		id := runToRejection(t, first, q, 1)
		if _, err := first.Continue(id, 2); err != nil {
			t.Fatalf("Continue: %v", err)
		}
		batches := len(q.batches)

		// The daemon dies with the record continued and nothing submitted.
		restarted := NewManager(q, store)
		restarted.Restore()
		restarted.tick()

		got, err := restarted.Get(id)
		if err != nil {
			t.Fatalf("Get after Restore: %v", err)
		}
		if got.State != StateRunning || got.MaxRounds != 3 || len(got.Rounds) != 2 {
			t.Fatalf("restored flow = %s at %d rounds with %d rounds recorded, want round 2 opened "+
				"from the continued record", got.State, got.MaxRounds, len(got.Rounds))
		}
		if len(q.batches) != batches+1 {
			t.Errorf("submitted %d batches, want exactly round 2", len(q.batches)-batches)
		}
	})

	t.Run("restart after the round is submitted", func(t *testing.T) {
		store := newStore(t)
		q := newFakeQueue()
		first := NewManager(q, store)
		id := runToRejection(t, first, q, 1)
		if _, err := first.Continue(id, 2); err != nil {
			t.Fatalf("Continue: %v", err)
		}
		first.tick()
		before, _ := first.Get(id)
		if len(before.Rounds) != 2 {
			t.Fatalf("rounds = %d, want round 2 submitted before the restart", len(before.Rounds))
		}
		batches := len(q.batches)

		restarted := NewManager(q, store)
		restarted.Restore()
		restarted.tick()
		restarted.tick()

		if len(q.batches) != batches {
			t.Errorf("submitted %d batches after the restart, want none: round 2 already exists",
				len(q.batches)-batches)
		}
		got, _ := restarted.Get(id)
		if len(got.Rounds) != 2 || got.Rounds[1].WorkTaskID != before.Rounds[1].WorkTaskID {
			t.Errorf("rounds = %+v, want the round the dead daemon submitted, not a second one", got.Rounds)
		}
		// And it still finishes: the restored round settles normally.
		r := currentRound(t, restarted, id)
		q.setState(t, r.WorkTaskID, queue.StateDone)
		q.setState(t, r.ReviewTaskID, queue.StateDone)
		writeVerdict(t, restarted, id, 2, 1, acceptVerdict)
		restarted.tick()
		if got, _ := restarted.Get(id); got.State != StateAccepted {
			t.Errorf("flow = %s (%q), want accepted", got.State, got.Error)
		}
	})
}

// Hard case 4: verdict files are per round and per attempt, and a continued
// flow's rounds keep counting up — so a new round can neither collide with an
// earlier round's file nor be settled by one.
func TestContinuedRoundsUseFreshVerdictPaths(t *testing.T) {
	m, q := newDriven(t)
	id := runToRejection(t, m, q, 2)
	if _, err := m.Continue(id, 2); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	m.tick()

	seen := map[string]bool{}
	for _, p := range []string{
		m.verdictPath(id, 1, 1), m.verdictPath(id, 1, 2),
		m.verdictPath(id, 2, 1), m.verdictPath(id, 3, 1),
	} {
		if p == "" || seen[p] {
			t.Fatalf("verdict path %q is empty or a duplicate of an earlier round's", p)
		}
		seen[p] = true
	}
	fresh := m.verdictPath(id, 3, 1)
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Errorf("round 3's verdict file already exists at %s before its reviewer ran", fresh)
	}
	if !strings.Contains(q.batches[len(q.batches)-1][1].Prompt, fresh) {
		t.Errorf("round 3's reviewer was not told to write %s", fresh)
	}

	// Rounds 1 and 2's files are still on disk and say reject. Round 3 must
	// read its own file and nothing else, so a done round 3 with no verdict
	// of its own takes §3.3 rather than inheriting round 2's rejection.
	r3 := finishSteps(t, m, q, id)
	m.tick()
	got := currentRound(t, m, id)
	if got.N != 3 || got.ReviewAttempt != 2 {
		t.Fatalf("round = %+v, want round 3 on a re-review: an earlier round's verdict must not settle it",
			got)
	}
	if got.ReviewTaskID == r3.ReviewTaskID {
		t.Errorf("review task unchanged (%s), want a second attempt submitted", got.ReviewTaskID)
	}
	// Its own file settles it.
	q.setState(t, got.ReviewTaskID, queue.StateDone)
	writeVerdict(t, m, id, 3, 2, acceptVerdict)
	m.tick()
	if end, _ := m.Get(id); end.State != StateAccepted {
		t.Fatalf("flow = %s (%q), want accepted from round 3 attempt 2's own verdict", end.State, end.Error)
	}
}

// A flow cancelled before round 1 was ever submitted has no round to settle;
// continuing it starts from the top rather than wedging on a nil round.
func TestContinueAFlowCancelledWhilePending(t *testing.T) {
	m, q := newDriven(t)
	f := startFlow(t, m, 3)
	if err := m.Cancel(f.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	got, err := m.Continue(f.ID, 1)
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if len(got.Rounds) != 0 || got.State != StateRunning {
		t.Fatalf("flow = %s with %d rounds, want running with none", got.State, len(got.Rounds))
	}
	m.tick()
	after, _ := m.Get(f.ID)
	if len(after.Rounds) != 1 || after.Rounds[0].N != 1 {
		t.Fatalf("rounds = %+v, want round 1 submitted", after.Rounds)
	}
	if q.batches[0][0].Title != "f1 r1 implement" {
		t.Errorf("first task = %q, want the implementer", q.batches[0][0].Title)
	}
}

// An errored flow whose round could not even record a task is continuable too,
// and the continued flow keeps counting from the round it stopped on.
func TestContinueFromAFailedTask(t *testing.T) {
	m, q := newDriven(t)
	f := startFlow(t, m, 3)
	m.tick()
	r := currentRound(t, m, f.ID)
	q.setState(t, r.WorkTaskID, queue.StateFailed)
	q.setError(t, r.WorkTaskID, "agent timed out")
	m.tick()
	if got, _ := m.Get(f.ID); got.State != StateErrored {
		t.Fatalf("flow = %s, want errored", got.State)
	}

	if _, err := m.Continue(f.ID, 3); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	m.tick()
	got, _ := m.Get(f.ID)
	if got.State != StateRunning || len(got.Rounds) != 2 || got.Rounds[1].N != 2 {
		t.Fatalf("flow = %s with rounds %+v, want round 2 running", got.State, got.Rounds)
	}
	if got.Rounds[1].WorkTaskID == r.WorkTaskID {
		t.Errorf("round 2 reuses the failed task %s", r.WorkTaskID)
	}
}

// Continue mutates a record the reconciler is also reading, so it has to hold
// the same lock discipline every other transition does: reads and queue calls
// outside m.mu, the write behind a re-check. This drives all three at once and
// asserts the invariant that survives every interleaving — a flow is either
// continued and running with a raised cap, or refused and left where it was,
// never half of each.
func TestContinueIsRaceFreeAgainstTickAndCancel(t *testing.T) {
	m, q := newDriven(t)
	recordChanges(t, m)

	var ids []string
	for i := 0; i < 4; i++ {
		f := startFlow(t, m, 2)
		ids = append(ids, f.ID)
	}
	m.tick()
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
		for i := 0; i < 50; i++ {
			for _, id := range ids {
				// Refused while the flow is still live, accepted once
				// Cancel has landed: both are correct answers here, and
				// nothing else is.
				f, err := m.Continue(id, 1)
				switch {
				case err == nil:
					if f.State != StateRunning {
						t.Errorf("continued flow %s = %s, want running", id, f.State)
					}
				case errors.Is(err, ErrNotContinuable), errors.Is(err, ErrInvalid):
				default:
					t.Errorf("Continue %s: %v", id, err)
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
		if got.MaxRounds < 2 || got.MaxRounds > maxRoundsCap {
			t.Errorf("flow %s cap = %d, want it raised within the ceiling", id, got.MaxRounds)
		}
		if got.State == StateRunning && (got.EndedAt != nil || got.Error != "") {
			t.Errorf("flow %s is running but still carries %q / %v", id, got.Error, got.EndedAt)
		}
		if got.State.Terminal() && got.EndedAt == nil {
			t.Errorf("flow %s is %s with no end time", id, got.State)
		}
		for _, r := range got.Rounds[:len(got.Rounds)-1] {
			if !r.State.Terminal() {
				t.Errorf("flow %s round %d = %s, want every round but the last settled", id, r.N, r.State)
			}
		}
	}
}
