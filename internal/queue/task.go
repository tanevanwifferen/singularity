// Package queue implements the daemon-side task queue: a dependency-ordered
// set of agent tasks that the scheduler dispatches as capacity frees up.
//
// The queue exists because sequencing agent work used to live entirely in the
// caller's head. An orchestrator had to spawn an agent, poll `agents get`
// until it reached a terminal state, then spawn the next one — and stay alive
// for the whole chain. Submitting a DAG instead moves the waiting into the
// daemon: tasks declare what they depend on, the scheduler starts each one
// when its dependencies are satisfied and a slot is free, and the caller can
// disconnect and come back.
//
// Two invariants the scheduler enforces that callers previously had to
// remember for themselves:
//
//   - Capacity is backpressure, not an error. The engine's MaxAgents cap used
//     to surface as ErrAgentLimit for the caller to handle; a queued task
//     simply stays ready until a slot opens.
//   - One agent per working directory. Two agents pointed at the same
//     worktree corrupt each other's edits, so a task whose WorkDir already
//     has a live agent is not dispatched even when its dependencies are met.
//
// The package deliberately does not import internal/engine: the engine is
// reached through the AgentRunner interface so the scheduler is testable
// without spawning subprocesses. engine_runner.go holds the real adapter.
package queue

import "time"

// State is a task's position in its lifecycle.
//
//	blocked ──deps satisfied──► ready ──slot free──► running ──complete──► done
//	   ▲                                                │
//	   │                                                ├─error──► failed ──retry──► ready
//	   └── a dependency failed (OnFailure=block)         └─asks──► waiting_human ──answer──► running
//	                    │
//	                    ▼
//	                 skipped
type State string

const (
	// StateBlocked means at least one dependency has not finished yet.
	StateBlocked State = "blocked"
	// StateReady means every dependency is done; the task is waiting for a
	// free agent slot (or for its queue to be resumed).
	StateReady State = "ready"
	// StateRunning means an agent has been dispatched for this task.
	StateRunning State = "running"
	// StateWaitingHuman means the agent stopped to ask the operator a
	// question. Unreachable in this build: the engine never emits the
	// agent state that produces it (see agentStateWaitingHuman in
	// scheduler.go), so nothing user-facing may advertise it as live
	// behaviour. The state machine below keeps the arc because the queue
	// half is complete; only the engine's report is missing and will resume once Answer is called.
	StateWaitingHuman State = "waiting_human"
	// StateDone means the agent finished successfully.
	StateDone State = "done"
	// StateFailed means the agent errored or was killed, and no retries remain.
	StateFailed State = "failed"
	// StateCancelled means the operator cancelled the task before it finished.
	StateCancelled State = "cancelled"
	// StateSkipped means a dependency failed and this task's OnFailure
	// policy is "block", so it will never run.
	StateSkipped State = "skipped"
)

// Terminal reports whether the task has reached a state it never leaves
// without operator action. Retryable failure is still terminal: the
// scheduler has stopped working on it.
func (s State) Terminal() bool {
	switch s {
	case StateDone, StateFailed, StateCancelled, StateSkipped:
		return true
	}
	return false
}

// Active reports whether the task currently occupies an agent slot.
// waiting_human counts: the subprocess is alive and holding its worktree.
func (s State) Active() bool {
	return s == StateRunning || s == StateWaitingHuman
}

// Valid reports whether s is a known state. Used when validating states
// supplied as list filters.
func (s State) Valid() bool {
	switch s {
	case StateBlocked, StateReady, StateRunning, StateWaitingHuman,
		StateDone, StateFailed, StateCancelled, StateSkipped:
		return true
	}
	return false
}

// FailurePolicy decides what happens to a task's dependents when it fails.
type FailurePolicy string

const (
	// FailBlock marks every dependent skipped. The default: a fix task
	// whose review task crashed has nothing to work from.
	FailBlock FailurePolicy = "block"
	// FailContinue lets dependents run anyway — for tasks whose output is
	// advisory rather than required.
	FailContinue FailurePolicy = "continue"
	// FailAbortQueue cancels every not-yet-finished task in the queue.
	FailAbortQueue FailurePolicy = "abort-queue"
)

// Valid reports whether p is a known policy.
func (p FailurePolicy) Valid() bool {
	switch p {
	case FailBlock, FailContinue, FailAbortQueue:
		return true
	}
	return false
}

