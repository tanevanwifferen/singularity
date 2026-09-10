package flow

import (
	"log"
	"sort"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/queue"
)

// The driver's lifecycle and pass structure: start, stop, wake, the goroutine,
// and one pass over the active flows. The transitions a pass makes live in
// rounds.go; the small shared helpers in reconcile_helpers.go.
//
// It is a reconciler, not an event handler: every pass re-derives each
// non-terminal flow's position by reading its tasks out of the queue and
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
// (see apply, in rounds.go). The queue is never called under m.mu — the
// reconciler is registered as a queue change observer, so that would put the
// two locks in opposite orders — and neither is a change callback.

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
	// Planning, when enabled, runs to completion before round 1 is ever
	// considered: a flow with EnablePlanning set has nothing else to do
	// until its Plan is on record, the same one-pass-wide reasoning that
	// makes StatePending safe for round 1 below.
	if snap.EnablePlanning && snap.Plan == "" {
		if snap.PlanTaskID == "" {
			return m.submitPlan(&snap)
		}
		return m.advancePlan(&snap)
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
