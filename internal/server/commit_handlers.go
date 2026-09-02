package server

import (
	"net/http"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// handleCommitSuggest handles POST /api/commit/suggest.
func (s *Server) handleCommitSuggest(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.RepoPathRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	msg, err := s.Services.Commit.SuggestMessage(r.Context(), s.resolveRepoPath(req.RepoPath))
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.CommitSuggestResponse{Message: msg}})
}

// handleCommitFiles handles GET /api/commit/files?repo_path=&hash=.
func (s *Server) handleCommitFiles(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	q := r.URL.Query()
	files, err := s.Services.Commit.Files(r.Context(), s.resolveRepoPath(q.Get("repo_path")), q.Get("hash"))
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.CommitFilesResponse{Files: files}})
}

// handleCommitFileDiff handles POST /api/commit/file_diff.
func (s *Server) handleCommitFileDiff(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.CommitFileDiffRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	diff, err := s.Services.Commit.FileDiff(r.Context(), s.resolveRepoPath(req.RepoPath), req.Hash, req.Path)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.FileDiffResponse{Diff: diff}})
}

// handleCommitFullDiff handles POST /api/commit/full_diff.
func (s *Server) handleCommitFullDiff(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.CommitFullDiffRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	diff, err := s.Services.Commit.FullDiff(r.Context(), s.resolveRepoPath(req.RepoPath), req.Hash)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.FileDiffResponse{Diff: diff}})
}

// handleCommitCherryPick handles POST /api/commit/cherry_pick.
func (s *Server) handleCommitCherryPick(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.CommitHashRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if err := s.Services.Commit.CherryPick(r.Context(), s.resolveRepoPath(req.RepoPath), req.Hash); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleCommitReset handles POST /api/commit/reset.
func (s *Server) handleCommitReset(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.CommitResetRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if err := s.Services.Commit.Reset(r.Context(), s.resolveRepoPath(req.RepoPath), req.Hash, req.Mode); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleCommitAmend handles POST /api/commit/amend.
func (s *Server) handleCommitAmend(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.CommitAmendRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if err := s.Services.Commit.AmendMessage(r.Context(), s.resolveRepoPath(req.RepoPath), req.Message); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleCommitStage handles POST /api/commit/stage.
func (s *Server) handleCommitStage(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.CommitStageRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if len(req.Files) == 0 && !req.All {
		s.writeCoded(w, api.ErrCodeBadRequest, "files or all required")
		return
	}
	if err := s.Services.Commit.Stage(r.Context(), s.resolveRepoPath(req.RepoPath), req.Files, req.All); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleCommitCreate handles POST /api/commit/create.
func (s *Server) handleCommitCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.CommitCreateRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if req.Message == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "message required")
		return
	}
	hash, err := s.Services.Commit.Create(r.Context(), s.resolveRepoPath(req.RepoPath), req.Message)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.CommitCreateResponse{Hash: hash}})
}

// handleCommitMessage handles POST /api/commit/message (legacy path).
func (s *Server) handleCommitMessage(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.CommitMessageRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	msg, err := s.Services.Commit.GenerateMessage(r.Context(), s.resolveRepoPath(req.RepoPath))
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: msg})
}
