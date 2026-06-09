package engine

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewEngine(t *testing.T) {
	e := New(5)
	if e == nil {
		t.Fatal("New returned nil")
	}

	stats := e.Stats()
	if stats.MaxAgents != 5 {
		t.Errorf("expected max agents 5, got %d", stats.MaxAgents)
	}
	if stats.Total != 0 {
		t.Errorf("expected 0 total agents, got %d", stats.Total)
	}
}

func TestNewEngineDefaultMax(t *testing.T) {
	e := New(0)
	stats := e.Stats()
	if stats.MaxAgents != 10 {
		t.Errorf("expected default max agents 10, got %d", stats.MaxAgents)
	}
}

func TestStartAgentInvalidPath(t *testing.T) {
	e := New(5)
	_, err := e.StartAgent("/nonexistent/path", "test task", AgentOptions{})
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestStartAgentFilePath(t *testing.T) {
	e := New(5)
	// Create a temp file (not directory)
	f, err := os.CreateTemp("", "agent-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.Close()

	_, err = e.StartAgent(f.Name(), "test task", AgentOptions{})
	if err == nil {
		t.Fatal("expected error for file path (not directory)")
	}
}

// TestStartAgentWithEcho tests agent lifecycle using echo as a mock command
func TestStartAgentWithEcho(t *testing.T) {
	e := New(5)

	// Use a simple echo command via a script approach
	// We'll test the engine structure without actually invoking claude
	tmpDir := t.TempDir()

	// Start an agent - this will fail because 'claude' isn't available,
	// but we can verify the setup
	id, err := e.StartAgent(tmpDir, "echo hello", AgentOptions{})
	if err != nil {
		// Expected if claude CLI is not installed
		if strings.Contains(err.Error(), "executable file not found") ||
			strings.Contains(err.Error(), "not found") {
			t.Skip("claude CLI not available, skipping integration test")
		}
		t.Fatalf("unexpected error: %v", err)
	}

	if id == "" {
		t.Fatal("expected non-empty session ID")
	}

	// Verify agent exists
	agent := e.GetAgent(id)
	if agent == nil {
		t.Fatal("agent should exist")
	}
	if agent.WorkDir != tmpDir {
		t.Errorf("expected work dir %q, got %q", tmpDir, agent.WorkDir)
	}
}

func TestAgentCapacityLimit(t *testing.T) {
	e := New(2)

	// Simulate agents at capacity by creating agents and setting them active
	a1 := newAgent("test-1", os.TempDir(), "task1", AgentOptions{}, NewClaudeBackend())
	a1.State = AgentRunning
	a2 := newAgent("test-2", os.TempDir(), "task2", AgentOptions{}, NewClaudeBackend())
	a2.State = AgentRunning

	e.mu.Lock()
	e.agents["test-1"] = a1
	e.agents["test-2"] = a2
	e.mu.Unlock()

	_, err := e.StartAgent(os.TempDir(), "task3", AgentOptions{})
	if err == nil {
		t.Fatal("expected capacity limit error")
	}
	if !strings.Contains(err.Error(), "agent limit reached") {
		t.Errorf("expected 'agent limit reached' error, got: %v", err)
	}
}

func TestListAgents(t *testing.T) {
	e := New(10)

	a1 := newAgent("test-1", os.TempDir(), "task1", AgentOptions{}, NewClaudeBackend())
	a2 := newAgent("test-2", os.TempDir(), "task2", AgentOptions{}, NewClaudeBackend())

	e.mu.Lock()
	e.agents["test-1"] = a1
	e.agents["test-2"] = a2
	e.mu.Unlock()

	agents := e.ListAgents()
	if len(agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(agents))
	}
}

func TestActiveAgents(t *testing.T) {
	e := New(10)

	a1 := newAgent("test-1", os.TempDir(), "task1", AgentOptions{}, NewClaudeBackend())
	a1.State = AgentRunning
	a2 := newAgent("test-2", os.TempDir(), "task2", AgentOptions{}, NewClaudeBackend())
	a2.State = AgentComplete
	a3 := newAgent("test-3", os.TempDir(), "task3", AgentOptions{}, NewClaudeBackend())
	a3.State = AgentStarting

	e.mu.Lock()
	e.agents["test-1"] = a1
	e.agents["test-2"] = a2
	e.agents["test-3"] = a3
	e.mu.Unlock()

	active := e.ActiveAgents()
	if len(active) != 2 {
		t.Errorf("expected 2 active agents, got %d", len(active))
	}
}

func TestGetStatusNotFound(t *testing.T) {
	e := New(5)
	_, err := e.GetStatus("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
}

func TestGetOutputNotFound(t *testing.T) {
	e := New(5)
	_, err := e.GetOutput("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
}

func TestKillAgentNotFound(t *testing.T) {
	e := New(5)
	err := e.KillAgent("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
}

func TestRemoveAgent(t *testing.T) {
	e := New(10)

	a := newAgent("test-1", os.TempDir(), "task1", AgentOptions{}, NewClaudeBackend())
	a.State = AgentComplete

	e.mu.Lock()
	e.agents["test-1"] = a
	e.mu.Unlock()

	err := e.RemoveAgent("test-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if e.GetAgent("test-1") != nil {
		t.Error("agent should be removed")
	}
}

func TestRemoveAgentNotFound(t *testing.T) {
	e := New(5)
	err := e.RemoveAgent("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
}

func TestEngineStats(t *testing.T) {
	e := New(10)

	agents := map[string]AgentState{
		"a1": AgentRunning,
		"a2": AgentComplete,
		"a3": AgentError,
		"a4": AgentKilled,
		"a5": AgentStarting,
	}

	e.mu.Lock()
	for id, state := range agents {
		a := newAgent(id, os.TempDir(), "task", AgentOptions{}, NewClaudeBackend())
		a.State = state
		e.agents[id] = a
	}
	e.mu.Unlock()

	stats := e.Stats()
	if stats.Total != 5 {
		t.Errorf("expected total 5, got %d", stats.Total)
	}
	if stats.Active != 2 {
		t.Errorf("expected active 2, got %d", stats.Active)
	}
	if stats.Completed != 1 {
		t.Errorf("expected completed 1, got %d", stats.Completed)
	}
	if stats.Errored != 1 {
		t.Errorf("expected errored 1, got %d", stats.Errored)
	}
	if stats.Killed != 1 {
		t.Errorf("expected killed 1, got %d", stats.Killed)
	}
}

func TestShutdown(t *testing.T) {
	e := New(10)

	a := newAgent("test-1", os.TempDir(), "task1", AgentOptions{}, NewClaudeBackend())
	a.State = AgentComplete // not active, won't try to kill

	e.mu.Lock()
	e.agents["test-1"] = a
	e.mu.Unlock()

	e.Shutdown()

	if len(e.ListAgents()) != 0 {
		t.Error("shutdown should remove all agents")
	}
}

func TestAgentStateString(t *testing.T) {
	tests := []struct {
		state AgentState
		want  string
	}{
		{AgentIdle, "idle"},
		{AgentStarting, "starting"},
		{AgentRunning, "running"},
		{AgentComplete, "complete"},
		{AgentError, "error"},
		{AgentKilled, "killed"},
		{AgentState(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("AgentState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestAgentOutput(t *testing.T) {
	a := newAgent("test", os.TempDir(), "task", AgentOptions{}, NewClaudeBackend())

	a.appendOutput("text", "line 1")
	a.appendOutput("text", "line 2")
	a.appendOutput("error", "warning")
	a.appendOutput("text", "line 3")

	// Get all output
	entries := a.getOutput(0)
	if len(entries) != 4 {
		t.Errorf("expected 4 entries, got %d", len(entries))
	}

	// Get output from offset
	entries = a.getOutput(2)
	if len(entries) != 2 {
		t.Errorf("expected 2 entries from offset 2, got %d", len(entries))
	}

	// Get output past end
	entries = a.getOutput(10)
	if entries != nil {
		t.Errorf("expected nil for offset past end, got %v", entries)
	}

	// Full output (text entries only)
	full := a.getFullOutput()
	if full != "line 1\nline 2\nline 3" {
		t.Errorf("unexpected full output: %q", full)
	}
}

func TestAgentIsActive(t *testing.T) {
	a := newAgent("test", os.TempDir(), "task", AgentOptions{}, NewClaudeBackend())

	a.State = AgentIdle
	if a.IsActive() {
		t.Error("idle agent should not be active")
	}

	a.State = AgentStarting
	if !a.IsActive() {
		t.Error("starting agent should be active")
	}

	a.State = AgentRunning
	if !a.IsActive() {
		t.Error("running agent should be active")
	}

	a.State = AgentComplete
	if a.IsActive() {
		t.Error("complete agent should not be active")
	}

	a.State = AgentError
	if a.IsActive() {
		t.Error("error agent should not be active")
	}

	a.State = AgentKilled
	if a.IsActive() {
		t.Error("killed agent should not be active")
	}
}

func TestBuildArgs(t *testing.T) {
	opts := AgentOptions{
		Model:        "sonnet",
		MaxTurns:     5,
		AllowedTools: []string{"Read", "Write"},
	}
	a := newAgent("test", os.TempDir(), "do something", opts, NewClaudeBackend())

	args := a.backend.Args(a.model, a.effort, a.maxTurns, a.allowedTools)

	hasModel := false
	hasMaxTurns := false
	hasTool1 := false
	hasTool2 := false
	hasOutputStreamJSON := false
	hasInputStreamJSON := false

	for i, arg := range args {
		switch {
		case arg == "--model" && i+1 < len(args) && args[i+1] == "sonnet":
			hasModel = true
		case arg == "--max-turns" && i+1 < len(args) && args[i+1] == "5":
			hasMaxTurns = true
		case arg == "--allowedTools" && i+1 < len(args) && args[i+1] == "Read":
			hasTool1 = true
		case arg == "--allowedTools" && i+1 < len(args) && args[i+1] == "Write":
			hasTool2 = true
		case arg == "--output-format" && i+1 < len(args) && args[i+1] == "stream-json":
			hasOutputStreamJSON = true
		case arg == "--input-format" && i+1 < len(args) && args[i+1] == "stream-json":
			hasInputStreamJSON = true
		}
	}

	if !hasModel {
		t.Error("expected --model sonnet in args")
	}
	if !hasMaxTurns {
		t.Error("expected --max-turns 5 in args")
	}
	if !hasTool1 || !hasTool2 {
		t.Error("expected --allowedTools for Read and Write")
	}
	if !hasOutputStreamJSON {
		t.Error("expected --output-format stream-json in args")
	}
	if !hasInputStreamJSON {
		t.Error("expected --input-format stream-json in args")
	}
}

func TestBuildTaskNoContextFiles(t *testing.T) {
	a := newAgent("test", os.TempDir(), "do something", AgentOptions{}, NewClaudeBackend())
	task := a.buildTask()
	if task != "do something" {
		t.Errorf("expected bare task, got %q", task)
	}
}

func TestBuildTaskWithContextFiles(t *testing.T) {
	// Create a temp context file
	tmpFile, err := os.CreateTemp("", "context-*.md")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString("# Project Info\nThis is context."); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	a := newAgent("test", os.TempDir(), "do something", AgentOptions{
		ContextFiles: []string{tmpFile.Name()},
	}, NewClaudeBackend())
	task := a.buildTask()

	if !strings.Contains(task, "# Project Info") {
		t.Error("expected context file content in task")
	}
	if !strings.Contains(task, "do something") {
		t.Error("expected original task in task")
	}
	if !strings.Contains(task, "<context file=") {
		t.Error("expected context XML wrapper in task")
	}
}

func TestBuildTaskWithMissingContextFile(t *testing.T) {
	a := newAgent("test", os.TempDir(), "do something", AgentOptions{
		ContextFiles: []string{"/nonexistent/file.md"},
	}, NewClaudeBackend())
	task := a.buildTask()
	if task != "do something" {
		t.Errorf("expected bare task when context file missing, got %q", task)
	}
}

func TestGenerateID(t *testing.T) {
	e := New(5)
	ids := make(map[string]bool)

	for i := 0; i < 100; i++ {
		id := e.generateID()
		if ids[id] {
			t.Fatalf("duplicate ID: %s", id)
		}
		ids[id] = true
	}
}

func TestSendInputNotFound(t *testing.T) {
	e := New(5)
	err := e.SendInput("nonexistent", "hello")
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
}

func TestSendInputNotRunning(t *testing.T) {
	e := New(5)

	a := newAgent("test-1", os.TempDir(), "task", AgentOptions{}, NewClaudeBackend())
	a.State = AgentError

	e.mu.Lock()
	e.agents["test-1"] = a
	e.mu.Unlock()

	err := e.SendInput("test-1", "hello")
	if err == nil {
		t.Fatal("expected error for non-running agent")
	}
	if !strings.Contains(err.Error(), "cannot send input") {
		t.Errorf("expected 'cannot send input' error, got: %v", err)
	}
}

func TestWaitForNotFound(t *testing.T) {
	e := New(5)
	_, err := e.WaitFor("nonexistent", time.Second)
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
}

func TestWaitForCompleted(t *testing.T) {
	e := New(5)

	a := newAgent("test-1", os.TempDir(), "task", AgentOptions{}, NewClaudeBackend())
	a.State = AgentComplete
	close(a.done) // signal completion

	e.mu.Lock()
	e.agents["test-1"] = a
	e.mu.Unlock()

	state, err := e.WaitFor("test-1", time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != AgentComplete {
		t.Errorf("expected complete, got %s", state)
	}
}

func TestWaitForTimeout(t *testing.T) {
	e := New(5)

	a := newAgent("test-1", os.TempDir(), "task", AgentOptions{}, NewClaudeBackend())
	a.State = AgentRunning
	// Don't close done channel - agent is still "running"

	e.mu.Lock()
	e.agents["test-1"] = a
	e.mu.Unlock()

	_, err := e.WaitFor("test-1", 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}
