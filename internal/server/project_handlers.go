package server

import (
	"net/http"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/project"
)

// handleProjectList handles GET /api/project/list
func (s *Server) handleProjectList(w http.ResponseWriter, r *http.Request) {
	if s.projectLoader == nil {
		s.writeJSON(w, http.StatusOK, api.APIResponse{
			Success: true,
			Data: api.ProjectListResponse{
				Projects: []string{},
				Loaded:   []string{},
			},
		})
		return
	}

	s.writeJSON(w, http.StatusOK, api.APIResponse{
		Success: true,
		Data: api.ProjectListResponse{
			Projects: s.projectLoader.ListProjectKeys(),
			Loaded:   s.projectLoader.ListLoadedProjects(),
		},
	})
}

// handleProjectLoad handles POST /api/project/load
func (s *Server) handleProjectLoad(w http.ResponseWriter, r *http.Request) {
	if s.projectLoader == nil {
		s.writeError(w, http.StatusBadRequest, "no project config loaded")
		return
	}

	var req api.ProjectLoadRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	proj, err := s.projectLoader.LoadProject(req.Key)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, api.APIResponse{
		Success: true,
		Data:    proj.Status(),
	})
}

// handleProjectStatus handles GET /api/project/status?key=<key>
func (s *Server) handleProjectStatus(w http.ResponseWriter, r *http.Request) {
	if s.projectLoader == nil {
		s.writeError(w, http.StatusBadRequest, "no project config loaded")
		return
	}

	key := r.URL.Query().Get("key")
	if key == "" {
		s.writeError(w, http.StatusBadRequest, "missing project key")
		return
	}

	proj := s.projectLoader.GetProject(key)
	if proj == nil {
		s.writeError(w, http.StatusNotFound, "project not loaded")
		return
	}

	s.writeJSON(w, http.StatusOK, api.APIResponse{
		Success: true,
		Data:    proj.Status(),
	})
}

// handleProjectRefresh handles POST /api/project/refresh
func (s *Server) handleProjectRefresh(w http.ResponseWriter, r *http.Request) {
	if s.projectLoader == nil {
		s.writeError(w, http.StatusBadRequest, "no project config loaded")
		return
	}

	var req api.ProjectLoadRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	if err := s.projectLoader.RefreshProject(req.Key); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	proj := s.projectLoader.GetProject(req.Key)
	if proj == nil {
		s.writeError(w, http.StatusNotFound, "project not found after refresh")
		return
	}

	status := proj.Status()

	// Broadcast update
	s.wsBroadcast(api.WSMessage{Type: api.WSEventProjectUpdate, Payload: status})

	s.writeJSON(w, http.StatusOK, api.APIResponse{
		Success: true,
		Data:    status,
	})
}

// handleProjectBranchCheck handles POST /api/project/branch/check
func (s *Server) handleProjectBranchCheck(w http.ResponseWriter, r *http.Request) {
	if s.projectLoader == nil {
		s.writeError(w, http.StatusBadRequest, "no project config loaded")
		return
	}

	var req api.ProjectBranchRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	proj := s.projectLoader.GetProject(req.Key)
	if proj == nil {
		s.writeError(w, http.StatusNotFound, "project not loaded")
		return
	}

	existence := proj.BranchExistsAcross(req.Branch)
	s.writeJSON(w, http.StatusOK, api.APIResponse{
		Success: true,
		Data:    existence,
	})
}

// handleProjectBranchCompare handles POST /api/project/branch/compare
func (s *Server) handleProjectBranchCompare(w http.ResponseWriter, r *http.Request) {
	if s.projectLoader == nil {
		s.writeError(w, http.StatusBadRequest, "no project config loaded")
		return
	}

	var req api.ProjectBranchRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	proj := s.projectLoader.GetProject(req.Key)
	if proj == nil {
		s.writeError(w, http.StatusNotFound, "project not loaded")
		return
	}

	comparison := project.CompareBranchAcrossRepos(proj, req.Branch)
	s.writeJSON(w, http.StatusOK, api.APIResponse{
		Success: true,
		Data:    comparison,
	})
}

// handleProjectContext handles GET /api/project/context?key=<key>
// Returns a text summary suitable for Claude Code agent context
func (s *Server) handleProjectContext(w http.ResponseWriter, r *http.Request) {
	if s.projectLoader == nil {
		s.writeError(w, http.StatusBadRequest, "no project config loaded")
		return
	}

	key := r.URL.Query().Get("key")
	if key == "" {
		s.writeError(w, http.StatusBadRequest, "missing project key")
		return
	}

	proj := s.projectLoader.GetProject(key)
	if proj == nil {
		s.writeError(w, http.StatusNotFound, "project not loaded")
		return
	}

	s.writeJSON(w, http.StatusOK, api.APIResponse{
		Success: true,
		Data:    proj.ContextSummary(),
	})
}
