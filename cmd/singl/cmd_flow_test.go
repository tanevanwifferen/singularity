package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"testing"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// TestDecideFlowWait is the exit-code contract of `flow wait`, and the whole
// point of the verb: a caller scripts `flow wait && land it`, so only an
// accept may exit 0. `rejected` is a designed outcome rather than a
// malfunction and still exits 1 — the work must not be landed on it.
func TestDecideFlowWait(t *testing.T) {
	cases := []struct {
		name     string
		state    api.FlowState
		timedOut bool
		want     flowWaitOutcome
		wantCode int
	}{
		{"pending", service.FlowPending, false, flowWaitPending, 0},
		{"running", service.FlowRunning, false, flowWaitPending, 0},
		{"accepted", service.FlowAccepted, false, flowWaitAccepted, 0},
		{"rejected", service.FlowRejected, false, flowWaitRejected, 1},
		{"errored", service.FlowErrored, false, flowWaitErrored, 1},
		{"cancelled", service.FlowCancelled, false, flowWaitCancelled, 1},
		{"timeout running", service.FlowRunning, true, flowWaitTimedOut, 1},
		{"timeout pending", service.FlowPending, true, flowWaitTimedOut, 1},
		// A flow that settled on the very tick --timeout fired did settle;
		// reporting a timeout would fail a run that succeeded.
		{"accept beats timeout", service.FlowAccepted, true, flowWaitAccepted, 0},
		{"reject beats timeout", service.FlowRejected, true, flowWaitRejected, 1},
	}
	for _, tc := range cases {
		got := decideFlowWait(api.Flow{ID: "f1", State: tc.state}, tc.timedOut)
		if got != tc.want {
			t.Fatalf("%s: decideFlowWait = %q, want %q", tc.name, got, tc.want)
		}
		if code := got.exitCode(); code != tc.wantCode {
			t.Errorf("%s: exitCode = %d, want %d", tc.name, code, tc.wantCode)
		}
		// The four terminal outcomes are spelled exactly like the states
		// they report, so the JSON `outcome` field and the `state` inside
		// the flow it carries can never disagree.
		if tc.state.Terminal() && !tc.timedOut && string(got) != string(tc.state) {
			t.Errorf("state %q reported as outcome %q", tc.state, got)
		}
	}
	// An unhandled terminal state would fall through to pending and hang the
	// poll loop forever, so a new one has to add a decideFlowWait arm.
	if got := decideFlowWait(api.Flow{State: "who-knows"}, false); got != flowWaitPending {
		t.Errorf("unknown state reported as %q", got)
	}
	// The documented codes and exitCode must not drift apart — an
	// orchestrating agent branches on what the help text claims. Same
	// contract cmd_help_test.go pins for `queue wait`.
	if n := strings.Count(flowWaitUsage, "\n  0  "); n != 1 {
		t.Errorf("flow wait usage documents %d exit-0 outcomes, want exactly 1", n)
	}
	if n := strings.Count(flowWaitUsage, "\n  1  "); n != 2 {
		t.Errorf("flow wait usage documents %d exit-1 outcomes, want 2", n)
	}
	if !strings.Contains(flowWaitUsage, "accepted") {
		t.Error("flow wait usage does not name the one outcome that exits 0")
	}
}

