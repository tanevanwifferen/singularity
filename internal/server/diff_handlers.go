package server

import (
	"net/http"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// handleDiffBranch handles POST /api/diff/branch and the legacy
// /api/branch/diff alias.
func (s *Server) handleDiffBranch(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.BranchDiffRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	d, err := s.Services.Diff.BranchDiff(r.Context(), s.resolveRepoPath(req.RepoPath), req.BranchA, req.BranchB)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: d})
}

// handleDiffWorkdir handles GET /api/diff/workdir?repo_path=.
func (s *Server) handleDiffWorkdir(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	d, err := s.Services.Diff.WorkdirStatus(r.Context(), s.resolveRepoPath(r.URL.Query().Get("repo_path")))
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: d})
}

// handleDiffFile handles POST /api/diff/file.
func (s *Server) handleDiffFile(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.FileDiffRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	diff, err := s.Services.Diff.FileDiff(r.Context(), s.resolveRepoPath(req.RepoPath), req.BranchA, req.BranchB, req.Path)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.FileDiffResponse{Diff: diff}})
}

// handleDiffFileStaged handles POST /api/diff/file_staged.
func (s *Server) handleDiffFileStaged(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.SingleFileDiffRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	diff, err := s.Services.Diff.StagedFileDiff(r.Context(), s.resolveRepoPath(req.RepoPath), req.Path)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.FileDiffResponse{Diff: diff}})
}

// handleDiffFileUnstaged handles POST /api/diff/file_unstaged.
func (s *Server) handleDiffFileUnstaged(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.SingleFileDiffRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	diff, err := s.Services.Diff.UnstagedFileDiff(r.Context(), s.resolveRepoPath(req.RepoPath), req.Path)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.FileDiffResponse{Diff: diff}})
}

// handleDiffFileDeep handles POST /api/diff/file_deep.
func (s *Server) handleDiffFileDeep(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.DeepFileDiffRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	hunks, raw, err := s.Services.Diff.DeepFileDiff(r.Context(), s.resolveRepoPath(req.RepoPath), req.MergeBase, req.Branch, req.DefaultBranch, req.Path)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.DeepFileDiffResponse{Hunks: hunks, RawDiff: raw}})
}

// handleDiffMergeBase handles POST /api/diff/merge_base.
func (s *Server) handleDiffMergeBase(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.MergeBaseRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	sha, err := s.Services.Diff.MergeBase(r.Context(), s.resolveRepoPath(req.RepoPath), req.RefA, req.RefB)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.MergeBaseResponse{SHA: sha}})
}

// handleDiffStageHunk handles POST /api/diff/stage_hunk.
func (s *Server) handleDiffStageHunk(w http.ResponseWriter, r *http.Request) {
	s.handleHunk(w, r, true, false)
}

// handleDiffUnstageHunk handles POST /api/diff/unstage_hunk.
func (s *Server) handleDiffUnstageHunk(w http.ResponseWriter, r *http.Request) {
	s.handleHunk(w, r, false, false)
}

// handleDiffStageLines handles POST /api/diff/stage_lines.
func (s *Server) handleDiffStageLines(w http.ResponseWriter, r *http.Request) {
	s.handleHunk(w, r, true, true)
}

// handleDiffUnstageLines handles POST /api/diff/unstage_lines.
func (s *Server) handleDiffUnstageLines(w http.ResponseWriter, r *http.Request) {
	s.handleHunk(w, r, false, true)
}

// handleHunk dispatches to the matching DiffService stage/unstage method.
func (s *Server) handleHunk(w http.ResponseWriter, r *http.Request, stage, byLines bool) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var err error
	if byLines {
		var req api.HunkLinesRequest
		if perr := s.parseJSON(r, &req); perr != nil {
			s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
			return
		}
		if stage {
			err = s.Services.Diff.StageLines(r.Context(), s.resolveRepoPath(req.RepoPath), req.Path, req.Hunk, req.SelectedLineIndices)
		} else {
			err = s.Services.Diff.UnstageLines(r.Context(), s.resolveRepoPath(req.RepoPath), req.Path, req.Hunk, req.SelectedLineIndices)
		}
	} else {
		var req api.HunkRequest
		if perr := s.parseJSON(r, &req); perr != nil {
			s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
			return
		}
		if stage {
			err = s.Services.Diff.StageHunk(r.Context(), s.resolveRepoPath(req.RepoPath), req.Path, req.Hunk)
		} else {
			err = s.Services.Diff.UnstageHunk(r.Context(), s.resolveRepoPath(req.RepoPath), req.Path, req.Hunk)
		}
	}
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleDiffAllRepos handles POST /api/diff/all_repos.
func (s *Server) handleDiffAllRepos(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.ProjectHandleRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	repos, err := s.Services.Diff.DiffAllRepos(r.Context(), req.Handle)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.DiffAllReposResponse{Repos: repos}})
}
