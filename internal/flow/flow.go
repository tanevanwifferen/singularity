// Package flow implements adversarial review flows: repeated
// implement → review → fix rounds over one piece of work, driven until a
// reviewer accepts it or a round cap is hit.
//
// A flow is built on top of internal/queue rather than beside it. It owns one
// queue ("flow-<id>") and appends one round's two tasks at a time — a work
// task and a review task that depends on it — because a round count that
// depends on a verdict cannot be expressed as a DAG submitted up front. The
// queue therefore never learns what a flow is; the flow→task mapping lives in
// the Round record, and the reverse direction is recoverable from the task's
// QueueID.
//
// The accept/reject decision is never a task's exit state. A reviewer that
// finds ten blockers still exits successfully, so the decision travels in a
// structured Verdict the reviewer writes and the daemon parses (verdict.go);
// the task's terminal state is liveness only, saying when to go look.
//
// This file holds the domain types, wire-first with snake_case tags — the
// choice internal/queue made, and the reason internal/api can alias these
// types instead of re-projecting them.
package flow

import (
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/queue"
)

// TaskOptions is queue.TaskOptions: a flow's per-step options are exactly a
// queued task's, and aliasing keeps them one type all the way to the wire.
// The dependency only goes this way — internal/queue must not import this
// package.
type TaskOptions = queue.TaskOptions

// State is a flow's position in its lifecycle.
//
//	pending ──round 1 submitted──► running ──accept verdict──► accepted
//	                                  │
//	                                  ├─cap reached, still rejecting──► rejected
//	                                  ├─task failed / no parseable verdict──► errored
//	                                  └─operator cancelled the flow or a task──► cancelled
type State string

const (
	// StatePending means the flow record exists but round 1 has not been
	// submitted yet. One reconciler pass wide, so a flow is never observed
	// stateless.
	StatePending State = "pending"
	// StateRunning means a round's tasks are queued or in flight.
	StateRunning State = "running"
	// StateAccepted means a round's verdict was accept: the work in WorkDir
	// passed review.
	StateAccepted State = "accepted"
	// StateRejected means round MaxRounds ended in a reject. The work is on
	// disk and unaccepted; the last round's findings say why.
	StateRejected State = "rejected"
	// StateErrored means the flow machinery gave up — a task failed after
	// its retries, an agent timed out, or two review attempts in a row
	// produced no parseable verdict. Error says which.
	StateErrored State = "errored"
	// StateCancelled means the operator cancelled the flow, or cancelled one
	// of its tasks directly.
	StateCancelled State = "cancelled"
)

// Terminal reports whether the flow has reached a state it never leaves.
// Unlike a queued task, a flow has no retryable terminal state: nothing the
// operator does resumes a finished flow.
func (s State) Terminal() bool {
	switch s {
	case StateAccepted, StateRejected, StateErrored, StateCancelled:
		return true
	}
	return false
}

// Valid reports whether s is a known state. Used when validating states
// supplied as list filters.
func (s State) Valid() bool {
	switch s {
	case StatePending, StateRunning, StateAccepted, StateRejected,
		StateErrored, StateCancelled:
		return true
	}
	return false
}

// RoundState is one round's outcome. A round is running until its verdict is
// read; accepted and rejected are the two parseable verdicts, and errored
// means the round produced none the flow could use.
type RoundState string

const (
	// RoundRunning means the round's work or review task has not settled.
	RoundRunning RoundState = "running"
	// RoundAccepted means the round's verdict was accept.
	RoundAccepted RoundState = "accepted"
	// RoundRejected means the round's verdict was reject — including the
	// synthetic reject recorded for an unparseable first review attempt.
	RoundRejected RoundState = "rejected"
	// RoundErrored means the round could not be concluded: a task failed, or
	// both review attempts produced unparseable verdicts.
	RoundErrored RoundState = "errored"
)

// Terminal reports whether the round has settled.
func (s RoundState) Terminal() bool {
	switch s {
	case RoundAccepted, RoundRejected, RoundErrored:
		return true
	}
	return false
}

