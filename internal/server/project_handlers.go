package server

import (
	"net/http"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// handleProjectList handles GET /api/project/list.
func (s *Server) handleProjectList(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	keys, err := s.Services.Project.List(r.Context())
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	// We surface both "configured" and "loaded" via the same list for now;
	// service.ProjectService.List returns one consolidated set. Filling Loaded
	// with the same slice keeps the legacy ProjectListResponse shape stable.
	s.writeJSON(w, http.StatusOK, api.APIResponse{
		Success: true,
		Data: api.ProjectListResponse{
			Projects: keys,
			Loaded:   keys,
		},
	})
}

// handleProjectLoad handles POST /api/project/load.
func (s *Server) handleProjectLoad(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.ProjectLoadRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	info, err := s.Services.Project.Load(r.Context(), req.Key)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: info})
}

// handleProjectInfo handles GET /api/project/info?handle=.
func (s *Server) handleProjectInfo(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	handle := service.ProjectHandle(r.URL.Query().Get("handle"))
	info, err := s.Services.Project.Info(r.Context(), handle)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: info})
}

// handleProjectStatus handles GET /api/project/status?handle=.
func (s *Server) handleProjectStatus(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	handle := service.ProjectHandle(r.URL.Query().Get("handle"))
	if handle == "" {
		handle = service.ProjectHandle(r.URL.Query().Get("key")) // legacy
	}
	st, err := s.Services.Project.Status(r.Context(), handle)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: st})
}

// handleProjectRefresh handles POST /api/project/refresh.
func (s *Server) handleProjectRefresh(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.ProjectHandleRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	st, err := s.Services.Project.Refresh(r.Context(), req.Handle)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	// Broadcast the post-mismatch envelope so client subscribers can decode it
	// uniformly (fixes COVERAGE.md §3e).
	s.wsBroadcast(api.WSMessage{
		Type: api.WSEventProjectUpdate,
		Payload: api.ProjectUpdatePayload{
			Handle: string(req.Handle),
			Status: st,
		},
	})
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: st})
}

// handleProjectBranchCheck handles POST /api/project/branch/check.
func (s *Server) handleProjectBranchCheck(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.ProjectBranchRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	handle := req.Handle
	if handle == "" && req.Key != "" {
		handle = service.ProjectHandle(req.Key)
	}
	res, err := s.Services.Project.BranchExists(r.Context(), handle, req.Branch)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: res})
}

// handleProjectContext handles GET /api/project/context?handle=.
func (s *Server) handleProjectContext(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	handle := service.ProjectHandle(r.URL.Query().Get("handle"))
	if handle == "" {
		handle = service.ProjectHandle(r.URL.Query().Get("key"))
	}
	ctx, err := s.Services.Project.ContextSummary(r.Context(), handle)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.ProjectContextResponse{Context: ctx}})
}

// handleProjectConfigPath handles GET /api/project/config_path.
func (s *Server) handleProjectConfigPath(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	path, err := s.Services.Project.DefaultConfigPath(r.Context())
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.ProjectConfigPathResponse{Path: path}})
}

// handleProjectSubscribe handles POST /api/project/subscribe — stream.
func (s *Server) handleProjectSubscribe(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.ProjectHandleRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	ctx, ctxCancel := streamContext()
	ch, cancel, err := s.Services.Project.Subscribe(ctx, req.Handle)
	if err != nil {
		ctxCancel()
		s.writeServiceErr(w, err)
		return
	}
	id := s.registerStream(combineCancel(ctxCancel, cancel))
	go pumpStream(s, id, ch, func(ev service.ProjectEvent) (api.StreamFrame, bool) {
		s.wsBroadcast(api.WSMessage{
			Type: api.WSEventProjectUpdate,
			Payload: api.ProjectUpdatePayload{
				Handle:   string(ev.Handle),
				Status:   ev.Status,
				RepoName: ev.RepoName,
			},
		})
		return api.StreamFrame{Frame: ev}, false
	})
	s.writeJSON(w, http.StatusAccepted, api.APIResponse{Success: true, Data: api.StreamStartResponse{StreamID: id}})
}

// --- workflow endpoints ---

// handleWorkflowCreate handles POST /api/project/workflow/create.
func (s *Server) handleWorkflowCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.WorkflowCreateRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	wf, err := s.Services.Project.CreateWorkflow(r.Context(), req.Handle, req.Branch, req.BaseDir)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: wf})
}

// handleWorkflowRemove handles POST /api/project/workflow/remove.
func (s *Server) handleWorkflowRemove(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.WorkflowRemoveRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	wf, err := s.Services.Project.RemoveWorkflow(r.Context(), req.Handle, req.Branch)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: wf})
}

// handleWorkflowList handles GET /api/project/workflow/list.
func (s *Server) handleWorkflowList(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	handle := service.ProjectHandle(r.URL.Query().Get("handle"))
	wfs, err := s.Services.Project.LoadWorkflows(r.Context(), handle)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.WorkflowListResponse{Workflows: wfs}})
}

// handleWorkflowSave handles POST /api/project/workflow/save.
func (s *Server) handleWorkflowSave(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.WorkflowSaveRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if err := s.Services.Project.SaveWorkflows(r.Context(), req.Handle, req.Workflows); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleWorkflowDiscover handles POST /api/project/workflow/discover — stream.
func (s *Server) handleWorkflowDiscover(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.WorkflowDiscoverRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	ctx, ctxCancel := streamContext()
	ch, cancel, err := s.Services.Project.DiscoverWorkflowsAllRepos(ctx, req.Handle, req.Skip)
	if err != nil {
		ctxCancel()
		s.writeServiceErr(w, err)
		return
	}
	id := s.registerStream(combineCancel(ctxCancel, cancel))
	go pumpStream(s, id, ch, func(ev service.DiscoveryProgressEvent) (api.StreamFrame, bool) {
		s.wsBroadcast(api.WSMessage{Type: api.WSEventDiscoveryProgress, Payload: ev})
		return api.StreamFrame{Frame: ev, Done: ev.Done, Error: ev.Err}, ev.Done
	})
	s.writeJSON(w, http.StatusAccepted, api.APIResponse{Success: true, Data: api.StreamStartResponse{StreamID: id}})
}

// handleWorkflowSubscribe handles POST /api/project/workflow/subscribe — stream.
func (s *Server) handleWorkflowSubscribe(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.ProjectHandleRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	ctx, ctxCancel := streamContext()
	ch, cancel, err := s.Services.Project.SubscribeWorkflows(ctx, req.Handle)
	if err != nil {
		ctxCancel()
		s.writeServiceErr(w, err)
		return
	}
	id := s.registerStream(combineCancel(ctxCancel, cancel))
	go pumpStream(s, id, ch, func(ev service.WorkflowEvent) (api.StreamFrame, bool) {
		s.wsBroadcast(api.WSMessage{Type: api.WSEventWorkflowUpdated, Payload: ev})
		return api.StreamFrame{Frame: ev}, false
	})
	s.writeJSON(w, http.StatusAccepted, api.APIResponse{Success: true, Data: api.StreamStartResponse{StreamID: id}})
}
