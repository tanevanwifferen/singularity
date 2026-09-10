package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

type scriptedAgents struct {
	AgentService

	snaps      []AgentSnapshot
	listErr    error
	terminate  map[string]error
	terminated []string
}

func (s *scriptedAgents) List(context.Context) ([]AgentSnapshot, error) {
	return s.snaps, s.listErr
}

func (s *scriptedAgents) Terminate(_ context.Context, id string) error {
	s.terminated = append(s.terminated, id)
	return s.terminate[id]
}

func testWorkflow(t *testing.T) *FeatureWorkflow {
	t.Helper()
	proj := &Project{Name: "p", Repos: []*Repo{{Name: "web", Path: "/src/web"}}}
	return NewFeatureWorkflow(proj, "feat/x", t.TempDir())
}

func TestTerminateWorkflowAgentsSelectsByDirectory(t *testing.T) {
	wf := testWorkflow(t)
	wf.SetWorkflowAgentID("recorded")
	dir := wf.WorkflowDir()
	agents := &scriptedAgents{snaps: []AgentSnapshot{
		{ID: "recorded", WorkDir: dir},
		{ID: "nested", WorkDir: filepath.Join(dir, "web", "sub")},
		{ID: "unclean", WorkDir: dir + "/web/../web"},
		{ID: "sibling", WorkDir: dir + "2"},
		{ID: "parent", WorkDir: filepath.Dir(dir)},
	}}
	if err := TerminateWorkflowAgents(context.Background(), agents, wf); err != nil {
		t.Fatalf("TerminateWorkflowAgents: %v", err)
	}
	want := []string{"recorded", "nested", "unclean"}
	if len(agents.terminated) != len(want) {
		t.Fatalf("terminated %v, want %v", agents.terminated, want)
	}
	for i := range want {
		if agents.terminated[i] != want[i] {
			t.Errorf("terminated %v, want %v", agents.terminated, want)
		}
	}
}

func TestTerminateWorkflowAgentsToleratesGoneAgents(t *testing.T) {
	wf := testWorkflow(t)
	wf.SetWorkflowAgentID("gone")
	agents := &scriptedAgents{terminate: map[string]error{"gone": ErrNotFound}}
	if err := TerminateWorkflowAgents(context.Background(), agents, wf); err != nil {
		t.Errorf("a vanished agent is not a failure, got %v", err)
	}
	agents = &scriptedAgents{listErr: ErrUnavailable}
	if err := TerminateWorkflowAgents(context.Background(), agents, wf); err != nil {
		t.Errorf("no engine means nothing to kill, got %v", err)
	}
	if len(agents.terminated) != 0 {
		t.Errorf("terminated %v without an engine", agents.terminated)
	}
	if err := TerminateWorkflowAgents(context.Background(), nil, wf); err != nil {
		t.Errorf("nil service: %v", err)
	}
}

func TestTerminateWorkflowAgentsReportsEveryFailure(t *testing.T) {
	wf := testWorkflow(t)
	dir := wf.WorkflowDir()
	boom := errors.New("boom")
	agents := &scriptedAgents{
		snaps:     []AgentSnapshot{{ID: "a", WorkDir: dir}, {ID: "b", WorkDir: dir}, {ID: "c", WorkDir: dir}},
		terminate: map[string]error{"a": boom, "c": boom},
	}
	err := TerminateWorkflowAgents(context.Background(), agents, wf)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want to wrap %v", err, boom)
	}
	if len(agents.terminated) != 3 {
		t.Errorf("one failure must not stop the others: terminated %v", agents.terminated)
	}
	for _, id := range []string{"agent a", "agent c"} {
		if !contains(err.Error(), id) {
			t.Errorf("err %q does not name %s", err, id)
		}
	}
	if contains(err.Error(), "agent b") {
		t.Errorf("err %q blames b, which was terminated fine", err)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
