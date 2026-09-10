package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/flow"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// The `flow` noun: adversarial review flows, driven daemon-side through
// repeated implement → review → fix rounds until a reviewer accepts the work
// or the round cap is hit. Split the way `queue` is — dispatch and the
// mutating verbs here, the renderers in cmd_flow_view.go, the poll loop in
// cmd_flow_wait.go — because it is deliberately the same shape for a new
// noun and the seams are what makes each half testable on its own.
func cmdFlow(ctx context.Context, verb string, args []string) int {
	switch verb {
	case "start":
		return runFlowStart(ctx, args)
	case "continue":
		return runFlowContinue(ctx, args)
	case "list":
		return runFlowList(ctx, args)
	case "show":
		return runFlowShow(ctx, args)
	case "tree":
		return runFlowTree(ctx, args)
	case "wait":
		return runFlowWait(ctx, args)
	case "cancel":
		return runFlowCancel(ctx, args)
	case "remove":
		return runFlowRemove(ctx, args)
	default:
		return nounHelp("flow", verb)
	}
}

func runFlowStart(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("flow-start", flag.ContinueOnError)
	workdir := fs.String("workdir", "", "working directory every round runs in (required)")
	prompt := fs.String("prompt", "", "the implementer's goal, repeated verbatim to every fixer (required unless --jira is given)")
	jiraKey := fs.String("jira", "", "Jira issue key (e.g. PROJ-123) to build the goal from instead of --prompt")
	reviewPrompt := fs.String("review-prompt", "", "extra instructions for the reviewer")
	maxRounds := fs.Int("max-rounds", 0, "give up after N rejected rounds, 1..20 (0 = daemon default of 3)")
	title := fs.String("title", "", "short label for the flow")
	model := fs.String("model", "", "model override for every step")
	effort := fs.String("effort", "", "effort level: low|medium|high")
	// Shared with `queue add` and `agents spawn` on purpose. A flow's tasks
	// have to route exactly as the same prompt would from either of those,
	// and the precedence lives in one helper so the TaskOptions.RouteEnabled
	// bug — an effort pinned without a word about routing being routed
	// anyway — cannot be reintroduced at a third call site.
	smartRoute := smartRouteFlags(fs)
	timeout := fs.Int("timeout", 0, "agent timeout in seconds (0 = daemon default)")
	backend := fs.String("backend", "", "agent backend: claude, pi or herdr (default: daemon default)")
	var contextFiles pathListFlag
	fs.Var(&contextFiles, "context-file", "file to inject into every step's context (repeatable)")
	var allowedTools idListFlag
	fs.Var(&allowedTools, "allowed-tools", "restrict every step's agent to these tools (repeatable, or comma-separated)")
	maxRetries := fs.Int("max-retries", 0, "unsupported: the flow wire contract carries no per-task retry count")
	reviewerModel := fs.String("reviewer-model", "", "model for the review step only (default: sonnet)")
	reviewerEffort := fs.String("reviewer-effort", "", "effort for the review step only (default: --effort)")
	planning := fs.Bool("planning", false, "run a planning phase before round 1, refining the goal before the implementer starts")
	plannerModel := fs.String("planner-model", "", "model for the planning step only (default: --model)")
	plannerEffort := fs.String("planner-effort", "", "effort for the planning step only (default: --effort)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if *workdir == "" {
		fmt.Fprintln(os.Stderr, "error: --workdir is required")
		return 2
	}
	if *prompt == "" && *jiraKey == "" {
		fmt.Fprintln(os.Stderr, "error: --prompt or --jira is required")
		return 2
	}
	// Rejected rather than ignored, the rule `queue add --file` established:
	// an operator who passed --max-retries believed a flaky step would be
	// retried. It would not be. api.FlowStartRequest has no field for it and
	// queue.TaskOptions has none either, so there is nowhere on the wire for
	// the number to go — a flow's liveness bound is --timeout and its round
	// bound is --max-rounds. Kept registered so `flow start --max-retries 2`
	// says that instead of "flag provided but not defined".
	if flagTyped(fs, "max-retries") {
		fmt.Fprintf(os.Stderr, "error: --max-retries (%d) is not supported by flows: no per-task retry count reaches the daemon\n", *maxRetries)
		fmt.Fprintln(os.Stderr, "hint: bound a step with --timeout and the flow with --max-rounds")
		return 2
	}

	base := api.TaskOptions{
		Model:        *model,
		Effort:       *effort,
		Backend:      *backend,
		TimeoutSecs:  *timeout,
		ContextFiles: contextFiles,
		AllowedTools: allowedTools,
	}
	work, review := composeFlowOpts(base, *reviewerModel, *reviewerEffort, smartRoute)
	plan := planOptsFromFlags(base, *plannerModel, *plannerEffort, smartRoute)

	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()

	issueTitle, goal, err := resolveFlowGoal(tctx, c.JiraGetIssue, *jiraKey, *prompt)
	if err != nil {
		return die(err)
	}
	flowTitle := *title
	if flowTitle == "" {
		flowTitle = issueTitle
	}

	f, err := c.FlowStart(tctx, api.FlowStartRequest{
		Title:          flowTitle,
		IssueKey:       *jiraKey,
		Goal:           goal,
		ReviewGoal:     *reviewPrompt,
		WorkDir:        *workdir,
		MaxRounds:      *maxRounds,
		Opts:           work,
		ReviewOpts:     review,
		EnablePlanning: *planning,
		PlanOpts:       plan,
	})
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(f)
	}
	md := fmt.Sprintf("## Flow `%s` started\n\n", f.ID)
	if f.Title != "" {
		md += fmt.Sprintf("Title: %s  \n", f.Title)
	}
	if f.IssueKey != "" {
		md += fmt.Sprintf("Jira: %s  \n", f.IssueKey)
	}
	md += fmt.Sprintf("State: `%s`  \nQueue: `%s`  \nWorkdir: `%s`  \nMax rounds: %d  \n",
		f.State, f.QueueID, f.WorkDir, f.MaxRounds)
	if f.EnablePlanning {
		md += "Planning: enabled  \n"
	}
	md += fmt.Sprintf("\nWait for it: `singl flow wait --id %s`  \n", f.ID)
	md += fmt.Sprintf("Watch its tasks: `singl queue list --queue %s`\n", f.QueueID)
	return renderMarkdown(md)
}

