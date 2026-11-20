package remote

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// remoteQuestionService implements service.QuestionService.
type remoteQuestionService struct {
	c *client.Client
}

func (s *remoteQuestionService) Ask(ctx context.Context, agentID string, questions []string) (service.QuestionBatch, error) {
	return s.c.QuestionAsk(ctx, agentID, questions)
}

func (s *remoteQuestionService) Batch(ctx context.Context, batchID string) (service.QuestionBatch, error) {
	return s.c.QuestionBatch(ctx, batchID)
}

func (s *remoteQuestionService) Cancel(ctx context.Context, batchID string) error {
	return s.c.QuestionCancel(ctx, batchID)
}

func (s *remoteQuestionService) List(ctx context.Context) ([]service.Question, error) {
	return s.c.QuestionList(ctx)
}

func (s *remoteQuestionService) Answer(ctx context.Context, questionID, answer string) (service.Question, error) {
	return s.c.QuestionAnswer(ctx, questionID, answer)
}
