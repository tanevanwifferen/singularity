package server

import (
	"net/http"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
)

// handleAgentStart handles POST /api/agent/start
func (s *Server) handleAgentStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req api.AgentStartRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ProjectPath == "" {
		req.ProjectPath = s.repoPath
	}
	if req.ProjectPath == "" {
		s.writeError(w, http.StatusBadRequest, "project_path required")
		return
	}
	if req.Task == "" {
		s.writeError(w, http.StatusBadRequest, "task required")
		return
	}

	opts := engine.AgentOptions{
		Model:        req.Model,
		Effort:       req.Effort,
		AllowedTools: req.AllowedTools,
		MaxTurns:     req.MaxTurns,
		ContextFiles: req.ContextFiles,
		SmartRoute:   req.SmartRoute,
	}
	if req.TimeoutSecs > 0 {
		opts.Timeout = time.Duration(req.TimeoutSecs) * time.Second
	}

	sessionID, err := s.engine.StartAgent(req.ProjectPath, req.Task, opts)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Broadcast agent started event
	s.wsBroadcast(api.WSMessage{
		Type:    api.WSEventAgentStarted,
		Payload: map[string]string{"session_id": sessionID, "task": req.Task},
	})

	s.writeJSON(w, http.StatusOK, api.APIResponse{
		Success: true,
		Data:    map[string]string{"session_id": sessionID},
	})
}

// requireSessionID extracts the session_id query parameter, writing a 400 error
// and returning ("", false) if it is missing.
func (s *Server) requireSessionID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.URL.Query().Get("session_id")
	if id == "" {
		s.writeError(w, http.StatusBadRequest, "session_id required")
		return "", false
	}
	return id, true
}

// handleAgentStatus handles GET /api/agent/status?session_id=...
func (s *Server) handleAgentStatus(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := s.requireSessionID(w, r)
	if !ok {
		return
	}

	agent := s.engine.GetAgent(sessionID)
	if agent == nil {
		s.writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	s.writeJSON(w, http.StatusOK, api.APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"session_id": agent.ID,
			"state":      agent.State.String(),
			"work_dir":   agent.WorkDir,
			"task":       agent.Task,
			"created_at": agent.CreatedAt,
			"started_at": agent.StartedAt,
			"ended_at":   agent.EndedAt,
			"error":      agent.Error,
			"exit_code":  agent.ExitCode,
		},
	})
}

// handleAgentOutput handles GET /api/agent/output?session_id=...&offset=0
func (s *Server) handleAgentOutput(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := s.requireSessionID(w, r)
	if !ok {
		return
	}

	output, err := s.engine.GetOutput(sessionID)
	if err != nil {
		s.writeError(w, http.StatusNotFound, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, api.APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"session_id": sessionID,
			"output":     output,
		},
	})
}

// handleAgentKill handles POST /api/agent/kill
func (s *Server) handleAgentKill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req api.AgentQueryRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.SessionID == "" {
		s.writeError(w, http.StatusBadRequest, "session_id required")
		return
	}

	if err := s.engine.KillAgent(req.SessionID); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleAgentInput handles POST /api/agent/input
func (s *Server) handleAgentInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req api.AgentInputRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.SessionID == "" {
		s.writeError(w, http.StatusBadRequest, "session_id required")
		return
	}
	if req.Message == "" {
		s.writeError(w, http.StatusBadRequest, "message required")
		return
	}

	if err := s.engine.SendInput(req.SessionID, req.Message); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleAgentList handles GET /api/agent/list
func (s *Server) handleAgentList(w http.ResponseWriter, r *http.Request) {
	agents := s.engine.ListAgents()

	type agentSummary struct {
		ID        string     `json:"id"`
		State     string     `json:"state"`
		WorkDir   string     `json:"work_dir"`
		Task      string     `json:"task"`
		Summary   string     `json:"summary"`
		CreatedAt time.Time  `json:"created_at"`
		EndedAt   *time.Time `json:"ended_at,omitempty"`
	}

	summaries := make([]agentSummary, len(agents))
	for i, a := range agents {
		summaries[i] = agentSummary{
			ID:        a.ID,
			State:     a.State.String(),
			WorkDir:   a.WorkDir,
			Task:      a.Task,
			Summary:   a.Summary,
			CreatedAt: a.CreatedAt,
			EndedAt:   a.EndedAt,
		}
	}

	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: summaries})
}

// handleAgentStats handles GET /api/agent/stats
func (s *Server) handleAgentStats(w http.ResponseWriter, r *http.Request) {
	stats := s.engine.Stats()
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: stats})
}
