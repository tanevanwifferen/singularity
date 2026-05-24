package server

import (
	"net/http"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// handleJiraSearch handles POST /api/jira/search.
func (s *Server) handleJiraSearch(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.JiraSearchRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	res, err := s.Services.Jira.SearchIssues(r.Context(), req.JQL, req.MaxResults)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: res})
}

// handleJiraIssue handles GET /api/jira/issue?key=.
func (s *Server) handleJiraIssue(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "key required")
		return
	}
	issue, err := s.Services.Jira.GetIssue(r.Context(), key)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: issue})
}

// handleJiraMy handles GET /api/jira/my?project=.
func (s *Server) handleJiraMy(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	res, err := s.Services.Jira.GetMyIssues(r.Context(), r.URL.Query().Get("project"))
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: res})
}

// handleJiraUpdate handles POST /api/jira/update.
func (s *Server) handleJiraUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.JiraUpdateRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if err := s.Services.Jira.UpdateFields(r.Context(), req.Key, req.Fields); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleJiraComment handles POST /api/jira/comment.
func (s *Server) handleJiraComment(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.JiraCommentRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if err := s.Services.Jira.AddComment(r.Context(), req.Key, req.Body); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleJiraCreate handles POST /api/jira/create.
func (s *Server) handleJiraCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.JiraCreateRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	issue, err := s.Services.Jira.CreateIssue(r.Context(), req.Project, req.IssueType, req.Summary, req.Description, req.Priority)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: issue})
}

// handleJiraLink handles POST /api/jira/link.
func (s *Server) handleJiraLink(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.JiraLinkRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	if err := s.Services.Jira.LinkIssues(r.Context(), req.InwardKey, req.OutwardKey, req.LinkType); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleJiraActions handles GET /api/jira/actions?path=.
func (s *Server) handleJiraActions(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	actions, err := s.Services.Jira.ParseActions(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.JiraActionsResponse{Actions: actions}})
}

// handleJiraRefineTicket handles POST /api/jira/ai/refine.
func (s *Server) handleJiraRefineTicket(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.JiraRefineTicketRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	id, err := s.Services.Jira.RefineTicket(r.Context(), req.Issue, req.RepoPath, req.Focus, req.ActionsFile)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.broadcastAgentStarted(id, "jira refine: "+req.Focus, req.RepoPath)
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.AgentStartResponse{AgentID: id}})
}

// handleJiraCreateStories handles POST /api/jira/ai/stories.
func (s *Server) handleJiraCreateStories(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.JiraCreateStoriesRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	id, err := s.Services.Jira.CreateStories(r.Context(), req.Issue, req.RawText, req.Project, req.RepoPath, req.ActionsFile)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.broadcastAgentStarted(id, "jira create stories", req.RepoPath)
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.AgentStartResponse{AgentID: id}})
}

// handleJiraRefineProposal handles POST /api/jira/ai/refine_proposal.
func (s *Server) handleJiraRefineProposal(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.JiraRefineProposalRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	id, err := s.Services.Jira.RefineProposalWithContext(r.Context(), req.Issue, req.ExistingActions, req.UserFeedback, req.RepoPath, req.ActionsFile)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.broadcastAgentStarted(id, "jira refine proposal", req.RepoPath)
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.AgentStartResponse{AgentID: id}})
}

// handleJiraReviewTickets handles POST /api/jira/ai/review.
func (s *Server) handleJiraReviewTickets(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.JiraReviewTicketsRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request")
		return
	}
	id, err := s.Services.Jira.ReviewTickets(r.Context(), req.Issues, req.RepoPath, req.Instruction, req.ActionsFile)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.broadcastAgentStarted(id, "jira review", req.RepoPath)
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.AgentStartResponse{AgentID: id}})
}
