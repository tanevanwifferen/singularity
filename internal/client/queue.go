package client

import (
	"context"
	"net/url"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// QueueAdd calls Queue.Add. The whole batch travels in one request so specs
// may reference each other by their batch-local Name in After.
func (c *Client) QueueAdd(ctx context.Context, specs []api.TaskSpec) ([]api.Task, error) {
	var resp api.QueueAddResponse
	if err := c.post(ctx, "/api/queue/add", api.QueueAddRequest{Tasks: specs}, &resp); err != nil {
		return nil, err
	}
	return resp.Tasks, nil
}

// QueueList calls Queue.List. An empty queueID means every queue; an empty
// states slice means every state. States are sent as repeated `state`
// params — the daemon also accepts them comma-separated, but url.Values
// spelling keeps values with stray spaces from being mangled.
func (c *Client) QueueList(ctx context.Context, queueID string, states []api.TaskState) ([]api.Task, error) {
	q := url.Values{}
	if queueID != "" {
		q.Set("queue", queueID)
	}
	for _, s := range states {
		q.Add("state", string(s))
	}
	path := "/api/queue/list"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var resp api.QueueListResponse
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	return resp.Tasks, nil
}

// QueueGet calls Queue.Get.
func (c *Client) QueueGet(ctx context.Context, taskID string) (*api.Task, error) {
	var task api.Task
	if err := c.get(ctx, "/api/queue/get?task_id="+url.QueryEscape(taskID), &task); err != nil {
		return nil, err
	}
	return &task, nil
}

// QueueQueues calls Queue.Queues.
func (c *Client) QueueQueues(ctx context.Context) ([]api.QueueInfo, error) {
	var resp api.QueueQueuesResponse
	if err := c.get(ctx, "/api/queue/queues", &resp); err != nil {
		return nil, err
	}
	return resp.Queues, nil
}

// QueueInfo returns one queue's summary. The daemon exposes no single-queue
// endpoint (the tallies are computed in memory and shipped for every queue
// at once), so this selects from the listing rather than adding a route the
// wire contract does not have.
func (c *Client) QueueInfo(ctx context.Context, queueID string) (*api.QueueInfo, error) {
	infos, err := c.QueueQueues(ctx)
	if err != nil {
		return nil, err
	}
	for i := range infos {
		if infos[i].ID == queueID {
			return &infos[i], nil
		}
	}
	return nil, sentinelErr{"queue " + queueID + " not found", service.ErrNotFound}
}

// QueueGraph calls Queue.Graph.
func (c *Client) QueueGraph(ctx context.Context, queueID string) (*api.QueueGraph, error) {
	var resp api.QueueGraphResponse
	if err := c.get(ctx, "/api/queue/graph?queue="+url.QueryEscape(queueID), &resp); err != nil {
		return nil, err
	}
	return &resp.Graph, nil
}

// QueueCancel calls Queue.Cancel.
func (c *Client) QueueCancel(ctx context.Context, taskID string) error {
	return c.post(ctx, "/api/queue/cancel", api.QueueTaskRequest{TaskID: taskID}, nil)
}

// QueueCancelQueue calls Queue.CancelQueue.
func (c *Client) QueueCancelQueue(ctx context.Context, queueID string) error {
	return c.post(ctx, "/api/queue/cancel_queue", api.QueueIDRequest{QueueID: queueID}, nil)
}

// QueueRetry calls Queue.Retry.
func (c *Client) QueueRetry(ctx context.Context, taskID string) error {
	return c.post(ctx, "/api/queue/retry", api.QueueTaskRequest{TaskID: taskID}, nil)
}

// QueueAnswer calls Queue.Answer.
func (c *Client) QueueAnswer(ctx context.Context, taskID, message string) error {
	return c.post(ctx, "/api/queue/answer", api.QueueAnswerRequest{TaskID: taskID, Message: message}, nil)
}

// QueuePause calls Queue.Pause.
func (c *Client) QueuePause(ctx context.Context, queueID string) error {
	return c.post(ctx, "/api/queue/pause", api.QueueIDRequest{QueueID: queueID}, nil)
}

// QueueResume calls Queue.Resume.
func (c *Client) QueueResume(ctx context.Context, queueID string) error {
	return c.post(ctx, "/api/queue/resume", api.QueueIDRequest{QueueID: queueID}, nil)
}

// QueueRemove calls Queue.RemoveQueue: forgets a drained queue and deletes
// its state file. Refused while any task is still active.
func (c *Client) QueueRemove(ctx context.Context, queueID string) error {
	return c.post(ctx, "/api/queue/remove", api.QueueIDRequest{QueueID: queueID}, nil)
}
