package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/client"
)

func runFlowList(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("flow-list", flag.ContinueOnError)
	var states idListFlag
	fs.Var(&states, "state", "only flows in these states (repeatable, or comma-separated)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	flows, err := c.FlowList(tctx, flowStates(states))
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]any{"flows": flows})
	}
	md := fmt.Sprintf("## Flows (%d)\n\n", len(flows))
	if len(flows) == 0 {
		return renderMarkdown(md + "_No flows._\n")
	}
	md += "| Flow | Title | State | Rounds | Workdir |\n|---|---|---|---|---|\n"
	for _, f := range flows {
		md += fmt.Sprintf("| `%s` | %s | `%s` | %s | `%s` |\n",
			f.ID, flowLabel(f), f.State, flowRounds(f), filepath.Base(f.WorkDir))
	}
	return renderMarkdown(md)
}

func runFlowShow(ctx context.Context, args []string) int {
	id, code := flowIDArg("flow-show", args)
	if id == "" {
		return code
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	f, err := c.FlowGet(tctx, id)
	if err != nil {
		return die(err)
	}
	if globals.json {
		// The bare flow, the shape `queue show` returns. The agent IDs the
		// markdown view joins in are the tree's, and a caller that wants
		// them in JSON asks for the tree — which is what it is for.
		return printJSON(f)
	}
	return renderMarkdown(fmtFlow(*f, flowStepAgents(ctx, c, id)))
}

func runFlowTree(ctx context.Context, args []string) int {
	id, code := flowIDArg("flow-tree", args)
	if id == "" {
		return code
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	t, err := c.FlowTree(tctx, id)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(t)
	}
	return renderMarkdown(renderFlowTree(*t))
}

// flowStepAgents maps each step's task ID to its tree node, which is where
// the agent IDs live: a Round records task IDs only, deliberately, so the
// join to an agent happens daemon-side in the tree.
//
// Best-effort: `flow show` has already fetched the flow it was asked for, so
// a tree error must not turn the whole verb into a failure. It only costs
// the agent lines.
func flowStepAgents(ctx context.Context, c *client.Client, flowID string) map[string]api.FlowTreeNode {
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	t, err := c.FlowTree(tctx, flowID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not fetch the flow tree for agent IDs: %v\n", err)
		return nil
	}
	out := make(map[string]api.FlowTreeNode, len(t.Nodes))
	for _, n := range t.Nodes {
		if n.TaskID != "" {
			out[n.TaskID] = n
		}
	}
	return out
}

// fmtFlow formats one flow as a markdown section: the record, then every
// round with its verdict, its findings and its two steps' task and agent
// IDs. steps may be nil, in which case the agent lines are simply absent.
func fmtFlow(f api.Flow, steps map[string]api.FlowTreeNode) string {
	s := fmt.Sprintf("### `%s` %s\n\n", f.ID, flowLabel(f))
	s += fmt.Sprintf("State: `%s`  \nQueue: `%s`  \nWorkdir: `%s`  \nRounds: %s  \n",
		f.State, f.QueueID, f.WorkDir, flowRounds(f))
	if line := fmtFlowOpts(f.Opts); line != "" {
		s += fmt.Sprintf("Work options: %s  \n", line)
	}
	if line := fmtFlowOpts(f.ReviewOpts); line != "" {
		s += fmt.Sprintf("Review options: %s  \n", line)
	}
	s += fmt.Sprintf("Created: %s  \n", f.CreatedAt.Format(time.DateTime))
	if f.EndedAt != nil {
		s += fmt.Sprintf("Ended: %s  \n", f.EndedAt.Format(time.DateTime))
	}
	if f.Error != "" {
		s += fmt.Sprintf("Error: %s  \n", f.Error)
	}
	s += fmt.Sprintf("\nGoal:\n\n```\n%s\n```\n", f.Goal)
	if f.ReviewGoal != "" {
		s += fmt.Sprintf("\nReview goal:\n\n```\n%s\n```\n", f.ReviewGoal)
	}

	if len(f.Rounds) == 0 {
		return s + "\n_No rounds yet._\n"
	}
	for _, r := range f.Rounds {
		if r == nil {
			continue
		}
		s += fmt.Sprintf("\n#### Round %d — `%s`\n\n", r.N, r.State)
		if r.ReviewAttempt > 1 {
			s += fmt.Sprintf("Review attempts: %d  \n", r.ReviewAttempt)
		}
		s += fmtFlowStep(workStepName(r.N), r.WorkTaskID, steps)
		s += fmtFlowStep("review", r.ReviewTaskID, steps)
		if !r.StartedAt.IsZero() {
			s += fmt.Sprintf("Started: %s", r.StartedAt.Format(time.DateTime))
			if r.EndedAt != nil {
				s += fmt.Sprintf(" — ended: %s", r.EndedAt.Format(time.DateTime))
			}
			s += "  \n"
		}
		s += fmtFlowVerdict(r.Verdict)
	}
	return s
}

// fmtFlowStep renders one step line: its label, task ID, the task's live
// state and the agent that ran it, so `agents output --id ...` is one copy
// away.
func fmtFlowStep(label, taskID string, steps map[string]api.FlowTreeNode) string {
	if taskID == "" {
		return fmt.Sprintf("%s: _not submitted_  \n", label)
	}
	line := fmt.Sprintf("%s: `%s`", label, taskID)
	n, ok := steps[taskID]
	if ok && n.State != "" {
		line += fmt.Sprintf(" — `%s`", n.State)
	}
	if ok && n.AgentID != "" {
		line += fmt.Sprintf(" — agent `%s`", n.AgentID)
		if n.AgentState != "" {
			line += fmt.Sprintf(" (`%s`)", n.AgentState)
		}
		line += fmt.Sprintf(" — `singl agents output --id %s`", n.AgentID)
	}
	return line + "  \n"
}

// fmtFlowVerdict renders a round's verdict and every finding under it. A
// round still running has none; a round that settled always does, including
// the synthetic reject that stands in for an unparseable review.
func fmtFlowVerdict(v *api.FlowVerdict) string {
	if v == nil {
		return "\n_No verdict yet._\n"
	}
	s := fmt.Sprintf("\nVerdict: **%s**", v.Decision)
	if v.Summary != "" {
		s += " — " + v.Summary
	}
	s += "  \n"
	if len(v.Findings) == 0 {
		return s + "\n_No findings._\n"
	}
	s += fmt.Sprintf("\nFindings (%d):\n\n", len(v.Findings))
	for _, fi := range v.Findings {
		s += fmt.Sprintf("- **%s**", fi.Severity)
		if fi.File != "" {
			s += fmt.Sprintf(" `%s", fi.File)
			if fi.Line > 0 {
				s += fmt.Sprintf(":%d", fi.Line)
			}
			s += "`"
		}
		s += " — " + fi.Detail + "\n"
	}
	return s
}

// renderFlowTree draws the flat, parent-linked node list as an ascii tree.
// The daemon emits the nodes root-first and in submit order, so children
// keep their given order rather than being sorted — a round's work step
// belongs above its review step, which is also the order they run in.
func renderFlowTree(t api.FlowTree) string {
	children := map[string][]api.FlowTreeNode{}
	var roots []api.FlowTreeNode
	for _, n := range t.Nodes {
		if n.ParentID == "" {
			roots = append(roots, n)
			continue
		}
		children[n.ParentID] = append(children[n.ParentID], n)
	}

	md := fmt.Sprintf("## Flow `%s` (%d nodes)\n\n```\n", t.FlowID, len(t.Nodes))
	if len(t.Nodes) == 0 {
		return md + "(empty)\n```\n"
	}
	seen := map[string]bool{}
	var walk func(n api.FlowTreeNode, prefix string, last bool, depth int)
	walk = func(n api.FlowTreeNode, prefix string, last bool, depth int) {
		branch := ""
		if depth > 0 {
			branch = "└── "
			if !last {
				branch = "├── "
			}
		}
		md += prefix + branch + flowNodeLabel(n) + "\n"
		if seen[n.ID] {
			return
		}
		seen[n.ID] = true
		childPrefix := prefix
		if depth > 0 {
			if last {
				childPrefix += "    "
			} else {
				childPrefix += "│   "
			}
		}
		kids := children[n.ID]
		for i, k := range kids {
			walk(k, childPrefix, i == len(kids)-1, depth+1)
		}
	}
	for i, r := range roots {
		walk(r, "", i == len(roots)-1, 0)
	}
	// A node whose parent is missing from the list cannot be reached from a
	// root. Listing the leftovers beats silently dropping a step.
	var orphans []string
	for _, n := range t.Nodes {
		if !seen[n.ID] {
			orphans = append(orphans, flowNodeLabel(n))
		}
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		md += "\nunreachable from any root:\n" + strings.Join(orphans, "\n") + "\n"
	}
	return md + "```\n"
}

// flowNodeLabel is one tree line: the node's label and state, plus whatever
// the node's kind actually carries — a task and agent for a step, a verdict
// for a round.
func flowNodeLabel(n api.FlowTreeNode) string {
	label := n.Label
	if label == "" {
		label = n.ID
	}
	s := fmt.Sprintf("%s [%s]", label, n.State)
	if n.TaskID != "" {
		s += " (" + n.TaskID + ")"
	}
	if n.AgentID != "" {
		s += " agent " + n.AgentID
		if n.AgentState != "" {
			s += " (" + n.AgentState + ")"
		}
	}
	if n.Verdict != nil {
		s += fmt.Sprintf(" — %s", n.Verdict.Decision)
		if len(n.Verdict.Findings) > 0 {
			s += fmt.Sprintf(": %d finding(s)", len(n.Verdict.Findings))
		}
	}
	return s
}

// flowStates converts the --state flag values into wire states. Unknown
// values are passed through: the daemon rejects them naming the offender,
// which beats a client-side list drifting out of sync with the enum.
func flowStates(values []string) []api.FlowState {
	out := make([]api.FlowState, 0, len(values))
	for _, v := range values {
		out = append(out, api.FlowState(v))
	}
	return out
}

// flowLabel is the short human handle for a flow: its title, or the first
// line of its goal, shortened. Mirrors taskLabel.
func flowLabel(f api.Flow) string {
	if t := strings.TrimSpace(f.Title); t != "" {
		return t
	}
	line := strings.TrimSpace(f.Goal)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	if len([]rune(line)) > 60 {
		line = string([]rune(line)[:60]) + "…"
	}
	return line
}

// flowRounds is the "2/3" progress cell: rounds recorded over the cap.
func flowRounds(f api.Flow) string {
	return fmt.Sprintf("%d/%d", len(f.Rounds), f.MaxRounds)
}

// workStepName names a round's work step for what it does — round 1
// implements, every later round fixes the previous round's findings. It
// matches the daemon's own tree label so the two views agree.
func workStepName(round int) string {
	if round <= 1 {
		return "implement"
	}
	return "fix"
}

// fmtFlowOpts renders one option block's pins, or "" when it pins nothing
// and the line is better left out than printed empty. SmartRoute is shown
// only when it is off: it resolves to on for almost every flow, so stating
// it every time would bury the case that changes what runs.
func fmtFlowOpts(o api.TaskOptions) string {
	parts := []string{}
	if o.Model != "" {
		parts = append(parts, "model `"+o.Model+"`")
	}
	if o.Effort != "" {
		parts = append(parts, "effort `"+o.Effort+"`")
	}
	if o.Backend != "" {
		parts = append(parts, "backend `"+o.Backend+"`")
	}
	if o.TimeoutSecs > 0 {
		parts = append(parts, fmt.Sprintf("timeout %ds", o.TimeoutSecs))
	}
	if len(o.AllowedTools) > 0 {
		parts = append(parts, "tools `"+strings.Join(o.AllowedTools, "`, `")+"`")
	}
	if len(o.ContextFiles) > 0 {
		parts = append(parts, fmt.Sprintf("%d context file(s)", len(o.ContextFiles)))
	}
	if !o.RouteEnabled() {
		parts = append(parts, "no smart routing")
	}
	return strings.Join(parts, ", ")
}
