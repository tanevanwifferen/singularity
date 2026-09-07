package server

import (
	"net/http"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// handleQueueAdd handles POST /api/queue/add.
func (s *Server) handleQueueAdd(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.QueueAddRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if len(req.Tasks) == 0 {
		s.writeCoded(w, api.ErrCodeBadRequest, "tasks required")
		return
	}
	// Per-spec validation (prompt, work_dir, dependency resolution) belongs
	// to the manager: it validates the batch as a unit and rejects all or
	// nothing. Path cleaning is ours, though — the queue never sees a raw
	// request and would otherwise hand an unvalidated path to the engine.
	for i := range req.Tasks {
		if req.Tasks[i].WorkDir == "" {
			continue
		}
		cleaned, err := s.validateRepoPath(req.Tasks[i].WorkDir)
		if err != nil {
			s.writeCoded(w, api.ErrCodeBadRequest, "invalid work_dir")
			return
		}
		req.Tasks[i].WorkDir = cleaned
	}
	tasks, err := s.Services.Queue.Add(r.Context(), req.Tasks)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.QueueAddResponse{Tasks: tasks}})
}

// handleQueueList handles GET /api/queue/list?queue=&state=.
func (s *Server) handleQueueList(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	q := r.URL.Query()
	tasks, err := s.Services.Queue.List(r.Context(), q.Get("queue"), parseTaskStates(q["state"]))
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.QueueListResponse{Tasks: tasks}})
}

// handleQueueGet handles GET /api/queue/get?task_id=.
func (s *Server) handleQueueGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	id := r.URL.Query().Get("task_id")
	if id == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "task_id required")
		return
	}
	task, err := s.Services.Queue.Get(r.Context(), id)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: task})
}

// handleQueueQueues handles GET /api/queue/queues.
func (s *Server) handleQueueQueues(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	infos, err := s.Services.Queue.Queues(r.Context())
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.QueueQueuesResponse{Queues: infos}})
}

// handleQueueGraph handles GET /api/queue/graph?queue=.
func (s *Server) handleQueueGraph(w http.ResponseWriter, r *http.Request) {
	if !s.requireServices(w) {
		return
	}
	queueID := r.URL.Query().Get("queue")
	if queueID == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "queue required")
		return
	}
	graph, err := s.Services.Queue.Graph(r.Context(), queueID)
	if err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: api.QueueGraphResponse{Graph: *graph}})
}

// handleQueueCancel handles POST /api/queue/cancel.
func (s *Server) handleQueueCancel(w http.ResponseWriter, r *http.Request) {
	s.withTaskID(w, r, func(taskID string) error {
		return s.Services.Queue.Cancel(r.Context(), taskID)
	})
}

// handleQueueRetry handles POST /api/queue/retry.
func (s *Server) handleQueueRetry(w http.ResponseWriter, r *http.Request) {
	s.withTaskID(w, r, func(taskID string) error {
		return s.Services.Queue.Retry(r.Context(), taskID)
	})
}

// handleQueueCancelQueue handles POST /api/queue/cancel_queue.
func (s *Server) handleQueueCancelQueue(w http.ResponseWriter, r *http.Request) {
	s.withQueueID(w, r, func(queueID string) error {
		return s.Services.Queue.CancelQueue(r.Context(), queueID)
	})
}

// handleQueuePause handles POST /api/queue/pause.
func (s *Server) handleQueuePause(w http.ResponseWriter, r *http.Request) {
	s.withQueueID(w, r, func(queueID string) error {
		return s.Services.Queue.Pause(r.Context(), queueID)
	})
}

// handleQueueResume handles POST /api/queue/resume.
func (s *Server) handleQueueResume(w http.ResponseWriter, r *http.Request) {
	s.withQueueID(w, r, func(queueID string) error {
		return s.Services.Queue.Resume(r.Context(), queueID)
	})
}

// handleQueueAnswer handles POST /api/queue/answer.
func (s *Server) handleQueueAnswer(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.QueueAnswerRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.TaskID == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "task_id required")
		return
	}
	if req.Message == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "message required")
		return
	}
	if err := s.Services.Queue.Answer(r.Context(), req.TaskID, req.Message); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// withTaskID is the shared body of the single-task mutations: decode a
// QueueTaskRequest, require task_id, run op, reply with a bare success.
// Extracted because the three of them differ only in the method they call.
func (s *Server) withTaskID(w http.ResponseWriter, r *http.Request, op func(taskID string) error) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.QueueTaskRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.TaskID == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "task_id required")
		return
	}
	if err := op(req.TaskID); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// withQueueID is withTaskID for the whole-queue mutations.
func (s *Server) withQueueID(w http.ResponseWriter, r *http.Request, op func(queueID string) error) {
	if !s.requireMethod(w, r, http.MethodPost) || !s.requireServices(w) {
		return
	}
	var req api.QueueIDRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeCoded(w, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.QueueID == "" {
		s.writeCoded(w, api.ErrCodeBadRequest, "queue_id required")
		return
	}
	if err := op(req.QueueID); err != nil {
		s.writeServiceErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true})
}

// parseTaskStates reads the `state` list filter. Both spellings are accepted
// — repeated (?state=ready&state=running) and comma-separated
// (?state=ready,running) — because curl users reach for the second and
// url.Values.Add produces the first; rejecting either would be a gratuitous
// trap. Unknown values are passed through so the queue rejects them with a
// message naming the offending state rather than silently ignoring it.
func parseTaskStates(values []string) []service.TaskState {
	var out []service.TaskState
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			out = append(out, service.TaskState(part))
		}
	}
	return out
}

// broadcastQueueTaskChanged emits a queue_task_changed WS frame. The daemon
// registers this as the queue manager's OnChange callback, which is why it
// takes the task by value: the manager hands out copies precisely so a
// consumer like this one cannot mutate scheduler state.
func (s *Server) broadcastQueueTaskChanged(task api.Task) {
	s.wsBroadcast(api.WSMessage{
		Type:    api.WSEventQueueTaskChanged,
		Payload: api.QueueTaskChangedPayload{Task: task},
	})
}

// QueueChangeHook returns the callback the daemon hands to
// queue.Manager.OnChange. Exported (unlike the broadcast itself) because the
// daemon wires it from outside the package; keeping the broadcast unexported
// means there is exactly one way in.
func (s *Server) QueueChangeHook() func(api.Task) {
	return s.broadcastQueueTaskChanged
}
