package api

import "gitlab.com/tanevanwifferen1/singularity/internal/service"

// Queue DTOs. Unlike the agent surface, the queue's domain types already
// carry snake_case JSON tags (internal/queue was written wire-first), so
// there is nothing to re-project: aliasing keeps one definition of the shape
// and makes a wire-tag change impossible to forget on one side.
type (
	Task        = service.Task
	TaskSpec    = service.TaskSpec
	TaskState   = service.TaskState
	TaskOptions = service.TaskOptions
	QueueInfo   = service.QueueInfo
	QueueGraph  = service.QueueGraph
)

// QueueAddRequest is the body for POST /api/queue/add. A batch is submitted
// as one unit so a whole DAG can be declared in a single call: specs
// reference each other by their batch-local Name in After.
type QueueAddRequest struct {
	Tasks []TaskSpec `json:"tasks"`
}

// QueueAddResponse returns the accepted tasks with their assigned IDs, in
// the order they were submitted.
type QueueAddResponse struct {
	Tasks []Task `json:"tasks"`
}

// QueueListResponse is the body for GET /api/queue/list.
type QueueListResponse struct {
	Tasks []Task `json:"tasks"`
}

// QueueQueuesResponse is the body for GET /api/queue/queues.
type QueueQueuesResponse struct {
	Queues []QueueInfo `json:"queues"`
}

// QueueGraphResponse is the body for GET /api/queue/graph.
type QueueGraphResponse struct {
	Graph QueueGraph `json:"graph"`
}

// QueueTaskRequest is the body for the per-task mutations: POST
// /api/queue/cancel and /api/queue/retry.
type QueueTaskRequest struct {
	TaskID string `json:"task_id"`
}

// QueueIDRequest is the body for the per-queue mutations: POST
// /api/queue/cancel_queue, /api/queue/pause and /api/queue/resume.
type QueueIDRequest struct {
	QueueID string `json:"queue_id"`
}

// QueueAnswerRequest is the body for POST /api/queue/answer — the operator's
// reply to a task whose agent stopped to ask a question.
type QueueAnswerRequest struct {
	TaskID  string `json:"task_id"`
	Message string `json:"message"`
}
