package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

func cmdAgents(ctx context.Context, verb string, args []string) int {
	switch verb {
	case "list":
		return runAgentsList(ctx, args)
	case "get":
		return runAgentsGet(ctx, args)
	case "spawn":
		return runAgentsSpawn(ctx, args)
	case "resume":
		return runAgentsResume(ctx, args)
	case "kill":
		return runAgentsKill(ctx, args)
	case "remove":
		return runAgentsRemove(ctx, args)
	case "output":
		return runAgentsOutput(ctx, args)
	case "input":
		return runAgentsInput(ctx, args)
	case "wait":
		return runAgentsWait(ctx, args)
	case "wait-all":
		return runAgentsWaitAll(ctx, args)
	case "watch":
		return runAgentsWatch(ctx, args)
	case "watch-all":
		return runAgentsWatchAll(ctx, args)
	case "chat":
		return runAgentsChat(ctx, args)
	case "stats":
		return runAgentsStats(ctx, args)
	default:
		fmt.Fprintf(os.Stderr, "unknown agents verb: %q\nverbs: list get spawn resume kill remove output input wait wait-all watch watch-all chat stats\n", verb)
		return 2
	}
}

func runAgentsList(ctx context.Context, _ []string) int {
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	agents, err := c.AgentList(tctx)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(agents)
	}
	md := fmt.Sprintf("## Agents (%d)\n\n", len(agents))
	for _, a := range agents {
		md += fmtAgent(a)
	}
	if len(agents) == 0 {
		md += "_No agents._\n"
	}
	return renderMarkdown(md)
}

func runAgentsGet(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("agents-get", flag.ContinueOnError)
	id := fs.String("id", "", "agent ID (required)")
	last := fs.Int("last", 0, "append last N output lines to the snapshot")
	if err := fs.Parse(args); err != nil {
		return 2
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
	a, err := c.AgentGet(tctx, *id)
	if err != nil {
		return die(err)
	}
	if globals.json {
		if *last > 0 {
			entries, oerr := c.AgentOutput(tctx, *id, 0)
			if oerr != nil {
				return die(oerr)
			}
			start := len(entries) - *last
			if start < 0 {
				start = 0
			}
			return printJSON(map[string]any{"agent": a, "output": entries[start:]})
		}
		return printJSON(a)
	}
	md := fmtAgent(*a)
	if *last > 0 {
		entries, err := c.AgentOutput(tctx, *id, 0)
		if err == nil && len(entries) > 0 {
			start := len(entries) - *last
			if start < 0 {
				start = 0
			}
			md += fmt.Sprintf("\n#### Last %d output lines\n\n```\n", len(entries[start:]))
			for _, e := range entries[start:] {
				if e.Content != "" {
					md += e.Content + "\n"
				}
			}
			md += "```\n"
		}
	}
	return renderMarkdown(md)
}

func runAgentsSpawn(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("agents-spawn", flag.ContinueOnError)
	workdir := fs.String("workdir", "", "working directory for the agent (required)")
	prompt := fs.String("prompt", "", "task prompt (required)")
	model := fs.String("model", "", "model override")
	effort := fs.String("effort", "", "effort level: low|medium|high")
	smartRoute := smartRouteFlags(fs)
	maxTurns := fs.Int("max-turns", 0, "max agent turns (0 = unlimited)")
	timeout := fs.Int("timeout", 0, "timeout in seconds (0 = no timeout)")
	useWorktree := fs.Bool("use-worktree", false, "run agent in isolated git worktree")
	backend := fs.String("backend", "", "agent backend: claude or pi (default: daemon default)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *workdir == "" || *prompt == "" {
		fmt.Fprintln(os.Stderr, "error: --workdir and --prompt are required")
		return 2
	}
	opts := api.AgentOptions{
		Model:       *model,
		Effort:      *effort,
		SmartRoute:  smartRoute(*model, *effort),
		MaxTurns:    *maxTurns,
		UseWorktree: *useWorktree,
		BackendName: *backend,
	}
	if *timeout > 0 {
		opts.Timeout = time.Duration(*timeout) * time.Second
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	agentID, err := c.AgentStart(tctx, *workdir, *prompt, opts)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"agent_id": agentID})
	}
	return renderMarkdown(fmt.Sprintf("Agent spawned.\n\nAgent ID: `%s`\n", agentID))
}

func runAgentsKill(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("agents-kill", flag.ContinueOnError)
	id := fs.String("id", "", "agent ID (required)")
	if err := fs.Parse(args); err != nil {
		return 2
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
	if err := c.AgentKill(tctx, *id); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "killed", "agent_id": *id})
	}
	return renderMarkdown(fmt.Sprintf("Agent `%s` killed.\n", *id))
}

func runAgentsRemove(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("agents-remove", flag.ContinueOnError)
	id := fs.String("id", "", "agent ID (required)")
	if err := fs.Parse(args); err != nil {
		return 2
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
	if err := c.AgentRemove(tctx, *id); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "removed", "agent_id": *id})
	}
	return renderMarkdown(fmt.Sprintf("Agent `%s` removed.\n", *id))
}

