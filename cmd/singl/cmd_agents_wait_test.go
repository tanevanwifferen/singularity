package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// scriptedPoller returns, per agent id, a sequence of states; once the
// sequence is exhausted the last state repeats. It counts polls so tests
// can assert that terminal agents are not re-polled.
type scriptedPoller struct {
	states map[string][]string
	calls  map[string]int
}

func (p *scriptedPoller) poll(_ context.Context, id string) (*api.AgentSnapshotDTO, error) {
	seq, ok := p.states[id]
	if !ok {
		return nil, fmt.Errorf("unknown agent %q", id)
	}
	i := p.calls[id]
	p.calls[id]++
	if i >= len(seq) {
		i = len(seq) - 1
	}
	state := seq[i]
	dto := &api.AgentSnapshotDTO{ID: id, State: state}
	if isTerminalAgentState(state) {
		code := 0
		if state != "complete" {
			code = 1
		}
		dto.ExitCode = &code
	}
	return dto, nil
}

func TestWaitForAgentsAll(t *testing.T) {
	p := &scriptedPoller{states: map[string][]string{
		"a": {"running", "running", "complete"},
		"b": {"running", "complete"},
	}, calls: map[string]int{}}

	latest, timedOut, err := waitForAgents(context.Background(), p.poll,
		[]string{"a", "b"}, false, 0, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if timedOut {
		t.Fatal("unexpected timeout")
	}
	for _, id := range []string{"a", "b"} {
		if latest[id].State != "complete" {
			t.Errorf("agent %s state = %q, want complete", id, latest[id].State)
		}
	}
	// b completed on the second poll; it must not be polled again while a
	// is still running.
	if p.calls["b"] != 2 {
		t.Errorf("agent b polled %d times, want 2", p.calls["b"])
	}
	if code := waitExitCode(latest, []string{"a", "b"}, false); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestWaitForAgentsAny(t *testing.T) {
	p := &scriptedPoller{states: map[string][]string{
		"slow": {"running", "running", "running", "running"},
		"fast": {"running", "error"},
	}, calls: map[string]int{}}

	latest, timedOut, err := waitForAgents(context.Background(), p.poll,
		[]string{"slow", "fast"}, true, 0, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if timedOut {
		t.Fatal("unexpected timeout")
	}
	if latest["fast"].State != "error" {
		t.Errorf("fast state = %q, want error", latest["fast"].State)
	}
	if latest["slow"].State != "running" {
		t.Errorf("slow state = %q, want running (still in flight)", latest["slow"].State)
	}
	// One agent errored → non-zero exit.
	if code := waitExitCode(latest, []string{"slow", "fast"}, false); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

func TestWaitForAgentsTimeout(t *testing.T) {
	p := &scriptedPoller{states: map[string][]string{
		"a": {"running"},
	}, calls: map[string]int{}}

	latest, timedOut, err := waitForAgents(context.Background(), p.poll,
		[]string{"a"}, false, 20*time.Millisecond, 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !timedOut {
		t.Fatal("expected timeout")
	}
	if latest["a"].State != "running" {
		t.Errorf("state = %q, want running", latest["a"].State)
	}
	if code := waitExitCode(latest, []string{"a"}, true); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

func TestWaitForAgentsUnknownID(t *testing.T) {
	p := &scriptedPoller{states: map[string][]string{}, calls: map[string]int{}}
	_, _, err := waitForAgents(context.Background(), p.poll,
		[]string{"nope"}, false, 0, time.Millisecond)
	if err == nil {
		t.Fatal("expected error for unknown agent id")
	}
}

func TestIDListFlag(t *testing.T) {
	var ids idListFlag
	if err := ids.Set("a"); err != nil {
		t.Fatal(err)
	}
	if err := ids.Set("b, c"); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0] != "a" || ids[1] != "b" || ids[2] != "c" {
		t.Errorf("ids = %v, want [a b c]", ids)
	}
}

func TestAgentToJSONCompactVsFull(t *testing.T) {
	start := time.Now().Add(-10 * time.Second)
	end := start.Add(4 * time.Second)
	code := 0
	dto := api.AgentSnapshotDTO{
		ID:        "a",
		State:     "complete",
		Task:      "a very long prompt that should not appear by default",
		StartedAt: &start,
		EndedAt:   &end,
		ExitCode:  &code,
	}
	compact := agentToJSON(dto, false)
	if compact.Task != "" {
		t.Errorf("compact projection must omit task, got %q", compact.Task)
	}
	if compact.DurationSecs == nil || *compact.DurationSecs != 4 {
		t.Errorf("duration_secs = %v, want 4", compact.DurationSecs)
	}
	if compact.ExitCode == nil || *compact.ExitCode != 0 {
		t.Errorf("exit_code = %v, want 0", compact.ExitCode)
	}
	full := agentToJSON(dto, true)
	if full.Task != dto.Task {
		t.Errorf("full projection must include task")
	}
}

func TestOutputEntriesToJSONType(t *testing.T) {
	entries := []api.OutputEntry{
		{Source: "text", Content: "hello"},
		{Source: "tool_use", Content: "Read(x)", ToolName: "Read"},
	}
	out := outputEntriesToJSON(entries)
	if out[0].Type != "text" || out[1].Type != "tool_use" {
		t.Errorf("type field not populated: %+v", out)
	}
	if out[1].ToolName != "Read" {
		t.Errorf("tool_name lost in projection")
	}
}

func TestTailEntries(t *testing.T) {
	entries := []api.OutputEntry{{Content: "1"}, {Content: "2"}, {Content: "3"}}
	if got := tailEntries(entries, 2); len(got) != 2 || got[0].Content != "2" {
		t.Errorf("tail(2) = %v", got)
	}
	if got := tailEntries(entries, 0); len(got) != 3 {
		t.Errorf("tail(0) should return all entries")
	}
	if got := tailEntries(entries, 10); len(got) != 3 {
		t.Errorf("tail(10) should return all entries")
	}
}
