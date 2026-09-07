package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

func runQueueList(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("queue-list", flag.ContinueOnError)
	queueID := fs.String("queue", "", "only tasks in this queue (default: every queue)")
	var states idListFlag
	fs.Var(&states, "state", "only tasks in these states (repeatable, or comma-separated)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	tasks, err := c.QueueList(tctx, *queueID, taskStates(states))
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]any{"tasks": tasks})
	}
	md := fmt.Sprintf("## Queued tasks (%d)\n\n", len(tasks))
	for _, t := range tasks {
		md += fmt.Sprintf("- `%s` [%s] %s — `%s`\n", t.ID, t.QueueID, taskLabel(t), t.State)
	}
	if len(tasks) == 0 {
		md += "_No tasks._\n"
	}
	return renderMarkdown(md)
}

func runQueueShow(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("queue-show", flag.ContinueOnError)
	id := fs.String("id", "", "task ID (required)")
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
	t, err := c.QueueGet(tctx, *id)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(t)
	}
	return renderMarkdown(fmtTask(*t))
}

func runQueueQueues(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("queue-queues", flag.ContinueOnError)
	if code, done := parseArgs(fs, args); done {
		return code
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	infos, err := c.QueueQueues(tctx)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]any{"queues": infos})
	}
	md := fmt.Sprintf("## Queues (%d)\n\n", len(infos))
	if len(infos) > 0 {
		md += "| Queue | Total | Blocked | Ready | Running | Waiting | Done | Failed | Cancelled | Skipped | Paused |\n"
		md += "|---|--:|--:|--:|--:|--:|--:|--:|--:|--:|---|\n"
	}
	for _, i := range infos {
		md += fmt.Sprintf("| `%s` | %d | %d | %d | %d | %d | %d | %d | %d | %d | %v |\n",
			i.ID, i.Total, i.Blocked, i.Ready, i.Running, i.Waiting,
			i.Done, i.Failed, i.Cancelled, i.Skipped, i.Paused)
	}
	if len(infos) == 0 {
		md += "_No queues._\n"
	}
	return renderMarkdown(md)
}

func runQueueGraph(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("queue-graph", flag.ContinueOnError)
	queueID := fs.String("queue", "", "queue to render (required unless exactly one queue exists)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	id := *queueID
	if id == "" {
		// A single-queue daemon is the common case for a scripted
		// orchestrator; making it type an ID it never chose is friction.
		infos, err := c.QueueQueues(tctx)
		if err != nil {
			return die(err)
		}
		if len(infos) != 1 {
			fmt.Fprintln(os.Stderr, "error: --queue is required (there are "+fmt.Sprint(len(infos))+" queues)")
			return 2
		}
		id = infos[0].ID
	}
	g, err := c.QueueGraph(tctx, id)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(g)
	}
	return renderMarkdown(renderGraphTree(*g))
}

