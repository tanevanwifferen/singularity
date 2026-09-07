package flow

import (
	"fmt"
	"log"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/queue"
)

// The round-advancement state machine: given a snapshot of one flow, decide
// what it does next and write that outcome down. Every entry point here is
// reached from advanceOnce in reconcile.go, and every one of them returns the
// flow as it stands afterwards plus whether anything actually changed.
//
// They all end in apply, the write half of a transition: re-take m.mu,
// re-check everything the snapshot assumed, mutate, persist. The reads and the
// queue.Add happen in between with the lock released, because the reconciler
// is registered as a queue change observer and calling the queue under m.mu
// would put the two locks in opposite orders.

// unparseableAfterTwo is the exact Error a flow carries when §3.3 fired twice
// in one round. Callers match on state, not on this text; an operator reads it.
const unparseableAfterTwo = "unparseable reviewer verdict after 2 attempts"

// afterSettledRound decides what follows a round that already has its verdict.
// Reached two ways: right after settleRound recorded a rejection with rounds
// still on the clock, and after a restart that found a settled round under a
// running flow — the daemon died between recording the verdict and submitting
// the next round. One branch serves both, which is the point of re-deriving.
func (m *Manager) afterSettledRound(snap *Flow, cur *Round) (Flow, bool) {
	switch cur.State {
	case RoundAccepted:
		return m.finish(snap, StateAccepted, "")
	case RoundRejected:
		if cur.N >= snap.MaxRounds {
			return m.finish(snap, StateRejected, "")
		}
		return m.submitRound(snap, cur.N+1)
	default:
		// RoundErrored under a live flow: the round concluded nothing, so
		// there is nothing for a next round to fix from.
		return m.finish(snap, StateErrored, fmt.Sprintf("round %d could not be concluded", cur.N))
	}
}

// advanceRound reads the current round's two tasks and acts on what they say.
//
// The work task goes first, and a work task that settled done produces no
// transition at all: the queue's own after edge releases the review task, so
// the flow has nothing to do but wait for the review to settle. Only the
// liveness failures are the flow's business — see step.
func (m *Manager) advanceRound(snap *Flow, cur *Round) (Flow, bool) {
	if cur.WorkTaskID == "" || cur.ReviewTaskID == "" {
		return m.finish(snap, StateErrored,
			fmt.Sprintf("round %d has no task recorded for one of its steps", cur.N))
	}

	work, ok, out, changed := m.step(snap, cur, cur.WorkTaskID)
	if !ok {
		return out, changed
	}
	if work.State != queue.StateDone {
		return Flow{}, false
	}

	review, ok, out, changed := m.step(snap, cur, cur.ReviewTaskID)
	if !ok {
		return out, changed
	}
	if review.State != queue.StateDone {
		return Flow{}, false
	}
	return m.readRoundVerdict(snap, cur)
}

// step fetches one of a round's tasks and translates the states that end a
// flow. ok is true when the task is present and its state leaves the flow
// alive, and out/changed are then meaningless; when ok is false, out and
// changed are advanceRound's return values.
func (m *Manager) step(snap *Flow, cur *Round, taskID string) (t queue.Task, ok bool, out Flow, changed bool) {
	t, err := m.getTask(taskID)
	if err != nil {
		// Not in the queue any more — `queue remove` on the flow's queue,
		// most likely. The round can never settle, so the flow says so
		// instead of polling a task that will not return.
		f, ch := m.finish(snap, StateErrored,
			fmt.Sprintf("task %s of round %d is no longer in the queue", taskID, cur.N))
		return t, false, f, ch
	}
	switch t.State {
	case queue.StateFailed:
		// Failed after its own retries, or its agent errored or timed out:
		// the queue has already spent MaxRetries by the time it says this.
		f, ch := m.finish(snap, StateErrored,
			fmt.Sprintf("%s failed: %s", describeTask(t), failureReason(t)))
		return t, false, f, ch
	case queue.StateCancelled:
		// Manager.Cancel moves the flow first, so a cancelled task under a
		// live flow is somebody cancelling a step directly. Continuing
		// would mean reviewing, or fixing, work deliberately stopped.
		f, ch := m.finish(snap, StateCancelled,
			fmt.Sprintf("%s was cancelled outside the flow", describeTask(t)))
		return t, false, f, ch
	case queue.StateSkipped:
		// A dependency did not complete. Only the review task can land
		// here, and only when its work task did not finish — which the
		// work-task branch has already reported unless an operator
		// rearranged the graph.
		f, ch := m.finish(snap, StateErrored,
			fmt.Sprintf("%s was skipped: %s", describeTask(t), failureReason(t)))
		return t, false, f, ch
	}
	return t, true, Flow{}, false
}

