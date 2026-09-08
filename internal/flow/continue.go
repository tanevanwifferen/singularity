package flow

import (
	"fmt"
	"os"
	"time"
)

// Continuing a flow that ended without acceptance: more rounds against the
// same goal, in the same tree, under the same options.
//
// The decision this file encodes is that a continue EXTENDS the flow rather
// than forking a new one. Round numbering carries on — a flow that stopped at
// round 3 gets round 4 next — the cap is raised by the rounds asked for, and
// every round already on the record stays exactly where it is. That is the
// whole point: FixPrompt composes round N+1's work from the rounds before it,
// so a continued flow's fixer sees the history a fresh flow would have to be
// told about by hand, and an operator reading `flow show` sees one contiguous
// account of the work rather than a chain of unrelated records.
//
// Nothing else is re-specifiable. Goal, ReviewGoal, WorkDir, Opts and
// ReviewOpts are the flow's, unchanged: a continue that could rewrite the goal
// would make the rounds before it findings about a different piece of work,
// and the history would stop meaning anything.
//
// Continue submits nothing itself. It moves the record and leaves the round to
// the reconciler, for the reason Start does the same with round 1: the submit
// path a restart takes must be the path a continue takes, or the two disagree
// exactly when the daemon dies between them (§5).

// defaultExtraRounds is how many more rounds a continue asks for when the
// caller says nothing — the same number a fresh flow's cap defaults to.
const defaultExtraRounds = 3

// Continue puts a flow that ended without acceptance back to running with a
// raised cap, so the reconciler opens another round against the same goal.
//
// Eligible from rejected, errored and cancelled. Refused for accepted (a flow
// whose work passed review has nothing to fix) and for a pending or running
// flow (it has not finished; there is nothing to continue) — both
// ErrNotContinuable, because the request is well formed and it is the flow's
// state that says no.
//
// extraRounds defaults to defaultExtraRounds when zero and raises MaxRounds by
// its own value, so the result must still satisfy the 1..20 the manager
// validates everywhere else. A flow already at the ceiling is refused by name
// rather than with a bare range complaint: no round count would have worked,
// and the caller needs to know that rather than to try a smaller number.
//
// The work directory is re-validated. It was checked when the flow started,
// but a continue can come hours later, and the commonest case of all is a flow
// aimed at a workflow's worktree that has since been removed.
//
// Error and EndedAt are cleared, so a continued flow does not carry the reason
// it stopped as though it were still true. The rounds before it keep theirs.
//
// The returned flow is the record as it stands afterwards. No change callback
// fires: that slot reports the reconciler's work, not the API's, exactly as
// Start's pending record and Cancel's terminal one go unemitted.
func (m *Manager) Continue(flowID string, extraRounds int) (Flow, error) {
	if extraRounds == 0 {
		extraRounds = defaultExtraRounds
	}
	if extraRounds < 0 {
		return Flow{}, fmt.Errorf("%w: rounds must be a positive number of extra rounds, got %d",
			ErrInvalid, extraRounds)
	}

	workDir, taskIDs, err := m.checkContinuable(flowID, extraRounds)
	if err != nil {
		return Flow{}, err
	}
	if err := workDirStillThere(flowID, workDir); err != nil {
		return Flow{}, err
	}

	// Strays go first, while the flow is still terminal and the reconciler
	// is therefore still skipping it. A half-run round can leave a task the
	// queue would still dispatch — a review left blocked behind a work task
	// that never settled, or a step whose cancel never reached the queue
	// because the daemon died — and that agent would be writing into the
	// very tree the new round is about to fix. Only the flow's own recorded
	// task IDs are touched, the same authority Cancel has (§4).
	m.stopTasks(flowID, taskIDs)

	now := time.Now()
	m.mu.Lock()
	f, ok := m.flows[flowID]
	if !ok {
		m.mu.Unlock()
		return Flow{}, ErrNotFound
	}
	// Re-checked because the state was read and released above: a second
	// continue, or a reconciler pass on the flow the first one revived,
	// could have moved it in between.
	if err := continuable(f, extraRounds); err != nil {
		m.mu.Unlock()
		return Flow{}, err
	}
	prior := f.State
	f.MaxRounds += extraRounds
	f.State = StateRunning
	f.Error = ""
	f.EndedAt = nil
	if cur := f.CurrentRound(); cur != nil {
		settleAbandonedRound(cur, prior, now)
	}
	m.saveLocked(f)
	out := f.Clone()
	m.mu.Unlock()

	// Unlike Start, which lets the next tick find its pending flow, a
	// continue is an operator watching for something to happen. Wake is
	// non-blocking and safe before the reconciler is even running.
	m.Wake()
	return out, nil
}