// jiraIssueFetcher matches (*client.Client).JiraGetIssue's shape, so
// resolveFlowGoal is testable against a fake without a live daemon.
type jiraIssueFetcher func(ctx context.Context, key string) (*api.Issue, error)

// resolveFlowGoal picks `flow start`'s title and goal: --prompt verbatim
// when no --jira is given, or a Jira issue's summary and description via
// flow.GoalFromIssue otherwise. An --prompt given alongside --jira is not
// discarded — it is appended after the issue's own text as extra
// instructions, the way --review-prompt narrows a reviewer without
// replacing the rest of its brief.
func resolveFlowGoal(ctx context.Context, fetch jiraIssueFetcher, jiraKey, explicitPrompt string) (title, goal string, err error) {
	if jiraKey == "" {
		return "", explicitPrompt, nil
	}
	issue, err := fetch(ctx, jiraKey)
	if err != nil {
		return "", "", fmt.Errorf("fetching jira issue %s: %w", jiraKey, err)
	}
	title, goal = flow.GoalFromIssue(issue)
	if explicitPrompt != "" {
		goal = strings.TrimSpace(goal) + "\n\n## Additional instructions\n\n" + explicitPrompt
	}
	return title, goal, nil
}

// runFlowContinue gives a flow that finished unaccepted more rounds against
// the same goal. It has its own flag set rather than sharing flowIDArg's
// because --rounds rides along with --id; nothing else is offered, since
// nothing else is re-specifiable.
//
// --rounds is passed through unvalidated, the way --max-rounds is: the
// daemon knows the flow's current cap and answers a number that will not fit
// by naming the largest one that would, which no client-side range check
// could do.
func runFlowContinue(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("flow-continue", flag.ContinueOnError)
	id := fs.String("id", "", "flow ID (required)")
	rounds := fs.Int("rounds", 0, "how many more rounds to allow, on top of the flow's cap (0 = daemon default of 3)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if *id == "" {
		fmt.Fprintln(os.Stderr, "error: --id is required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	f, err := c.FlowContinue(tctx, *id, *rounds)
	if err != nil {
		// The refusal that needs a way out, as with `flow remove`: a
		// conflict here is either a flow whose work already passed review
		// or one that has not finished, and "conflict" alone says neither.
		if errors.Is(err, service.ErrConflict) {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			fmt.Fprintf(os.Stderr, "hint: only a rejected, errored or cancelled flow can be continued; "+
				"check it (`singl flow show --id %s`), and cancel it first (`singl flow cancel --id %s`) if it is still live\n", *id, *id)
			return 1
		}
		return die(err)
	}
	if globals.json {
		// A map rather than the bare flow, for the reason `flow wait`
		// reports one: next_round is derived, and it is the number the
		// operator just authorised work at.
		return printJSON(map[string]any{
			"flow":       f,
			"flow_id":    f.ID,
			"max_rounds": f.MaxRounds,
			"next_round": nextFlowRound(*f),
		})
	}
	return renderMarkdown(fmtFlowContinued(*f))
}

// fmtFlowContinued renders what a continue actually bought: the raised cap
// and the round the flow resumes at. Both are the point of the verb — an
// operator who asked for 2 more rounds needs to see the flow is now capped
// at 5 and that round 4 is next, not merely that the call succeeded.
func fmtFlowContinued(f api.Flow) string {
	md := fmt.Sprintf("## Flow `%s` continued\n\n", f.ID)
	if f.Title != "" {
		md += fmt.Sprintf("Title: %s  \n", f.Title)
	}
	md += fmt.Sprintf("State: `%s`  \nMax rounds: %d  \nResumes at round %d  \nWorkdir: `%s`  \n",
		f.State, f.MaxRounds, nextFlowRound(f), f.WorkDir)
	md += fmt.Sprintf("\nWait for it: `singl flow wait --id %s`  \n", f.ID)
	md += fmt.Sprintf("Watch its tasks: `singl queue list --queue %s`\n", f.QueueID)
	return md
}

// nextFlowRound is the round a continued flow will open next: rounds are
// numbered from 1 and every round the flow ran is still on the record, so
// the next one is one past what is there. Computed the same way the daemon
// numbers rounds, which is what makes the two agree.
func nextFlowRound(f api.Flow) int {
	return len(f.Rounds) + 1
}

// composeFlowOpts builds the work and reviewer option blocks from the shared
// flags plus the --reviewer-* overrides.
//
// The reviewer's block starts as a copy of the work block, so a reviewer
// inherits the backend, timeout, context files and tool restriction it was
// not asked to differ on; --reviewer-model and --reviewer-effort override
// only what they name. It is composed here rather than left empty for the
// daemon to default because flow.Start only substitutes Opts for an
// *entirely* zero ReviewOpts, and a resolved SmartRoute is never zero.
//
// The reviewer's model defaults to "sonnet" rather than inheriting --model:
// code review always runs on sonnet unless --reviewer-model explicitly names
// something else, regardless of what model the implementer used.
//
// resolve is smartRouteFlags' resolver, called once per block with that
// block's effective model and effort: the reviewer routes on its own pins,
// so `--reviewer-model opus` leaves the classifier deciding the reviewer's
// effort exactly as `--model opus` does the implementer's.
func composeFlowOpts(base api.TaskOptions, reviewerModel, reviewerEffort string, resolve func(model, effort string) bool) (work, review api.TaskOptions) {
	work = base
	workRoute := resolve(base.Model, base.Effort)
	work.SmartRoute = &workRoute

	review = base
	review.Model = "sonnet"
	if reviewerModel != "" {
		review.Model = reviewerModel
	}
	if reviewerEffort != "" {
		review.Effort = reviewerEffort
	}
	reviewRoute := resolve(review.Model, review.Effort)
	review.SmartRoute = &reviewRoute
	return work, review
}

// planOptsFromFlags builds the planning step's options: base with
// --planner-model/--planner-effort overrides, routed on its own pins the
// same way composeFlowOpts routes the reviewer's. Kept as its own function
// rather than a third return value off composeFlowOpts because planning is
// optional and most flows pass none of this.
func planOptsFromFlags(base api.TaskOptions, plannerModel, plannerEffort string, resolve func(model, effort string) bool) api.TaskOptions {
	plan := base
	if plannerModel != "" {
		plan.Model = plannerModel
	}
	if plannerEffort != "" {
		plan.Effort = plannerEffort
	}
	route := resolve(plan.Model, plan.Effort)
	plan.SmartRoute = &route
	return plan
}

// flagTyped reports whether the user actually passed the named flag.
// flag.Visit is what makes it possible: it reports only flags that were set,
// so an explicit --max-retries 0 is distinguishable from an unset one.
func flagTyped(fs *flag.FlagSet, name string) bool {
	typed := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			typed = true
		}
	})
	return typed
}

