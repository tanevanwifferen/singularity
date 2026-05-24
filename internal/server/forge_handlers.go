package server

import (
	"net/http"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// handleForgeAuth handles GET /api/forge/auth.
func (s *Server) handleForgeAuth(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	auth, err := s.Services.Forge.DetectAuth(r.Context())
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: auth})
}

// handleForgeInfo handles GET /api/forge/info.
func (s *Server) handleForgeInfo(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	info, err := s.Services.Forge.Detect(r.Context())
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: info})
}

// handleForgeProvider handles GET /api/forge/provider?repo_path=.
func (s *Server) handleForgeProvider(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	provider, err := s.Services.Forge.DetectProvider(r.Context(), s.resolveRepoPath(r.URL.Query().Get("repo_path")))
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.ForgeProviderResponse{Provider: provider}})
}
