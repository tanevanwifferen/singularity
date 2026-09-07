package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

func cmdQueue(ctx context.Context, verb string, args []string) int {
	switch verb {
	case "add":
		return runQueueAdd(ctx, args)
	case "list":
		return runQueueList(ctx, args)
	case "show":
		return runQueueShow(ctx, args)
	case "graph":
		return runQueueGraph(ctx, args)
	case "wait":
		return runQueueWait(ctx, args)
	case "cancel":
		return runQueueCancel(ctx, args)
	case "retry":
		return runQueueRetry(ctx, args)
	case "answer":
		return runQueueAnswer(ctx, args)
	case "pause":
		return runQueuePause(ctx, args)
	case "resume":
		return runQueueResume(ctx, args)
	case "queues":
		return runQueueQueues(ctx, args)
	case "remove":
		return runQueueRemove(ctx, args)
	default:
		return nounHelp("queue", verb)
	}
}

// pathListFlag collects a repeatable path flag. Unlike idListFlag it does
// not split on commas: a path may legitimately contain one, and a silently
// halved filename is worse than typing the flag twice.
type pathListFlag []string

func (l *pathListFlag) String() string { return strings.Join(*l, string(os.PathListSeparator)) }

func (l *pathListFlag) Set(v string) error {
	if v = strings.TrimSpace(v); v != "" {
		*l = append(*l, v)
	}
	return nil
}

// batchFile is the --file schema: a whole DAG in one document. Task work
// dirs are spelled `workdir` to match the CLI flag, with `work_dir`
// accepted too so a task list pasted out of `queue list --json` still
// parses. Unknown keys are rejected — a typo'd `prompts` would otherwise
// submit a task with an empty prompt.
type batchFile struct {
	// Queue applies to every task that does not name its own.
	Queue string      `json:"queue,omitempty"`
	Tasks []batchTask `json:"tasks"`
}

type batchTask struct {
	// Name is a file-local alias other tasks reference in After; the
	// daemon resolves it to the assigned task ID at submit time.
	Name       string          `json:"name,omitempty"`
	Queue      string          `json:"queue,omitempty"`
	Title      string          `json:"title,omitempty"`
	WorkDir    string          `json:"workdir,omitempty"`
	WorkDirAlt string          `json:"work_dir,omitempty"`
	Prompt     string          `json:"prompt"`
	After      []string        `json:"after,omitempty"`
	Opts       api.TaskOptions `json:"opts,omitempty"`
	Priority   int             `json:"priority,omitempty"`
	MaxRetries int             `json:"max_retries,omitempty"`
	OnFailure  string          `json:"on_failure,omitempty"`
}

// parseBatchFile turns a --file document into the specs to submit. It is a
// pure function so the schema is testable without a daemon; anything it
// cannot decide locally (does an After entry exist? is the DAG acyclic?) is
// left to the manager, which validates the batch as a unit.
func parseBatchFile(data []byte) ([]api.TaskSpec, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f batchFile
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse task file: %w", err)
	}
	if len(f.Tasks) == 0 {
		return nil, errors.New("task file declares no tasks")
	}
	seen := make(map[string]bool, len(f.Tasks))
	specs := make([]api.TaskSpec, 0, len(f.Tasks))
	for i, t := range f.Tasks {
		where := fmt.Sprintf("task %d", i)
		if t.Name != "" {
			where = fmt.Sprintf("task %q", t.Name)
			if seen[t.Name] {
				return nil, fmt.Errorf("duplicate task name %q", t.Name)
			}
			seen[t.Name] = true
		}
		workDir := t.WorkDir
		if workDir == "" {
			workDir = t.WorkDirAlt
		}
		if t.Prompt == "" || workDir == "" {
			return nil, fmt.Errorf("%s: prompt and workdir are required", where)
		}
		queueID := t.Queue
		if queueID == "" {
			queueID = f.Queue
		}
		policy, err := parseFailurePolicy(t.OnFailure)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		specs = append(specs, api.TaskSpec{
			Name:       t.Name,
			QueueID:    queueID,
			Title:      t.Title,
			Prompt:     t.Prompt,
			WorkDir:    workDir,
			After:      t.After,
			Opts:       t.Opts,
			Priority:   t.Priority,
			MaxRetries: t.MaxRetries,
			OnFailure:  policy,
		})
	}
	return specs, nil
}

// parseFailurePolicy validates the on-failure enum. Empty means "leave it
// unset" so the manager applies its own default (block).
func parseFailurePolicy(s string) (service.FailurePolicy, error) {
	if s == "" {
		return "", nil
	}
	p := service.FailurePolicy(s)
	if !p.Valid() {
		return "", fmt.Errorf("invalid on-failure %q: want block, continue or abort-queue", s)
	}
	return p, nil
}

