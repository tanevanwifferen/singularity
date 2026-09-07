package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// flowWaitOutcome is what a poll of the flow concluded. The four terminal
// values are spelled exactly like the flow states they come from, so the
// JSON `outcome` field and the `state` inside the flow it carries never
// disagree; `timeout` is the only outcome the daemon does not name.
type flowWaitOutcome string

const (
	// flowWaitPending means the flow can still make progress on its own —
	// pending (round 1 not submitted yet) or running.
	flowWaitPending flowWaitOutcome = "pending"
	// flowWaitAccepted means a round's verdict was accept: the work in the
	// flow's work dir passed review. The only outcome that exits 0.
	flowWaitAccepted flowWaitOutcome = "accepted"
	// flowWaitRejected means the round cap was reached with the reviewer
	// still rejecting. The work is on disk and unaccepted.
	flowWaitRejected flowWaitOutcome = "rejected"
	// flowWaitErrored means the flow machinery gave up.
	flowWaitErrored flowWaitOutcome = "errored"
	// flowWaitCancelled means the flow, or one of its tasks, was cancelled.
	flowWaitCancelled flowWaitOutcome = "cancelled"
	// flowWaitTimedOut means --timeout elapsed with the flow still live.
	flowWaitTimedOut flowWaitOutcome = "timeout"
)

// exitCode is the process exit status for the outcome, and the entire point
// of the verb: a caller scripts `flow wait && land the work`, so anything
// short of an accept has to be an error. `rejected` is a legitimate,
// designed outcome rather than a malfunction — and it still exits 1, because
// the work must not be landed unreviewed on the strength of it.
func (o flowWaitOutcome) exitCode() int {
	switch o {
	case flowWaitRejected, flowWaitErrored, flowWaitCancelled, flowWaitTimedOut:
		return 1
	}
	return 0
}

// decideFlowWait maps a flow snapshot onto the wait outcome. Pure so the
// exit-code contract is testable without a daemon.
//
// A terminal state beats the timeout: a flow that reached accepted on the
// very tick --timeout fired did reach accepted, and reporting a timeout
// instead would fail a run that succeeded.
func decideFlowWait(f api.Flow, timedOut bool) flowWaitOutcome {
	switch f.State {
	case service.FlowAccepted:
		return flowWaitAccepted
	case service.FlowRejected:
		return flowWaitRejected
	case service.FlowErrored:
		return flowWaitErrored
	case service.FlowCancelled:
		return flowWaitCancelled
	}
	if timedOut {
		return flowWaitTimedOut
	}
	return flowWaitPending
}

// flowWaitUsage is the --help preamble. Hoisted out of fs.Usage so the
// exit-code contract it advertises can be asserted directly, the reason
// queueWaitUsage is: help text that drifts from the codes teaches an
// orchestrating agent to branch on the wrong thing.
const flowWaitUsage = `Usage: singl flow wait --id <flow-id> [--timeout SECS] [--interval SECS]

Blocks until the flow reaches a terminal state, polling the daemon (no
streaming). The exit code is the verdict.

Exit codes:
  0  the flow was accepted — the work in its work dir passed review
  1  the flow was rejected (round cap reached), errored or was cancelled
  1  --timeout expired with the flow still running

Flags:
`

func runFlowWait(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("flow-wait", flag.ContinueOnError)
	id := fs.String("id", "", "flow ID to wait for (required)")
	timeout := fs.Int("timeout", 0, "give up after N seconds, exit 1 (0 = wait forever)")
	interval := fs.Int("interval", 5, "poll interval in seconds")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, flowWaitUsage)
		fs.PrintDefaults()
	}
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if *id == "" {
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
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	start := time.Now()
	var deadline <-chan time.Time
	if *timeout > 0 {
		t := time.NewTimer(time.Duration(*timeout) * time.Second)
		defer t.Stop()
		deadline = t.C
	}
	timedOut := false
	for {
		f, err := pollFlow(ctx, c, *id)
		if err != nil {
			return die(err)
		}
		outcome := decideFlowWait(*f, timedOut)
		if outcome != flowWaitPending {
			return reportFlowWait(*f, outcome, time.Since(start))
		}
		select {
		case <-ctx.Done():
			return die(ctx.Err())
		case <-deadline:
			// Re-poll once with the flag set so the report carries the
			// flow as of the timeout rather than the previous tick's, and
			// so a flow that settled in between is reported as settled.
			timedOut = true
		case <-time.After(time.Duration(*interval) * time.Second):
		}
	}
}