// TestComposeFlowOpts covers the reviewer-defaulting rule: the reviewer
// inherits the work options and --reviewer-* overrides only what it names.
func TestComposeFlowOpts(t *testing.T) {
	base := api.TaskOptions{
		Model: "sonnet", Effort: "medium", Backend: "claude", TimeoutSecs: 1800,
		ContextFiles: []string{"/w/notes.md"}, AllowedTools: []string{"Read", "Edit"},
	}
	always := func(string, string) bool { return true }
	// Nothing named: the reviewer runs exactly like the implementer.
	work, review := composeFlowOpts(base, "", "", always)
	if review.Model != work.Model || review.Effort != work.Effort {
		t.Errorf("reviewer did not default to the work options: %+v vs %+v", review, work)
	}

	// Only the model named. The effort, backend, timeout, context files and
	// tool restriction all still come from the work options, and the work
	// options are untouched.
	work, review = composeFlowOpts(base, "opus", "", always)
	if review.Model != "opus" || review.Effort != "medium" ||
		review.Backend != "claude" || review.TimeoutSecs != 1800 ||
		len(review.ContextFiles) != 1 || len(review.AllowedTools) != 2 {
		t.Errorf("--reviewer-model overrode more than the model: %+v", review)
	}
	if work.Model != "sonnet" {
		t.Errorf("--reviewer-model leaked into the work options: %q", work.Model)
	}

	// Only the effort named.
	work, review = composeFlowOpts(base, "", "high", always)
	if review.Effort != "high" || review.Model != "sonnet" {
		t.Errorf("--reviewer-effort should override the effort alone: %+v", review)
	}
	if work.Effort != "medium" {
		t.Errorf("--reviewer-effort leaked into the work options: %q", work.Effort)
	}
}

// TestComposeFlowOptsRoutingPrecedence is the reason composeFlowOpts takes
// the resolver rather than a bool: routing is resolved through
// smartRouteFlags/resolveSmartRoute — the one place the precedence lives, so
// a flow's steps route exactly as the same prompt would from `queue add` or
// `agents spawn` — and separately per step, because --reviewer-* can leave
// the two with different pins.
//
// The bug it must not reintroduce is TaskOptions.RouteEnabled's: a step that
// pins an effort and says nothing about routing must not be routed anyway.
func TestComposeFlowOptsRoutingPrecedence(t *testing.T) {
	cases := []struct {
		name                          string
		args                          []string
		base                          api.TaskOptions
		reviewerModel, reviewerEffort string
		wantWork, wantReview          bool
	}{
		{"unpinned routes both", nil, api.TaskOptions{}, "", "", true, true},
		// The case where the two steps genuinely diverge: --model leaves the
		// work step's effort to the classifier, while --reviewer-effort
		// fills in the effort the reviewer inherits, leaving that step
		// nothing to decide. Resolving routing once for both gets one wrong.
		{"reviewer effort pins only the reviewer",
			[]string{"--model", "sonnet", "--reviewer-effort", "high"},
			api.TaskOptions{Model: "sonnet"}, "", "high", true, false},
		{"both pinned routes neither, since the reviewer inherits both",
			[]string{"--model", "sonnet", "--effort", "high"},
			api.TaskOptions{Model: "sonnet", Effort: "high"}, "opus", "", false, false},
		// An effort alone is the exact shape RouteEnabled documents.
		{"effort alone is a pin, not a routing request",
			[]string{"--effort", "medium"}, api.TaskOptions{Effort: "medium"}, "", "", true, true},
		{"--no-smart-route wins over everything",
			[]string{"--no-smart-route"}, api.TaskOptions{}, "opus", "high", false, false},
		{"explicit --smart-route beats the both-pinned default-off",
			[]string{"--smart-route", "--model", "sonnet", "--effort", "high"},
			api.TaskOptions{Model: "sonnet", Effort: "high"}, "", "", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			work, review := flowOptsFromArgs(t, tc.args, tc.base, tc.reviewerModel, tc.reviewerEffort)
			if *work.SmartRoute != tc.wantWork {
				t.Errorf("work step routing = %v, want %v", *work.SmartRoute, tc.wantWork)
			}
			if *review.SmartRoute != tc.wantReview {
				t.Errorf("review step routing = %v, want %v", *review.SmartRoute, tc.wantReview)
			}
			if work.SmartRoute == review.SmartRoute {
				t.Error("the two steps share one SmartRoute pointer; editing one would move the other")
			}
			// The answer must equal what the daemon infers from the same
			// options, so neither side can drift from the other.
			for name, o := range map[string]api.TaskOptions{"work": work, "review": review} {
				if o.RouteEnabled() != *o.SmartRoute {
					t.Errorf("%s step: RouteEnabled = %v but SmartRoute = %v",
						name, o.RouteEnabled(), *o.SmartRoute)
				}
			}
		})
	}
}