// Valid reports whether s is a known round state.
func (s RoundState) Valid() bool {
	switch s {
	case RoundRunning, RoundAccepted, RoundRejected, RoundErrored:
		return true
	}
	return false
}

// Decision is the reviewer's verdict word. Exactly two values are accepted;
// anything else makes the verdict unparseable (see ParseVerdict).
type Decision string

const (
	// DecisionAccept means the reviewer passed the work.
	DecisionAccept Decision = "accept"
	// DecisionReject means the reviewer wants another round.
	DecisionReject Decision = "reject"
)

// Valid reports whether d is a known decision.
func (d Decision) Valid() bool {
	return d == DecisionAccept || d == DecisionReject
}

// Severity grades a finding. Unknown values coming from a reviewer normalise
// to SeverityMajor rather than being rejected — a model will invent words, and
// treating an unrecognised grade as serious errs against the work, which is
// the direction §3.2 resolves every ambiguity in.
type Severity string

const (
	// SeverityBlocker is a defect that must be fixed before acceptance.
	SeverityBlocker Severity = "blocker"
	// SeverityMajor is a serious defect; also where unknown grades land.
	SeverityMajor Severity = "major"
	// SeverityMinor is advisory: it does not on its own contradict an accept.
	SeverityMinor Severity = "minor"
)

// Valid reports whether s is a known severity.
func (s Severity) Valid() bool {
	switch s {
	case SeverityBlocker, SeverityMajor, SeverityMinor:
		return true
	}
	return false
}

// Blocking reports whether a finding of this severity contradicts an accept.
func (s Severity) Blocking() bool {
	return s == SeverityBlocker || s == SeverityMajor
}

// Finding is one item the reviewer raised, mirroring its JSON one-for-one.
type Finding struct {
	Severity Severity `json:"severity"`
	File     string   `json:"file,omitempty"`
	// Line is 1-based and optional: a finding about a file as a whole, or
	// about the change in general, carries none.
	Line   int    `json:"line,omitempty"`
	Detail string `json:"detail"`
}

// Verdict is the reviewer's structured decision, parsed from the JSON file it
// was told to write. It is the sole accept path: a flow never infers an accept
// from a task exiting cleanly.
type Verdict struct {
	Decision Decision  `json:"verdict"`
	Summary  string    `json:"summary,omitempty"`
	Findings []Finding `json:"findings,omitempty"`
}

// Blockers returns the findings that would contradict an accept.
func (v *Verdict) Blockers() []Finding {
	var out []Finding
	for _, f := range v.Findings {
		if f.Severity.Blocking() {
			out = append(out, f)
		}
	}
	return out
}

