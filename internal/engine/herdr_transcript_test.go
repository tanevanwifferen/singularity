package engine

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestHerdrTranscriptEventsAssistant(t *testing.T) {
	events := herdrTranscriptEvents(`{"type":"assistant","isSidechain":false,"message":{"role":"assistant","content":[` +
		`{"type":"thinking","thinking":"hmm"},` +
		`{"type":"text","text":"Looking at the tree."},` +
		`{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"git status"}}]}}`)
	if len(events) != 2 {
		t.Fatalf("events = %+v, want text + tool_use (thinking ignored)", events)
	}
	if events[0].Kind != BackendText || events[0].Content != "Looking at the tree." {
		t.Errorf("events[0] = %+v, want the text block", events[0])
	}
	if events[1].Kind != BackendToolUse || events[1].ToolName != "Bash" || events[1].ToolID != "toolu_1" ||
		events[1].ToolInput["command"] != "git status" {
		t.Errorf("events[1] = %+v, want the tool_use block", events[1])
	}
}

func TestHerdrTranscriptEventsToolResult(t *testing.T) {
	events := herdrTranscriptEvents(`{"type":"user","message":{"role":"user","content":[` +
		`{"type":"tool_result","tool_use_id":"toolu_1","is_error":true,"content":"fatal: not a git repository"}]}}`)
	if len(events) != 1 || events[0].Kind != BackendToolResult || events[0].ToolID != "toolu_1" ||
		!events[0].IsError || events[0].Content != "fatal: not a git repository" {
		t.Errorf("events = %+v, want one tool_result with the string content", events)
	}

	events = herdrTranscriptEvents(`{"type":"user","message":{"role":"user","content":[` +
		`{"type":"tool_result","tool_use_id":"toolu_2","content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}]}}`)
	if len(events) != 1 || events[0].Content != "a\nb" || events[0].IsError {
		t.Errorf("events = %+v, want one tool_result with the text blocks joined", events)
	}
}

func TestHerdrTranscriptEventsIgnoresTheRest(t *testing.T) {
	for _, record := range []string{
		// The prompt the engine itself typed in, which it already logs.
		`{"type":"user","message":{"role":"user","content":"do the thing"}}`,
		// A sub-agent's record.
		`{"type":"assistant","isSidechain":true,"message":{"content":[{"type":"text","text":"sub"}]}}`,
		`{"type":"attachment","attachment":{}}`,
		`{"type":"queue-operation"}`,
	} {
		events := herdrTranscriptEvents(record)
		if len(events) != 1 || events[0].Kind != BackendIgnore {
			t.Errorf("%s → %+v, want a single BackendIgnore", record, events)
		}
	}
	if events := herdrTranscriptEvents("not json"); len(events) != 1 || events[0].Kind != BackendError {
		t.Errorf("garbage → %+v, want one BackendError", events)
	}
}

func TestHerdrTranscriptTailerFollowsFile(t *testing.T) {
	dir := t.TempDir()
	tail := newHerdrTranscriptTailer(dir, "sess")
	var got []string
	emit := func(r string) { got = append(got, r) }

	// No file yet: nothing, and no error.
	if err := tail.poll(emit); err != nil || len(got) != 0 {
		t.Fatalf("poll before the file exists: got=%v err=%v", got, err)
	}

	proj := filepath.Join(dir, "projects", "-home-x-proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj, "sess.jsonl")
	// One complete record and one still being written.
	if err := os.WriteFile(path, []byte("{\"a\":1}\n{\"b\":"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tail.poll(emit); err != nil || len(got) != 1 || got[0] != `{"a":1}` {
		t.Fatalf("first poll: got=%v err=%v, want the one complete record", got, err)
	}
	// The rest of the partial record arrives.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString("2}\n\n{\"c\":3}\n")
	_ = f.Close()
	if err := tail.poll(emit); err != nil || len(got) != 3 || got[1] != `{"b":2}` || got[2] != `{"c":3}` {
		t.Fatalf("second poll: got=%v err=%v, want the completed record and the next", got, err)
	}
	if tail.records != 3 {
		t.Errorf("records = %d, want 3", tail.records)
	}
	// Nothing new: nothing emitted.
	if err := tail.poll(emit); err != nil || len(got) != 3 {
		t.Fatalf("idle poll: got=%v err=%v", got, err)
	}
}

func TestNewSessionUUIDIsCanonicalV4(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	a, b := newSessionUUID(), newSessionUUID()
	if !re.MatchString(a) || !re.MatchString(b) {
		t.Errorf("uuids %q %q are not canonical v4 (claude --session-id rejects anything else)", a, b)
	}
	if a == b {
		t.Error("two uuids collided")
	}
}
