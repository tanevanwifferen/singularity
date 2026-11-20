package server

import (
	"net/http"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// handleQuestionAsk handles POST /api/question/ask.
func (s *Server) handleQuestionAsk(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.QuestionAskRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.AgentID == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "agent_id required")
		return
	}
	batch, err := s.Services.Question.Ask(r.Context(), req.AgentID, req.Questions)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: batch})
}

// handleQuestionBatch handles GET /api/question/batch?batch_id=.
func (s *Server) handleQuestionBatch(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	id := r.URL.Query().Get("batch_id")
	if id == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "batch_id required")
		return
	}
	batch, err := s.Services.Question.Batch(r.Context(), id)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: batch})
}

// handleQuestionCancel handles POST /api/question/cancel.
func (s *Server) handleQuestionCancel(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.QuestionBatchRequest
	if err := s.parseJSON(r, &req); err != nil || req.BatchID == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "batch_id required")
		return
	}
	if err := s.Services.Question.Cancel(r.Context(), req.BatchID); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// handleQuestionList handles GET /api/question/list.
func (s *Server) handleQuestionList(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	questions, err := s.Services.Question.List(r.Context())
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	if questions == nil {
		questions = []api.Question{}
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.QuestionListResponse{Questions: questions}})
}

// handleQuestionAnswer handles POST /api/question/answer.
func (s *Server) handleQuestionAnswer(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.QuestionAnswerRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.QuestionID == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "question_id required")
		return
	}
	q, err := s.Services.Question.Answer(r.Context(), req.QuestionID, req.Answer)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: q})
}