// flowOptsFromArgs runs composeFlowOpts against a real flag set, so the
// resolver under test is the one `flow start` builds rather than a stand-in.
func flowOptsFromArgs(t *testing.T, args []string, base api.TaskOptions, reviewerModel, reviewerEffort string) (api.TaskOptions, api.TaskOptions) {
	t.Helper()
	fs := flag.NewFlagSet("flow-start", flag.ContinueOnError)
	fs.String("model", "", "")
	fs.String("effort", "", "")
	fs.String("reviewer-model", "", "")
	fs.String("reviewer-effort", "", "")
	resolve := smartRouteFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("Parse(%v): %v", args, err)
	}
	return composeFlowOpts(base, reviewerModel, reviewerEffort, resolve)
}

// TestFlowStartUsageErrors covers every way `flow start` refuses before it
// ever reaches the daemon. All of them exit 2 — a usage error, not a
// failure — and none of them opens a connection, which is what makes them
// testable here.
func TestFlowStartUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no flags at all", nil},
		{"--workdir without --prompt or --jira", []string{"--workdir", "/w"}},
		{"--jira without --workdir", []string{"--jira", "PROJ-1"}},
		// --use-worktree must not exist. §2: the engine rewrites an isolated
		// agent's work dir, so the reviewer would review a different tree
		// than the implementer wrote, and each isolated agent merges back on
		// its own, so a rejected round's work would already be merged. The
		// daemon refuses use_worktree outright, and the CLI must not offer a
		// flag whose only possible outcome is that refusal — isolation is
		// the caller's job, done by passing a worktree as --workdir.
		{"--use-worktree", []string{"--workdir", "/w", "--prompt", "p", "--use-worktree"}},
		// --max-retries is rejected rather than ignored, the rule `queue add
		// --file` established: nothing on the flow wire carries a per-task
		// retry count — neither FlowStartRequest nor TaskOptions has a field
		// for it — so accepting it silently would tell an operator their
		// flaky step would be retried when it would not.
		{"--max-retries", []string{"--workdir", "/w", "--prompt", "p", "--max-retries", "2"}},
		// An explicitly passed zero is still a typed flag, which is why the
		// check uses flag.Visit rather than comparing against the default.
		{"--max-retries 0", []string{"--workdir", "/w", "--prompt", "p", "--max-retries", "0"}},
	} {
		if code := runFlowStart(context.Background(), tc.args); code != 2 {
			t.Errorf("`flow start` with %s exited %d, want 2", tc.name, code)
		}
	}
}

// TestResolveFlowGoalMissingKey covers the plain --prompt path: with no
// --jira, resolveFlowGoal must not call the fetcher at all — a flow that
// never named a ticket has no key to resolve — and the prompt passes through
// verbatim as the goal, with no title guessed.
func TestResolveFlowGoalMissingKey(t *testing.T) {
	calls := 0
	fetch := func(context.Context, string) (*api.Issue, error) {
		calls++
		return nil, fmt.Errorf("should not be called")
	}
	title, goal, err := resolveFlowGoal(context.Background(), fetch, "", "fix the parser")
	if err != nil {
		t.Fatalf("resolveFlowGoal: %v", err)
	}
	if calls != 0 {
		t.Errorf("fetch called %d times, want 0 when --jira is empty", calls)
	}
	if title != "" {
		t.Errorf("title = %q, want empty without --jira", title)
	}
	if goal != "fix the parser" {
		t.Errorf("goal = %q, want --prompt verbatim", goal)
	}
}

// TestResolveFlowGoalUnreachableJira covers what an operator sees when the
// daemon cannot reach Jira, or the key does not exist: the fetcher's error
// must come back wrapped with the key that failed, not swallowed or
// replaced by a generic message runFlowStart would then hand to die().
func TestResolveFlowGoalUnreachableJira(t *testing.T) {
	fetch := func(context.Context, string) (*api.Issue, error) {
		return nil, fmt.Errorf("jira: connection refused")
	}
	_, _, err := resolveFlowGoal(context.Background(), fetch, "PROJ-1", "")
	if err == nil {
		t.Fatal("resolveFlowGoal: want an error when the fetch fails")
	}
	if !strings.Contains(err.Error(), "PROJ-1") || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error = %q, want it to name the key and wrap the fetch error", err)
	}
}

