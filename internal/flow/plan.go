package flow

import (
	"fmt"

	"gitlab.com/tanevanwifferen1/singularity/internal/queue"
)

// The planning phase: one optional task, submitted before round 1 on
// PlanOpts rather than Opts, that writes a plan document ImplementPrompt and
// FixPrompt fold in under their own heading. It has no round of its own —
// Round is one implement/fix → review cycle, and planning happens once per
// flow, not once per round — so its liveness lives directly on Flow
// (PlanTaskID, Plan) the way a round's task IDs live on Round.
//
// Both functions below are reached from advanceOnce (reconcile.go), which
// gates on EnablePlanning and Plan == "" the same way it gates on
// len(Rounds) == 0 for round 1: a flow with planning enabled has nothing
// else to do until its plan is on record.

// submitPlan submits the planning task. Reached once per flow, when
// EnablePlanning is set and no PlanTaskID is recorded yet — including after
// a restart whose submission did not survive the daemon that made it, the
// same story submitRound tells for round 1.
func (m *Manager) submitPlan(snap *Flow) (Flow, bool) {
	if m.queue == nil {
		return Flow{}, false
	}

	path := m.preparePlanPath(snap.ID)
	tasks, err := m.queue.Add([]queue.TaskSpec{{
		Name:    "plan",
		QueueID: snap.QueueID,
		Title:   fmt.Sprintf("%s plan", snap.ID),
		Prompt:  PlanPrompt(snap, path),
		WorkDir: snap.WorkDir,
		Opts:    stepOpts(snap.PlanOpts),
	}})
	if err != nil {
		return m.finish(snap, StateErrored, fmt.Sprintf("submitting plan: %v", err))
	}

	return m.apply(snap, nil, func(f *Flow, _ *Round) bool {
		// Already recorded means this batch belongs to a flow that moved on
		// while Add was in flight; apply's orphan handling cancels it.
		if f.PlanTaskID != "" {
			return false
		}
		f.State = StateRunning
		f.Error = ""
		f.PlanTaskID = tasks[0].ID
		return true
	}, tasks)
}

// advancePlan reads the planning task and, once it is done, the plan file it
// wrote. Reached while EnablePlanning is set, PlanTaskID is recorded and
// Plan has not been read yet.
func (m *Manager) advancePlan(snap *Flow) (Flow, bool) {
	t, err := m.getTask(snap.PlanTaskID)
	if err != nil {
		return m.finish(snap, StateErrored,
			fmt.Sprintf("plan task %s is no longer in the queue", snap.PlanTaskID))
	}
	switch t.State {
	case queue.StateFailed:
		return m.finish(snap, StateErrored,
			fmt.Sprintf("%s failed: %s", describeTask(t), failureReason(t)))
	case queue.StateCancelled:
		return m.finish(snap, StateCancelled,
			fmt.Sprintf("%s was cancelled outside the flow", describeTask(t)))
	case queue.StateSkipped:
		return m.finish(snap, StateErrored,
			fmt.Sprintf("%s was skipped: %s", describeTask(t), failureReason(t)))
	}
	if t.State != queue.StateDone {
		return Flow{}, false
	}

	path := m.planPath(snap.ID)
	plan, rerr := readPlanFile(path)
	if rerr != nil {
		return m.finish(snap, StateErrored,
			fmt.Sprintf("the planner did not produce a plan at %s: %v", describePath(path), rerr))
	}

	return m.apply(snap, nil, func(f *Flow, _ *Round) bool {
		if f.Plan != "" || f.PlanTaskID != t.ID {
			return false
		}
		f.Plan = plan
		return true
	}, nil)
}
