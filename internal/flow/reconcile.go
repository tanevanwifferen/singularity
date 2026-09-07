package flow

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/queue"
)

// The driver. It is a reconciler, not an event handler: every pass re-derives
// each non-terminal flow's position by reading its tasks out of the queue and
// acting on what it finds, rather than by remembering what it was told.
//
// That is the whole restart story (§5). If the daemon dies between a review
// task reaching done and the flow reacting, a handler-driven design has lost
// that edge for good — nothing delivers it twice. The reconciler reads `done`
// on the next pass, finds the verdict file for that round and attempt, and
// carries on; a restored flow needs no replay because there is nothing to
// replay. The same property is why an operator's `queue retry` on a review
// task works with no code: the pass reads whatever verdict is on disk for the
// current attempt.
//
// Every transition has the same three-phase shape: snapshot the flow under
// m.mu, do the reads and the queue.Add with the lock released, then apply the
// result under m.mu behind a guard that re-checks what the snapshot assumed
// (see apply). The queue is never called under m.mu — the reconciler is
// registered as a queue change observer, so that would put the two locks in
// opposite orders — and neither is a change callback.

const (
	// defaultTickInterval is the fallback poll period, queue.Manager's own:
	// a flow transition is only ever as prompt as the queue transition
	// before it, and Notify normally makes both immediate.
	defaultTickInterval = time.Second

	// stopDrainTimeout bounds StopReconciler's wait. A pass does file reads
	// and queue calls and never touches an agent process, so the bound is
	// there only so a wedged state directory cannot hold the daemon's
	// shutdown open past its grace period.
	stopDrainTimeout = 3 * time.Second

	// maxStepsPerPass bounds how many transitions one flow may make in one
	// pass. A legal chain is at most two — settle a round, submit the next
	// — and the headroom is slack, not budget: stopping short loses
	// nothing, because every transition is persisted before the next is
	// attempted and the following pass resumes from the record.
	maxStepsPerPass = 4
)

// unparseableAfterTwo is the exact Error a flow carries when §3.3 fired twice
// in one round. Callers match on state, not on this text; an operator reads it.
const unparseableAfterTwo = "unparseable reviewer verdict after 2 attempts"

// OnFlowChange registers the flow-change callback, replacing any previous one.
// It fires once per transition the reconciler makes, carrying the whole flow
// so a view needs no follow-up fetch — queue_task_changed's reasoning.
//
// The callback runs on the reconciler goroutine with no lock held, so it may
// call back into the Manager. It must not block: a listener waiting on a
// wedged websocket peer delays every other flow's next transition, which is
// what queue.Manager.emit had to buy its way out of with a buffered channel.
// Whoever wires the daemon's broadcast in owns that decoupling.
//
// Transitions the API makes directly — Start's pending record, Cancel's
// terminal one — are not emitted here; this slot reports the driver's work.
func (m *Manager) OnFlowChange(fn func(Flow)) {
	m.onChangeMu.Lock()
	m.onFlowChange = fn
	m.onChangeMu.Unlock()
}

// notifyChange invokes the change callback. Called with no lock held.
func (m *Manager) notifyChange(f Flow) {
	m.onChangeMu.RLock()
	fn := m.onFlowChange
	m.onChangeMu.RUnlock()
	if fn != nil {
		fn(f)
	}
}

// StartReconciler launches the reconciler goroutine. Idempotent, so daemon
// wiring can call it unconditionally. Not named Start, which is the
// flow-creation API: one verb, one meaning per package.
func (m *Manager) StartReconciler() {
	m.runOnce.Do(func() {
		m.started.Store(true)
		go m.loop()
	})
}