// renderGraphTree draws the DAG as an ascii tree rooted at the tasks with
// no dependencies. A task with several dependencies appears under each of
// them, marked `(again)` on repeats so the tree stays finite and readable
// without pretending the shape is one.
func renderGraphTree(g api.QueueGraph) string {
	children := map[string][]string{}
	indegree := map[string]int{}
	nodes := map[string]service.GraphNode{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
		if _, ok := indegree[n.ID]; !ok {
			indegree[n.ID] = 0
		}
	}
	for _, e := range g.Edges {
		children[e.From] = append(children[e.From], e.To)
		indegree[e.To]++
	}
	var roots []string
	for _, n := range g.Nodes {
		if indegree[n.ID] == 0 {
			roots = append(roots, n.ID)
		}
	}
	sort.Strings(roots)

	md := fmt.Sprintf("## Queue `%s` (%d tasks)\n\n```\n", g.QueueID, len(g.Nodes))
	if len(g.Nodes) == 0 {
		return md + "(empty)\n```\n"
	}
	seen := map[string]bool{}
	var walk func(id, prefix string, last bool, depth int)
	walk = func(id, prefix string, last bool, depth int) {
		branch := ""
		if depth > 0 {
			branch = "└── "
			if !last {
				branch = "├── "
			}
		}
		line := prefix + branch + graphNodeLabel(nodes[id])
		if seen[id] {
			md += line + " (again)\n"
			return
		}
		seen[id] = true
		md += line + "\n"
		kids := append([]string(nil), children[id]...)
		sort.Strings(kids)
		childPrefix := prefix
		if depth > 0 {
			if last {
				childPrefix += "    "
			} else {
				childPrefix += "│   "
			}
		}
		for i, k := range kids {
			walk(k, childPrefix, i == len(kids)-1, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, "", true, 0)
	}
	// Cycles cannot reach a root, and neither can a node whose only
	// dependencies were cancelled away; list the leftovers rather than
	// silently dropping them.
	var orphans []string
	for _, n := range g.Nodes {
		if !seen[n.ID] {
			orphans = append(orphans, graphNodeLabel(n))
		}
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		md += "\nunreachable from any root:\n" + strings.Join(orphans, "\n") + "\n"
	}
	return md + "```\n"
}

func graphNodeLabel(n service.GraphNode) string {
	label := n.Title
	if label == "" {
		label = n.ID
	}
	return fmt.Sprintf("%s [%s] (%s)", label, n.State, n.ID)
}

// taskStates converts the --state flag values into wire states. Unknown
// values are passed through: the daemon rejects them naming the offender,
// which beats a client-side list drifting out of sync with the enum.
func taskStates(values []string) []api.TaskState {
	out := make([]api.TaskState, 0, len(values))
	for _, v := range values {
		out = append(out, api.TaskState(v))
	}
	return out
}

// taskLabel is the short human handle for a task: its title, or the first
// line of its prompt when it has none.
func taskLabel(t api.Task) string {
	if t.Title != "" {
		return t.Title
	}
	line := t.Prompt
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if len([]rune(line)) > 60 {
		line = string([]rune(line)[:60]) + "…"
	}
	return line
}

// fmtTask formats one task as a markdown section, mirroring fmtAgent.
func fmtTask(t api.Task) string {
	s := fmt.Sprintf("### `%s`\n\n", t.ID)
	s += fmt.Sprintf("Queue: `%s`  \nState: `%s`  \nWorkdir: `%s`  \n", t.QueueID, t.State, t.WorkDir)
	if t.Title != "" {
		s += fmt.Sprintf("Title: %s  \n", t.Title)
	}
	if len(t.DependsOn) > 0 {
		s += fmt.Sprintf("After: `%s`  \n", strings.Join(t.DependsOn, "`, `"))
	}
	if t.AgentID != "" {
		s += fmt.Sprintf("Agent: `%s` (transcript: `singl agents output --id %s`)  \n", t.AgentID, t.AgentID)
	}
	if t.Question != "" {
		s += fmt.Sprintf("Question: %s  \n", t.Question)
	}
	if t.Attempts > 0 || t.MaxRetries > 0 {
		s += fmt.Sprintf("Attempts: %d (max retries %d)  \n", t.Attempts, t.MaxRetries)
	}
	if t.OnFailure != "" {
		s += fmt.Sprintf("On failure: `%s`  \n", t.OnFailure)
	}
	if t.Priority != 0 {
		s += fmt.Sprintf("Priority: %d  \n", t.Priority)
	}
	if t.StartedAt != nil {
		s += fmt.Sprintf("Started: %s  \n", t.StartedAt.Format(time.DateTime))
	}
	if t.EndedAt != nil {
		s += fmt.Sprintf("Ended: %s  \n", t.EndedAt.Format(time.DateTime))
	}
	if t.Error != "" {
		s += fmt.Sprintf("Error: %s  \n", t.Error)
	}
	if t.Prompt != "" {
		s += fmt.Sprintf("\nPrompt:\n\n```\n%s\n```\n", t.Prompt)
	}
	return s
}
