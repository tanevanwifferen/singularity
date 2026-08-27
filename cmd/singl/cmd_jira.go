package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

func cmdJira(ctx context.Context, verb string, args []string) int {
	switch verb {
	case "search":
		return runJiraSearch(ctx, args)
	case "get":
		return runJiraGet(ctx, args)
	case "mine":
		return runJiraMine(ctx, args)
	case "create":
		return runJiraCreate(ctx, args)
	case "update":
		return runJiraUpdate(ctx, args)
	case "comment":
		return runJiraComment(ctx, args)
	case "link":
		return runJiraLink(ctx, args)
	case "ai":
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "usage: singl jira ai <refine|stories|review>")
			return 2
		}
		sub := args[0]
		rest := args[1:]
		switch sub {
		case "refine":
			return runJiraAIRefine(ctx, rest)
		case "stories":
			return runJiraAIStories(ctx, rest)
		case "review":
			return runJiraAIReview(ctx, rest)
		default:
			fmt.Fprintf(os.Stderr, "unknown jira ai sub-verb: %q\nverbs: refine stories review\n", sub)
			return 2
		}
	default:
		return nounHelp("jira", verb)
	}
}

// fmtIssue renders one Jira issue as a markdown section.
func fmtIssue(issue api.Issue) string {
	s := fmt.Sprintf("### %s — %s\n\n", issue.Key, issue.Summary)
	s += fmt.Sprintf("Type: `%s` | Priority: `%s` | Status: `%s`  \n", issue.Type, issue.Priority, issue.Status)
	if issue.Assignee != "" {
		s += fmt.Sprintf("Assignee: %s  \n", issue.Assignee)
	}
	if issue.Sprint != "" {
		s += fmt.Sprintf("Sprint: %s  \n", issue.Sprint)
	}
	if len(issue.Labels) > 0 {
		labels := make([]string, len(issue.Labels))
		for i, l := range issue.Labels {
			labels[i] = "`" + l + "`"
		}
		s += fmt.Sprintf("Labels: %s  \n", strings.Join(labels, ", "))
	}
	if issue.Description != "" {
		desc := issue.Description
		if len([]rune(desc)) > 300 {
			desc = string([]rune(desc)[:300]) + "…"
		}
		s += fmt.Sprintf("\n> %s\n", strings.ReplaceAll(desc, "\n", "\n> "))
	}
	s += "\n---\n"
	return s
}

