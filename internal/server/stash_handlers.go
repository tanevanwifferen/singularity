package server

import (
	"net/http"
	"strconv"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// handleStashList handles GET /api/stash/list.
func (s *Server) handleStashList(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	entries, err := s.Services.Stash.List(r.Context(), s.resolveRepoPath(r.URL.Query().Get("repo_path")))
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.StashListResponse{Entries: entries}})
}

// handleStashGet handles GET /api/stash/get.
func (s *Server) handleStashGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	q := r.URL.Query()
	idx, err := strconv.Atoi(q.Get("index"))
	if err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid index")
		return
	}
	entry, err := s.Services.Stash.Get(r.Context(), s.resolveRepoPath(q.Get("repo_path")), idx)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: entry})
}

// handleStashCreate handles POST /api/stash/create.
func (s *Server) handleStashCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.StashCreateRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	idx, err := s.Services.Stash.Create(r.Context(), s.resolveRepoPath(req.RepoPath), req.Message, req.IncludeUntracked)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.StashCreateResponse{Index: idx}})
}

// handleStashApply handles POST /api/stash/apply.
func (s *Server) handleStashApply(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.StashApplyRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if err := s.Services.Stash.Apply(r.Context(), s.resolveRepoPath(req.RepoPath), req.Index, req.Pop); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleStashDrop handles POST /api/stash/drop.
func (s *Server) handleStashDrop(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.StashDropRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if err := s.Services.Stash.Drop(r.Context(), s.resolveRepoPath(req.RepoPath), req.Index); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleStashClear handles POST /api/stash/clear.
func (s *Server) handleStashClear(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.RepoPathRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if err := s.Services.Stash.Clear(r.Context(), s.resolveRepoPath(req.RepoPath)); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleStashListAll handles POST /api/stash/list_all.
func (s *Server) handleStashListAll(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.ProjectHandleRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	list, err := s.Services.Stash.ListAllRepos(r.Context(), req.Handle)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.StashListAllResponse{Repos: list}})
}

// handleStashAll handles POST /api/stash/all.
func (s *Server) handleStashAll(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.StashAllRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	res, err := s.Services.Stash.StashAllRepos(r.Context(), req.Handle, req.Message, req.IncludeUntracked)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.StashBulkResponse{Results: res}})
}

// handleStashApplyAll handles POST /api/stash/apply_all.
func (s *Server) handleStashApplyAll(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.StashApplyAllRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	res, err := s.Services.Stash.ApplyStashAllRepos(r.Context(), req.Handle, req.Message, req.Pop)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.StashBulkResponse{Results: res}})
}
