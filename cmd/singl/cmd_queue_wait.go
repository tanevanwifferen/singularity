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

// queueWaitOutcome is what a poll of the queue's tallies concluded.
type queueWaitOutcome string

const (
	// queueWaitPending means the queue can still make progress on its own.
	queueWaitPending queueWaitOutcome = "pending"
	// queueWaitAnswerNeeded means a task stopped to ask the operator a
	// question. That is actionable, not a failure, so the wait returns
	// early and successfully — the caller is expected to answer it.
	queueWaitAnswerNeeded queueWaitOutcome = "waiting_human"
	// queueWaitDone means the queue drained and every task is done.
	queueWaitDone queueWaitOutcome = "done"
	// queueWaitFailed means the queue drained but something failed, was
	// cancelled, or was skipped because a dependency failed.
	queueWaitFailed queueWaitOutcome = "failed"
	// queueWaitTimedOut means --timeout elapsed first.
	queueWaitTimedOut queueWaitOutcome = "timeout"
)

// exitCode is the process exit status for the outcome. Only an actual
// failure or a timeout is an error; a queue waiting for an answer exits 0
// because the operator can act on it.
func (o queueWaitOutcome) exitCode() int {
	switch o {
	case queueWaitFailed, queueWaitTimedOut:
		return 1
	}
	return 0
}

// decideQueueWait maps a queue's tallies onto the wait outcome. Pure so the
// exit-code contract is testable without a daemon.
func decideQueueWait(info api.QueueInfo, timedOut bool) queueWaitOutcome {
	// A waiting_human task is reported even on the timeout tick: the
	// operator needs to know a question is outstanding, and the answer is
	// what unblocks the queue either way.
	if info.Waiting > 0 {
		return queueWaitAnswerNeeded
	}
	if timedOut {
		return queueWaitTimedOut
	}
	if !info.Drained() {
		return queueWaitPending
	}
	if info.Failed+info.Cancelled+info.Skipped > 0 {
		return queueWaitFailed
	}
	return queueWaitDone
}

// aggregateQueueInfo sums every queue's tallies into one synthetic Info, so
// a bare `queue wait` (no --queue) waits for the whole daemon with the same
// decision logic. The ID is left empty: the aggregate is not a queue.
func aggregateQueueInfo(infos []api.QueueInfo) api.QueueInfo {
	var out api.QueueInfo
	for _, i := range infos {
		out.Total += i.Total
		out.Blocked += i.Blocked
		out.Ready += i.Ready
		out.Running += i.Running
		out.Waiting += i.Waiting
		out.Done += i.Done
		out.Failed += i.Failed
		out.Cancelled += i.Cancelled
		out.Skipped += i.Skipped
		out.Paused = out.Paused || i.Paused
	}
	return out
}

