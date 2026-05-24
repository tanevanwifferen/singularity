package server

import (
	"net/http"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// handleMRTitle handles POST /api/mr/title.
func (s *Server) handleMRTitle(w http.ResponseWriter, r *http.Request) {
	s.handleMRGenerate(w, r, true)
}

// handleMRDescription handles POST /api/mr/description.
func (s *Server) handleMRDescription(w http.ResponseWriter, r *http.Request) {
	s.handleMRGenerate(w, r, false)
}

func (s *Server) handleMRGenerate(w http.ResponseWriter, r *http.Request, title bool) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.MRGenerateRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	var text string
	var err error
	if title {
		text, err = s.Services.MR.GenerateTitle(r.Context(), s.resolveRepoPath(req.RepoPath), req.SourceBranch, req.TargetBranch)
	} else {
		text, err = s.Services.MR.GenerateDescription(r.Context(), s.resolveRepoPath(req.RepoPath), req.SourceBranch, req.TargetBranch)
	}
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.MRTextResponse{Text: text}})
}

// handleMRCreate handles POST /api/mr/create.
func (s *Server) handleMRCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.MRRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	mr, err := s.Services.MR.Create(r.Context(), s.resolveRepoPath(req.RepoPath), req.SourceBranch, req.TargetBranch, req.Title, req.Description, req.Reviewers)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: mr})
}

// handleMRCreateCLI handles POST /api/mr/create_cli.
func (s *Server) handleMRCreateCLI(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.MRCreateCLIRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	res, err := s.Services.MR.CreateCLI(r.Context(), s.resolveRepoPath(req.RepoPath), req.Provider, req.BaseBranch)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: res})
}