func runJiraSearch(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("jira-search", flag.ContinueOnError)
	jql := fs.String("jql", "", "JQL query (required)")
	max := fs.Int("max", 20, "max results")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if *jql == "" {
		fmt.Fprintln(os.Stderr, "error: --jql is required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	res, err := c.JiraSearchIssues(tctx, *jql, *max)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(res)
	}
	md := fmt.Sprintf("## Jira search (%d of %d)\n\n", len(res.Issues), res.Total)
	if len(res.Issues) == 0 {
		md += "_No issues found._\n"
		return renderMarkdown(md)
	}
	for _, issue := range res.Issues {
		md += fmtIssue(issue)
	}
	return renderMarkdown(md)
}

func runJiraGet(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("jira-get", flag.ContinueOnError)
	key := fs.String("key", "", "issue key e.g. PROJ-123 (required)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if *key == "" {
		fmt.Fprintln(os.Stderr, "error: --key is required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	issue, err := c.JiraGetIssue(tctx, *key)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(issue)
	}
	return renderMarkdown(fmtIssue(*issue))
}

func runJiraMine(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("jira-mine", flag.ContinueOnError)
	project := fs.String("project", "", "project key filter (optional)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	res, err := c.JiraGetMyIssues(tctx, *project)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(res)
	}
	md := fmt.Sprintf("## My Jira issues (%d)\n\n", len(res.Issues))
	if len(res.Issues) == 0 {
		md += "_No issues assigned to you._\n"
		return renderMarkdown(md)
	}
	for _, issue := range res.Issues {
		md += fmtIssue(issue)
	}
	return renderMarkdown(md)
}

func runJiraCreate(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("jira-create", flag.ContinueOnError)
	project := fs.String("project", "", "project key (required)")
	issueType := fs.String("type", "Task", "issue type e.g. Task, Bug, Story")
	summary := fs.String("summary", "", "issue summary (required)")
	desc := fs.String("desc", "", "description")
	priority := fs.String("priority", "", "priority e.g. High, Medium, Low")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if *project == "" || *summary == "" {
		fmt.Fprintln(os.Stderr, "error: --project and --summary are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	issue, err := c.JiraCreateIssue(tctx, *project, *issueType, *summary, *desc, *priority)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(issue)
	}
	return renderMarkdown(fmt.Sprintf("## Issue created\n\n%s", fmtIssue(*issue)))
}

func runJiraUpdate(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("jira-update", flag.ContinueOnError)
	key := fs.String("key", "", "issue key (required)")
	field := fs.String("field", "", "field name (required)")
	value := fs.String("value", "", "field value (required)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if *key == "" || *field == "" || *value == "" {
		fmt.Fprintln(os.Stderr, "error: --key, --field, and --value are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.JiraUpdateFields(tctx, *key, map[string]any{*field: *value}); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "updated", "key": *key})
	}
	return renderMarkdown(fmt.Sprintf("Updated `%s`: `%s` = `%s`.\n", *key, *field, *value))
}

func runJiraComment(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("jira-comment", flag.ContinueOnError)
	key := fs.String("key", "", "issue key (required)")
	body := fs.String("body", "", "comment body (required)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if *key == "" || *body == "" {
		fmt.Fprintln(os.Stderr, "error: --key and --body are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.JiraAddComment(tctx, *key, *body); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "commented", "key": *key})
	}
	return renderMarkdown(fmt.Sprintf("Comment added to `%s`.\n", *key))
}

func runJiraLink(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("jira-link", flag.ContinueOnError)
	from := fs.String("from", "", "inward issue key (required)")
	to := fs.String("to", "", "outward issue key (required)")
	linkType := fs.String("type", "", "link type e.g. 'blocks', 'relates to' (required)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if *from == "" || *to == "" || *linkType == "" {
		fmt.Fprintln(os.Stderr, "error: --from, --to, and --type are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.JiraLinkIssues(tctx, *from, *to, *linkType); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "linked", "from": *from, "to": *to})
	}
	return renderMarkdown(fmt.Sprintf("Linked `%s` → `%s` (%s).\n", *from, *to, *linkType))
}

// AI subcommands spawn agents; they return the agent ID for follow-up watching.

func runJiraAIRefine(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("jira-ai-refine", flag.ContinueOnError)
	key := fs.String("key", "", "issue key (required)")
	repo := fs.String("repo", "", "repo path (optional)")
	focus := fs.String("focus", "", "focus area for refinement")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if *key == "" {
		fmt.Fprintln(os.Stderr, "error: --key is required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	issue, err := c.JiraGetIssue(tctx, *key)
	if err != nil {
		return die(err)
	}
	agentID, err := c.JiraRefineTicket(tctx, issue, repoArg(*repo), *focus, "")
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"agent_id": agentID})
	}
	return renderMarkdown(fmt.Sprintf("Refinement agent spawned for `%s`.\n\nAgent ID: `%s`\n\n"+
		"_Use `singl agents watch --id %s` to stream output._\n", *key, agentID, agentID))
}

func runJiraAIStories(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("jira-ai-stories", flag.ContinueOnError)
	key := fs.String("key", "", "issue key (required)")
	repo := fs.String("repo", "", "repo path (optional)")
	project := fs.String("project", "", "Jira project key for created stories")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if *key == "" {
		fmt.Fprintln(os.Stderr, "error: --key is required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	issue, err := c.JiraGetIssue(tctx, *key)
	if err != nil {
		return die(err)
	}
	agentID, err := c.JiraCreateStories(tctx, issue, "", *project, repoArg(*repo), "")
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"agent_id": agentID})
	}
	return renderMarkdown(fmt.Sprintf("Story-creation agent spawned for `%s`.\n\nAgent ID: `%s`\n\n"+
		"_Use `singl agents watch --id %s` to stream output._\n", *key, agentID, agentID))
}

func runJiraAIReview(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("jira-ai-review", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path (optional)")
	instruction := fs.String("instruction", "", "review instruction")
	project := fs.String("project", "", "Jira project key to pull issues from")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	res, err := c.JiraGetMyIssues(tctx, *project)
	if err != nil {
		return die(err)
	}
	if len(res.Issues) == 0 {
		if globals.json {
			return printJSON(map[string]any{"agent_id": "", "issues_count": 0})
		}
		return renderMarkdown("No issues to review.\n")
	}
	agentID, err := c.JiraReviewTickets(tctx, res.Issues, repoArg(*repo), *instruction, "")
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]any{"agent_id": agentID, "issues_count": len(res.Issues)})
	}
	return renderMarkdown(fmt.Sprintf("Review agent spawned for %d issues.\n\nAgent ID: `%s`\n\n"+
		"_Use `singl agents watch --id %s` to stream output._\n", len(res.Issues), agentID, agentID))
}