// TestResolveFlowGoalFromIssue covers prompt construction: with --jira and
// no --prompt, the goal and title come entirely from the fetched issue.
func TestResolveFlowGoalFromIssue(t *testing.T) {
	fetch := func(_ context.Context, key string) (*api.Issue, error) {
		if key != "PROJ-9" {
			t.Fatalf("fetch key = %q, want PROJ-9", key)
		}
		return &api.Issue{Key: "PROJ-9", Summary: "Retry on 429", Description: "Honour Retry-After."}, nil
	}
	title, goal, err := resolveFlowGoal(context.Background(), fetch, "PROJ-9", "")
	if err != nil {
		t.Fatalf("resolveFlowGoal: %v", err)
	}
	if title != "PROJ-9: Retry on 429" {
		t.Errorf("title = %q", title)
	}
	for _, want := range []string{"PROJ-9", "Retry on 429", "Honour Retry-After."} {
		if !strings.Contains(goal, want) {
			t.Errorf("goal missing %q:\n%s", want, goal)
		}
	}
}

// TestResolveFlowGoalFromIssueWithExtraPrompt covers the --jira + --prompt
// combination: the issue's own text must survive, with --prompt appended
// rather than replacing it — the same "narrows, does not replace" rule
// --review-prompt follows.
func TestResolveFlowGoalFromIssueWithExtraPrompt(t *testing.T) {
	fetch := func(context.Context, string) (*api.Issue, error) {
		return &api.Issue{Key: "PROJ-9", Summary: "Retry on 429"}, nil
	}
	_, goal, err := resolveFlowGoal(context.Background(), fetch, "PROJ-9", "focus on the client, not the server")
	if err != nil {
		t.Fatalf("resolveFlowGoal: %v", err)
	}
	if !strings.Contains(goal, "PROJ-9") || !strings.Contains(goal, "Retry on 429") {
		t.Errorf("goal dropped the issue's own text:\n%s", goal)
	}
	if !strings.Contains(goal, "focus on the client, not the server") {
		t.Errorf("goal dropped the explicit --prompt:\n%s", goal)
	}
}

// TestFlowContinueUsageErrors covers the refusals `flow continue` makes
// before it opens a connection. A missing --id is a usage error (2); --help
// is not (0). A round count is deliberately absent from this list: the
// daemon knows the flow's current cap, so a number that will not fit is
// answered by the manager naming the largest one that would, and refusing it
// here would replace that with a guess.
func TestFlowContinueUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"no flags at all", nil, 2},
		{"--rounds without --id", []string{"--rounds", "2"}, 2},
		{"--help", []string{"--help"}, 0},
		{"unknown flag", []string{"--id", "f1", "--goal", "something else"}, 2},
	} {
		if code := runFlowContinue(context.Background(), tc.args); code != tc.want {
			t.Errorf("`flow continue` with %s exited %d, want %d", tc.name, code, tc.want)
		}
	}
}

// TestFmtFlowContinued covers what a continue prints, which is the point of
// the verb: an operator who authorised more rounds has to see the cap they
// bought and the round the work resumes at, not merely that the call
// succeeded.
func TestFmtFlowContinued(t *testing.T) {
	f := api.Flow{
		ID: "f3", QueueID: "flow-f3", Title: "retry-after handling",
		State: service.FlowRunning, WorkDir: "/w/api", MaxRounds: 5,
		Rounds: []*api.FlowRound{
			{N: 1, State: service.FlowRoundRejected},
			{N: 2, State: service.FlowRoundRejected},
			{N: 3, State: service.FlowRoundRejected},
		},
	}
	out := fmtFlowContinued(f)
	for _, want := range []string{
		"`f3` continued", "retry-after handling", "State: `running`",
		"Max rounds: 5", "Resumes at round 4", "/w/api",
		"singl flow wait --id f3", "singl queue list --queue flow-f3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("continue output missing %q:\n%s", want, out)
		}
	}
}