func runQueueAdd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("queue-add", flag.ContinueOnError)
	file := fs.String("file", "", "submit a whole DAG from a JSON file (mutually exclusive with every per-task flag except --queue)")
	workdir := fs.String("workdir", "", "working directory for the task (required without --file)")
	prompt := fs.String("prompt", "", "task prompt (required without --file)")
	title := fs.String("title", "", "short label, also used as the agent's summary")
	queueID := fs.String("queue", "", "queue to add to (default: a queue created for this submission)")
	var after idListFlag
	fs.Var(&after, "after", "task ID this task depends on (repeatable, or comma-separated)")
	model := fs.String("model", "", "model override")
	effort := fs.String("effort", "", "effort level: low|medium|high")
	timeout := fs.Int("timeout", 0, "agent timeout in seconds (0 = daemon default)")
	backend := fs.String("backend", "", "agent backend: claude or pi (default: daemon default)")
	useWorktree := fs.Bool("use-worktree", false, "run the task in its own git worktree (exempts it from the one-agent-per-directory rule)")
	var contextFiles pathListFlag
	fs.Var(&contextFiles, "context-file", "file to inject into the agent's context (repeatable)")
	var allowedTools idListFlag
	fs.Var(&allowedTools, "allowed-tools", "restrict the agent to these tools (repeatable, or comma-separated)")
	maxRetries := fs.Int("max-retries", 0, "retry the task N times before failing it")
	onFailure := fs.String("on-failure", "", "what happens to dependents when this task fails: block|continue|abort-queue (default block)")
	priority := fs.Int("priority", 0, "dispatch order among ready tasks; higher goes first")
	if code, done := parseArgs(fs, args); done {
		return code
	}

	var specs []api.TaskSpec
	if *file != "" {
		// Every per-task flag is ignored in file mode: the specs come from
		// the document. Accepting them silently was worse than rejecting
		// them — an operator who passed --use-worktree believed their
		// tasks were isolated (and exempt from the one-agent-per-directory
		// serialisation) when they were not. --queue is the one exception;
		// it deliberately overrides the document.
		if bad := typedFlagsOtherThan(fs, "file", "queue"); len(bad) > 0 {
			fmt.Fprintf(os.Stderr, "error: --file cannot be combined with --%s (set these per task in the document; only --queue overrides it)\n",
				strings.Join(bad, ", --"))
			return 2
		}
		data, err := os.ReadFile(*file)
		if err != nil {
			return die(err)
		}
		parsed, err := parseBatchFile(data)
		if err != nil {
			return die(err)
		}
		specs = parsed
		// A --queue on the command line overrides the document's own
		// top-level queue, so the same file can be resubmitted elsewhere.
		if *queueID != "" {
			for i := range specs {
				specs[i].QueueID = *queueID
			}
		}
	} else {
		if *workdir == "" || *prompt == "" {
			fmt.Fprintln(os.Stderr, "error: --workdir and --prompt are required (or use --file)")
			return 2
		}
		policy, err := parseFailurePolicy(*onFailure)
		if err != nil {
			return die(err)
		}
		specs = []api.TaskSpec{{
			QueueID: *queueID,
			Title:   *title,
			Prompt:  *prompt,
			WorkDir: *workdir,
			After:   after,
			Opts: api.TaskOptions{
				Model:        *model,
				Effort:       *effort,
				Backend:      *backend,
				TimeoutSecs:  *timeout,
				UseWorktree:  *useWorktree,
				ContextFiles: contextFiles,
				AllowedTools: allowedTools,
			},
			Priority:   *priority,
			MaxRetries: *maxRetries,
			OnFailure:  policy,
		}}
	}

	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	tasks, err := c.QueueAdd(tctx, specs)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]any{"tasks": tasks, "queue_id": submittedQueueID(tasks)})
	}
	md := fmt.Sprintf("## Queued %d task(s) in `%s`\n\n", len(tasks), submittedQueueID(tasks))
	for _, t := range tasks {
		md += fmt.Sprintf("- `%s` %s — `%s`\n", t.ID, taskLabel(t), t.State)
	}
	md += "\nWait for it: `singl queue wait --queue " + submittedQueueID(tasks) + "`\n"
	return renderMarkdown(md)
}

// typedFlagsOtherThan returns the names of the flags the user actually
// passed, minus the allowed ones. flag.Visit is what makes this possible:
// it reports only flags that were set, so an explicit --priority 0 is
// distinguishable from an unset one.
func typedFlagsOtherThan(fs *flag.FlagSet, allowed ...string) []string {
	ok := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		ok[name] = true
	}
	var out []string
	fs.Visit(func(f *flag.Flag) {
		if !ok[f.Name] {
			out = append(out, f.Name)
		}
	})
	sort.Strings(out)
	return out
}

// submittedQueueID reports the queue the batch landed in. The daemon mints
// one when the specs named none, so it is only knowable from the response.
func submittedQueueID(tasks []api.Task) string {
	if len(tasks) == 0 {
		return ""
	}
	return tasks[0].QueueID
}