func runQueueWait(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("queue-wait", flag.ContinueOnError)
	queueID := fs.String("queue", "", "queue to wait for (default: every queue)")
	timeout := fs.Int("timeout", 0, "give up after N seconds, exit 1 (0 = wait forever)")
	interval := fs.Int("interval", 5, "poll interval in seconds")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Usage: singl queue wait [--queue <id>] [--timeout SECS] [--interval SECS]

Blocks until the queue drains, polling the daemon (no streaming).

Exit codes:
  0  the queue drained and every task is done
  0  a task is waiting_human — returns early and prints the task ID and its
     question, because that is actionable by the operator, not an error
  1  the queue drained but at least one task failed or was cancelled
  1  --timeout expired

Flags:
`)
		fs.PrintDefaults()
	}
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
		info, err := pollQueueInfo(ctx, c, *queueID)
		if err != nil {
			return die(err)
		}
		outcome := decideQueueWait(*info, timedOut)
		if outcome != queueWaitPending {
			return reportQueueWait(ctx, c, *queueID, *info, outcome, time.Since(start))
		}
		select {
		case <-ctx.Done():
			return die(ctx.Err())
		case <-deadline:
			// Re-poll once with the flag set so the report carries the
			// tallies as of the timeout rather than the previous tick's.
			timedOut = true
		case <-time.After(time.Duration(*interval) * time.Second):
		}
	}
}

// pollQueueInfo fetches the tallies to decide on: one queue's, or the sum
// across every queue when no --queue was given.
func pollQueueInfo(ctx context.Context, c *client.Client, queueID string) (*api.QueueInfo, error) {
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if queueID != "" {
		return c.QueueInfo(tctx, queueID)
	}
	infos, err := c.QueueQueues(tctx)
	if err != nil {
		return nil, err
	}
	agg := aggregateQueueInfo(infos)
	return &agg, nil
}

// reportQueueWait renders the settled wait. The notable tasks (the ones
// asking a question, or the ones that failed) are fetched only now, so the
// poll loop itself stays a single cheap call per tick.
func reportQueueWait(ctx context.Context, c *client.Client, queueID string, info api.QueueInfo, outcome queueWaitOutcome, waited time.Duration) int {
	var notable []api.Task
	switch outcome {
	case queueWaitAnswerNeeded:
		notable = fetchQueueTasks(ctx, c, queueID, service.TaskWaitingHuman)
	case queueWaitFailed:
		notable = fetchQueueTasks(ctx, c, queueID, service.TaskFailed, service.TaskCancelled, service.TaskSkipped)
	}
	waitedSecs := waited.Round(time.Millisecond).Seconds()
	if globals.json {
		printJSON(map[string]any{
			"queue_id":    queueID,
			"outcome":     string(outcome),
			"info":        info,
			"tasks":       notable,
			"waited_secs": waitedSecs,
			"timed_out":   outcome == queueWaitTimedOut,
		})
	} else {
		renderMarkdown(fmtQueueWait(queueID, info, outcome, notable, waited))
	}
	switch outcome {
	case queueWaitTimedOut:
		fmt.Fprintf(os.Stderr, "error: timed out after %s waiting for the queue to drain (%d blocked, %d ready, %d running)\n",
			fmtWaitDur(waited), info.Blocked, info.Ready, info.Running)
	case queueWaitFailed:
		fmt.Fprintf(os.Stderr, "error: queue drained with %d failed, %d cancelled, %d skipped task(s)\n",
			info.Failed, info.Cancelled, info.Skipped)
	}
	return outcome.exitCode()
}

// fetchQueueTasks lists the tasks in the given states, best-effort: the
// wait's own verdict is already decided, so a listing error must not turn a
// successful drain into a failure. It only costs the detail lines.
func fetchQueueTasks(ctx context.Context, c *client.Client, queueID string, states ...api.TaskState) []api.Task {
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	tasks, err := c.QueueList(tctx, queueID, states)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not list tasks: %v\n", err)
		return nil
	}
	return tasks
}

func fmtQueueWait(queueID string, info api.QueueInfo, outcome queueWaitOutcome, notable []api.Task, waited time.Duration) string {
	label := queueID
	if label == "" {
		label = "all queues"
	}
	md := fmt.Sprintf("## Queue `%s`: %s after %s\n\n", label, outcome, fmtWaitDur(waited))
	md += fmt.Sprintf("%d/%d done — %d failed, %d cancelled, %d skipped, %d waiting, %d running, %d ready, %d blocked  \n",
		info.Done, info.Total, info.Failed, info.Cancelled, info.Skipped,
		info.Waiting, info.Running, info.Ready, info.Blocked)
	switch outcome {
	case queueWaitAnswerNeeded:
		md += "\nA task is waiting for you. Answer it to let the queue continue:\n\n"
		for _, t := range notable {
			md += fmt.Sprintf("- `%s` %s\n  question: %s\n  `singl queue answer --id %s --message \"...\"`\n",
				t.ID, taskLabel(t), t.Question, t.ID)
		}
	case queueWaitFailed:
		md += "\nDid not finish:\n\n"
		for _, t := range notable {
			md += fmt.Sprintf("- `%s` [%s] %s", t.ID, t.State, taskLabel(t))
			if t.Error != "" {
				md += " — " + t.Error
			}
			md += "\n"
		}
	}
	return md
}
