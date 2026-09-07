package remote

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// remoteQueueService implements service.QueueService.
//
// Nothing here projects between domain and wire types: internal/queue's
// types already carry the wire tags and internal/api aliases them, so every
// method is a straight call-through. The scheduler-side methods
// (Start/Stop/Wake/Restore) are absent from the interface by design — a
// remote client steers the queue, it does not run it.
type remoteQueueService struct {
	c *client.Client
}

// Add submits a batch of task specs as one atomic unit.
func (s *remoteQueueService) Add(ctx context.Context, specs []service.TaskSpec) ([]service.Task, error) {
	return s.c.QueueAdd(ctx, specs)
}

// List returns tasks filtered by queue and state.
func (s *remoteQueueService) List(ctx context.Context, queueID string, states []service.TaskState) ([]service.Task, error) {
	return s.c.QueueList(ctx, queueID, states)
}

// Get returns one task by ID.
func (s *remoteQueueService) Get(ctx context.Context, taskID string) (*service.Task, error) {
	return s.c.QueueGet(ctx, taskID)
}

// Queues returns a summary of every queue.
func (s *remoteQueueService) Queues(ctx context.Context) ([]service.QueueInfo, error) {
	return s.c.QueueQueues(ctx)
}

// QueueInfo returns the summary for one queue.
func (s *remoteQueueService) QueueInfo(ctx context.Context, queueID string) (*service.QueueInfo, error) {
	return s.c.QueueInfo(ctx, queueID)
}

// Graph returns one queue's dependency DAG.
func (s *remoteQueueService) Graph(ctx context.Context, queueID string) (*service.QueueGraph, error) {
	return s.c.QueueGraph(ctx, queueID)
}

// Cancel stops one task.
func (s *remoteQueueService) Cancel(ctx context.Context, taskID string) error {
	return s.c.QueueCancel(ctx, taskID)
}

// CancelQueue cancels every unfinished task in a queue.
func (s *remoteQueueService) CancelQueue(ctx context.Context, queueID string) error {
	return s.c.QueueCancelQueue(ctx, queueID)
}

// Retry puts a failed, cancelled or skipped task back in line.
func (s *remoteQueueService) Retry(ctx context.Context, taskID string) error {
	return s.c.QueueRetry(ctx, taskID)
}

// Answer delivers the operator's reply to a task waiting for input.
func (s *remoteQueueService) Answer(ctx context.Context, taskID, message string) error {
	return s.c.QueueAnswer(ctx, taskID, message)
}

// Pause stops a queue from dispatching anything new.
func (s *remoteQueueService) Pause(ctx context.Context, queueID string) error {
	return s.c.QueuePause(ctx, queueID)
}

// Resume lifts a pause.
func (s *remoteQueueService) Resume(ctx context.Context, queueID string) error {
	return s.c.QueueResume(ctx, queueID)
}

// RemoveQueue forgets a drained queue and deletes its state file.
func (s *remoteQueueService) RemoveQueue(ctx context.Context, queueID string) error {
	return s.c.QueueRemove(ctx, queueID)
}