// StopReconciler shuts the reconciler down and waits for the goroutine to
// exit, reporting whether the wait completed. Running agents are left alone —
// they belong to the engine; the flow only stops making new decisions.
//
// Idempotent, and safe before StartReconciler: a manager that never started
// has nothing that will ever close m.stopped, so waiting would burn the whole
// deadline for no reason.
func (m *Manager) StopReconciler() bool {
	m.stopOnce.Do(func() { close(m.stop) })
	if !m.started.Load() {
		return true
	}
	deadline := time.NewTimer(m.stopTimeout())
	defer deadline.Stop()
	select {
	case <-m.stopped:
		return true
	case <-deadline.C:
		log.Printf("flow: gave up after %s waiting for a reconciler pass to finish", m.stopTimeout())
		return false
	}
}

// stopTimeout is stopDrainTimeout unless a test overrode it.
func (m *Manager) stopTimeout() time.Duration {
	if d := m.stopTimeoutOverride; d > 0 {
		return d
	}
	return stopDrainTimeout
}

// Wake asks for a pass as soon as one can run. Non-blocking: the channel has
// capacity 1, so a burst coalesces into a single pass instead of queueing
// redundant work. Safe before StartReconciler and after StopReconciler.
func (m *Manager) Wake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Notify is the queue observer hook: the daemon registers it with
// queue.Manager.AddChangeObserver so a task change wakes the reconciler.
//
// A flow only ever submits into flow-<id>, so filtering on the queue ID keeps
// an unrelated queue's chatter from waking every flow. Nothing is lost if the
// filter is ever wrong — the tick reconciles the same flows regardless, and a
// pass ignores the task identity entirely, which is both simpler and immune
// to a frame arriving for a task no flow owns.
func (m *Manager) Notify(t queue.Task) {
	if !isFlowQueueID(t.QueueID) {
		return
	}
	m.Wake()
}

// loop is the reconciler goroutine.
func (m *Manager) loop() {
	defer close(m.stopped)
	ticker := time.NewTicker(m.tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stop:
			return
		case <-m.wake:
		case <-ticker.C:
		}
		m.tick()
	}
}

// tick is one pass: every non-terminal flow, oldest first, advanced as far as
// it can go without waiting on an agent. Flows go one at a time — a pass is
// two queue lookups and one small file read per flow, and serialising them
// keeps the change callbacks in a defensible order.
func (m *Manager) tick() {
	for _, id := range m.activeFlowIDs() {
		if m.stopping() {
			return
		}
		for step := 0; step < maxStepsPerPass; step++ {
			f, changed := m.advanceOnce(id)
			if !changed {
				break
			}
			// Outside every lock by construction: advanceOnce has released
			// m.mu before it returns.
			m.notifyChange(f)
			if f.State.Terminal() {
				break
			}
		}
	}
}

// stopping reports whether shutdown has begun, so a long pass gives up
// between flows rather than at the end.
func (m *Manager) stopping() bool {
	select {
	case <-m.stop:
		return true
	default:
		return false
	}
}

// activeFlowIDs snapshots the IDs worth a look, oldest first. Terminal flows
// are skipped: nothing resumes one, so re-deriving it is work with no
// possible outcome.
func (m *Manager) activeFlowIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.flows))
	for id, f := range m.flows {
		if f.State.Terminal() {
			continue
		}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return lessFlowID(out[i], out[j]) })
	return out
}

// advanceOnce makes at most one transition to one flow and returns the flow as
// it stands afterwards. changed is false when the flow is already where it
// should be — the common case, since most passes find a task still running.
func (m *Manager) advanceOnce(flowID string) (Flow, bool) {
	snap, ok := m.snapshot(flowID)
	if !ok {
		return Flow{}, false
	}
	// No rounds means either a brand-new flow (StatePending, one pass wide)
	// or a record whose round 1 submission did not survive the daemon that
	// made it. Both want the same thing.
	if len(snap.Rounds) == 0 {
		return m.submitRound(&snap, 1)
	}
	cur := snap.CurrentRound()
	if cur.State.Terminal() {
		return m.afterSettledRound(&snap, cur)
	}
	return m.advanceRound(&snap, cur)
}

