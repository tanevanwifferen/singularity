package service

import "context"

// QuestionService is the operator-question channel: an agent asks through
// its ask_questions MCP tool and reports state blocked until every question
// of that call is answered. Ask, Batch and Cancel are the MCP server's half
// (internal/questionmcp); List and Answer are the operator's.
type QuestionService interface {
	// Ask records one tool call's questions and blocks the agent. It
	// returns at once; the answers are collected with Batch. Returns
	// ErrNotFound for an unknown agent and ErrInvalidRequest when there is
	// no question to ask.
	Ask(ctx context.Context, agentID string, questions []string) (QuestionBatch, error)

	// Batch returns the batch's current state. Done is set once every
	// question has an answer; a done batch is returned once and then
	// forgotten. A batch that is no longer polled is dropped, so callers
	// waiting on one must keep calling this. ErrNotFound when it is gone.
	Batch(ctx context.Context, batchID string) (QuestionBatch, error)

	// Cancel withdraws a batch whose tool call ended early. Idempotent.
	Cancel(ctx context.Context, batchID string) error

	// List returns every question still in play, oldest first: the open
	// ones and the answered ones whose batch is waiting on a sibling.
	List(ctx context.Context) ([]Question, error)

	// Answer records the operator's answer to one question. ErrNotFound
	// for an unknown id, ErrConflict when it already has an answer,
	// ErrInvalidRequest for an empty answer.
	Answer(ctx context.Context, questionID, answer string) (Question, error)
}