// readRoundVerdict reads the verdict file for this round and this review
// attempt and applies §3.2/§3.3. The path is per round and per attempt, so no
// attempt can read another's file and a re-run attempt truncates its own.
func (m *Manager) readRoundVerdict(snap *Flow, cur *Round) (Flow, bool) {
	path := m.verdictPath(snap.ID, cur.N, cur.ReviewAttempt)
	v, err := readVerdictFile(path)
	if err == nil {
		if v.Decision == DecisionAccept {
			return m.settleRound(snap, cur, RoundAccepted, v, StateAccepted, "")
		}
		// A rejection at the cap ends the flow in the same transition;
		// below it the flow stays running and the next pass submits the
		// following round.
		flowState := StateRunning
		if cur.N >= snap.MaxRounds {
			flowState = StateRejected
		}
		return m.settleRound(snap, cur, RoundRejected, v, flowState, "")
	}

	// Unparseable or missing — the fail-open this design refuses (§3.3).
	// Never an accept: the reviewer did not review, so the work is not
	// passed, and the round gets exactly one more attempt.
	detail := fmt.Sprintf("the reviewer did not produce a parseable verdict at %s: %v",
		describePath(path), err)
	if cur.ReviewAttempt >= 2 {
		// Second failure in the same round. Attempt 1's synthetic verdict
		// stays on the record — it is what tells an operator what happened
		// — and the round is graded for what it is: concluded nothing.
		return m.settleRound(snap, cur, RoundErrored, cur.Verdict, StateErrored, unparseableAfterTwo)
	}
	return m.submitReReview(snap, cur, detail)
}

// submitRound submits round n: the work task and a review task that depends
// on it, in one atomic Add so a round is never half-queued.
//
// Round 1's work task is the implementer; every later one is a fixer, given
// the same goal plus the previous round's findings and a line per earlier
// round. Both run in the flow's one WorkDir with worktree isolation forced
// off — the engine rewrites an isolated agent's directory to a private
// checkout, so the reviewer would not be reading the tree the implementer
// wrote (§2).
func (m *Manager) submitRound(snap *Flow, n int) (Flow, bool) {
	if m.queue == nil {
		return Flow{}, false
	}

	work := workStepLabel(n)
	prompt := ImplementPrompt(snap)
	if n > 1 {
		prompt = FixPrompt(snap, snap.Rounds[:n-1])
	}
	verdictPath := m.prepareVerdictPath(snap.ID, n, 1)

	tasks, err := m.queue.Add([]queue.TaskSpec{
		{
			Name:    work,
			QueueID: snap.QueueID,
			Title:   fmt.Sprintf("%s r%d %s", snap.ID, n, work),
			Prompt:  prompt,
			WorkDir: snap.WorkDir,
			Opts:    stepOpts(snap.Opts),
		},
		{
			Name:    "review",
			QueueID: snap.QueueID,
			Title:   reviewTitle(snap.ID, n, 1),
			Prompt:  ReviewPrompt(snap, verdictPath),
			WorkDir: snap.WorkDir,
			After:   []string{work},
			Opts:    stepOpts(snap.ReviewOpts),
		},
	})
	if err != nil {
		// Add's refusals are validation, not congestion, so re-submitting
		// the same batch next pass would loop forever on one complaint.
		return m.finish(snap, StateErrored, fmt.Sprintf("submitting round %d: %v", n, err))
	}

	now := time.Now()
	return m.apply(snap, nil, func(f *Flow, _ *Round) bool {
		// Exactly n-1 rounds must still be recorded, or this batch belongs
		// to a flow that moved on while Add was in flight and its tasks are
		// cancelled rather than adopted.
		if len(f.Rounds) != n-1 {
			return false
		}
		f.State = StateRunning
		f.Error = ""
		f.Rounds = append(f.Rounds, &Round{
			N:             n,
			WorkTaskID:    tasks[0].ID,
			ReviewTaskID:  tasks[1].ID,
			ReviewAttempt: 1,
			State:         RoundRunning,
			StartedAt:     now,
		})
		return true
	}, tasks)
}

