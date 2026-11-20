package client

import (
	"context"
	"net/url"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// QuestionAsk calls Question.Ask.
func (c *Client) QuestionAsk(ctx context.Context, agentID string, questions []string) (api.QuestionBatch, error) {
	var batch api.QuestionBatch
	err := c.post(ctx, "/api/question/ask", api.QuestionAskRequest{AgentID: agentID, Questions: questions}, &batch)
	return batch, err
}

// QuestionBatch calls Question.Batch.
func (c *Client) QuestionBatch(ctx context.Context, batchID string) (api.QuestionBatch, error) {
	var batch api.QuestionBatch
	err := c.get(ctx, "/api/question/batch?batch_id="+url.QueryEscape(batchID), &batch)
	return batch, err
}

// QuestionCancel calls Question.Cancel.
func (c *Client) QuestionCancel(ctx context.Context, batchID string) error {
	return c.post(ctx, "/api/question/cancel", api.QuestionBatchRequest{BatchID: batchID}, nil)
}

// QuestionList calls Question.List.
func (c *Client) QuestionList(ctx context.Context) ([]api.Question, error) {
	var resp api.QuestionListResponse
	if err := c.get(ctx, "/api/question/list", &resp); err != nil {
		return nil, err
	}
	return resp.Questions, nil
}

// QuestionAnswer calls Question.Answer.
func (c *Client) QuestionAnswer(ctx context.Context, questionID, answer string) (api.Question, error) {
	var q api.Question
	err := c.post(ctx, "/api/question/answer", api.QuestionAnswerRequest{QuestionID: questionID, Answer: answer}, &q)
	return q, err
}
