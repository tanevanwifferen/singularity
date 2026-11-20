package local

import (
	"context"
	"errors"
	"fmt"

	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// localQuestionService implements service.QuestionService over the shared
// engine, which owns the questions alongside the agents that asked them.
type localQuestionService struct {
	eng *engine.Engine
}

// mapQuestionErr turns the engine's question sentinels into service ones.
func mapQuestionErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, engine.ErrQuestionAnswered):
		return fmt.Errorf("%w: %s", service.ErrConflict, err.Error())
	case errors.Is(err, engine.ErrQuestionInvalid):
		return fmt.Errorf("%w: %s", service.ErrInvalidRequest, err.Error())
	case errors.Is(err, engine.ErrQuestionNotFound):
		return fmt.Errorf("%w: %s", service.ErrNotFound, err.Error())
	}
	return wrapErr(err)
}

func (s *localQuestionService) Ask(ctx context.Context, agentID string, questions []string) (service.QuestionBatch, error) {
	if err := checkCtx(ctx); err != nil {
		return service.QuestionBatch{}, err
	}
	if s.eng == nil {
		return service.QuestionBatch{}, service.ErrUnavailable
	}
	batch, err := s.eng.AskQuestions(agentID, questions)
	return batch, mapQuestionErr(err)
}

func (s *localQuestionService) Batch(ctx context.Context, batchID string) (service.QuestionBatch, error) {
	if err := checkCtx(ctx); err != nil {
		return service.QuestionBatch{}, err
	}
	if s.eng == nil {
		return service.QuestionBatch{}, service.ErrUnavailable
	}
	batch, err := s.eng.QuestionBatch(batchID)
	return batch, mapQuestionErr(err)
}

func (s *localQuestionService) Cancel(ctx context.Context, batchID string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if s.eng == nil {
		return service.ErrUnavailable
	}
	s.eng.CancelQuestionBatch(batchID)
	return nil
}

func (s *localQuestionService) List(ctx context.Context) ([]service.Question, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	if s.eng == nil {
		return nil, service.ErrUnavailable
	}
	return s.eng.ListQuestions(), nil
}

func (s *localQuestionService) Answer(ctx context.Context, questionID, answer string) (service.Question, error) {
	if err := checkCtx(ctx); err != nil {
		return service.Question{}, err
	}
	if s.eng == nil {
		return service.Question{}, service.ErrUnavailable
	}
	q, err := s.eng.AnswerQuestion(questionID, answer)
	return q, mapQuestionErr(err)
}
