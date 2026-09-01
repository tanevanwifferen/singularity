package engine

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// stubBackend runs `cat` so agents start without a real coding-agent binary:
// stdin is echoed to stdout and every line parses to zero events.
type stubBackend struct{}

func (stubBackend) Name() string   { return "stub" }
func (stubBackend) Binary() string { return "cat" }
func (stubBackend) Args(model, effort string, maxTurns int, allowedTools []string) []string {
	return nil
}
func (stubBackend) Env() []string                                   { return nil }
func (stubBackend) InitialInput(task, _ string) ([]byte, error)     { return []byte(task + "\n"), nil }
func (stubBackend) PostStartCommands(string) [][]byte               { return nil }
func (stubBackend) ParseEvent(line []byte) ([]*BackendEvent, error) { return []*BackendEvent{}, nil }
func (stubBackend) OneShotCommand(prompt string) (string, []string) {
	return "true", nil
}
func (stubBackend) FollowUpInput(message, _ string, _ bool) ([]byte, error) {
	return []byte(message + "\n"), nil
}

// waitForUserInputEntries polls until at least n "user_input" entries exist.
func waitForUserInputEntries(t *testing.T, e *Engine, id string, n int) []OutputEntry {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := e.GetOutputEntries(id, 0)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, en := range entries {
			if en.Source == "user_input" {
				count++
			}
		}
		if count >= n {
			return entries
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fewer than %d user_input entries after 3s", n)
	return nil
}

// TestSpawnLogsPromptEntry checks the initial task shows up in the output
// stream (first or second entry) and that follow-ups are logged too.
func TestSpawnLogsPromptEntry(t *testing.T) {
	e := New(5)
	defer e.Shutdown()
	id, err := e.StartAgent(t.TempDir(), "do the thing", AgentOptions{Backend: stubBackend{}})
	if err != nil {
		t.Fatal(err)
	}

	entries := waitForUserInputEntries(t, e, id, 1)
	idx := -1
	for i, en := range entries {
		if en.Source == "user_input" {
			idx = i
			break
		}
	}
	if idx > 1 {
		t.Errorf("prompt entry at index %d, want 0 or 1 (entries: %v)", idx, entries)
	}
	if entries[idx].Content != "do the thing" {
		t.Errorf("prompt content = %q, want the task text", entries[idx].Content)
	}

	if err := e.SendInput(id, "now do the other thing"); err != nil {
		t.Fatal(err)
	}
	entries = waitForUserInputEntries(t, e, id, 2)
	var last string
	for _, en := range entries {
		if en.Source == "user_input" {
			last = en.Content
		}
	}
	if last != "now do the other thing" {
		t.Errorf("follow-up entry = %q, want the follow-up message", last)
	}
}

// TestResumeLogsNoteNotHistory checks the resume path logs one compact note
// instead of replaying the seeded history as a giant prompt entry.
func TestResumeLogsNoteNotHistory(t *testing.T) {
	e := New(5)
	defer e.Shutdown()
	id1, err := e.StartAgent(t.TempDir(), "original task", AgentOptions{Backend: stubBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	waitForUserInputEntries(t, e, id1, 1)

	id2, err := e.ResumeWithHistory(id1, "pick up milestone 2", AgentOptions{Backend: stubBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	entries := waitForUserInputEntries(t, e, id2, 1)
	var prompts []string
	for _, en := range entries {
		if en.Source == "user_input" {
			prompts = append(prompts, en.Content)
		}
	}
	if len(prompts) != 1 {
		t.Fatalf("resumed agent has %d prompt entries, want exactly 1: %v", len(prompts), prompts)
	}
	if !strings.Contains(prompts[0], "Resumed from "+id1) || !strings.Contains(prompts[0], "pick up milestone 2") {
		t.Errorf("resume note should name the old agent and the new message, got %q", prompts[0])
	}
	if strings.Contains(prompts[0], "=== CONVERSATION HISTORY") {
		t.Errorf("resume note must not replay the seeded history: %q", prompts[0])
	}
}

func TestTruncateForLog(t *testing.T) {
	if got := truncateForLog("short", 4000); got != "short" {
		t.Errorf("short input must pass through, got %q", got)
	}
	long := strings.Repeat("a", 5000)
	got := truncateForLog(long, 4000)
	if !strings.Contains(got, "[1000 bytes elided") {
		t.Errorf("truncation must state how much was elided, got tail %q", got[len(got)-80:])
	}
	if len(got) > 4100 {
		t.Errorf("truncated output too long: %d bytes", len(got))
	}
	// Multi-byte input must not be cut mid-rune.
	multi := strings.Repeat("é", 3000) // 6000 bytes
	if cut := truncateForLog(multi, 4001); !utf8.ValidString(cut) {
		t.Error("truncation split a rune")
	}
}
