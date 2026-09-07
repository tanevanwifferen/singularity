package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// queueTestServer serves the queue surface over TCP and records the last
// request path + decoded body, so the tests can assert what went on the wire.
type queueTestServer struct {
	*httptest.Server
	path string
	body map[string]any
}

func newQueueTestServer(t *testing.T, data func(path string) any) *queueTestServer {
	t.Helper()
	qs := &queueTestServer{}
	qs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qs.path = r.URL.RequestURI()
		qs.body = nil
		if r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&qs.body)
		}
		_ = json.NewEncoder(w).Encode(api.APIResponse{Success: true, Data: data(r.URL.Path)})
	}))
	t.Cleanup(qs.Close)
	return qs
}

func TestQueueClientReads(t *testing.T) {
	qs := newQueueTestServer(t, func(path string) any {
		switch path {
		case "/api/queue/list":
			return api.QueueListResponse{Tasks: []api.Task{{ID: "t1", QueueID: "q1"}}}
		case "/api/queue/get":
			return api.Task{ID: "t1", QueueID: "q1"}
		case "/api/queue/queues":
			return api.QueueQueuesResponse{Queues: []api.QueueInfo{{ID: "q1", Total: 2}, {ID: "q2"}}}
		case "/api/queue/graph":
			return api.QueueGraphResponse{Graph: api.QueueGraph{QueueID: "q1"}}
		}
		return nil
	})
	c := NewClient(qs.URL)
	ctx := context.Background()

	tasks, err := c.QueueList(ctx, "q 1", []api.TaskState{service.TaskReady, service.TaskRunning})
	if err != nil {
		t.Fatalf("QueueList: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "t1" {
		t.Errorf("QueueList = %+v", tasks)
	}
	// Filters travel as query params, escaped, one `state` per value.
	if qs.path != "/api/queue/list?queue=q+1&state=ready&state=running" {
		t.Errorf("list path = %q", qs.path)
	}

	if _, err := c.QueueGet(ctx, "t/1"); err != nil {
		t.Fatalf("QueueGet: %v", err)
	}
	if qs.path != "/api/queue/get?task_id=t%2F1" {
		t.Errorf("get path = %q", qs.path)
	}

	if _, err := c.QueueGraph(ctx, "q1"); err != nil {
		t.Fatalf("QueueGraph: %v", err)
	}
	if qs.path != "/api/queue/graph?queue=q1" {
		t.Errorf("graph path = %q", qs.path)
	}

	// QueueInfo has no endpoint of its own: it selects from the listing.
	info, err := c.QueueInfo(ctx, "q1")
	if err != nil {
		t.Fatalf("QueueInfo: %v", err)
	}
	if info.Total != 2 {
		t.Errorf("QueueInfo = %+v", info)
	}
	if _, err := c.QueueInfo(ctx, "nope"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("QueueInfo(unknown) = %v, want ErrNotFound", err)
	}
}

func TestQueueClientWrites(t *testing.T) {
	qs := newQueueTestServer(t, func(path string) any {
		if path == "/api/queue/add" {
			return api.QueueAddResponse{Tasks: []api.Task{{ID: "t1", QueueID: "q1"}}}
		}
		return nil
	})
	c := NewClient(qs.URL)
	ctx := context.Background()

	tasks, err := c.QueueAdd(ctx, []api.TaskSpec{{Name: "impl", Prompt: "p", WorkDir: "/w"}})
	if err != nil {
		t.Fatalf("QueueAdd: %v", err)
	}
	if len(tasks) != 1 || tasks[0].QueueID != "q1" {
		t.Errorf("QueueAdd = %+v", tasks)
	}
	if _, ok := qs.body["tasks"]; !ok {
		t.Errorf("add body = %v, want a tasks array", qs.body)
	}

	cases := []struct {
		name     string
		call     func() error
		wantPath string
		wantBody map[string]any
	}{
		{"cancel", func() error { return c.QueueCancel(ctx, "t1") },
			"/api/queue/cancel", map[string]any{"task_id": "t1"}},
		{"cancel_queue", func() error { return c.QueueCancelQueue(ctx, "q1") },
			"/api/queue/cancel_queue", map[string]any{"queue_id": "q1"}},
		{"retry", func() error { return c.QueueRetry(ctx, "t1") },
			"/api/queue/retry", map[string]any{"task_id": "t1"}},
		{"answer", func() error { return c.QueueAnswer(ctx, "t1", "yes") },
			"/api/queue/answer", map[string]any{"task_id": "t1", "message": "yes"}},
		{"pause", func() error { return c.QueuePause(ctx, "q1") },
			"/api/queue/pause", map[string]any{"queue_id": "q1"}},
		{"resume", func() error { return c.QueueResume(ctx, "q1") },
			"/api/queue/resume", map[string]any{"queue_id": "q1"}},
		{"remove", func() error { return c.QueueRemove(ctx, "q1") },
			"/api/queue/remove", map[string]any{"queue_id": "q1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if qs.path != tc.wantPath {
				t.Errorf("path = %q, want %q", qs.path, tc.wantPath)
			}
			for k, want := range tc.wantBody {
				if got := qs.body[k]; got != want {
					t.Errorf("body[%q] = %v, want %v", k, got, want)
				}
			}
		})
	}
}

// TestQueueAddRejectedDAG covers the sentinel the queue introduced: a
// refused batch must arrive as ErrInvalidRequest, not a bare error string.
func TestQueueAddRejectedDAG(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(api.APIResponse{
			Success: false,
			Code:    api.ErrCodeBadRequest,
			Error:   "task t3 depends on unknown task t9",
		})
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL).QueueAdd(context.Background(),
		[]api.TaskSpec{{Prompt: "p", WorkDir: "/w", After: []string{"t9"}}})
	if !errors.Is(err, service.ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
	if err.Error() != "task t3 depends on unknown task t9" {
		t.Errorf("message lost: %q", err)
	}
}
