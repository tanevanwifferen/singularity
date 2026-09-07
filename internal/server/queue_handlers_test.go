package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// stubQueue records what the handlers dispatched and returns canned data.
// Only the methods the tests exercise are meaningful; the rest satisfy the
// interface.
type stubQueue struct {
	gotQueueID string
	gotStates  []service.TaskState
	gotTaskID  string
	tasks      []service.Task
	err        error
}

func (s *stubQueue) Add(_ context.Context, specs []service.TaskSpec) ([]service.Task, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make([]service.Task, len(specs))
	for i, sp := range specs {
		out[i] = service.Task{ID: "t1", Prompt: sp.Prompt, WorkDir: sp.WorkDir}
	}
	return out, nil
}

func (s *stubQueue) List(_ context.Context, queueID string, states []service.TaskState) ([]service.Task, error) {
	s.gotQueueID = queueID
	s.gotStates = states
	return s.tasks, s.err
}

func (s *stubQueue) Get(_ context.Context, taskID string) (*service.Task, error) {
	s.gotTaskID = taskID
	if s.err != nil {
		return nil, s.err
	}
	return &service.Task{ID: taskID}, nil
}

func (s *stubQueue) Queues(context.Context) ([]service.QueueInfo, error) { return nil, s.err }
func (s *stubQueue) QueueInfo(context.Context, string) (*service.QueueInfo, error) {
	return nil, s.err
}
func (s *stubQueue) Graph(_ context.Context, queueID string) (*service.QueueGraph, error) {
	s.gotQueueID = queueID
	if s.err != nil {
		return nil, s.err
	}
	return &service.QueueGraph{QueueID: queueID}, nil
}
func (s *stubQueue) Cancel(_ context.Context, taskID string) error {
	s.gotTaskID = taskID
	return s.err
}
func (s *stubQueue) CancelQueue(_ context.Context, queueID string) error {
	s.gotQueueID = queueID
	return s.err
}
func (s *stubQueue) Retry(_ context.Context, taskID string) error {
	s.gotTaskID = taskID
	return s.err
}
func (s *stubQueue) Answer(_ context.Context, taskID, _ string) error {
	s.gotTaskID = taskID
	return s.err
}
func (s *stubQueue) Pause(_ context.Context, queueID string) error {
	s.gotQueueID = queueID
	return s.err
}
func (s *stubQueue) Resume(_ context.Context, queueID string) error {
	s.gotQueueID = queueID
	return s.err
}

// newQueueTestServer builds a Server whose only wired capability is the
// queue stub. server.New is deliberately not used: it constructs an engine
// and wires WS callbacks the handler tests have no use for.
func newQueueTestServer(q service.QueueService) *Server {
	return &Server{Services: &service.Services{Queue: q}}
}

// postJSON issues a POST with the JSON content type the parseJSON helper
// insists on.
func postJSON(path, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

// decodeResp pulls the envelope out of a recorded response.
func decodeResp(t *testing.T, rec *httptest.ResponseRecorder) api.APIResponse {
	t.Helper()
	var resp api.APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, rec.Body.String())
	}
	return resp
}