// TestNextFlowRound pins the numbering a continue reports against the
// daemon's own: rounds start at 1 and every round the flow ran stays on the
// record, so the next one is one past what is there. A client that guessed
// differently would tell the operator work resumes at a round the daemon
// will never open.
func TestNextFlowRound(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    api.Flow
		want int
	}{
		{"never ran", api.Flow{}, 1},
		{"stopped at 3", api.Flow{Rounds: []*api.FlowRound{{N: 1}, {N: 2}, {N: 3}}}, 4},
	} {
		if got := nextFlowRound(tc.f); got != tc.want {
			t.Errorf("%s: nextFlowRound = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestFlowIDArg covers the flag body the five --id verbs share: a missing
// --id is a usage error (2), and --help is not (0, with no ID to act on).
func TestFlowIDArg(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		wantID   string
		wantCode int
	}{
		{nil, "", 2},
		{[]string{"--id", "f3"}, "f3", 0},
		{[]string{"--help"}, "", 0},
	} {
		id, code := flowIDArg("flow-show", tc.args)
		if id != tc.wantID || code != tc.wantCode {
			t.Errorf("flowIDArg(%v) = (%q, %d), want (%q, %d)", tc.args, id, code, tc.wantID, tc.wantCode)
		}
	}
}

// TestFlowHelpCoversEveryVerb keeps the noun reference and the dispatcher
// from drifting: `singl flow` is where the surface is learned.
func TestFlowHelpCoversEveryVerb(t *testing.T) {
	usage := nounUsage["flow"]
	if usage == "" {
		t.Fatal("nounUsage has no flow entry")
	}
	for _, want := range []string{
		"  start", "  continue", "  list", "  show", "  tree", "  wait", "  cancel", "  remove",
		"--workdir", "--prompt", "--review-prompt", "--max-rounds", "--title",
		"--model", "--effort", "--timeout", "--backend", "--context-file",
		"--allowed-tools", "--reviewer-model", "--reviewer-effort",
		"--smart-route", "--no-smart-route", "--id", "--state", "--interval",
		"--rounds",
		"--json", // every non-streaming verb takes it
	} {
		if !strings.Contains(usage, want) {
			t.Errorf("flow help does not document %s", want)
		}
	}
	// A continue is refused from two of the six states and defaults its
	// round count, and an operator who has to discover either by being
	// refused has been failed by the reference.
	for _, want := range []string{"rejected", "errored", "cancelled", "accepted", "defaults to 3"} {
		if !strings.Contains(usage, want) {
			t.Errorf("flow help does not say what `continue` does about %q", want)
		}
	}
	// A bare `singl flow` prints this and exits 0; an unknown verb, 2.
	if code := nounHelp("flow", ""); code != 0 {
		t.Errorf("bare `singl flow` exited %d, want 0", code)
	}
	if code := nounHelp("flow", "sprint"); code != 2 {
		t.Errorf("unknown flow verb exited %d, want 2", code)
	}
}

// TestRenderFlowTree checks the ascii rendering: rounds nest under the flow,
// steps under their round, and a step carries its task, agent and state.
func TestRenderFlowTree(t *testing.T) {
	step := func(id, parent, label, state, task, agent, agentState string, round int) api.FlowTreeNode {
		return api.FlowTreeNode{ID: id, ParentID: parent, Kind: service.FlowNodeStep, Label: label,
			State: state, Round: round, TaskID: task, AgentID: agent, AgentState: agentState}
	}
	out := renderFlowTree(api.FlowTree{FlowID: "f3", Nodes: []api.FlowTreeNode{
		{ID: "f3", Kind: service.FlowNodeFlow, Label: "retry-after handling", State: "running"},
		{ID: "f3/r1", ParentID: "f3", Kind: service.FlowNodeRound, Label: "round 1", State: "rejected", Round: 1,
			Verdict: &api.FlowVerdict{Decision: service.FlowReject, Findings: []api.FlowFinding{
				{Severity: service.FlowSeverityBlocker, Detail: "no backoff"},
				{Severity: service.FlowSeverityMinor, Detail: "typo"},
			}}},
		step("f3/r1/implement", "f3/r1", "implement", "done", "t11", "agent-1712-4", "complete", 1),
		step("f3/r1/review", "f3/r1", "review", "done", "t12", "agent-1712-5", "complete", 1),
		{ID: "f3/r2", ParentID: "f3", Kind: service.FlowNodeRound, Label: "round 2", State: "running", Round: 2},
		step("f3/r2/fix", "f3/r2", "fix", "running", "t13", "agent-1712-6", "running", 2),
	}})
	for _, want := range []string{
		"retry-after handling [running]", "└── round 2 [running]",
		"├── round 1 [rejected] — reject: 2 finding(s)",
		"implement [done] (t11) agent agent-1712-4 (complete)",
		"review [done] (t12) agent agent-1712-5 (complete)",
		"fix [running] (t13) agent agent-1712-6 (running)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("tree output missing %q:\n%s", want, out)
		}
	}
	// A round's work step must come above its review step: that is submit
	// order and run order, and sorting the children would break it.
	if strings.Index(out, "implement [done]") > strings.Index(out, "review [done]") {
		t.Errorf("review rendered above implement:\n%s", out)
	}

	if empty := renderFlowTree(api.FlowTree{FlowID: "f1"}); !strings.Contains(empty, "(empty)") {
		t.Errorf("empty tree rendered as:\n%s", empty)
	}
	// A node whose parent is missing stays visible rather than being dropped
	// — the rule renderGraphTree follows for a cycle.
	orphan := renderFlowTree(api.FlowTree{FlowID: "f1", Nodes: []api.FlowTreeNode{
		{ID: "f1", Kind: service.FlowNodeFlow, Label: "goal", State: "running"},
		{ID: "f1/r9/review", ParentID: "f1/r9", Kind: service.FlowNodeStep, Label: "review", State: "done", TaskID: "t99"},
	}})
	if !strings.Contains(orphan, "unreachable from any root") || !strings.Contains(orphan, "t99") {
		t.Errorf("orphaned step was dropped:\n%s", orphan)
	}
}

// TestFmtFlow covers what `flow show` has to render: every round, its
// verdict, its findings and the agent ID behind each step — the join the
// tree provides, since a Round records task IDs only.
func TestFmtFlow(t *testing.T) {
	ended := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	f := api.Flow{
		ID: "f3", QueueID: "flow-f3", Title: "retry-after handling",
		IssueKey: "PROJ-9",
		Goal:     "honour Retry-After", ReviewGoal: "check the 429 path",
		WorkDir: "/w/api", MaxRounds: 3, State: service.FlowAccepted,
		Opts:       api.TaskOptions{Model: "sonnet", Effort: "medium"},
		ReviewOpts: api.TaskOptions{Model: "opus", Effort: "medium"},
		CreatedAt:  ended, EndedAt: &ended,
		Rounds: []*api.FlowRound{
			{N: 1, WorkTaskID: "t11", ReviewTaskID: "t12", ReviewAttempt: 1,
				State: service.FlowRoundRejected, StartedAt: ended,
				Verdict: &api.FlowVerdict{Decision: service.FlowReject, Summary: "no backoff", Findings: []api.FlowFinding{
					{Severity: service.FlowSeverityBlocker, File: "client.go", Line: 42, Detail: "ignores Retry-After"},
					{Severity: service.FlowSeverityMinor, Detail: "comment typo"},
				}}},
			{N: 2, WorkTaskID: "t13", ReviewTaskID: "t14", ReviewAttempt: 1,
				State: service.FlowRoundAccepted, StartedAt: ended, EndedAt: &ended,
				Verdict: &api.FlowVerdict{Decision: service.FlowAccept, Summary: "looks right"}},
		},
	}
	steps := map[string]api.FlowTreeNode{}
	for i, id := range []string{"t11", "t12", "t13", "t14"} {
		steps[id] = api.FlowTreeNode{State: "done", AgentState: "complete",
			AgentID: fmt.Sprintf("agent-%d", i+1)}
	}
	out := fmtFlow(f, steps)
	for _, want := range []string{
		"`f3` retry-after handling", "Jira: PROJ-9", "State: `accepted`", "Rounds: 2/3",
		"model `sonnet`", "model `opus`", "honour Retry-After", "check the 429 path",
		"#### Round 1 — `rejected`", "implement: `t11`", "review: `t12`",
		"agent `agent-1`", "singl agents output --id agent-2",
		"Verdict: **reject** — no backoff", "Findings (2)",
		"`client.go:42`", "ignores Retry-After",
		// Round 2's work step is a fix, not another implement.
		"#### Round 2 — `accepted`", "fix: `t13`", "Verdict: **accept**",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("flow show output missing %q:\n%s", want, out)
		}
	}
}

