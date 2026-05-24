package server

import (
	"context"
	"net/http"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// handleSyncUpstream handles GET /api/sync/upstream.
func (s *Server) handleSyncUpstream(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	st, err := s.Services.Sync.UpstreamStatus(r.Context(), s.resolveRepoPath(r.URL.Query().Get("repo_path")))
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: st})
}

// handleSyncLastFetch handles GET /api/sync/last_fetch.
func (s *Server) handleSyncLastFetch(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	t, err := s.Services.Sync.LastFetchTime(r.Context(), s.resolveRepoPath(r.URL.Query().Get("repo_path")))
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.LastFetchResponse{Time: t}})
}

// syncStream is the shared helper for the long-running sync stream endpoints.
// op is the service-layer call that returns the SyncProgressEvent channel and
// its cancel closure. The stream-id is returned in 202 + StreamStartResponse.
func (s *Server) syncStream(w http.ResponseWriter, r *http.Request, op func(ctx context.Context) (<-chan service.SyncProgressEvent, func(), error)) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	ctx, ctxCancel := streamContext()
	ch, cancel, err := op(ctx)
	if err != nil {
		ctxCancel()
		s.writeServiceErr(w, err)
		return
	}
	id := s.registerStream(combineCancel(ctxCancel, cancel))
	go pumpStream(s, id, ch, syncProgressFrame)
	s.writeJSON(w, http.StatusAccepted, api.APIResponse{Success: true, Data: api.StreamStartResponse{StreamID: id}})
}

// handleSyncFetch handles POST /api/sync/fetch.
func (s *Server) handleSyncFetch(w http.ResponseWriter, r *http.Request) {
	var req api.SyncFetchRequest
	if r.Method == http.MethodPost {
		if err := s.parseJSON(r, &req); err != nil {
			s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
			return
		}
	}
	s.syncStream(w, r, func(ctx context.Context) (<-chan service.SyncProgressEvent, func(), error) {
		return s.Services.Sync.Fetch(ctx, s.resolveRepoPath(req.RepoPath), req.Remote)
	})
}

// handleSyncPull handles POST /api/sync/pull.
func (s *Server) handleSyncPull(w http.ResponseWriter, r *http.Request) {
	var req api.RepoPathRequest
	if r.Method == http.MethodPost {
		if err := s.parseJSON(r, &req); err != nil {
			s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
			return
		}
	}
	s.syncStream(w, r, func(ctx context.Context) (<-chan service.SyncProgressEvent, func(), error) {
		return s.Services.Sync.Pull(ctx, s.resolveRepoPath(req.RepoPath))
	})
}

// handleSyncPush handles POST /api/sync/push.
func (s *Server) handleSyncPush(w http.ResponseWriter, r *http.Request) {
	var req api.SyncPushRequest
	if r.Method == http.MethodPost {
		if err := s.parseJSON(r, &req); err != nil {
			s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
			return
		}
	}
	s.syncStream(w, r, func(ctx context.Context) (<-chan service.SyncProgressEvent, func(), error) {
		return s.Services.Sync.Push(ctx, s.resolveRepoPath(req.RepoPath), req.Force)
	})
}

// handleSyncPullRebase handles POST /api/sync/pull_rebase.
func (s *Server) handleSyncPullRebase(w http.ResponseWriter, r *http.Request) {
	var req api.RepoPathRequest
	if r.Method == http.MethodPost {
		if err := s.parseJSON(r, &req); err != nil {
			s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
			return
		}
	}
	s.syncStream(w, r, func(ctx context.Context) (<-chan service.SyncProgressEvent, func(), error) {
		return s.Services.Sync.PullRebase(ctx, s.resolveRepoPath(req.RepoPath))
	})
}

// handleSyncSetUpstream handles POST /api/sync/set_upstream.
func (s *Server) handleSyncSetUpstream(w http.ResponseWriter, r *http.Request) {
	var req api.SyncSetUpstreamRequest
	if r.Method == http.MethodPost {
		if err := s.parseJSON(r, &req); err != nil {
			s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
			return
		}
	}
	s.syncStream(w, r, func(ctx context.Context) (<-chan service.SyncProgressEvent, func(), error) {
		return s.Services.Sync.SetUpstreamAndPush(ctx, s.resolveRepoPath(req.RepoPath), req.Remote)
	})
}

// handleSyncAll handles POST /api/sync/all.
func (s *Server) handleSyncAll(w http.ResponseWriter, r *http.Request) {
	var req api.SyncAllRequest
	if r.Method == http.MethodPost {
		if err := s.parseJSON(r, &req); err != nil {
			s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
			return
		}
	}
	s.syncStream(w, r, func(ctx context.Context) (<-chan service.SyncProgressEvent, func(), error) {
		return s.Services.Sync.SyncAllRepos(ctx, req.Handle, req.Force)
	})
}
