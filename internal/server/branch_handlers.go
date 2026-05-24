package server

import (
	"net/http"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// handleBranchList handles GET /api/branch/list?repo_path=.
func (s *Server) handleBranchList(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	path := s.resolveRepoPath(r.URL.Query().Get("repo_path"))
	branches, err := s.Services.Branch.List(r.Context(), path)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.BranchListResponse{Branches: branches}})
}

// handleBranchCreate handles POST /api/branch/create.
func (s *Server) handleBranchCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.BranchCreateRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if err := s.Services.Branch.Create(r.Context(), s.resolveRepoPath(req.RepoPath), req.Name, req.From); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleBranchCheckout handles POST /api/branch/checkout.
func (s *Server) handleBranchCheckout(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.BranchCheckoutRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if err := s.Services.Branch.Checkout(r.Context(), s.resolveRepoPath(req.RepoPath), req.Branch); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.BroadcastBranchUpdate(req.Branch)
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleBranchCheckoutDetached handles POST /api/branch/checkout_detached.
func (s *Server) handleBranchCheckoutDetached(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.BranchCheckoutDetachedRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if err := s.Services.Branch.CheckoutDetached(r.Context(), s.resolveRepoPath(req.RepoPath)); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleBranchCheckoutDetachedAt handles POST /api/branch/checkout_detached_at.
func (s *Server) handleBranchCheckoutDetachedAt(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.BranchCheckoutDetachedAtRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if err := s.Services.Branch.CheckoutDetachedAt(r.Context(), s.resolveRepoPath(req.RepoPath), req.Commit); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleBranchDelete handles POST /api/branch/delete.
func (s *Server) handleBranchDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.BranchDeleteRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if err := s.Services.Branch.Delete(r.Context(), s.resolveRepoPath(req.RepoPath), req.Branch, req.Force); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleBranchHEAD handles GET /api/branch/head?repo_path=.
func (s *Server) handleBranchHEAD(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	head, err := s.Services.Branch.HEAD(r.Context(), s.resolveRepoPath(r.URL.Query().Get("repo_path")))
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.BranchHEADResponse{HEAD: head}})
}

// handleBranchResolveRef handles GET /api/branch/resolve?repo_path=&ref=.
func (s *Server) handleBranchResolveRef(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	q := r.URL.Query()
	sha, err := s.Services.Branch.ResolveRef(r.Context(), s.resolveRepoPath(q.Get("repo_path")), q.Get("ref"))
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.BranchResolveRefResponse{SHA: sha}})
}

// handleBranchCompare handles POST /api/branch/compare.
func (s *Server) handleBranchCompare(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.BranchComparisonRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	cmp, err := s.Services.Branch.Compare(r.Context(), s.resolveRepoPath(req.RepoPath), req.BranchA, req.BranchB)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: cmp})
}

// handleBranchCompareTree handles POST /api/branch/compare_tree.
func (s *Server) handleBranchCompareTree(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.BranchComparisonRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	cmp, err := s.Services.Branch.CompareByTree(r.Context(), s.resolveRepoPath(req.RepoPath), req.BranchA, req.BranchB)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: cmp})
}
