package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"testing"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// fakeGetter returns an agentGetter whose state per id advances through
// states on successive polls, sticking on the last one.
func fakeGetter(states map[string][]string) agentGetter {
	polls := map[string]int{}
	return func(_ context.Context, id string) (*api.AgentSnapshotDTO, error) {
		seq, ok := states[id]
		if !ok {
			return nil, fmt.Errorf("no such agent: %s", id)
		}
		i := polls[id]
		if i >= len(seq) {
			i = len(seq) - 1
		}
		polls[id]++
		return &api.AgentSnapshotDTO{ID: id, State: seq[i]}, nil
	}
}

func TestWaitForAgentsImmediateTerminal(t *testing.T) {
	get := fakeGetter(map[string][]string{"a": {"complete"}})
	snaps, waited, timedOut, err := waitForAgents(context.Background(), get, []string{"a"}, false, 0, time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if timedOut {
		t.Error("already-terminal agent should not time out")
	}
	if snaps[0].State != "complete" {
		t.Errorf("state = %q, want complete", snaps[0].State)
	}
	if waited > time.Second {
		t.Errorf("terminal agent should return without sleeping, waited %v", waited)
	}
}

func TestWaitForAgentsTransitions(t *testing.T) {
	get := fakeGetter(map[string][]string{
		"a": {"starting", "running", "complete"},
		"b": {"running", "error"},
	})
	snaps, _, timedOut, err := waitForAgents(context.Background(), get,
		[]string{"a", "b"}, false, 0, time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if timedOut {
		t.Error("should not time out with timeout=0")
	}
	if snaps[0].State != "complete" || snaps[1].State != "error" {
		t.Errorf("states = %q, %q; want complete, error", snaps[0].State, snaps[1].State)
	}
}

func TestWaitForAgentsTimeout(t *testing.T) {
	get := fakeGetter(map[string][]string{"a": {"running"}})
	snaps, _, timedOut, err := waitForAgents(context.Background(), get,
		[]string{"a"}, false, 20*time.Millisecond, 5*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !timedOut {
		t.Fatal("expected timeout")
	}
	if snaps[0].State != "running" {
		t.Errorf("timeout must return last known state, got %q", snaps[0].State)
	}
}

func TestWaitForAgentsGetterError(t *testing.T) {
	get := fakeGetter(map[string][]string{})
	_, _, _, err := waitForAgents(context.Background(), get, []string{"ghost"}, false, 0, time.Millisecond)
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

func TestWaitForAgentsContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	get := fakeGetter(map[string][]string{"a": {"running"}})
	_, _, _, err := waitForAgents(ctx, get, []string{"a"}, false, 0, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestTerminalAgentState(t *testing.T) {
	for _, s := range []string{"complete", "error", "killed"} {
		if !terminalAgentState(s) {
			t.Errorf("%q should be terminal", s)
		}
	}
	for _, s := range []string{"idle", "routing", "starting", "running", ""} {
		if terminalAgentState(s) {
			t.Errorf("%q should not be terminal", s)
		}
	}
}

func TestIDListFlag(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	var ids idListFlag
	fs.Var(&ids, "id", "")
	if err := fs.Parse([]string{"--id", "a, b", "--id", "c"}); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0] != "a" || ids[1] != "b" || ids[2] != "c" {
		t.Errorf("ids = %v, want [a b c]", ids)
	}
}

// TestAgentsWaitUsageErrors checks usage errors exit 2 before touching the daemon.
func TestAgentsWaitUsageErrors(t *testing.T) {
	ctx := context.Background()
	if code := runAgentsWait(ctx, nil); code != 2 {
		t.Errorf("wait without --id: code = %d, want 2", code)
	}
	if code := runAgentsWait(ctx, []string{"--id", "x", "--interval", "0"}); code != 2 {
		t.Errorf("wait with --interval 0: code = %d, want 2", code)
	}
	if code := runAgentsWait(ctx, []string{"--id", "x", "--timeout", "-1"}); code != 2 {
		t.Errorf("wait with negative --timeout: code = %d, want 2", code)
	}
	if code := runAgentsWaitAll(ctx, []string{"--interval", "-3"}); code != 2 {
		t.Errorf("wait-all with negative --interval: code = %d, want 2", code)
	}
}

// TestUnknownVerbsExitTwo guards the usage-error contract: an unknown verb
// must exit 2, never 0, while a bare noun is an explicit help request and
// exits 0 after printing the noun's verb reference.
func TestUnknownVerbsExitTwo(t *testing.T) {
	ctx := context.Background()
	dispatchers := map[string]func(context.Context, string, []string) int{
		"agents": cmdAgents, "workflows": cmdWorkflows, "branches": cmdBranches,
		"repos": cmdRepos, "stash": cmdStash, "sync": cmdSync,
		"pipeline": cmdPipeline, "project": cmdProject, "diff": cmdDiff,
		"commit": cmdCommit, "mr": cmdMR, "rebase": cmdRebase,
		"forge": cmdForge, "jira": cmdJira,
	}
	for noun, fn := range dispatchers {
		if code := fn(ctx, "definitely-not-a-verb", nil); code != 2 {
			t.Errorf("%s with unknown verb: code = %d, want 2", noun, code)
		}
		if code := fn(ctx, "", nil); code != 0 {
			t.Errorf("%s with no verb: code = %d, want 0 (help)", noun, code)
		}
	}
}

// TestWaitForAgentsAny returns as soon as the first agent settles instead of
// waiting for its still-running peer (the --any flag).
func TestWaitForAgentsAny(t *testing.T) {
	get := fakeGetter(map[string][]string{
		"a": {"complete"},
		"b": {"running"},
	})
	snaps, _, timedOut, err := waitForAgents(context.Background(), get,
		[]string{"a", "b"}, true, 0, time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if timedOut {
		t.Error("should not time out when one agent already settled")
	}
	if snaps[0].State != "complete" {
		t.Errorf("snaps[0].State = %q, want complete", snaps[0].State)
	}
	if snaps[1].State != "running" {
		t.Errorf("snaps[1].State = %q, want the unsettled peer's last state", snaps[1].State)
	}
}