// submitReReview submits one more review task for the same round — §3.3's
// first half. It depends on nothing: the work is already done, and the point
// is another go at writing a verdict, not at doing the work. The synthetic
// reject is recorded in the same transition, so a record that says
// ReviewAttempt 2 always also says why.
func (m *Manager) submitReReview(snap *Flow, cur *Round, detail string) (Flow, bool) {
	if m.queue == nil {
		return Flow{}, false
	}

	attempt := cur.ReviewAttempt + 1
	verdictPath := m.prepareVerdictPath(snap.ID, cur.N, attempt)
	tasks, err := m.queue.Add([]queue.TaskSpec{{
		QueueID: snap.QueueID,
		Title:   reviewTitle(snap.ID, cur.N, attempt),
		Prompt:  ReviewPrompt(snap, verdictPath),
		WorkDir: snap.WorkDir,
		Opts:    stepOpts(snap.ReviewOpts),
	}})
	if err != nil {
		return m.finish(snap, StateErrored,
			fmt.Sprintf("submitting review attempt %d of round %d: %v", attempt, cur.N, err))
	}

	synthetic := &Verdict{
		Decision: DecisionReject,
		Summary:  "no parseable verdict from the reviewer",
		Findings: []Finding{{Severity: SeverityMajor, Detail: detail}},
	}
	return m.apply(snap, cur, func(_ *Flow, r *Round) bool {
		r.Verdict = synthetic
		r.ReviewAttempt = attempt
		r.ReviewTaskID = tasks[0].ID
		return true
	}, tasks)
}

// settleRound records a round's outcome and, when that outcome ends the flow,
// the flow's too — one transition, so nothing observes an accepted round under
// a running flow. flowState is StateRunning when the flow carries on, in which
// case the next pass submits the following round.
func (m *Manager) settleRound(snap *Flow, cur *Round, round RoundState, v *Verdict,
	flowState State, errText string) (Flow, bool) {
	now := time.Now()
	return m.apply(snap, cur, func(f *Flow, r *Round) bool {
		r.State = round
		if v != nil {
			r.Verdict = v
		}
		r.EndedAt = &now
		if flowState != StateRunning {
			finishLocked(f, flowState, errText, now)
		}
		return true
	}, nil)
}

// finish drives the flow to a terminal state.
func (m *Manager) finish(snap *Flow, state State, errText string) (Flow, bool) {
	now := time.Now()
	return m.apply(snap, nil, func(f *Flow, _ *Round) bool {
		finishLocked(f, state, errText, now)
		return true
	}, nil)
}

// finishLocked is finish's mutation, shared with settleRound. Caller holds
// m.mu. Rounds still open are graded errored, as Manager.Cancel grades them:
// RoundState has no cancelled member on purpose — a round's grade describes
// its review, and there was none — and leaving one "running" under a finished
// flow would make the tree lie about a step that will never run.
func finishLocked(f *Flow, state State, errText string, now time.Time) {
	f.State = state
	if errText != "" {
		f.Error = errText
	}
	if f.EndedAt == nil {
		f.EndedAt = &now
	}
	for _, r := range f.Rounds {
		if r.State.Terminal() {
			continue
		}
		r.State = RoundErrored
		if r.EndedAt == nil {
			r.EndedAt = &now
		}
	}
}

// apply is the write half of every transition: re-take the lock, re-check
// everything the snapshot assumed, run the mutation, persist the result.
//
// The guard is what makes a pass safe against a concurrent Cancel. A flow that
// went terminal, a round that settled or was regraded, a round whose task IDs
// or review attempt no longer match the snapshot the decision was made from —
// each aborts the transition, leaving the next pass to re-derive from what is
// actually recorded. orphans are tasks the caller submitted for a transition
// that may not land; if it does not they are cancelled, because a flow that is
// not recording a task is also not going to stop it later.
func (m *Manager) apply(snap *Flow, cur *Round, mutate func(*Flow, *Round) bool,
	orphans []queue.Task) (Flow, bool) {

	m.mu.Lock()
	f, ok := m.flows[snap.ID]
	if ok && !f.State.Terminal() {
		var r *Round
		if cur != nil {
			r = findRound(f, cur.N)
			if r == nil || r.State.Terminal() || r.WorkTaskID != cur.WorkTaskID ||
				r.ReviewTaskID != cur.ReviewTaskID || r.ReviewAttempt != cur.ReviewAttempt {
				ok = false
			}
		}
		if ok && mutate(f, r) {
			m.saveLocked(f)
			out := f.Clone()
			m.mu.Unlock()
			return out, true
		}
	}
	m.mu.Unlock()

	m.cancelOrphans(snap.ID, orphans)
	return Flow{}, false
}

// cancelOrphans stops tasks submitted for a transition that did not land.
// Called with no lock held. Failures are logged, not returned: the flow is
// where it is either way, and no caller could act on this.
func (m *Manager) cancelOrphans(flowID string, tasks []queue.Task) {
	if m.queue == nil {
		return
	}
	for _, t := range tasks {
		log.Printf("flow: %s moved on while its tasks were being submitted; cancelling %s", flowID, t.ID)
		if err := m.queue.Cancel(t.ID); err != nil {
			log.Printf("flow: cancel orphaned task %s of flow %s: %v", t.ID, flowID, err)
		}
	}
}