// TestParseTaskStates covers both spellings of the repeatable list filter.
func TestParseTaskStates(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  []service.TaskState
	}{
		{"absent", "", nil},
		{"single", "?state=ready", []service.TaskState{service.TaskReady}},
		{"repeated", "?state=ready&state=running",
			[]service.TaskState{service.TaskReady, service.TaskRunning}},
		{"comma", "?state=ready,running",
			[]service.TaskState{service.TaskReady, service.TaskRunning}},
		{"mixed", "?state=ready,running&state=done",
			[]service.TaskState{service.TaskReady, service.TaskRunning, service.TaskDone}},
		{"spaces", "?state=ready,%20running",
			[]service.TaskState{service.TaskReady, service.TaskRunning}},
		{"empty entries", "?state=&state=,,", nil},
		// Unknown values reach the queue on purpose: it names the offending
		// state in its error rather than the filter silently doing nothing.
		{"unknown passthrough", "?state=bogus", []service.TaskState{service.TaskState("bogus")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/queue/list"+tc.query, nil)
			got := parseTaskStates(r.URL.Query()["state"])
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestHandleQueueListHappyPath is the happy path through a GET handler: the
// query params reach the service and the tasks come back in the envelope.
func TestHandleQueueListHappyPath(t *testing.T) {
	q := &stubQueue{tasks: []service.Task{{ID: "t1", QueueID: "q1", State: service.TaskReady}}}
	s := newQueueTestServer(q)

	rec := httptest.NewRecorder()
	s.handleQueueList(rec, httptest.NewRequest(http.MethodGet, "/api/queue/list?queue=q1&state=ready,running", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if q.gotQueueID != "q1" {
		t.Errorf("service got queue %q, want q1", q.gotQueueID)
	}
	if len(q.gotStates) != 2 {
		t.Errorf("service got states %v, want 2 entries", q.gotStates)
	}
	resp := decodeResp(t, rec)
	if !resp.Success {
		t.Fatalf("success = false, error %q", resp.Error)
	}
	data, _ := json.Marshal(resp.Data)
	if !strings.Contains(string(data), `"id":"t1"`) {
		t.Errorf("response missing the task: %s", data)
	}
}

// TestHandleQueueAddHappyPath covers the POST path including the work_dir
// cleaning the handler does before the queue sees the spec.
func TestHandleQueueAddHappyPath(t *testing.T) {
	q := &stubQueue{}
	s := newQueueTestServer(q)

	rec := httptest.NewRecorder()
	s.handleQueueAdd(rec, postJSON("/api/queue/add", `{"tasks":[{"prompt":"p","work_dir":"/w/api"}]}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	resp := decodeResp(t, rec)
	if !resp.Success {
		t.Fatalf("success = false, error %q", resp.Error)
	}
	data, _ := json.Marshal(resp.Data)
	if !strings.Contains(string(data), `"work_dir":"/w/api"`) {
		t.Errorf("response missing the cleaned work_dir: %s", data)
	}
}

// TestQueueHandlerValidation covers the requests the handlers must reject
// before dispatching: missing identifiers, traversal-laden paths, wrong
// method. Each must be a 400 BAD_REQUEST (or 405) and must never reach the
// service.
func TestQueueHandlerValidation(t *testing.T) {
	cases := []struct {
		name       string
		handler    func(*Server) http.HandlerFunc
		req        *http.Request
		wantStatus int
	}{
		{
			"add without tasks",
			func(s *Server) http.HandlerFunc { return s.handleQueueAdd },
			postJSON("/api/queue/add", `{"tasks":[]}`),
			http.StatusBadRequest,
		},
		{
			"add with traversal work_dir",
			func(s *Server) http.HandlerFunc { return s.handleQueueAdd },
			postJSON("/api/queue/add", `{"tasks":[{"prompt":"p","work_dir":"/w/../etc"}]}`),
			http.StatusBadRequest,
		},
		{
			"get without task_id",
			func(s *Server) http.HandlerFunc { return s.handleQueueGet },
			httptest.NewRequest(http.MethodGet, "/api/queue/get", nil),
			http.StatusBadRequest,
		},
		{
			"graph without queue",
			func(s *Server) http.HandlerFunc { return s.handleQueueGraph },
			httptest.NewRequest(http.MethodGet, "/api/queue/graph", nil),
			http.StatusBadRequest,
		},
		{
			"cancel without task_id",
			func(s *Server) http.HandlerFunc { return s.handleQueueCancel },
			postJSON("/api/queue/cancel", `{}`),
			http.StatusBadRequest,
		},
		{
			"pause without queue_id",
			func(s *Server) http.HandlerFunc { return s.handleQueuePause },
			postJSON("/api/queue/pause", `{}`),
			http.StatusBadRequest,
		},
		{
			"answer without message",
			func(s *Server) http.HandlerFunc { return s.handleQueueAnswer },
			postJSON("/api/queue/answer", `{"task_id":"t1"}`),
			http.StatusBadRequest,
		},
		{
			"retry via GET",
			func(s *Server) http.HandlerFunc { return s.handleQueueRetry },
			httptest.NewRequest(http.MethodGet, "/api/queue/retry", nil),
			http.StatusMethodNotAllowed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := &stubQueue{}
			s := newQueueTestServer(q)
			rec := httptest.NewRecorder()
			tc.handler(s)(rec, tc.req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if q.gotTaskID != "" || q.gotQueueID != "" {
				t.Errorf("service was dispatched to despite invalid request (task %q queue %q)", q.gotTaskID, q.gotQueueID)
			}
			resp := decodeResp(t, rec)
			if resp.Success {
				t.Error("success = true on a rejected request")
			}
			if tc.wantStatus == http.StatusBadRequest && resp.Code != api.ErrCodeBadRequest {
				t.Errorf("code = %q, want %q", resp.Code, api.ErrCodeBadRequest)
			}
		})
	}
}

// TestQueueServiceErrMapping verifies a queue sentinel travels out as its
// wire code rather than collapsing to INTERNAL — ErrInvalidRequest in
// particular, since it is the queue that decides a DAG is malformed.
func TestQueueServiceErrMapping(t *testing.T) {
	cases := []struct {
		err      error
		wantCode string
	}{
		{service.ErrNotFound, api.ErrCodeNotFound},
		{service.ErrConflict, api.ErrCodeConflict},
		{service.ErrInvalidRequest, api.ErrCodeBadRequest},
		{service.ErrUnavailable, api.ErrCodeUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.wantCode, func(t *testing.T) {
			s := newQueueTestServer(&stubQueue{err: tc.err})
			rec := httptest.NewRecorder()
			s.handleQueueCancel(rec, postJSON("/api/queue/cancel", `{"task_id":"t1"}`))

			resp := decodeResp(t, rec)
			if resp.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", resp.Code, tc.wantCode)
			}
			if rec.Code != api.HTTPStatusForCode(tc.wantCode) {
				t.Errorf("status = %d, want %d", rec.Code, api.HTTPStatusForCode(tc.wantCode))
			}
		})
	}
}

// TestQueueHandlersRequireServices locks the 503 behaviour every
// service-routed handler shares: an unwired daemon must not panic.
func TestQueueHandlersRequireServices(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleQueueList(rec, httptest.NewRequest(http.MethodGet, "/api/queue/list", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
	}
}
