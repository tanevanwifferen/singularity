package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestQueueTaskWireShape locks the assumption the queue DTOs are built on:
// the domain types are aliased rather than projected, so their own JSON tags
// are the wire contract. If someone drops a tag in internal/queue the shape
// silently changes here, which is what this test catches.
func TestQueueTaskWireShape(t *testing.T) {
	task := Task{
		ID:        "t1",
		QueueID:   "q1",
		Prompt:    "do the thing",
		WorkDir:   "/w/api",
		DependsOn: []string{"t0"},
		State:     TaskState("ready"),
	}
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"id":"t1"`, `"queue_id":"q1"`, `"work_dir":"/w/api"`,
		`"depends_on":["t0"]`, `"state":"ready"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("task JSON missing %s, got: %s", want, data)
		}
	}
	// Optional fields must stay absent rather than reading as real values —
	// an "error":"" would render as a failed task in a view.
	for _, unwanted := range []string{"error", "agent_id", "question", "started_at", "ended_at"} {
		if strings.Contains(string(data), `"`+unwanted+`"`) {
			t.Errorf("task JSON should omit %s, got: %s", unwanted, data)
		}
	}
}

// TestQueueResponseEnvelopes verifies the response wrappers carry their
// documented keys — clients decode by key, not by position.
func TestQueueResponseEnvelopes(t *testing.T) {
	cases := []struct {
		name string
		val  interface{}
		want string
	}{
		{"add", QueueAddResponse{Tasks: []Task{{ID: "t1"}}}, `"tasks":[`},
		{"list", QueueListResponse{Tasks: []Task{{ID: "t1"}}}, `"tasks":[`},
		{"queues", QueueQueuesResponse{Queues: []QueueInfo{{ID: "q1"}}}, `"queues":[`},
		{"graph", QueueGraphResponse{Graph: QueueGraph{QueueID: "q1"}}, `"graph":{`},
		{"changed", QueueTaskChangedPayload{Task: Task{ID: "t1"}}, `"task":{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.val)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), tc.want) {
				t.Errorf("JSON missing %s, got: %s", tc.want, data)
			}
		})
	}
}

// TestQueueRequestDecoding verifies the request bodies decode from the
// snake_case field names the wire contract documents.
func TestQueueRequestDecoding(t *testing.T) {
	var add QueueAddRequest
	body := `{"tasks":[{"name":"review","prompt":"p","work_dir":"/w","after":["build"],
	           "opts":{"model":"opus","use_worktree":true},"on_failure":"continue"}]}`
	if err := json.Unmarshal([]byte(body), &add); err != nil {
		t.Fatal(err)
	}
	if len(add.Tasks) != 1 {
		t.Fatalf("decoded %d tasks, want 1", len(add.Tasks))
	}
	spec := add.Tasks[0]
	if spec.Name != "review" || spec.WorkDir != "/w" || len(spec.After) != 1 {
		t.Errorf("spec decoded wrong: %+v", spec)
	}
	if spec.Opts.Model != "opus" || !spec.Opts.UseWorktree {
		t.Errorf("opts decoded wrong: %+v", spec.Opts)
	}
	if spec.OnFailure != "continue" {
		t.Errorf("on_failure = %q, want continue", spec.OnFailure)
	}

	var answer QueueAnswerRequest
	if err := json.Unmarshal([]byte(`{"task_id":"t3","message":"yes"}`), &answer); err != nil {
		t.Fatal(err)
	}
	if answer.TaskID != "t3" || answer.Message != "yes" {
		t.Errorf("answer decoded wrong: %+v", answer)
	}

	var id QueueIDRequest
	if err := json.Unmarshal([]byte(`{"queue_id":"q7"}`), &id); err != nil {
		t.Fatal(err)
	}
	if id.QueueID != "q7" {
		t.Errorf("queue_id = %q, want q7", id.QueueID)
	}
}
