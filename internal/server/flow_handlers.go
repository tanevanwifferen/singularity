package server

import (
	"net/http"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// handleFlowStart handles POST /api/flow/start.
func (s *Server) handleFlowStart(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireFlow(w) {
		return
	}
	var req api.FlowStartRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	// Everything the flow manager can judge — an empty goal, a work_dir that
	// is not a directory, a max_rounds out of range, worktree isolation it
	// refuses — is its own to reject, so it rejects the request as a unit.
	// Path cleaning is ours, exactly as in handleQueueAdd: the service must
	// never see a raw request path, and an empty work_dir stays empty so the
	// manager reports it missing rather than us reporting the daemon's cwd.
	if req.WorkDir != "" {
		cleaned, err := s.validateRepoPath(req.WorkDir)
		if err != nil {
			s.writeCoded(w, api.ErrCodeBadRequest, "invalid work_dir")
			return
		}
		req.WorkDir = cleaned
	}
	f, err := s.Services.Flow.Start(r.Context(), req)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.FlowStartResponse{Flow: *f}})
}

// handleFlowContinue handles POST /api/flow/continue.
//
// Only flow_id is checked here, the same line withFlowID draws: an id the
// handler cannot act on at all is ours to reject, and every judgement about
// the request — whether the flow's state allows a continue, whether the
// raised cap fits under the ceiling, whether the work dir is still there —
// belongs to the flow manager, which owns the record the answer depends on.
// A round count is deliberately not range-checked here either, so the
// operator gets the manager's message naming the largest number that would
// have worked rather than a bare "out of range" from the edge.
func (s *Server) handleFlowContinue(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireFlow(w) {
		return
	}
	var req api.FlowContinueRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.FlowID == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "flow_id required")
		return
	}
	f, err := s.Services.Flow.Continue(r.Context(), req.FlowID, req.Rounds)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.FlowContinueResponse{Flow: *f}})
}

// handleFlowList handles GET /api/flow/list?state=.
func (s *Server) handleFlowList(w http.ResponseWriter, r *http.Request) {
	if !s.requireFlow(w) {
		return
	}
	flows, err := s.Services.Flow.List(r.Context(), parseFlowStates(r.URL.Query()["state"]))
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.FlowListResponse{Flows: flows}})
}

// handleFlowGet handles GET /api/flow/get?flow_id=.
func (s *Server) handleFlowGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireFlow(w) {
		return
	}
	id := r.URL.Query().Get("flow_id")
	if id == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "flow_id required")
		return
	}
	f, err := s.Services.Flow.Get(r.Context(), id)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: f})
}

// handleFlowTree handles GET /api/flow/tree?flow_id=.
func (s *Server) handleFlowTree(w http.ResponseWriter, r *http.Request) {
	if !s.requireFlow(w) {
		return
	}
	id := r.URL.Query().Get("flow_id")
	if id == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "flow_id required")
		return
	}
	tree, err := s.Services.Flow.Tree(r.Context(), id)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.FlowTreeResponse{Tree: *tree}})
}

// handleFlowCancel handles POST /api/flow/cancel.
func (s *Server) handleFlowCancel(w http.ResponseWriter, r *http.Request) {
	s.withFlowID(w, r, func(flowID string) error {
		return s.Services.Flow.Cancel(r.Context(), flowID)
	})
}

// handleFlowRemove handles POST /api/flow/remove.
func (s *Server) handleFlowRemove(w http.ResponseWriter, r *http.Request) {
	s.withFlowID(w, r, func(flowID string) error {
		return s.Services.Flow.Remove(r.Context(), flowID)
	})
}

// withFlowID is the shared body of the per-flow mutations — withQueueID's
// counterpart: decode a FlowIDRequest, require flow_id, run op, reply with a
// bare success. Both of them differ only in the method they call.
func (s *Server) withFlowID(w http.ResponseWriter, r *http.Request, op func(flowID string) error) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireFlow(w) {
		return
	}
	var req api.FlowIDRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.FlowID == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "flow_id required")
		return
	}
	if err := op(req.FlowID); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// requireFlow is requireServices plus a nil check on the capability itself,
// which the queue handlers can do without and these cannot: a daemon that
// could not open its flow state directory is expected to keep serving, and
// the design says every /api/flow/* route answers UNAVAILABLE in that case.
// The service layer already reports ErrUnavailable for a nil flow manager;
// this covers the step before it, a Services with no Flow at all, which
// would otherwise be a nil-interface panic rather than a 503.
func (s *Server) requireFlow(w http.ResponseWriter) bool {
	if !s.requireServices(w) {
		return false
	}
	if s.Services.Flow == nil {
		s.writeCoded(w, api.ErrCodeUnavailable, "flow manager not available")
		return false
	}
	return true
}

// parseFlowStates reads the `state` list filter, accepting both spellings
// for the reason parseTaskStates does — repeated (?state=running&state=
// accepted) and comma-separated (?state=running,accepted). Unknown values
// are passed through so the flow manager rejects them with a message naming
// the offending state, which is a BAD_REQUEST and not an empty result.
func parseFlowStates(values []string) []service.FlowState {
	var out []service.FlowState
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			out = append(out, service.FlowState(part))
		}
	}
	return out
}

// broadcastFlowUpdated emits a flow_updated WS frame. The daemon registers
// this as the flow manager's OnFlowChange callback, which is why it takes the
// flow by value: the manager hands out clones precisely so a consumer like
// this one cannot mutate reconciler state.
func (s *Server) broadcastFlowUpdated(f api.Flow) {
	s.wsBroadcast(api.WSMessage{
		Type:    api.WSEventFlowUpdated,
		Payload: api.FlowUpdatedPayload{Flow: f},
	})
}

// FlowChangeHook returns the callback the daemon hands to
// flow.Manager.OnFlowChange. Exported (unlike the broadcast itself) for the
// reason QueueChangeHook is: the daemon wires it from outside the package,
// and keeping the broadcast unexported means there is exactly one way in.
func (s *Server) FlowChangeHook() func(api.Flow) {
	return s.broadcastFlowUpdated
}