// TestFmtFlowWithoutTree pins the degradation `flow show` accepts: the tree
// fetch is best-effort, so an unavailable tree costs the agent lines and
// nothing else. A show that failed outright would lose the verdicts too.
func TestFmtFlowWithoutTree(t *testing.T) {
	out := fmtFlow(api.Flow{
		ID: "f1", QueueID: "flow-f1", Goal: "do it", WorkDir: "/w", MaxRounds: 3,
		State:  service.FlowRunning,
		Rounds: []*api.FlowRound{{N: 1, WorkTaskID: "t1", ReviewTaskID: "t2", ReviewAttempt: 2, State: service.FlowRoundRunning}},
	}, nil)
	for _, want := range []string{"implement: `t1`", "review: `t2`", "Review attempts: 2", "_No verdict yet._"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "agent `") {
		t.Errorf("agent line rendered without a tree:\n%s", out)
	}

	// A flow observed before the reconciler submitted round 1 — the state a
	// caller sees immediately after `flow start`.
	out = fmtFlow(api.Flow{ID: "f1", Goal: "do it", MaxRounds: 3, State: service.FlowPending}, nil)
	if !strings.Contains(out, "_No rounds yet._") || !strings.Contains(out, "Rounds: 0/3") {
		t.Errorf("pending flow rendered as:\n%s", out)
	}
}

