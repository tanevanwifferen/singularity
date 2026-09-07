package local

import (
	"context"
	"errors"

	"gitlab.com/tanevanwifferen1/singularity/internal/queue"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// localQueueService implements service.QueueService over the daemon's
// *queue.Manager.
//
// The manager may be nil: a daemon that failed to open its queue state
// directory still serves every other capability, and every method here
// answers ErrUnavailable rather than panicking. That mirrors how the jira
// and project services behave when their dependency is missing.
type localQueueService struct {
	mgr *queue.Manager
}

// Add submits a batch of task specs as one atomic unit.
func (s *localQueueService) Add(ctx context.Context, specs []service.TaskSpec) ([]service.Task, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	if s.mgr == nil {
		return nil, service.ErrUnavailable
	}
	tasks, err := s.mgr.Add(specs)
	if err != nil {
		return nil, mapQueueErr(err)
	}
	return tasks, nil
}

// List returns tasks filtered by queue and state.
func (s *localQueueService) List(ctx context.Context, queueID string, states []service.TaskState) ([]service.Task, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	if s.mgr == nil {
		return nil, service.ErrUnavailable
	}
	tasks, err := s.mgr.List(queueID, states)
	if err != nil {
		return nil, mapQueueErr(err)
	}
	return tasks, nil
}

// Get returns one task by ID.
func (s *localQueueService) Get(ctx context.Context, taskID string) (*service.Task, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	if s.mgr == nil {
		return nil, service.ErrUnavailable
	}
	t, err := s.mgr.Get(taskID)
	if err != nil {
		return nil, mapQueueErr(err)
	}
	return &t, nil
}

// Queues returns a summary of every queue.
func (s *localQueueService) Queues(ctx context.Context) ([]service.QueueInfo, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	if s.mgr == nil {
		return nil, service.ErrUnavailable
	}
	return s.mgr.Queues(), nil
}

// QueueInfo returns the summary for one queue.
func (s *localQueueService) QueueInfo(ctx context.Context, queueID string) (*service.QueueInfo, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	if s.mgr == nil {
		return nil, service.ErrUnavailable
	}
	info, err := s.mgr.QueueInfo(queueID)
	if err != nil {
		return nil, mapQueueErr(err)
	}
	return &info, nil
}

// Graph returns one queue's dependency DAG.
func (s *localQueueService) Graph(ctx context.Context, queueID string) (*service.QueueGraph, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	if s.mgr == nil {
		return nil, service.ErrUnavailable
	}
	g, err := s.mgr.Graph(queueID)
	if err != nil {
		return nil, mapQueueErr(err)
	}
	return &g, nil
}

// Cancel stops one task.
func (s *localQueueService) Cancel(ctx context.Context, taskID string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if s.mgr == nil {
		return service.ErrUnavailable
	}
	return mapQueueErr(s.mgr.Cancel(taskID))
}

// CancelQueue cancels every unfinished task in a queue.
func (s *localQueueService) CancelQueue(ctx context.Context, queueID string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if s.mgr == nil {
		return service.ErrUnavailable
	}
	return mapQueueErr(s.mgr.CancelQueue(queueID))
}

// Retry puts a failed, cancelled or skipped task back in line.
func (s *localQueueService) Retry(ctx context.Context, taskID string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if s.mgr == nil {
		return service.ErrUnavailable
	}
	return mapQueueErr(s.mgr.Retry(taskID))
}

// Answer delivers the operator's reply to a task waiting for input. Inert
// in this build — see service.QueueService.Answer.
func (s *localQueueService) Answer(ctx context.Context, taskID, message string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if s.mgr == nil {
		return service.ErrUnavailable
	}
	return mapQueueErr(s.mgr.Answer(taskID, message))
}

// Pause stops a queue from dispatching anything new.
func (s *localQueueService) Pause(ctx context.Context, queueID string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if s.mgr == nil {
		return service.ErrUnavailable
	}
	return mapQueueErr(s.mgr.Pause(queueID))
}

// Resume lifts a pause.
func (s *localQueueService) Resume(ctx context.Context, queueID string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if s.mgr == nil {
		return service.ErrUnavailable
	}
	return mapQueueErr(s.mgr.Resume(queueID))
}

// RemoveQueue forgets a drained queue and deletes its state file.
func (s *localQueueService) RemoveQueue(ctx context.Context, queueID string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if s.mgr == nil {
		return service.ErrUnavailable
	}
	return mapQueueErr(s.mgr.RemoveQueue(queueID))
}

// mapQueueErr translates the queue package's sentinels into the service
// ones. It is deliberately separate from mapErr rather than folded into it:
// mapErr's job is to recover meaning from the stringly-typed errors that
// internal/git and internal/engine produce, and it matches on substrings to
// do so. The queue returns real sentinels, so matching them here keeps the
// translation exact — routing them through mapErr would let a message like
// "task t3 depends on unknown task t9" fall into its "not found" substring
// case and surface as a 404 instead of a 400.
//
// Anything unrecognised still goes through wrapErr so a queue error that
// originated in the engine (a failed spawn, say) keeps its usual mapping.
func mapQueueErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, queue.ErrNotFound):
		return errFromSentinel{sentinel: service.ErrNotFound, original: err}
	case errors.Is(err, queue.ErrInvalid):
		return errFromSentinel{sentinel: service.ErrInvalidRequest, original: err}
	case errors.Is(err, queue.ErrNotRetryable), errors.Is(err, queue.ErrNotWaiting):
		return errFromSentinel{sentinel: service.ErrConflict, original: err}
	}
	return wrapErr(err)
}