// pollFlow fetches the flow to decide on. One call per tick: the whole flow
// including every round comes back in it, so there is nothing to follow up.
func pollFlow(ctx context.Context, c *client.Client, flowID string) (*api.Flow, error) {
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	return c.FlowGet(tctx, flowID)
}

// reportFlowWait renders the settled wait. Everything worth reporting is
// already in the flow the poll returned — the rounds, the last verdict and
// its findings — so unlike the queue's report this needs no second fetch.
func reportFlowWait(f api.Flow, outcome flowWaitOutcome, waited time.Duration) int {
	if globals.json {
		printJSON(map[string]any{
			"flow_id":     f.ID,
			"outcome":     string(outcome),
			"flow":        f,
			"rounds":      len(f.Rounds),
			"max_rounds":  f.MaxRounds,
			"waited_secs": waited.Round(time.Millisecond).Seconds(),
			"timed_out":   outcome == flowWaitTimedOut,
		})
	} else {
		renderMarkdown(fmtFlowWait(f, outcome, waited))
	}
	switch outcome {
	case flowWaitTimedOut:
		fmt.Fprintf(os.Stderr, "error: timed out after %s waiting for flow %s (state %s, round %s)\n",
			fmtWaitDur(waited), f.ID, f.State, flowRounds(f))
	case flowWaitRejected:
		fmt.Fprintf(os.Stderr, "error: flow %s was rejected after %s round(s); read the findings and decide by hand\n",
			f.ID, flowRounds(f))
	case flowWaitErrored:
		fmt.Fprintf(os.Stderr, "error: flow %s errored: %s\n", f.ID, flowWaitReason(f))
	case flowWaitCancelled:
		fmt.Fprintf(os.Stderr, "error: flow %s was cancelled: %s\n", f.ID, flowWaitReason(f))
	}
	return outcome.exitCode()
}

// flowWaitReason is the flow's own explanation, or a stand-in when it has
// none — a flow cancelled by the operator records no error.
func flowWaitReason(f api.Flow) string {
	if f.Error != "" {
		return f.Error
	}
	return "no reason recorded"
}

func fmtFlowWait(f api.Flow, outcome flowWaitOutcome, waited time.Duration) string {
	md := fmt.Sprintf("## Flow `%s`: %s after %s\n\n", f.ID, outcome, fmtWaitDur(waited))
	md += fmt.Sprintf("State: `%s`  \nRounds: %s  \nWorkdir: `%s`  \n", f.State, flowRounds(f), f.WorkDir)
	if f.Error != "" {
		md += fmt.Sprintf("Error: %s  \n", f.Error)
	}
	switch outcome {
	case flowWaitAccepted:
		md += "\nThe work in the flow's work dir passed review. Diff it, commit it, land it.\n"
	case flowWaitTimedOut:
		md += fmt.Sprintf("\nStill running. Keep waiting (`singl flow wait --id %s`) or cancel it (`singl flow cancel --id %s`).\n", f.ID, f.ID)
	}
	if last := lastFlowRound(f); last != nil {
		md += fmt.Sprintf("\n### Round %d — `%s`\n", last.N, last.State)
		md += fmtFlowVerdict(last.Verdict)
	}
	return md
}

// lastFlowRound returns the most recent round, or nil before round 1 is
// submitted. Nil round pointers are skipped rather than trusted: the flow
// arrived over the wire, so its slice is whatever the peer sent.
func lastFlowRound(f api.Flow) *api.FlowRound {
	for i := len(f.Rounds) - 1; i >= 0; i-- {
		if f.Rounds[i] != nil {
			return f.Rounds[i]
		}
	}
	return nil
}