// TestFlowLabel mirrors taskLabel's contract: the title if there is one, the
// goal's first line shortened otherwise. A row labelled with nothing at all
// is unusable in `flow list`.
func TestFlowLabel(t *testing.T) {
	for _, tc := range []struct {
		f    api.Flow
		want string
	}{
		{api.Flow{Title: "retry-after", Goal: "long goal"}, "retry-after"},
		{api.Flow{Goal: "honour Retry-After\nand back off"}, "honour Retry-After"},
		{api.Flow{Title: "   ", Goal: "the goal"}, "the goal"},
		{api.Flow{Goal: strings.Repeat("x", 80)}, strings.Repeat("x", 60) + "…"},
	} {
		if got := flowLabel(tc.f); got != tc.want {
			t.Errorf("flowLabel(%+v) = %q, want %q", tc.f, got, tc.want)
		}
	}
}

// TestWorkStepName keeps the CLI's step naming aligned with the daemon's own
// tree label: round 1 implements, later rounds fix. The label is part of a
// tree node's ID, so a client that renamed it locally would print an ID no
// endpoint recognises.
func TestWorkStepName(t *testing.T) {
	for round, want := range map[int]string{0: "implement", 1: "implement", 2: "fix", 7: "fix"} {
		if got := workStepName(round); got != want {
			t.Errorf("workStepName(%d) = %q, want %q", round, got, want)
		}
	}
}

