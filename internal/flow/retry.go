package flow

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/queue"
)

// Retrying a step of a flow: redoing a work, review or plan task the flow
// already ran, including one that finished done.
//
// queue.Manager.Retry alone is not this operation. It puts the named task
// back in line and nothing else — the flow record it belongs to never
// learns, so a rerun reviewer's verdict is written to the same per-attempt
// path and never read (rounds.go settled the round on the pass that saw the
// old review done, and apply's terminal guard refuses every further
// transition on it), and a rerun work task is neither re-reviewed nor
// followed by a new round. RetryStep is what makes the flow notice: it
// reopens the step's round — or, for the plan step, the flow itself — with
// fresh tasks the reconciler picks up exactly as it would a brand new round,
// and revives a terminal flow the way Continue does.
//
// It deliberately submits fresh tasks rather than reusing the old task IDs
// through queue.Retry. A retried review must depend on the retried work
// finishing again, and queue.Retry's own doc explains why that dependency
// would not re-form: "refreshBlockedLocked does not walk back down to a done
// dependent of the retried task". Fresh tasks, added the same atomic way
// submitRound already does, sidestep that instead of fighting it.

// RetryStep re-runs one step of flowID's tree: the work or review step of
// its current round, or the plan step before round 1 has been submitted.
//
// Only the current round is eligible. Retrying an earlier one would rewrite
// a round every later round's fix prompt and findings already depend on
// (FixPrompt folds each prior round's findings in by reference to what
// actually happened) — continuing the flow for more rounds is the operation
// for "the last round's grade should not stand", not reopening history.
// ErrNotRetryable names the step's actual round and the current one so the
// caller knows Continue is what they want instead.
//
// Refused, also as ErrNotRetryable, when taskID's task is not terminal — the
// caller should wait or cancel, not get a step swapped out from under a live
// agent — or when any OTHER task in the flow's queue is not terminal either,
// recorded on the flow or not (the commit task an accept fires is not; see
// requireQuiescent). Every round of a flow runs in the same WorkDir with
// worktree isolation forced off (rounds.go submitRound), so a live task
// anywhere else in the flow is exactly the hazard a retried step must not
// be dispatched alongside: two agents editing one working tree at once. ErrInvalid for a taskID that
// names no step of this flow, and ErrNotFound for an unknown flowID.
//
// The returned flow is the record as it stands afterwards, the same shape
// Continue returns. No change callback fires here either — that slot
// reports the reconciler's work, and reopening a round is the API's.
func (m *Manager) RetryStep(flowID, taskID string) (Flow, error) {
	snap, which, round, err := m.locateStep(flowID, taskID)
	if err != nil {
		return Flow{}, err
	}
	if err := m.requireQuiescent(&snap, taskID); err != nil {
		return Flow{}, err
	}

	var out Flow
	switch which {
	case "plan":
		out, err = m.resubmitPlan(&snap)
	case "work":
		out, err = m.resubmitRound(&snap, round)
	case "review":
		out, err = m.resubmitReview(&snap, round)
	}
	switch {
	case errors.Is(err, errRetryRaced):
		return Flow{}, fmt.Errorf("%w: flow %s changed while %s was being retried; check its state and try again",
			ErrNotRetryable, flowID, taskID)
	case err != nil:
		// The queue refused the batch, or there is no queue: nothing raced,
		// the retry simply could not be submitted, and the operator needs
		// the queue's own complaint rather than a hint to look for a race.
		return Flow{}, fmt.Errorf("retrying %s of flow %s: %w", taskID, flowID, err)
	}

	// Unlike Start, which lets the next tick find its pending flow, an
	// operator retrying a step is watching for something to happen.
	m.Wake()
	return out, nil
}

// errRetryRaced is resubmit*'s report that applyRetry found the flow no
// longer in the shape RetryStep snapshotted — the reconciler or another
// call moved it in the window Add ran in — and cancelled the fresh tasks
// rather than adopt them. RetryStep turns it into the ErrNotRetryable an
// operator reads; nothing outside this file sees it.
var errRetryRaced = errors.New("flow changed while the step was being retried")

// errNoQueue is the failure of a manager with no task queue at all: a retry
// has nowhere to submit to. Not ErrNotRetryable, because no state of the
// flow is refusing anything — the daemon is misconfigured.
var errNoQueue = errors.New("no task queue to submit to")