// TaskOptions mirrors the subset of engine.AgentOptions a queued task can
// set. Timeout is carried in seconds rather than a time.Duration so the
// persisted JSON stays readable and stable across Go versions.
type TaskOptions struct {
	Model       string `json:"model,omitempty"`
	Effort      string `json:"effort,omitempty"`
	Backend     string `json:"backend,omitempty"`
	MaxTurns    int    `json:"max_turns,omitempty"`
	TimeoutSecs int    `json:"timeout_secs,omitempty"`
	UseWorktree bool   `json:"use_worktree,omitempty"`
	// SmartRoute is a tri-state: nil means the submitter did not say, and
	// RouteEnabled decides. A plain bool would make "unset" and "off"
	// indistinguishable, which is how a queued task ended up running on
	// bare backend defaults while the same prompt given to `agents spawn`
	// was routed.
	SmartRoute   *bool    `json:"smart_route,omitempty"`
	ContextFiles []string `json:"context_files,omitempty"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
}

// RouteEnabled reports whether this task should be smart-routed: route
// unless told otherwise.
//
// The default deliberately does not restate the CLI's precedence rules
// (resolveSmartRoute: on unless --model or --effort was pinned). Those
// belong in one place and are being changed on another branch; a task that
// pins a model is already handled downstream, where engine.StartAgent skips
// routing whenever a model is set. All this needs to express is intent.
func (o TaskOptions) RouteEnabled() bool {
	return o.SmartRoute == nil || *o.SmartRoute
}

// Task is one unit of queued agent work.
type Task struct {
	ID      string `json:"id"`
	QueueID string `json:"queue_id"`
	// Title is a short label for display; it also becomes the agent's
	// Summary so the agent list stays readable.
	Title   string `json:"title,omitempty"`
	Prompt  string `json:"prompt"`
	WorkDir string `json:"work_dir"`
	// DependsOn holds task IDs (never local batch names — those are
	// resolved at submit time).
	DependsOn []string    `json:"depends_on,omitempty"`
	Opts      TaskOptions `json:"opts,omitempty"`

	State State  `json:"state"`
	Error string `json:"error,omitempty"`
	// AgentID is the most recent agent dispatched for this task. It stays
	// set after the task finishes so the transcript remains reachable.
	AgentID string `json:"agent_id,omitempty"`
	// Question carries the text the agent is blocked on while the task is
	// in waiting_human.
	Question string `json:"question,omitempty"`

	Attempts   int           `json:"attempts"`
	MaxRetries int           `json:"max_retries,omitempty"`
	OnFailure  FailurePolicy `json:"on_failure,omitempty"`
	Priority   int           `json:"priority,omitempty"`

	CreatedAt time.Time  `json:"created_at"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

// Clone returns a deep copy. Callers outside the package always receive
// copies so they cannot mutate scheduler state through a shared pointer.
func (t *Task) Clone() Task {
	out := *t
	out.DependsOn = append([]string(nil), t.DependsOn...)
	out.Opts.ContextFiles = append([]string(nil), t.Opts.ContextFiles...)
	out.Opts.AllowedTools = append([]string(nil), t.Opts.AllowedTools...)
	if t.StartedAt != nil {
		v := *t.StartedAt
		out.StartedAt = &v
	}
	if t.EndedAt != nil {
		v := *t.EndedAt
		out.EndedAt = &v
	}
	return out
}

// TaskSpec is the submit-time description of a task. Add resolves it into a
// Task, assigning the ID and translating After entries into DependsOn.
type TaskSpec struct {
	// Name is a batch-local alias so a single Add call can declare a whole
	// DAG without round-tripping through assigned IDs. Optional, and never
	// persisted.
	Name string `json:"name,omitempty"`
	// QueueID groups the task. Empty means "the queue the batch creates".
	QueueID string `json:"queue_id,omitempty"`

	Title   string `json:"title,omitempty"`
	Prompt  string `json:"prompt"`
	WorkDir string `json:"work_dir"`
	// After entries are either batch-local Names or already-assigned task
	// IDs; Add accepts both and rejects anything it cannot resolve.
	After []string `json:"after,omitempty"`

	Opts       TaskOptions   `json:"opts,omitempty"`
	Priority   int           `json:"priority,omitempty"`
	MaxRetries int           `json:"max_retries,omitempty"`
	OnFailure  FailurePolicy `json:"on_failure,omitempty"`
}

// Info summarises one queue for listings.
type Info struct {
	ID        string    `json:"id"`
	Paused    bool      `json:"paused"`
	CreatedAt time.Time `json:"created_at"`
	Total     int       `json:"total"`
	Blocked   int       `json:"blocked"`
	Ready     int       `json:"ready"`
	Running   int       `json:"running"`
	Waiting   int       `json:"waiting_human"`
	Done      int       `json:"done"`
	Failed    int       `json:"failed"`
	Cancelled int       `json:"cancelled"`
	Skipped   int       `json:"skipped"`
}

// Drained reports whether no task in the queue can still make progress on
// its own. A queue with a waiting_human task is NOT drained — it needs an
// answer, which is progress the operator can supply.
func (i Info) Drained() bool {
	return i.Blocked == 0 && i.Ready == 0 && i.Running == 0 && i.Waiting == 0
}

// GraphNode is one vertex of a queue's dependency graph.
type GraphNode struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	State State  `json:"state"`
}

// GraphEdge records that To depends on From (From must finish first).
type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Graph is a queue's dependency DAG, ordered topologically where possible.
type Graph struct {
	QueueID string      `json:"queue_id"`
	Nodes   []GraphNode `json:"nodes"`
	Edges   []GraphEdge `json:"edges"`
}
