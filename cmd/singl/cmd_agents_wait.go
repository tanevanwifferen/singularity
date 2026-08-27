package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// idListFlag collects repeatable --id flags; comma-separated values are
// split so both `--id a --id b` and `--id a,b` work.
type idListFlag []string

func (f *idListFlag) String() string { return strings.Join(*f, ",") }

func (f *idListFlag) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			*f = append(*f, part)
		}
	}
	return nil
}

// isTerminalAgentState mirrors engine.AgentState.Terminal() for the string
// form that travels over the wire.
func isTerminalAgentState(state string) bool {
	return state == "complete" || state == "error" || state == "killed"
}

// runAgentsWait blocks until the given agent(s) reach a terminal state
// (complete/error/killed), then prints the final snapshot(s). This is not a
// streaming command: it polls the daemon and prints only the end result, so
// it is safe for unattended `--json` scripting.
//
// Exit codes: 0 = all waited-for agents completed successfully;
// 1 = an agent ended in error/killed, the timeout expired, or a poll failed;
// 2 = usage error.
func runAgentsWait(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("agents-wait", flag.ContinueOnError)
	var ids idListFlag
	fs.Var(&ids, "id", "agent ID (repeatable, or comma-separated)")
	timeout := fs.Int("timeout", 0, "give up after N seconds (0 = wait indefinitely)")
	interval := fs.Int("interval", 2, "poll interval in seconds")
	anyDone := fs.Bool("any", false, "with multiple --id: return as soon as one agent finishes (default: wait for all)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if len(ids) == 0 {
		fmt.Fprintln(os.Stderr, "error: at least one --id is required")
		return 2
	}
	if *interval < 1 {
		*interval = 1
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	poll := func(pctx context.Context, id string) (*api.AgentSnapshotDTO, error) {
		tctx, cancel := withTimeout(pctx)
		defer cancel()
		return c.AgentGet(tctx, id)
	}

	latest, timedOut, err := waitForAgents(ctx, poll, ids, *anyDone,
		time.Duration(*timeout)*time.Second, time.Duration(*interval)*time.Second)
	if err != nil {
		return die(err)
	}
	if timedOut {
		fmt.Fprintf(os.Stderr, "error: timeout after %ds waiting for agent(s)\n", *timeout)
	}

	if globals.json {
		if len(ids) == 1 {
			printJSON(agentToJSON(*latest[ids[0]], false))
		} else {
			out := make([]agentJSON, 0, len(ids))
			for _, id := range ids {
				out = append(out, agentToJSON(*latest[id], false))
			}
			printJSON(map[string]any{"agents": out, "timed_out": timedOut})
		}
	} else {
		md := ""
		for _, id := range ids {
			md += fmtAgent(*latest[id])
		}
		renderMarkdown(md)
	}
	return waitExitCode(latest, ids, timedOut)
}

// waitForAgents polls each id until it reaches a terminal state. With
// anyDone it returns as soon as one agent is terminal; otherwise it waits
// for all of them. Returns the latest snapshot per id and whether the
// timeout expired first (timeout 0 = wait indefinitely).
func waitForAgents(
	ctx context.Context,
	poll func(context.Context, string) (*api.AgentSnapshotDTO, error),
	ids []string,
	anyDone bool,
	timeout, interval time.Duration,
) (map[string]*api.AgentSnapshotDTO, bool, error) {
	latest := make(map[string]*api.AgentSnapshotDTO, len(ids))
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	for {
		done := 0
		for _, id := range ids {
			if s, ok := latest[id]; ok && isTerminalAgentState(s.State) {
				done++
				continue
			}
			s, err := poll(ctx, id)
			if err != nil {
				return nil, false, err
			}
			latest[id] = s
			if isTerminalAgentState(s.State) {
				done++
			}
		}
		if done == len(ids) || (anyDone && done > 0) {
			return latest, false, nil
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return latest, true, nil
		}
		wait := interval
		if !deadline.IsZero() {
			if rem := time.Until(deadline); rem < wait {
				wait = rem
			}
		}
		select {
		case <-ctx.Done():
			return latest, false, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// waitExitCode maps the wait outcome to a process exit code: timeout or any
// terminal state other than "complete" is a failure.
func waitExitCode(latest map[string]*api.AgentSnapshotDTO, ids []string, timedOut bool) int {
	if timedOut {
		return 1
	}
	for _, id := range ids {
		s, ok := latest[id]
		if !ok {
			return 1
		}
		if isTerminalAgentState(s.State) && s.State != "complete" {
			return 1
		}
	}
	return 0
}