func runQueueCancel(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("queue-cancel", flag.ContinueOnError)
	id := fs.String("id", "", "task ID to cancel")
	queueID := fs.String("queue", "", "cancel every unfinished task in this queue")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if (*id == "") == (*queueID == "") {
		fmt.Fprintln(os.Stderr, "error: exactly one of --id or --queue is required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if *id != "" {
		if err := c.QueueCancel(tctx, *id); err != nil {
			return die(err)
		}
		return reportQueueAction("task", *id, "cancelled")
	}
	if err := c.QueueCancelQueue(tctx, *queueID); err != nil {
		return die(err)
	}
	return reportQueueAction("queue", *queueID, "cancelled")
}

func runQueueRetry(ctx context.Context, args []string) int {
	return runQueueTaskAction(ctx, args, "queue-retry", "retried",
		func(tctx context.Context, c *client.Client, id string) error { return c.QueueRetry(tctx, id) })
}

// runQueueAnswer is wired but inert in this build: the agent engine has no
// way to report that an agent stopped to ask something, so no task ever
// reaches the state Manager.Answer requires and every call returns CONFLICT.
// Kept rather than removed because the queue side is complete — only the
// engine's report is missing — but nothing user-facing may advertise it as
// live. See "Known gaps" in prime.md.
func runQueueAnswer(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("queue-answer", flag.ContinueOnError)
	id := fs.String("id", "", "task ID to answer (required)")
	message := fs.String("message", "", "the answer to hand the agent (required)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if *id == "" || *message == "" {
		fmt.Fprintln(os.Stderr, "error: --id and --message are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.QueueAnswer(tctx, *id, *message); err != nil {
		return die(err)
	}
	return reportQueueAction("task", *id, "answered")
}

func runQueuePause(ctx context.Context, args []string) int {
	return runQueueIDAction(ctx, args, "queue-pause", "paused",
		func(tctx context.Context, c *client.Client, id string) error { return c.QueuePause(tctx, id) })
}

func runQueueResume(ctx context.Context, args []string) int {
	return runQueueIDAction(ctx, args, "queue-resume", "resumed",
		func(tctx context.Context, c *client.Client, id string) error { return c.QueueResume(tctx, id) })
}

// runQueueRemove is spelled out rather than routed through
// runQueueIDAction because its one interesting failure needs a way out:
// the daemon refuses to forget a queue that still has a live agent in it,
// and "invalid queue request" alone does not tell the operator what to do.
func runQueueRemove(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("queue-remove", flag.ContinueOnError)
	queueID := fs.String("queue", "", "queue ID (required)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if *queueID == "" {
		fmt.Fprintln(os.Stderr, "error: --queue is required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.QueueRemove(tctx, *queueID); err != nil {
		if errors.Is(err, service.ErrInvalidRequest) {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			fmt.Fprintf(os.Stderr, "hint: cancel it (`singl queue cancel --queue %s`) or wait for it (`singl queue wait --queue %s`) first\n",
				*queueID, *queueID)
			return 1
		}
		return die(err)
	}
	return reportQueueAction("queue", *queueID, "removed")
}

// queueOp is one steering call on the daemon. The verbs below differ only
// in which flag names the target and which method they invoke.
type queueOp func(ctx context.Context, c *client.Client, id string) error

// runQueueTaskAction is the shared body of the single-flag --id verbs.
func runQueueTaskAction(ctx context.Context, args []string, name, done string, op queueOp) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	id := fs.String("id", "", "task ID (required)")
	if code, doneParsing := parseArgs(fs, args); doneParsing {
		return code
	}
	if *id == "" {
		fmt.Fprintln(os.Stderr, "error: --id is required")
		return 2
	}
	return applyQueueOp(ctx, op, *id, "task", done)
}

// runQueueIDAction is runQueueTaskAction for the whole-queue verbs.
func runQueueIDAction(ctx context.Context, args []string, name, done string, op queueOp) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	id := fs.String("queue", "", "queue ID (required)")
	if code, doneParsing := parseArgs(fs, args); doneParsing {
		return code
	}
	if *id == "" {
		fmt.Fprintln(os.Stderr, "error: --queue is required")
		return 2
	}
	return applyQueueOp(ctx, op, *id, "queue", done)
}

func applyQueueOp(ctx context.Context, op queueOp, id, noun, done string) int {
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := op(tctx, c, id); err != nil {
		return die(err)
	}
	return reportQueueAction(noun, id, done)
}

// reportQueueAction renders the one-line confirmation the steering verbs
// share. noun is "task" or "queue"; it also names the JSON id field.
func reportQueueAction(noun, id, done string) int {
	if globals.json {
		return printJSON(map[string]string{"status": done, noun + "_id": id})
	}
	return renderMarkdown(fmt.Sprintf("%s `%s` %s.\n", strings.ToUpper(noun[:1])+noun[1:], id, done))
}
