package engine

import (
	"strings"
	"testing"
)

func toolUse(id, name string, input map[string]interface{}) *BackendEvent {
	return &BackendEvent{Kind: BackendToolUse, ToolID: id, ToolName: name, ToolInput: input}
}

func turnEnd() *BackendEvent {
	return &BackendEvent{Kind: BackendResult, Subtype: "success"}
}

func TestResultWithAbandonedBackgroundWorkIsAnError(t *testing.T) {
	cases := []struct {
		name string
		uses []*BackendEvent
	}{
		{"monitor", []*BackendEvent{toolUse("t1", "Monitor", map[string]interface{}{"command": "tail -f x"})}},
		{"background bash", []*BackendEvent{toolUse("t1", "Bash", map[string]interface{}{"command": "make test", "run_in_background": true})}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &Agent{State: AgentRunning}
			for _, ev := range tc.uses {
				a.handleBackendEvent(ev)
			}
			a.handleBackendEvent(turnEnd())
			if a.State != AgentError {
				t.Fatalf("state = %s, want %s", a.State, AgentError)
			}
			if !strings.Contains(a.Error, "background work still running") {
				t.Errorf("error %q does not name the background work", a.Error)
			}
		})
	}
}

func TestResultWithoutBackgroundWorkStaysComplete(t *testing.T) {
	a := &Agent{State: AgentRunning}
	a.handleBackendEvent(toolUse("t1", "Bash", map[string]interface{}{"command": "go test ./..."}))
	a.handleBackendEvent(toolUse("t2", "Bash", map[string]interface{}{"command": "x", "run_in_background": false}))
	a.handleBackendEvent(turnEnd())
	if a.State != AgentComplete {
		t.Fatalf("state = %s, want %s", a.State, AgentComplete)
	}
}

func TestAwaitingBackgroundWorkClearsIt(t *testing.T) {
	a := &Agent{State: AgentRunning}
	a.handleBackendEvent(toolUse("t1", "Bash", map[string]interface{}{"command": "make test", "run_in_background": true}))
	a.handleBackendEvent(toolUse("t2", "TaskOutput", map[string]interface{}{"task_id": "x"}))
	a.handleBackendEvent(turnEnd())
	if a.State != AgentComplete {
		t.Fatalf("state = %s, want %s", a.State, AgentComplete)
	}
}

func TestBackgroundWorkDoesNotLeakIntoNextTurn(t *testing.T) {
	a := &Agent{State: AgentRunning}
	a.handleBackendEvent(toolUse("t1", "Monitor", nil))
	a.handleBackendEvent(turnEnd())
	a.mu.Lock()
	a.State = AgentRunning
	a.mu.Unlock()
	a.handleBackendEvent(turnEnd())
	if a.State != AgentComplete {
		t.Fatalf("second turn state = %s, want %s", a.State, AgentComplete)
	}
}
