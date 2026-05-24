package server

import (
	"net/http"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// handlePipelineStatuses handles POST /api/pipeline/statuses.
func (s *Server) handlePipelineStatuses(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.PipelineStatusesRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	statuses, err := s.Services.Pipeline.Statuses(r.Context(), s.resolveRepoPath(req.RepoPath), req.Branches)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.PipelineStatusesResponse{Pipelines: statuses}})
}

// handlePipelineRetry handles POST /api/pipeline/retry.
func (s *Server) handlePipelineRetry(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.PipelineRetryRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if err := s.Services.Pipeline.Retry(r.Context(), s.resolveRepoPath(req.RepoPath), req.Branch); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handlePipelineSubscribe handles POST /api/pipeline/subscribe — stream.
func (s *Server) handlePipelineSubscribe(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.RepoPathRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	ctx, ctxCancel := streamContext()
	ch, cancel, err := s.Services.Pipeline.Subscribe(ctx, s.resolveRepoPath(req.RepoPath))
	if err != nil {
		ctxCancel()
		s.writeServiceErr(w, err)
		return
	}
	id := s.registerStream(combineCancel(ctxCancel, cancel))
	go pumpStream(s, id, ch, func(ev service.PipelineEvent) (api.StreamFrame, bool) {
		// Also fan out a top-level pipeline_update broadcast for the legacy
		// WS handler in internal/app/ws.go.
		s.wsBroadcast(api.WSMessage{Type: api.WSEventPipelineUpdate, Payload: ev})
		return api.StreamFrame{Frame: ev}, false
	})
	s.writeJSON(w, http.StatusAccepted, api.APIResponse{Success: true, Data: api.StreamStartResponse{StreamID: id}})
}