func runAgentsOutput(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("agents-output", flag.ContinueOnError)
	id := fs.String("id", "", "agent ID (required)")
	offset := fs.Int("offset", 0, "start offset")
	if err := fs.Parse(args); err != nil {
		return 2
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
	entries, err := c.AgentOutput(tctx, *id, *offset)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(entries)
	}
	md := fmt.Sprintf("## Output for agent `%s`\n\n", *id)
	for _, e := range entries {
		switch e.Source {
		case "user_input":
			md += fmt.Sprintf("**[prompt]** %s  \n", e.Content)
		case "tool_use":
			md += fmt.Sprintf("**[tool:%s]** %s  \n", e.ToolName, e.Content)
		case "tool_result":
			if e.Content != "" {
				md += fmt.Sprintf("**[result]** %s  \n", e.Content)
			}
		case "system":
			md += fmt.Sprintf("_[system]_ %s  \n", e.Content)
		case "error":
			md += fmt.Sprintf("**[error]** %s  \n", e.Content)
		default:
			if e.Content != "" {
				md += "> " + e.Content + "  \n"
			}
		}
	}
	if len(entries) == 0 {
		md += "_No output yet._\n"
	}
	return renderMarkdown(md)
}

func runAgentsResume(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("agents-resume", flag.ContinueOnError)
	id := fs.String("id", "", "agent ID to resume (required)")
	message := fs.String("message", "", "follow-up message (required)")
	model := fs.String("model", "", "model override")
	effort := fs.String("effort", "", "effort level: low|medium|high")
	smartRoute := smartRouteFlags(fs)
	maxTurns := fs.Int("max-turns", 0, "max agent turns")
	timeout := fs.Int("timeout", 0, "timeout in seconds")
	useWorktree := fs.Bool("use-worktree", false, "run agent in isolated git worktree")
	backend := fs.String("backend", "", "agent backend: claude or pi (default: daemon default)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *id == "" || *message == "" {
		fmt.Fprintln(os.Stderr, "error: --id and --message are required")
		return 2
	}
	opts := api.AgentOptions{
		Model:       *model,
		Effort:      *effort,
		SmartRoute:  smartRoute(*model, *effort),
		MaxTurns:    *maxTurns,
		UseWorktree: *useWorktree,
		BackendName: *backend,
	}
	if *timeout > 0 {
		opts.Timeout = time.Duration(*timeout) * time.Second
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	newID, err := c.AgentResume(tctx, *id, *message, opts)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"agent_id": newID, "resumed_from": *id})
	}
	return renderMarkdown(fmt.Sprintf("Agent resumed as new agent.\n\nAgent ID: `%s`\n", newID))
}

func runAgentsInput(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("agents-input", flag.ContinueOnError)
	id := fs.String("id", "", "agent ID (required)")
	message := fs.String("message", "", "message to send (required)")
	if err := fs.Parse(args); err != nil {
		return 2
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
	if err := c.AgentSendInput(tctx, *id, *message); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "sent", "agent_id": *id})
	}
	return renderMarkdown(fmt.Sprintf("Input sent to agent `%s`.\n", *id))
}

func runAgentsStats(ctx context.Context, _ []string) int {
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	stats, err := c.AgentStats(tctx)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(stats)
	}
	md := "## Agent Engine Stats\n\n"
	md += fmt.Sprintf("Active: **%d** / %d  \n", stats.Active, stats.MaxAgents)
	md += fmt.Sprintf("Total: %d — Completed: %d, Errored: %d, Killed: %d  \n",
		stats.Total, stats.Completed, stats.Errored, stats.Killed)
	return renderMarkdown(md)
}

// smartRouteFlags registers --smart-route and --no-smart-route on fs and
// returns a resolver to call after fs.Parse with the final --model/--effort
// values. Routing defaults to ON when the user pinned neither model nor
// effort; --no-smart-route always wins.
func smartRouteFlags(fs *flag.FlagSet) func(model, effort string) bool {
	sr := fs.Bool("smart-route", false, "force Haiku routing on (--smart-route=false forces off; default: on unless --model/--effort given)")
	nsr := fs.Bool("no-smart-route", false, "disable smart routing")
	return func(model, effort string) bool {
		explicit := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "smart-route" {
				explicit = true
			}
		})
		return resolveSmartRoute(explicit, *sr, *nsr, model, effort)
	}
}

// resolveSmartRoute decides whether to ask the daemon for Haiku routing.
// Precedence: --no-smart-route > explicit --smart-route[=bool] > default,
// where the default is ON only when the user gave neither model nor effort.
func resolveSmartRoute(explicit, smartRoute, noSmartRoute bool, model, effort string) bool {
	if noSmartRoute {
		return false
	}
	if explicit {
		return smartRoute
	}
	return model == "" && effort == ""
}

// fmtAgent formats a single AgentSnapshotDTO as a markdown section.
// Used by both `agents list` (many entries) and `agents get` (one entry).
func fmtAgent(a api.AgentSnapshotDTO) string {
	s := fmt.Sprintf("### `%s`\n\n", a.ID)
	s += fmt.Sprintf("State: `%s`  \n", a.State)
	s += fmt.Sprintf("Workdir: `%s`  \n", a.WorkDir)
	if a.Task != "" {
		task := a.Task
		if len([]rune(task)) > 120 {
			task = string([]rune(task)[:120]) + "…"
		}
		s += fmt.Sprintf("Task: %s  \n", task)
	}
	if a.Summary != "" {
		s += fmt.Sprintf("Summary: %s  \n", a.Summary)
	}
	if a.StartedAt != nil {
		s += fmt.Sprintf("Started: %s  \n", a.StartedAt.Format(time.DateTime))
	}
	if a.EndedAt != nil {
		s += fmt.Sprintf("Ended: %s  \n", a.EndedAt.Format(time.DateTime))
	}
	if a.TotalCostUSD > 0 {
		s += fmt.Sprintf("Cost: $%s  \n", strconv.FormatFloat(a.TotalCostUSD, 'f', 4, 64))
	}
	if a.Error != "" {
		s += fmt.Sprintf("Error: %s  \n", a.Error)
	}
	if a.MergeResult != "" {
		s += fmt.Sprintf("Merge: %s  \n", a.MergeResult)
	}
	s += "\n---\n"
	return s
}
