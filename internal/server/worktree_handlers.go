package server

import (
	"net/http"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// handleWorktreeList handles GET /api/worktree/list.
func (s *Server) handleWorktreeList(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	wts, err := s.Services.Worktree.List(r.Context(), s.resolveRepoPath(r.URL.Query().Get("repo_path")))
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.WorktreeListResponse{Worktrees: wts}})
}

// handleWorktreeCreate handles POST /api/worktree/create.
func (s *Server) handleWorktreeCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.WorktreeCreateRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if err := s.Services.Worktree.Create(r.Context(), s.resolveRepoPath(req.RepoPath), req.Path, req.Branch, req.CreateBranch, req.StartPoint); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleWorktreeRemove handles POST /api/worktree/remove.
func (s *Server) handleWorktreeRemove(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.WorktreeRemoveRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if err := s.Services.Worktree.Remove(r.Context(), s.resolveRepoPath(req.RepoPath), req.Path, req.Force); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleWorktreePrune handles POST /api/worktree/prune.
func (s *Server) handleWorktreePrune(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.RepoPathRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if err := s.Services.Worktree.Prune(r.Context(), s.resolveRepoPath(req.RepoPath)); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleWorktreeLock handles POST /api/worktree/lock.
func (s *Server) handleWorktreeLock(w http.ResponseWriter, r *http.Request) {
	s.handleWorktreeLockUnlock(w, r, true)
}

// handleWorktreeUnlock handles POST /api/worktree/unlock.
func (s *Server) handleWorktreeUnlock(w http.ResponseWriter, r *http.Request) {
	s.handleWorktreeLockUnlock(w, r, false)
}

func (s *Server) handleWorktreeLockUnlock(w http.ResponseWriter, r *http.Request, lock bool) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.WorktreePathRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	var err error
	if lock {
		err = s.Services.Worktree.Lock(r.Context(), s.resolveRepoPath(req.RepoPath), req.Path)
	} else {
		err = s.Services.Worktree.Unlock(r.Context(), s.resolveRepoPath(req.RepoPath), req.Path)
	}
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}