// locateStep resolves taskID to a step of flowID's tree — which kind it is
// and, for a round step, the round it belongs to — and rejects it outright
// if it is not eligible by shape: not a step of this flow at all, a round
// other than the current one, or the plan step after round 1 has started.
// Liveness (requireQuiescent) is checked separately, with the lock released.
func (m *Manager) locateStep(flowID, taskID string) (snap Flow, which string, round *Round, err error) {
	m.mu.Lock()
	f, ok := m.flows[flowID]
	if !ok {
		m.mu.Unlock()
		return Flow{}, "", nil, ErrNotFound
	}
	snap = f.Clone()
	m.mu.Unlock()

	if snap.PlanTaskID == taskID {
		if len(snap.Rounds) > 0 {
			return Flow{}, "", nil, fmt.Errorf(
				"%w: round 1 of flow %s has already been submitted; its plan cannot be redone "+
					"without rewriting rounds already run against it", ErrNotRetryable, flowID)
		}
		return snap, "plan", nil, nil
	}

	if cur := snap.CurrentRound(); cur != nil {
		for _, r := range snap.Rounds {
			var kind string
			switch taskID {
			case r.WorkTaskID:
				kind = "work"
			case r.ReviewTaskID:
				kind = "review"
			default:
				continue
			}
			if r.N != cur.N {
				return Flow{}, "", nil, fmt.Errorf(
					"%w: %s is round %d of flow %s, not its current round %d; only the current round "+
						"can be retried — continue the flow instead if round %d's outcome should not stand",
					ErrNotRetryable, taskID, r.N, flowID, cur.N, r.N)
			}
			return snap, kind, r, nil
		}
	}
	return Flow{}, "", nil, fmt.Errorf("%w: task %s is not a step of flow %s", ErrInvalid, taskID, flowID)
}

// requireQuiescent reports why taskID may not be retried yet, or nil if it
// may: every task in the flow's queue must be terminal, taskID included.
//
// It asks the queue for the flow's whole queue rather than walking
// recordedTaskIDs, because the record is not the full set of agents the
// flow puts in its WorkDir. The commit task an accept fires
// (submitCommitTask) is deliberately unrecorded — fire-and-forget, the
// accept already decided — yet it runs in the same tree, and RetryStep is
// the one operation that acts on an accepted flow (Continue refuses one):
// the TUI shows the round done the instant its verdict lands, while the
// commit agent may still be running. Anything else an operator queued
// into flow-<id> by hand is the same hazard for the same reason. Reading
// the queue by QueueID sees all of them; the record's authority to CANCEL
// stays exactly recordedTaskIDs (§4), this only refuses.
//
// A queue the task manager no longer has cannot be running anything, so it
// is treated as quiescent rather than refused.
func (m *Manager) requireQuiescent(f *Flow, taskID string) error {
	if m.queue == nil {
		return nil
	}
	tasks, err := m.queue.List(f.QueueID, nil)
	if errors.Is(err, queue.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking flow %s's tasks: %w", f.ID, err)
	}
	for _, t := range tasks {
		if t.State.Terminal() {
			continue
		}
		if t.ID == taskID {
			return fmt.Errorf("%w: %s is %s; wait for it to finish or cancel it first",
				ErrNotRetryable, describeTask(t), t.State)
		}
		return fmt.Errorf("%w: %s is still %s; flow %s runs every step in one working directory with "+
			"worktree isolation off, so retrying %s while %s can still run risks two agents editing it "+
			"at once", ErrNotRetryable, describeTask(t), t.State, f.ID, taskID, t.ID)
	}
	return nil
}

// resubmitRound redoes a round's work step: fresh work and review tasks,
// submitted atomically exactly as submitRound would for this round number,
// depending on the same prior rounds' findings a first attempt would have.
// The old tasks are left in the queue as history; nothing else refers to
// them once the round record is repointed.
func (m *Manager) resubmitRound(snap *Flow, cur *Round) (Flow, error) {
	if m.queue == nil {
		return Flow{}, errNoQueue
	}
	n := cur.N
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
			Title:   fmt.Sprintf("%s r%d %s (retry)", snap.ID, n, work),
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
		return Flow{}, err
	}

	now := time.Now()
	return m.applyRetry(snap, cur, func(f *Flow, r *Round) bool {
		r.WorkTaskID = tasks[0].ID
		r.ReviewTaskID = tasks[1].ID
		r.ReviewAttempt = 1
		r.Verdict = nil
		r.State = RoundRunning
		r.StartedAt = now
		r.EndedAt = nil
		reviveIfTerminal(f)
		return true
	}, tasks)
}

