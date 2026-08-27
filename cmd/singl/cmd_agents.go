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
	case "watch":
		return runAgentsWatch(ctx, args)
	case "watch-all":
		return runAgentsWatchAll(ctx, args)
	case "chat":
		return runAgentsChat(ctx, args)
	case "stats":
		return runAgentsStats(ctx, args)
	default:
		return nounHelp("agents", verb)
	}
}

// agentJSON is the compact CLI JSON projection of an agent snapshot. The
// full task prompt can be kilobytes; it is only included with --full so
// polling `agents get/list --json` stays cheap for scripted consumers.
type agentJSON struct {
	ID           string     `json:"id"`
	State        string     `json:"state"`
	WorkDir      string     `json:"work_dir"`
	Summary      string     `json:"summary,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	DurationSecs *float64   `json:"duration_secs,omitempty"`
	ExitCode     *int       `json:"exit_code,omitempty"`
	Error        string     `json:"error,omitempty"`
	TotalCostUSD float64    `json:"total_cost_usd,omitempty"`
	MergeResult  string     `json:"merge_result,omitempty"`
	Task         string     `json:"task,omitempty"`
}

// agentToJSON projects a wire DTO into the CLI JSON shape. duration_secs is
// EndedAt-StartedAt for finished agents and time-since-start for running
// ones. The task prompt is included only when full is true.
func agentToJSON(a api.AgentSnapshotDTO, full bool) agentJSON {
	out := agentJSON{
		ID:           a.ID,
		State:        a.State,
		WorkDir:      a.WorkDir,
		Summary:      a.Summary,
		CreatedAt:    a.CreatedAt,
		StartedAt:    a.StartedAt,
		EndedAt:      a.EndedAt,
		ExitCode:     a.ExitCode,
		Error:        a.Error,
		TotalCostUSD: a.TotalCostUSD,
		MergeResult:  a.MergeResult,
	}
	if a.StartedAt != nil {
		end := time.Now()
		if a.EndedAt != nil {
			end = *a.EndedAt
		}
		secs := end.Sub(*a.StartedAt).Round(time.Millisecond).Seconds()
		out.DurationSecs = &secs
	}
	if full {
		out.Task = a.Task
	}
	return out
}

// outputEntryJSON is the CLI JSON shape for one output entry. It exposes the
// entry kind as "type" (text | tool_use | tool_result | system | error |
// result | user_input) so tool calls and assistant text are distinguishable;
// "source" is kept as an alias for older consumers.
type outputEntryJSON struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Source    string    `json:"source"`
	Content   string    `json:"content"`
	ToolName  string    `json:"tool_name,omitempty"`
	ToolID    string    `json:"tool_id,omitempty"`
	IsError   bool      `json:"is_error,omitempty"`
}

func outputEntriesToJSON(entries []api.OutputEntry) []outputEntryJSON {
	out := make([]outputEntryJSON, len(entries))
	for i, e := range entries {
		out[i] = outputEntryJSON{
			Timestamp: e.Timestamp,
			Type:      e.Source,
			Source:    e.Source,
			Content:   e.Content,
			ToolName:  e.ToolName,
			ToolID:    e.ToolID,
			IsError:   e.IsError,
		}
	}
	return out
}

// tailEntries returns the last n entries (all of them when n <= 0).
func tailEntries(entries []api.OutputEntry, n int) []api.OutputEntry {
	if n <= 0 || n >= len(entries) {
		return entries
	}
	return entries[len(entries)-n:]
}

func runAgentsList(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("agents-list", flag.ContinueOnError)
	full := fs.Bool("full", false, "include the full task prompt in --json output")
	if code, done := parseArgs(fs, args); done {
		return code
	}
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
		out := make([]agentJSON, len(agents))
		for i, a := range agents {
			out[i] = agentToJSON(a, *full)
		}
		return printJSON(out)
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
	full := fs.Bool("full", false, "include the full task prompt in --json output")
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
			return printJSON(map[string]any{
				"agent":  agentToJSON(*a, *full),
				"output": outputEntriesToJSON(tailEntries(entries, *last)),
			})
		}
		return printJSON(agentToJSON(*a, *full))
	}
	md := fmtAgent(*a)
	if *last > 0 {
		entries, err := c.AgentOutput(tctx, *id, 0)
		if err == nil && len(entries) > 0 {
			shown := tailEntries(entries, *last)
			md += fmt.Sprintf("\n#### Last %d output lines\n\n```\n", len(shown))
			for _, e := range shown {
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
	smartRoute := fs.Bool("smart-route", false, "enable smart LLM routing")
	maxTurns := fs.Int("max-turns", 0, "max agent turns (0 = unlimited)")
	timeout := fs.Int("timeout", 0, "timeout in seconds (0 = no timeout)")
	backend := fs.String("backend", "", "agent backend: claude or pi (default: daemon default)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if *workdir == "" || *prompt == "" {
		fmt.Fprintln(os.Stderr, "error: --workdir and --prompt are required")
		return 2
	}
	opts := api.AgentOptions{
		Model:       *model,
		Effort:      *effort,
		SmartRoute:  *smartRoute,
		MaxTurns:    *maxTurns,
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
	tail := fs.Int("tail", 0, "only show the last N entries")
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
	entries, err := c.AgentOutput(tctx, *id, *offset)
	if err != nil {
		return die(err)
	}
	entries = tailEntries(entries, *tail)
	if globals.json {
		return printJSON(outputEntriesToJSON(entries))
	}
	md := fmt.Sprintf("## Output for agent `%s`\n\n", *id)
	for _, e := range entries {
		switch e.Source {
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
	smartRoute := fs.Bool("smart-route", false, "enable smart LLM routing")
	maxTurns := fs.Int("max-turns", 0, "max agent turns")
	timeout := fs.Int("timeout", 0, "timeout in seconds")
	backend := fs.String("backend", "", "agent backend: claude or pi (default: daemon default)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if *id == "" || *message == "" {
		fmt.Fprintln(os.Stderr, "error: --id and --message are required")
		return 2
	}
	opts := api.AgentOptions{
		Model:       *model,
		Effort:      *effort,
		SmartRoute:  *smartRoute,
		MaxTurns:    *maxTurns,
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
	if a.ExitCode != nil {
		s += fmt.Sprintf("Exit code: %d  \n", *a.ExitCode)
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
