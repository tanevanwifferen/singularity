package api

import "gitlab.com/tanevanwifferen1/singularity/internal/service"

// Flow DTOs. Like the queue surface and unlike the agent one, the domain
// types are wire-first — internal/flow declared them with snake_case tags and
// internal/service aliases them — so there is nothing to re-project here.
// Aliasing again keeps one definition of every shape from the reconciler to
// the wire, which makes a tag change impossible to apply on one side only.
type (
	Flow         = service.Flow
	FlowRound    = service.FlowRound
	FlowState    = service.FlowState
	FlowVerdict  = service.FlowVerdict
	FlowFinding  = service.FlowFinding
	FlowTree     = service.FlowTree
	FlowTreeNode = service.FlowTreeNode

	// FlowStartRequest is the body for POST /api/flow/start:
	// {title, goal, review_goal, work_dir, max_rounds, opts, review_opts}.
	// It is an alias for the same reason the others are — flow.StartRequest
	// was written as the wire shape, so a parallel struct here would be a
	// second truth about it that only differs when someone forgets.
	//
	// Only `goal` and `work_dir` are required; `max_rounds` defaults to 3.
	// The daemon cleans `work_dir` through Server.validateRepoPath before
	// the service sees it, so a relative path is resolved and a traversal
	// is rejected.
	FlowStartRequest = service.FlowStartRequest
)

// FlowStartResponse is the body for POST /api/flow/start. It carries the
// flow as recorded — FlowPending with no rounds, because submitting round 1
// is the reconciler's job. A caller waiting for work to be under way watches
// flow_updated frames rather than this response.
type FlowStartResponse struct {
	Flow Flow `json:"flow"`
}

// FlowContinueRequest is the body for POST /api/flow/continue. It names a
// flow and how many more rounds it may have, and carries nothing else on
// purpose: goal, review goal, work_dir and both option blocks are the
// flow's own and are not re-specifiable, because rounds recorded against
// one goal would stop meaning anything under another.
//
// Rounds is an increment, not a new ceiling: it is added to the flow's
// max_rounds, and 0 means the daemon's default of 3 more. The result must
// still land inside the 1..20 every flow is capped by, so a flow already at
// 20 is refused whatever is asked for.
type FlowContinueRequest struct {
	FlowID string `json:"flow_id"`
	Rounds int    `json:"rounds"`
}

// FlowContinueResponse is the body for POST /api/flow/continue, wrapped the
// way FlowStartResponse is. The flow comes back as recorded: FlowRunning
// with its raised cap and every round it already had, since opening the next
// one is the reconciler's job.
type FlowContinueResponse struct {
	Flow Flow `json:"flow"`
}

// FlowListResponse is the body for GET /api/flow/list?state=. Flows come
// back oldest first.
type FlowListResponse struct {
	Flows []Flow `json:"flows"`
}

// FlowTreeResponse is the body for GET /api/flow/tree?flow_id=. Wrapped
// rather than bare, matching QueueGraphResponse: the tree gains fields more
// readily than the envelope does.
type FlowTreeResponse struct {
	Tree FlowTree `json:"tree"`
}

// FlowIDRequest is the body for the per-flow mutations: POST
// /api/flow/cancel and /api/flow/remove.
type FlowIDRequest struct {
	FlowID string `json:"flow_id"`
}