// resubmitReview redoes a round's review step only: a fresh review task at
// the same review attempt the retried one was, writing to the same verdict
// path (the reviewer's own prompt truncates it, the way a first attempt
// always has), so readRoundVerdict on the next tick needs no changes to find
// it. The work step, already done, is left exactly as it is.
//
// The verdict is cleared only at attempt 1. At attempt 2 the round's Verdict
// is attempt 1's synthetic reject, recorded by submitReReview to say why a
// second reviewer was sent at all, and readRoundVerdict re-records that very
// value if the second attempt is unparseable too ("attempt 1's synthetic
// verdict stays on the record"). Clearing it here would make a retried
// attempt 2 that fails again settle the round errored with no verdict — the
// explanation the design says the operator keeps.
func (m *Manager) resubmitReview(snap *Flow, cur *Round) (Flow, error) {
	if m.queue == nil {
		return Flow{}, errNoQueue
	}
	verdictPath := m.prepareVerdictPath(snap.ID, cur.N, cur.ReviewAttempt)
	tasks, err := m.queue.Add([]queue.TaskSpec{{
		QueueID: snap.QueueID,
		Title:   reviewTitle(snap.ID, cur.N, cur.ReviewAttempt) + " (retry)",
		Prompt:  ReviewPrompt(snap, verdictPath),
		WorkDir: snap.WorkDir,
		Opts:    stepOpts(snap.ReviewOpts),
	}})
	if err != nil {
		return Flow{}, err
	}

	return m.applyRetry(snap, cur, func(f *Flow, r *Round) bool {
		r.ReviewTaskID = tasks[0].ID
		if r.ReviewAttempt <= 1 {
			r.Verdict = nil
		}
		r.State = RoundRunning
		r.EndedAt = nil
		reviveIfTerminal(f)
		return true
	}, tasks)
}

// resubmitPlan redoes the planning step: a fresh plan task, exactly as
// submitPlan would submit the first one, and Plan cleared so advancePlan
// reads the new file once it lands. Only reachable while len(Rounds) == 0
// (locateStep enforces it), so there is no round yet whose prompts were
// built from the old plan text.
func (m *Manager) resubmitPlan(snap *Flow) (Flow, error) {
	if m.queue == nil {
		return Flow{}, errNoQueue
	}
	path := m.preparePlanPath(snap.ID)
	tasks, err := m.queue.Add([]queue.TaskSpec{{
		Name:    "plan",
		QueueID: snap.QueueID,
		Title:   fmt.Sprintf("%s plan (retry)", snap.ID),
		Prompt:  PlanPrompt(snap, path),
		WorkDir: snap.WorkDir,
		Opts:    stepOpts(snap.PlanOpts),
	}})
	if err != nil {
		return Flow{}, err
	}

	return m.applyRetry(snap, nil, func(f *Flow, _ *Round) bool {
		f.PlanTaskID = tasks[0].ID
		f.Plan = ""
		reviveIfTerminal(f)
		return true
	}, tasks)
}

// reviveIfTerminal puts a flow that had already finished back to running —
// the point of retrying a step of its last round or its plan — clearing the
// reason it stopped, the same fields Continue clears. Caller holds m.mu.
func reviveIfTerminal(f *Flow) {
	if !f.State.Terminal() {
		return
	}
	f.State = StateRunning
	f.Error = ""
	f.EndedAt = nil
}

// applyRetry is apply's counterpart for a retry: the same re-lock,
// re-check-what-the-snapshot-assumed, mutate, persist, cancel-on-miss shape,
// except it may act on a terminal flow — reviving one is the point — so
// instead of apply's blanket f.State.Terminal() refusal it re-checks that
// the exact round (or, for the plan step, the exact pre-round-1 shape) is
// still what was snapshotted.
//
// The round comparison is by value, not just by task IDs the way apply's
// guard is: apply only ever acts on a round still in flight, where the task
// IDs are the one thing a concurrent call could have changed, but a retry's
// snapshot can just as easily be raced by the reconciler settling that same
// round in the instant between RetryStep's liveness check and this call —
// which changes State and Verdict, not the task IDs. Comparing the whole
// round catches that race too, and applyRetry aborts (cancelling the fresh
// tasks it was about to point the round at) rather than clobber a verdict
// that landed at the same moment.
//
// The round count is re-checked as well, because a round can be unchanged
// by value and still no longer be the flow's last: a settled rejection
// under a running flow — right after Continue, or below the cap — is a
// round the reconciler's submitRound(N+1) is about to follow, with Add in
// flight and the lock released. Were only the round compared, both writes
// would land: round N reopened here and round N+1 appended there, two
// implementers in one tree and the retried round's tasks never read.
// submitRound's own guard refuses the mirror-image ordering (it requires
// the last round to be terminal), so whichever lands second is the one
// that cancels its tasks.
//
// Returns errRetryRaced when the guard refuses; the orphans are cancelled
// before it returns.
func (m *Manager) applyRetry(snap *Flow, cur *Round, mutate func(*Flow, *Round) bool,
	orphans []queue.Task) (Flow, error) {

	m.mu.Lock()
	f, ok := m.flows[snap.ID]
	if ok {
		var r *Round
		if len(f.Rounds) != len(snap.Rounds) {
			ok = false
		} else if cur != nil {
			r = findRound(f, cur.N)
			if r == nil || !reflect.DeepEqual(*r, *cur) {
				ok = false
			}
		} else if f.PlanTaskID != snap.PlanTaskID || f.Plan != snap.Plan {
			ok = false
		}
		if ok && mutate(f, r) {
			m.saveLocked(f)
			out := f.Clone()
			m.mu.Unlock()
			return out, nil
		}
	}
	m.mu.Unlock()

	m.cancelOrphans(snap.ID, orphans)
	return Flow{}, errRetryRaced
}