// snapshot returns a private copy of a non-terminal flow. ok is false if the
// flow is gone (removed) or finished (cancelled between passes).
func (m *Manager) snapshot(flowID string) (Flow, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.flows[flowID]
	if !ok || f.State.Terminal() {
		return Flow{}, false
	}
	return f.Clone(), true
}

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

// getTask reads one task with the lock released. A nil queue reports the task
// as missing, which stops a queue-less manager from advancing a flow it could
// not have submitted in the first place.
func (m *Manager) getTask(taskID string) (queue.Task, error) {
	if m.queue == nil {
		return queue.Task{}, queue.ErrNotFound
	}
	return m.queue.Get(taskID)
}

// verdictPath is where the reviewer for one round and one attempt was told to
// write. Empty when the manager has no state directory.
func (m *Manager) verdictPath(flowID string, round, attempt int) string {
	dir := m.store.VerdictDir(flowID)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, fmt.Sprintf("r%d-a%d-verdict.json", round, attempt))
}

// prepareVerdictPath returns the path and makes sure its directory exists,
// because the reviewer writes the file with a shell redirect that will not
// create one. A failure is logged rather than fatal: the flow then reads a
// missing verdict and takes §3.3, which is honest and still terminates.
func (m *Manager) prepareVerdictPath(flowID string, round, attempt int) string {
	path := m.verdictPath(flowID, round, attempt)
	if path == "" {
		return ""
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		log.Printf("flow: create verdict dir for %s: %v", flowID, err)
	}
	return path
}

// readVerdictFile reads and parses one verdict document. A file that does not
// exist when its review task reached done is treated exactly like an
// unparseable one (§3.2), and so is a manager with nowhere to have put it: the
// alternative is inferring an accept from a task exiting cleanly, which is the
// one thing this design never does.
func readVerdictFile(path string) (*Verdict, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: no state directory is configured, so no verdict could have been written",
			ErrUnparseable)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnparseable, err)
	}
	return ParseVerdict(data)
}

// stepOpts is a step's options with worktree isolation forced off. Start
// refuses use_worktree outright, so this only matters for a hand-edited state
// file — and it matters there for the reason Start refuses it.
func stepOpts(o TaskOptions) TaskOptions {
	out := o
	out.UseWorktree = false
	return out
}

// reviewTitle names a round's review step. The first attempt is plain
// `f3 r2 review`; a §3.3 re-review names its attempt, so an operator reading
// `queue list` can tell a second reviewer from a retried first one.
func reviewTitle(flowID string, round, attempt int) string {
	if attempt <= 1 {
		return fmt.Sprintf("%s r%d review", flowID, round)
	}
	return fmt.Sprintf("%s r%d review a%d", flowID, round, attempt)
}

// describeTask names a task for an error message: the ID an operator can pass
// to `queue get`, plus the title if it has one.
func describeTask(t queue.Task) string {
	if t.Title == "" {
		return "task " + t.ID
	}
	return fmt.Sprintf("task %s (%s)", t.ID, t.Title)
}

// failureReason is a task's own explanation, or a stand-in when it has none.
func failureReason(t queue.Task) string {
	if t.Error == "" {
		return "no reason recorded"
	}
	return t.Error
}

// describePath reads sensibly when there was no verdict path at all.
func describePath(path string) string {
	if path == "" {
		return "(no verdict path)"
	}
	return path
}

// findRound returns the round numbered n, or nil.
func findRound(f *Flow, n int) *Round {
	for _, r := range f.Rounds {
		if r.N == n {
			return r
		}
	}
	return nil
}

// isFlowQueueID reports whether a queue ID looks like a flow's own queue.
func isFlowQueueID(queueID string) bool {
	const prefix = "flow-"
	return len(queueID) > len(prefix) && queueID[:len(prefix)] == prefix
}