func runFlowCancel(ctx context.Context, args []string) int {
	id, code := flowIDArg("flow-cancel", args)
	if id == "" {
		return code
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.FlowCancel(tctx, id); err != nil {
		return die(err)
	}
	return reportQueueAction("flow", id, "cancelled")
}

// runFlowRemove is spelled out rather than sharing a body with cancel
// because its one interesting failure needs a way out: the daemon refuses to
// forget a flow that is still running, and "conflict" alone does not tell
// the operator what to do about it.
func runFlowRemove(ctx context.Context, args []string) int {
	id, code := flowIDArg("flow-remove", args)
	if id == "" {
		return code
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.FlowRemove(tctx, id); err != nil {
		if errors.Is(err, service.ErrConflict) {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			fmt.Fprintf(os.Stderr, "hint: cancel it (`singl flow cancel --id %s`) or wait for it (`singl flow wait --id %s`) first\n", id, id)
			return 1
		}
		return die(err)
	}
	return reportQueueAction("flow", id, "removed")
}

// flowIDArg parses the single --id flag the per-flow verbs share. An empty
// id means the caller must return code: 0 when the user asked for help, 2
// when --id was missing.
func flowIDArg(name string, args []string) (string, int) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	id := fs.String("id", "", "flow ID (required)")
	if code, done := parseArgs(fs, args); done {
		return "", code
	}
	if *id == "" {
		fmt.Fprintln(os.Stderr, "error: --id is required")
		return "", 2
	}
	return *id, 0
}