// Round is one implement/fix → review cycle. It deliberately stores no step
// state of its own: a step's state is its queue.Task's, read on demand.
// Duplicating it would create two truths about one agent.
type Round struct {
	// N is 1-based.
	N int `json:"n"`
	// WorkTaskID is the implement task in round 1, the fix task afterwards.
	WorkTaskID   string `json:"work_task_id,omitempty"`
	ReviewTaskID string `json:"review_task_id,omitempty"`
	// ReviewAttempt is 1 or 2. It becomes 2 when the first review produced
	// no parseable verdict and the flow submitted one more review task for
	// the same round; a second failure errors the flow.
	ReviewAttempt int        `json:"review_attempt"`
	Verdict       *Verdict   `json:"verdict,omitempty"`
	State         RoundState `json:"state"`
	StartedAt     time.Time  `json:"started_at"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
}

// Flow is one adversarial review run over a single working directory.
type Flow struct {
	ID string `json:"id"` // "f1", "f2", …
	// QueueID is always "flow-<id>".
	QueueID string `json:"queue_id"`
	Title   string `json:"title,omitempty"`
	// IssueKey is the Jira issue (e.g. "PROJ-123") the flow's Goal was built
	// from, when it was started with --jira instead of a bare --prompt.
	// Empty for a flow given its goal directly.
	IssueKey string `json:"issue_key,omitempty"`
	// Goal is the implementer's task, repeated verbatim to every later fixer.
	Goal string `json:"goal"`
	// ReviewGoal is extra instruction for the reviewer, if any.
	ReviewGoal string      `json:"review_goal,omitempty"`
	WorkDir    string      `json:"work_dir"`
	MaxRounds  int         `json:"max_rounds"`
	Opts       TaskOptions `json:"opts,omitempty"`
	// ReviewOpts defaults to Opts; --reviewer-* overrides only what it names.
	ReviewOpts TaskOptions `json:"review_opts,omitempty"`
	// PlanOpts defaults to Opts, the same rule ReviewOpts follows;
	// --planner-* overrides only what it names. A flow that wants its
	// planning done on a bigger model than its implementer points this at
	// one, e.g. --planner-model opus.
	PlanOpts TaskOptions `json:"plan_opts,omitempty"`
	// EnablePlanning submits a one-off planning task (PlanPrompt) before
	// round 1, on PlanOpts. Its output becomes Plan once the task settles
	// done, and ImplementPrompt/FixPrompt fold Plan in under their own
	// heading. Off by default: a flow that does not ask for planning behaves
	// exactly as it did before this field existed.
	EnablePlanning bool `json:"enable_planning,omitempty"`

	State State  `json:"state"`
	Error string `json:"error,omitempty"`

	// PlanTaskID is the planning task, once submitted. Empty until then, and
	// always empty when EnablePlanning is false.
	PlanTaskID string `json:"plan_task_id,omitempty"`
	// Plan is the planner's output, read from its plan file once the
	// planning task settles done. Empty until then; written once and never
	// re-derived per round, the way Goal is repeated verbatim rather than
	// re-fetched.
	Plan string `json:"plan,omitempty"`

	Rounds    []*Round   `json:"rounds"`
	CreatedAt time.Time  `json:"created_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

// Clone returns a deep copy. Callers outside the package always receive copies
// so they cannot mutate manager state through a shared pointer — the rule
// queue.Task.Clone establishes.
func (f *Flow) Clone() Flow {
	out := *f
	out.Opts.ContextFiles = append([]string(nil), f.Opts.ContextFiles...)
	out.Opts.AllowedTools = append([]string(nil), f.Opts.AllowedTools...)
	out.ReviewOpts.ContextFiles = append([]string(nil), f.ReviewOpts.ContextFiles...)
	out.ReviewOpts.AllowedTools = append([]string(nil), f.ReviewOpts.AllowedTools...)
	out.PlanOpts.ContextFiles = append([]string(nil), f.PlanOpts.ContextFiles...)
	out.PlanOpts.AllowedTools = append([]string(nil), f.PlanOpts.AllowedTools...)
	if f.Opts.SmartRoute != nil {
		v := *f.Opts.SmartRoute
		out.Opts.SmartRoute = &v
	}
	if f.ReviewOpts.SmartRoute != nil {
		v := *f.ReviewOpts.SmartRoute
		out.ReviewOpts.SmartRoute = &v
	}
	if f.PlanOpts.SmartRoute != nil {
		v := *f.PlanOpts.SmartRoute
		out.PlanOpts.SmartRoute = &v
	}
	out.Rounds = make([]*Round, 0, len(f.Rounds))
	for _, r := range f.Rounds {
		rc := r.Clone()
		out.Rounds = append(out.Rounds, &rc)
	}
	if f.EndedAt != nil {
		v := *f.EndedAt
		out.EndedAt = &v
	}
	return out
}

// Clone returns a deep copy of the round, including its verdict.
func (r *Round) Clone() Round {
	out := *r
	if r.Verdict != nil {
		v := *r.Verdict
		v.Findings = append([]Finding(nil), r.Verdict.Findings...)
		out.Verdict = &v
	}
	if r.EndedAt != nil {
		v := *r.EndedAt
		out.EndedAt = &v
	}
	return out
}

// CurrentRound returns the most recent round, or nil before round 1 is
// submitted.
func (f *Flow) CurrentRound() *Round {
	if len(f.Rounds) == 0 {
		return nil
	}
	return f.Rounds[len(f.Rounds)-1]
}
