package server

import (
	"net/http"
	"strconv"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// handleAgentStart handles POST /api/agent/start.
func (s *Server) handleAgentStart(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.AgentStartRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.ProjectPath == "" {
		req.ProjectPath = s.repoPath
	}
	if req.ProjectPath == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "project_path required")
		return
	}
	cleaned, perr := s.validateRepoPath(req.ProjectPath)
	if perr != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid project_path")
		return
	}
	req.ProjectPath = cleaned
	if req.Task == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "task required")
		return
	}
	opts := agentOptionsFromStart(req)
	id, err := s.Services.Agent.Start(r.Context(), req.ProjectPath, req.Task, opts)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.broadcastAgentStarted(id, req.Task, req.ProjectPath)
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.AgentStartResponse{AgentID: id}})
}

// handleAgentResume handles POST /api/agent/resume.
func (s *Server) handleAgentResume(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.AgentResumeRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.AgentID == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "agent_id required")
		return
	}
	opts := agentOptionsFromResume(req)
	id, err := s.Services.Agent.Resume(r.Context(), req.AgentID, req.Message, opts)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.broadcastAgentStarted(id, req.Message, "")
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.AgentStartResponse{AgentID: id}})
}

// handleAgentInput handles POST /api/agent/input.
func (s *Server) handleAgentInput(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.AgentInputRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	id := req.ResolvedID()
	if id == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "agent_id required")
		return
	}
	if req.Message == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "message required")
		return
	}
	if err := s.Services.Agent.SendInput(r.Context(), id, req.Message); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleAgentKill handles POST /api/agent/kill.
func (s *Server) handleAgentKill(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.AgentQueryRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	id := req.ResolvedID()
	if id == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "agent_id required")
		return
	}
	if err := s.Services.Agent.Kill(r.Context(), id); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleAgentRemove handles POST /api/agent/remove.
func (s *Server) handleAgentRemove(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.AgentQueryRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	id := req.ResolvedID()
	if id == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "agent_id required")
		return
	}
	if err := s.Services.Agent.Remove(r.Context(), id); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	// Drop any output-offset bookkeeping.
	s.outputMu.Lock()
	delete(s.agentOutputOffsets, id)
	s.outputMu.Unlock()
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleAgentList handles GET /api/agent/list.
func (s *Server) handleAgentList(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	snaps, err := s.Services.Agent.List(r.Context())
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	dtos := make([]api.AgentSnapshotDTO, len(snaps))
	for i, sn := range snaps {
		dtos[i] = api.AgentSnapshotToDTO(sn)
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.AgentListResponse{Agents: dtos}})
}

// handleAgentGet handles GET /api/agent/get?agent_id= and the legacy
// /api/agent/status alias.
func (s *Server) handleAgentGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	id := r.URL.Query().Get("agent_id")
	if id == "" {
		id = r.URL.Query().Get("session_id") // legacy
	}
	if id == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "agent_id required")
		return
	}
	snap, err := s.Services.Agent.Get(r.Context(), id)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	dto := api.AgentSnapshotToDTO(*snap)
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: dto})
}

// handleAgentOutput handles GET /api/agent/output?agent_id=&offset=.
func (s *Server) handleAgentOutput(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	q := r.URL.Query()
	id := q.Get("agent_id")
	if id == "" {
		id = q.Get("session_id") // legacy
	}
	if id == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "agent_id required")
		return
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	entries, err := s.Services.Agent.Output(r.Context(), id, offset)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.AgentOutputResponse{AgentID: id, Entries: entries}})
}

// handleAgentStats handles GET /api/agent/stats.
func (s *Server) handleAgentStats(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	stats, err := s.Services.Agent.Stats(r.Context())
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: stats})
}

// handleAgentMax handles GET /api/agent/max.
func (s *Server) handleAgentMax(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	max, err := s.Services.Agent.MaxAgents(r.Context())
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.AgentMaxResponse{Max: max}})
}

// handleAgentSubscribe handles POST /api/agent/subscribe — stream.
func (s *Server) handleAgentSubscribe(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.AgentSubscribeRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	ctx, ctxCancel := streamContext()
	ch, cancel, err := s.Services.Agent.Subscribe(ctx, req.AgentID)
	if err != nil {
		ctxCancel()
		s.writeServiceErr(w, err)
		return
	}
	id := s.registerStream(combineCancel(ctxCancel, cancel))
	go pumpStream(s, id, ch, agentEventFrame)
	s.writeJSON(w, http.StatusAccepted, api.APIResponse{Success: true, Data: api.StreamStartResponse{StreamID: id}})
}

// handleAgentSubscribeAll handles POST /api/agent/subscribe_all — stream.
func (s *Server) handleAgentSubscribeAll(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	ctx, ctxCancel := streamContext()
	ch, cancel, err := s.Services.Agent.SubscribeAll(ctx)
	if err != nil {
		ctxCancel()
		s.writeServiceErr(w, err)
		return
	}
	id := s.registerStream(combineCancel(ctxCancel, cancel))
	go pumpStream(s, id, ch, agentEventFrame)
	s.writeJSON(w, http.StatusAccepted, api.APIResponse{Success: true, Data: api.StreamStartResponse{StreamID: id}})
}

// agentEventFrame is the frameOf hook for AgentEvent streams.
func agentEventFrame(ev service.AgentEvent) (api.StreamFrame, bool) {
	done := ev.Kind == service.AgentEventComplete || ev.Kind == service.AgentEventError
	return api.StreamFrame{Frame: ev, Done: done, Error: ev.Err, Timestamp: ev.Timestamp}, done
}

func agentOptionsFromStart(req api.AgentStartRequest) service.AgentOptions {
	opts := service.AgentOptions{
		Model:        req.Model,
		Effort:       req.Effort,
		AllowedTools: req.AllowedTools,
		MaxTurns:     req.MaxTurns,
		ContextFiles: req.ContextFiles,
		SmartRoute:   req.SmartRoute,
		Summary:      req.Summary,
		BackendName:  req.Backend,
	}
	if req.TimeoutSecs > 0 {
		opts.Timeout = time.Duration(req.TimeoutSecs) * time.Second
	}
	return opts
}

func agentOptionsFromResume(req api.AgentResumeRequest) service.AgentOptions {
	opts := service.AgentOptions{
		Model:        req.Model,
		Effort:       req.Effort,
		AllowedTools: req.AllowedTools,
		MaxTurns:     req.MaxTurns,
		ContextFiles: req.ContextFiles,
		SmartRoute:   req.SmartRoute,
		Summary:      req.Summary,
		BackendName:  req.Backend,
	}
	if req.TimeoutSecs > 0 {
		opts.Timeout = time.Duration(req.TimeoutSecs) * time.Second
	}
	return opts
}
