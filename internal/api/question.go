package api

import "gitlab.com/tanevanwifferen1/singularity/internal/service"

// Question DTOs. Question and QuestionBatch carry their own snake_case JSON
// tags, so they travel as they are.
type (
	Question      = service.Question
	QuestionBatch = service.QuestionBatch
)

// QuestionAskRequest is the body for POST /api/question/ask.
type QuestionAskRequest struct {
	AgentID   string   `json:"agent_id"`
	Questions []string `json:"questions"`
}

// QuestionBatchRequest is the body for POST /api/question/cancel.
type QuestionBatchRequest struct {
	BatchID string `json:"batch_id"`
}

// QuestionAnswerRequest is the body for POST /api/question/answer.
type QuestionAnswerRequest struct {
	QuestionID string `json:"question_id"`
	Answer     string `json:"answer"`
}

// QuestionListResponse is the body for GET /api/question/list.
type QuestionListResponse struct {
	Questions []Question `json:"questions"`
}
