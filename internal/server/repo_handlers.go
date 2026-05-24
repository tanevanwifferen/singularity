package server

import (
	"net/http"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// handleRepoOpen handles POST /api/repo/open.
func (s *Server) handleRepoOpen(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.RepoOpenRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	path := req.Path
	if path == "" {
		// Honor the legacy "no path -> cwd discovery" behavior.
		found, err := s.Services.Repo.Find(r.Context(), ".")
		if err != nil {
			s.writeServiceErr(w, err)
			return
		}
		path = found
	}
	info, err := s.Services.Repo.Open(r.Context(), path)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.repoPath = path
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: info})
}

// handleRepoInfo handles GET /api/repo/info?path= and the /api/repo alias.
func (s *Server) handleRepoInfo(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	path := s.resolveRepoPath(r.URL.Query().Get("path"))
	if path == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "no repo path provided")
		return
	}
	info, err := s.Services.Repo.Open(r.Context(), path)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: info})
}

// handleRepoFind handles GET /api/repo/find?path=.
func (s *Server) handleRepoFind(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "."
	}
	found, err := s.Services.Repo.Find(r.Context(), path)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.RepoFindResponse{Path: found}})
}

// handleRepoSubscribe handles POST /api/repo/subscribe — long-lived stream.
func (s *Server) handleRepoSubscribe(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.RepoSubscribeRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	ctx, ctxCancel := streamContext()
	ch, cancel, err := s.Services.Repo.Subscribe(ctx, req.RepoPath)
	if err != nil {
		ctxCancel()
		s.writeServiceErr(w, err)
		return
	}
	id := s.registerStream(combineCancel(ctxCancel, cancel))
	go pumpStream(s, id, ch, func(info *api.RepoInfo) (api.StreamFrame, bool) {
		return api.StreamFrame{Frame: info}, false
	})
	s.writeJSON(w, http.StatusAccepted, api.APIResponse{Success: true, Data: api.StreamStartResponse{StreamID: id}})
}
