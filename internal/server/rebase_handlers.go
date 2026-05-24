package server

import (
	"net/http"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// handleRebasePlan handles POST /api/rebase/plan.
func (s *Server) handleRebasePlan(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.RebasePlanRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	commits, err := s.Services.Rebase.Plan(r.Context(), s.resolveRepoPath(req.RepoPath), req.Base, req.Current)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.RebasePlanResponse{Commits: commits}})
}

// handleRebaseStatus handles GET /api/rebase/status.
func (s *Server) handleRebaseStatus(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	inProgress, commit, err := s.Services.Rebase.Status(r.Context(), s.resolveRepoPath(r.URL.Query().Get("repo_path")))
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.RebaseStatusResponse{InProgress: inProgress, Commit: commit}})
}

// handleRebaseTodo handles POST /api/rebase/todo.
func (s *Server) handleRebaseTodo(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.RebaseTodoRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	todo, err := s.Services.Rebase.GenerateTodo(r.Context(), req.Commits)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.RebaseTodoResponse{Todo: todo}})
}

// handleRebaseContinue handles POST /api/rebase/continue.
func (s *Server) handleRebaseContinue(w http.ResponseWriter, r *http.Request) {
	s.handleRebaseSimpleAction(w, r, func(repoPath string) error {
		return s.Services.Rebase.Continue(r.Context(), repoPath)
	})
}

// handleRebaseSkip handles POST /api/rebase/skip.
func (s *Server) handleRebaseSkip(w http.ResponseWriter, r *http.Request) {
	s.handleRebaseSimpleAction(w, r, func(repoPath string) error {
		return s.Services.Rebase.Skip(r.Context(), repoPath)
	})
}

// handleRebaseAbort handles POST /api/rebase/abort.
func (s *Server) handleRebaseAbort(w http.ResponseWriter, r *http.Request) {
	s.handleRebaseSimpleAction(w, r, func(repoPath string) error {
		return s.Services.Rebase.Abort(r.Context(), repoPath)
	})
}

// handleRebaseSimpleAction dispatches the shared continue/skip/abort flow.
func (s *Server) handleRebaseSimpleAction(w http.ResponseWriter, r *http.Request, op func(repoPath string) error) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.RepoPathRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if err := op(s.resolveRepoPath(req.RepoPath)); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleRebaseOntoMain handles POST /api/rebase/onto_main — stream.
func (s *Server) handleRebaseOntoMain(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.RepoPathRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	ctx, ctxCancel := streamContext()
	ch, cancel, err := s.Services.Rebase.OntoMain(ctx, s.resolveRepoPath(req.RepoPath))
	if err != nil {
		ctxCancel()
		s.writeServiceErr(w, err)
		return
	}
	id := s.registerStream(combineCancel(ctxCancel, cancel))
	go pumpStream(s, id, ch, syncProgressFrame)
	s.writeJSON(w, http.StatusAccepted, api.APIResponse{Success: true, Data: api.StreamStartResponse{StreamID: id}})
}

// handleRebaseContext handles POST /api/rebase/context.
func (s *Server) handleRebaseContext(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.RebaseContextRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	ctx, err := s.Services.Rebase.Context(r.Context(), s.resolveRepoPath(req.RepoPath), req.MainBranch, req.ConflictFiles)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.RebaseContextResponse{Context: ctx}})
}

// syncProgressFrame is the frameOf hook for SyncProgressEvent streams (used
// by sync.* and rebase.OntoMain).
func syncProgressFrame(ev service.SyncProgressEvent) (api.StreamFrame, bool) {
	return api.StreamFrame{Frame: ev, Done: ev.Done, Error: ev.Err}, ev.Done
}
