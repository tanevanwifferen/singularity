package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/client"
)

// terminalAgentState reports whether state is one an agent never leaves.
// Mirrors engine.AgentState.String() — the wire protocol carries strings.
func terminalAgentState(state string) bool {
	switch state {
	case "complete", "error", "killed":
		return true
	}
	return false
}

// idListFlag collects --id values: the flag may be repeated, and each value
// may itself be a comma-separated list.
type idListFlag []string

func (l *idListFlag) String() string { return strings.Join(*l, ",") }

func (l *idListFlag) Set(v string) error {
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			*l = append(*l, s)
		}
	}
	return nil
}

// agentGetter fetches one agent snapshot. Abstracted from client.Client so
// the poll loop is unit-testable without a daemon.
type agentGetter func(ctx context.Context, id string) (*api.AgentSnapshotDTO, error)

func agentGetterFor(c *client.Client) agentGetter {
	return func(ctx context.Context, id string) (*api.AgentSnapshotDTO, error) {
		tctx, cancel := withTimeout(ctx)
		defer cancel()
		return c.AgentGet(tctx, id)
	}
}

// waitForAgents polls get for every id until all reach a terminal state, the
// timeout elapses (0 = wait forever), or ctx is cancelled. With anyDone it
// returns as soon as the first agent settles. It returns the last known
// snapshot per id (in ids order), the total wait duration, and whether the
// wait timed out. Already-terminal agents cost one poll and no sleep.
func waitForAgents(ctx context.Context, get agentGetter, ids []string, anyDone bool, timeout, interval time.Duration) ([]api.AgentSnapshotDTO, time.Duration, bool, error) {
	start := time.Now()
	var deadline <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		deadline = t.C
	}
	snaps := make([]api.AgentSnapshotDTO, len(ids))
	settled := make([]bool, len(ids))
	for {
		allSettled := true
		anySettled := false
		for i, id := range ids {
			if settled[i] {
				anySettled = true
				continue
			}
			a, err := get(ctx, id)
			if err != nil {
				return snaps, time.Since(start), false, fmt.Errorf("agent %s: %w", id, err)
			}
			snaps[i] = *a
			if terminalAgentState(a.State) {
				settled[i] = true
				anySettled = true
			} else {
				allSettled = false
			}
		}
		if allSettled || (anyDone && anySettled) {
			return snaps, time.Since(start), false, nil
		}
		select {
		case <-ctx.Done():
			return snaps, time.Since(start), false, ctx.Err()
		case <-deadline:
			return snaps, time.Since(start), true, nil
		case <-time.After(interval):
		}
	}
}

// runAgentsWait blocks until the given agent(s) reach a terminal state.
// Non-streaming counterpart of `agents watch`: quiet, poll-based, --json-safe.
func runAgentsWait(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("agents-wait", flag.ContinueOnError)
	var ids idListFlag
	fs.Var(&ids, "id", "agent ID to wait for (repeatable, or comma-separated)")
	timeout := fs.Int("timeout", 0, "give up after N seconds (0 = wait forever)")
	interval := fs.Int("interval", 2, "poll interval in seconds")
	anyDone := fs.Bool("any", false, "with multiple --id: return as soon as one agent finishes (default: wait for all)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if len(ids) == 0 {
		fmt.Fprintln(os.Stderr, "error: --id is required")
		return 2
	}
	if code := validateWaitFlags(*timeout, *interval); code != 0 {
		return code
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	// A single id reports the bare agent object (agents get shape) plus wait
	// fields; multiple ids use the list envelope shared with wait-all.
	return awaitAndReport(ctx, agentGetterFor(c), ids, *anyDone, *timeout, *interval, len(ids) > 1)
}

// runAgentsWaitAll blocks until every agent that is non-terminal *now* reaches
// a terminal state. Agents spawned after the call do not extend the wait.
func runAgentsWaitAll(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("agents-wait-all", flag.ContinueOnError)
	timeout := fs.Int("timeout", 0, "give up after N seconds (0 = wait forever)")
	interval := fs.Int("interval", 2, "poll interval in seconds")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if code := validateWaitFlags(*timeout, *interval); code != 0 {
		return code
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	agents, err := c.AgentList(tctx)
	cancel()
	if err != nil {
		return die(err)
	}
	var ids []string
	for _, a := range agents {
		if !terminalAgentState(a.State) {
			ids = append(ids, a.ID)
		}
	}
	if len(ids) == 0 {
		if globals.json {
			return printJSON(map[string]any{
				"agents": []api.AgentSnapshotDTO{}, "waited_secs": 0.0, "timed_out": false,
			})
		}
		return renderMarkdown("No active agents to wait for.\n")
	}
	return awaitAndReport(ctx, agentGetterFor(c), ids, false, *timeout, *interval, true)
}

func validateWaitFlags(timeout, interval int) int {
	if interval <= 0 {
		fmt.Fprintln(os.Stderr, "error: --interval must be a positive number of seconds")
		return 2
	}
	if timeout < 0 {
		fmt.Fprintln(os.Stderr, "error: --timeout must be >= 0 (0 = wait forever)")
		return 2
	}
	return 0
}

// awaitAndReport runs the wait and renders the result. Exit code: 0 when every
// waited agent completed successfully; 1 on error/killed outcomes or timeout.
// listShape selects the {"agents":[...]} JSON envelope over the single-agent one.
func awaitAndReport(ctx context.Context, get agentGetter, ids []string, anyDone bool, timeoutSecs, intervalSecs int, listShape bool) int {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	snaps, waited, timedOut, err := waitForAgents(ctx, get, ids, anyDone,
		time.Duration(timeoutSecs)*time.Second, time.Duration(intervalSecs)*time.Second)
	if err != nil {
		return die(err)
	}
	code := 0
	if timedOut {
		code = 1
	}
	completed := 0
	for _, a := range snaps {
		switch {
		case a.State == "complete":
			completed++
		case terminalAgentState(a.State):
			// error/killed is a failure regardless of the wait mode.
			code = 1
		case !anyDone:
			// Still running while we were waiting for all of them: only
			// reachable on timeout. With --any, unsettled peers are expected
			// and must not turn a successful wait into a failure.
			code = 1
		}
	}
	waitedSecs := waited.Round(time.Millisecond).Seconds()
	if globals.json {
		if listShape {
			printJSON(map[string]any{"agents": snaps, "waited_secs": waitedSecs, "timed_out": timedOut})
		} else {
			printJSON(struct {
				api.AgentSnapshotDTO
				WaitedSecs float64 `json:"waited_secs"`
				TimedOut   bool    `json:"timed_out"`
			}{snaps[0], waitedSecs, timedOut})
		}
	} else {
		md := ""
		for _, a := range snaps {
			md += fmt.Sprintf("Agent `%s`: `%s` after %s  \n", a.ID, a.State, fmtWaitDur(waited))
		}
		if listShape {
			md += fmt.Sprintf("\n%d/%d agents completed in %s.\n", completed, len(snaps), fmtWaitDur(waited))
		}
		renderMarkdown(md)
	}
	if timedOut {
		var pending []string
		for _, a := range snaps {
			if !terminalAgentState(a.State) {
				pending = append(pending, a.ID)
			}
		}
		fmt.Fprintf(os.Stderr, "error: timed out after %s waiting for agent(s): %s\n",
			fmtWaitDur(waited), strings.Join(pending, ", "))
	}
	return code
}

// fmtWaitDur renders a wait duration at human precision: sub-10s waits keep
// one decimal, longer waits round to whole seconds.
func fmtWaitDur(d time.Duration) string {
	if d < 10*time.Second {
		return d.Round(100 * time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}