// TestFlowStatesPassThrough pins the deliberate absence of client-side
// validation on --state: an unknown value goes to the daemon, which rejects
// it naming the offender, rather than being filtered against a list that can
// drift out of sync with the enum.
func TestFlowStatesPassThrough(t *testing.T) {
	fs := flag.NewFlagSet("flow-list", flag.ContinueOnError)
	var states idListFlag
	fs.Var(&states, "state", "")
	if err := fs.Parse([]string{"--state", "accepted,rejected", "--state", " running "}); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(flowStates(states)); got != "[accepted rejected running]" {
		t.Errorf("flowStates = %s", got)
	}
	if bogus := flowStates([]string{"nope"}); len(bogus) != 1 || bogus[0] != "nope" {
		t.Errorf("unknown state was filtered client-side: %v", bogus)
	}
}

// TestFmtFlowWaitReportsTheVerdict covers what a settled wait prints: the
// outcome, the round count, and the last round's findings — everything the
// poll already returned, so the report needs no second fetch.
func TestFmtFlowWaitReportsTheVerdict(t *testing.T) {
	f := api.Flow{
		ID: "f1", State: service.FlowRejected, WorkDir: "/w/api", MaxRounds: 2,
		Rounds: []*api.FlowRound{
			{N: 1, State: service.FlowRoundRejected, Verdict: &api.FlowVerdict{Decision: service.FlowReject}},
			{N: 2, State: service.FlowRoundRejected, Verdict: &api.FlowVerdict{Decision: service.FlowReject, Summary: "still no backoff",
				Findings: []api.FlowFinding{{Severity: service.FlowSeverityBlocker, Detail: "no backoff"}}}},
		},
	}
	out := fmtFlowWait(f, flowWaitRejected, 90*time.Second)
	for _, want := range []string{"`f1`: rejected after 1m30s", "Rounds: 2/2", "### Round 2", "still no backoff", "no backoff"} {
		if !strings.Contains(out, want) {
			t.Errorf("wait report missing %q:\n%s", want, out)
		}
	}

	// An accepted flow says what to do next; a timeout says how to keep
	// waiting or stop.
	acc := api.Flow{ID: "f2", State: service.FlowAccepted, WorkDir: "/w", MaxRounds: 3}
	if out := fmtFlowWait(acc, flowWaitAccepted, time.Second); !strings.Contains(out, "passed review") {
		t.Errorf("accepted wait report missing the next step:\n%s", out)
	}
	run := api.Flow{ID: "f3", State: service.FlowRunning, WorkDir: "/w", MaxRounds: 3}
	if out := fmtFlowWait(run, flowWaitTimedOut, time.Minute); !strings.Contains(out, "flow cancel --id f3") {
		t.Errorf("timeout wait report missing the way out:\n%s", out)
	}

	// Cancel records no error, so the stderr line needs a stand-in rather
	// than a blank.
	if got := flowWaitReason(api.Flow{Error: "task t3 failed"}); got != "task t3 failed" {
		t.Errorf("flowWaitReason = %q", got)
	}
	if got := flowWaitReason(api.Flow{}); got == "" {
		t.Error("flowWaitReason returned a blank for a flow with no error")
	}
}

// TestLastFlowRound covers the nil guards on a slice that arrived over the
// wire: the peer's JSON decides its contents, so a nil round pointer must be
// skipped rather than dereferenced.
func TestLastFlowRound(t *testing.T) {
	if got := lastFlowRound(api.Flow{}); got != nil {
		t.Errorf("a flow with no rounds returned %+v", got)
	}
	if got := lastFlowRound(api.Flow{Rounds: []*api.FlowRound{nil, nil}}); got != nil {
		t.Errorf("a flow of nil rounds returned %+v", got)
	}
	got := lastFlowRound(api.Flow{Rounds: []*api.FlowRound{{N: 1}, {N: 2}, nil}})
	if got == nil || got.N != 2 {
		t.Errorf("lastFlowRound = %+v, want round 2", got)
	}
	// fmtFlow walks the same slice and must survive it too.
	if out := fmtFlow(api.Flow{ID: "f1", MaxRounds: 1, Rounds: []*api.FlowRound{nil}}, nil); out == "" {
		t.Error("fmtFlow produced nothing for a nil round")
	}
}