// checkContinuable validates under the lock and reports what the rest of
// Continue needs from the record: the directory to re-stat and the task IDs to
// stop. Both are read here rather than later so that the filesystem call and
// the queue calls happen with m.mu released — the queue is never called under
// it (the reconciler is registered as a queue change observer, so that would
// put the two locks in opposite orders), and a stat on a wedged mount must not
// hold every reader out.
func (m *Manager) checkContinuable(flowID string, extraRounds int) (workDir string, taskIDs []string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.flows[flowID]
	if !ok {
		return "", nil, ErrNotFound
	}
	if err := continuable(f, extraRounds); err != nil {
		return "", nil, err
	}
	return f.WorkDir, recordedTaskIDs(f), nil
}

// continuable reports why the flow may not be continued by extraRounds, or nil
// if it may. Caller holds m.mu.
func continuable(f *Flow, extraRounds int) error {
	switch {
	case f.State == StateAccepted:
		return fmt.Errorf("%w: flow %s was accepted; its work passed review and there is nothing to fix",
			ErrNotContinuable, f.ID)
	case !f.State.Terminal():
		return fmt.Errorf("%w: flow %s is %s; only a flow that has finished without being accepted "+
			"can be continued — cancel it first, or wait for it", ErrNotContinuable, f.ID, f.State)
	case f.MaxRounds >= maxRoundsCap:
		return fmt.Errorf("%w: flow %s is already capped at %d rounds, which is the ceiling; "+
			"no number of extra rounds is possible, so start a new flow against the same work_dir "+
			"if the work needs more", ErrInvalid, f.ID, maxRoundsCap)
	case f.MaxRounds+extraRounds > maxRoundsCap:
		return fmt.Errorf("%w: %d more rounds would take flow %s to %d, past the %d-round ceiling; "+
			"ask for at most %d more", ErrInvalid, extraRounds, f.ID, f.MaxRounds+extraRounds,
			maxRoundsCap, maxRoundsCap-f.MaxRounds)
	}
	return nil
}

// workDirStillThere re-validates the tree a continued flow is about to run in.
// Refusing here names the path; the alternative is a fixer agent that cannot
// find its directory, and a flow that errors one task failure later with the
// reason buried in an agent's output — the same argument Start makes for
// checking before the first dispatch rather than after it.
func workDirStillThere(flowID, dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("%w: work_dir %s of flow %s is no longer there: %v", ErrInvalid, dir, flowID, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: work_dir %s of flow %s is not a directory any more", ErrInvalid, dir, flowID)
	}
	return nil
}

// settleAbandonedRound closes a trailing round that never reached a verdict,
// which is what a cancelled flow's last round usually is: its work task done
// and its review cancelled, both cancelled, or a step still recorded
// non-terminal because the daemon died before Cancel reached the queue.
//
// The rule, and it is the one part of a continue that is not obvious: a
// trailing round that never reached a verdict is settled as errored, with an
// explicit reason, before a fresh round is opened — and it is never reopened.
// Reopening it would mean a round with two work tasks, and leaving it
// non-terminal would hand the reconciler a "current round" it would go on
// polling: advanceRound would read a cancelled step and terminate the flow the
// operator just continued, or wait forever on a step nothing will ever
// dispatch. Settling it means the next pass takes afterSettledRound, whose
// only question is whether the cap leaves room for round N+1.
//
// The reason is recorded as §3.3's synthetic reject rather than as free text
// because a Round has nowhere else to put one, and because it keeps the next
// round's fix prompt working the way every other round's does — with findings
// rather than with an empty list.
//
// A round that did reach a verdict is left exactly as it is: a rejection at
// the cap, or the synthetic reject §3.3 leaves behind after two unparseable
// reviews, is precisely what the next round is for. So is a verdict file a
// killed reviewer happened to leave on disk — that attempt was stopped, and
// letting an abandoned round's leftovers decide a continued flow's next move
// would be reading a decision out of an interrupted process.
func settleAbandonedRound(r *Round, prior State, now time.Time) {
	if r.State == RoundAccepted || r.State == RoundRejected {
		return
	}
	if !r.State.Terminal() {
		r.State = RoundErrored
	}
	if r.EndedAt == nil {
		r.EndedAt = &now
	}
	if r.Verdict != nil {
		return
	}
	r.Verdict = &Verdict{
		Decision: DecisionReject,
		Summary:  "the round was abandoned before it produced a verdict",
		Findings: []Finding{{
			Severity: SeverityMajor,
			Detail: fmt.Sprintf("round %d never reached a verdict: the flow ended %s while the round was "+
				"still in flight, so the working tree may hold a half-finished round. Check what is "+
				"actually there before building on it, and finish anything that round left undone.",
				r.N, prior),
		}},
	}
}
