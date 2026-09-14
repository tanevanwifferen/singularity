package fake

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// QueueStub is a settable service.QueueService fake, in the FlowStub idiom:
// every method but Retry falls back to queueStub's service.ErrUnavailable,
// and RetryFn lets a test drive that one call independently — the retry-a-
// done-step feature needs to assert both the success and the refused path.
type QueueStub struct {
	queueStub
	RetryFn func(ctx context.Context, taskID string) error
}

var _ service.QueueService = (*QueueStub)(nil)

// NewQueueStub returns a stub with no hook set: Retry reports
// service.ErrUnavailable until RetryFn is assigned.
func NewQueueStub() *QueueStub { return &QueueStub{} }

// Retry calls RetryFn, or falls back to queueStub's default.
func (q *QueueStub) Retry(ctx context.Context, taskID string) error {
	if q.RetryFn == nil {
		return q.queueStub.Retry(ctx, taskID)
	}
	return q.RetryFn(ctx, taskID)
}
