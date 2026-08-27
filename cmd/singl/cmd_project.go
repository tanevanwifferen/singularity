package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

func cmdProject(ctx context.Context, verb string, args []string) int {
	switch verb {
	case "list":
		return runProjectList(ctx, args)
	case "status":
		return runProjectStatus(ctx, args)
	case "load":
		return runProjectLoad(ctx, args)
	case "info":
		return runProjectInfo(ctx, args)
	case "refresh":
		return runProjectRefresh(ctx, args)
	case "branch-check":
		return runProjectBranchCheck(ctx, args)
	case "context":
		return runProjectContext(ctx, args)
	case "workflows":
		return runProjectWorkflows(ctx, verb, args)
	default:
		return nounHelp("project", verb)
	}
}

func runProjectList(ctx context.Context, _ []string) int {
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	projects, err := c.ProjectList(tctx)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(projects)
	}
	md := fmt.Sprintf("## Projects (%d)\n\n", len(projects))
	for _, p := range projects {
		md += fmt.Sprintf("- `%s`  \n", p)
	}
	if len(projects) == 0 {
		md += "_No projects configured._\n"
	}
	return renderMarkdown(md)
}

func runProjectLoad(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("project-load", flag.ContinueOnError)
	name := fs.String("name", "", "project key (required)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if *name == "" {
		fmt.Fprintln(os.Stderr, "error: --name is required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	info, err := c.ProjectLoad(tctx, *name)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(info)
	}
	md := fmt.Sprintf("## Project: %s\n\n", info.Name)
	md += fmt.Sprintf("Handle: `%s`  \nKey: `%s`  \nRepos: %d  \n\n", info.Handle, info.Key, len(info.Repos))
	for _, r := range info.Repos {
		dirty := ""
		if r.Dirty {
			dirty = " *(dirty)*"
		}
		md += fmt.Sprintf("- **%s** `%s`%s — branch: `%s`  \n", r.Name, r.Path, dirty, r.CurrentBranch)
	}
	return renderMarkdown(md)
}

func runProjectInfo(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("project-info", flag.ContinueOnError)
	project := fs.String("project", "", "project handle (see: singl project list)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	handle := service.ProjectHandle(*project)
	if handle == "" {
		fmt.Fprintln(os.Stderr, "error: --project is required (use `singl project list` to see available handles)")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	info, err := c.ProjectInfo(tctx, handle)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(info)
	}
	loaded := ""
	if !info.Loaded {
		loaded = " *(not loaded)*"
	}
	md := fmt.Sprintf("## Project: %s%s\n\n", info.Name, loaded)
	md += fmt.Sprintf("Handle: `%s`  \nKey: `%s`  \nRepos: %d  \n\n", info.Handle, info.Key, len(info.Repos))
	for _, r := range info.Repos {
		dirty := ""
		if r.Dirty {
			dirty = " *(dirty)*"
		}
		md += fmt.Sprintf("- **%s** `%s`%s — branch: `%s`  \n", r.Name, r.Path, dirty, r.CurrentBranch)
	}
	if info.Context != "" {
		md += fmt.Sprintf("\n> %s\n", info.Context)
	}
	return renderMarkdown(md)
}

func runProjectRefresh(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("project-refresh", flag.ContinueOnError)
	project := fs.String("project", "", "project handle (see: singl project list)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	handle := service.ProjectHandle(*project)
	if handle == "" {
		fmt.Fprintln(os.Stderr, "error: --project is required (use `singl project list` to see available handles)")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	st, err := c.ProjectRefresh(tctx, handle)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(st)
	}
	md := fmt.Sprintf("## Project refreshed: %s\n\nRepos: %d\n", st.Name, st.RepoCount)
	return renderMarkdown(md)
}

func runProjectBranchCheck(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("project-branch-check", flag.ContinueOnError)
	project := fs.String("project", "", "project handle (see: singl project list)")
	branch := fs.String("branch", "", "branch name (required)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	handle := service.ProjectHandle(*project)
	if handle == "" || *branch == "" {
		fmt.Fprintln(os.Stderr, "error: --project and --branch are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	ex, err := c.ProjectBranchExists(tctx, handle, *branch)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(ex)
	}
	md := fmt.Sprintf("## Branch `%s` existence\n\n", ex.Branch)
	for repo, exists := range ex.Repos {
		mark := "✗"
		if exists {
			mark = "✓"
		}
		md += fmt.Sprintf("- %s `%s`  \n", mark, repo)
	}
	return renderMarkdown(md)
}

func runProjectContext(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("project-context", flag.ContinueOnError)
	project := fs.String("project", "", "project handle (see: singl project list)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	handle := service.ProjectHandle(*project)
	if handle == "" {
		fmt.Fprintln(os.Stderr, "error: --project is required (use `singl project list` to see available handles)")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	ctxStr, err := c.ProjectContextSummary(tctx, handle)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"context": ctxStr})
	}
	if ctxStr == "" {
		return renderMarkdown("_No context summary available._\n")
	}
	return renderMarkdown(fmt.Sprintf("## Project context\n\n%s\n", ctxStr))
}

func runProjectWorkflows(ctx context.Context, _ string, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: singl project workflows <list|create|discover>")
		return 2
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list":
		return runWorkflowList(ctx, rest)
	case "create":
		return runWorkflowCreate(ctx, rest)
	case "discover":
		return runWorkflowDiscover(ctx, rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown workflows sub-verb: %q\nverbs: list create discover\n", sub)
		return 2
	}
}

func runWorkflowList(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("workflow-list", flag.ContinueOnError)
	project := fs.String("project", "", "project handle (required)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	handle := service.ProjectHandle(*project)
	if handle == "" {
		fmt.Fprintln(os.Stderr, "error: --project is required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	wfs, err := c.ProjectLoadWorkflows(tctx, handle)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(wfs)
	}
	md := fmt.Sprintf("## Workflows (%d)\n\n", len(wfs))
	if len(wfs) == 0 {
		md += "_No workflows._\n"
		return renderMarkdown(md)
	}
	for _, wf := range wfs {
		if wf == nil {
			continue
		}
		md += fmt.Sprintf("### `%s`\n\n", wf.BranchName)
		md += fmt.Sprintf("State: `%s`  \nCreated: %s  \n",
			wf.State, wf.CreatedAt.Format("2006-01-02 15:04"))
		if wf.Error != "" {
			md += fmt.Sprintf("Error: %s  \n", wf.Error)
		}
		md += fmt.Sprintf("Repos: %d  \n\n", len(wf.Repos))
		for name, r := range wf.Repos {
			created := ""
			if r.WorktreeCreated {
				created = " (worktree created)"
			}
			md += fmt.Sprintf("- `%s` `%s`%s  \n", name, r.WorktreePath, created)
		}
		md += "\n---\n"
	}
	return renderMarkdown(md)
}

func runWorkflowCreate(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("workflow-create", flag.ContinueOnError)
	project := fs.String("project", "", "project handle (required)")
	branch := fs.String("branch", "", "feature branch name (required)")
	baseDir := fs.String("base-dir", "", "base directory for worktrees (default: ~/.worktrees/<project-slug>)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	handle := service.ProjectHandle(*project)
	if handle == "" || *branch == "" {
		fmt.Fprintln(os.Stderr, "error: --project and --branch are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	wf, err := c.ProjectCreateWorkflow(tctx, handle, *branch, *baseDir)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(wf)
	}
	md := fmt.Sprintf("## Workflow created: `%s`\n\nState: `%s`  \nRepos: %d  \n",
		wf.BranchName, wf.State, len(wf.Repos))
	return renderMarkdown(md)
}

func runWorkflowDiscover(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("workflow-discover", flag.ContinueOnError)
	project := fs.String("project", "", "project handle (required)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	handle := service.ProjectHandle(*project)
	if handle == "" {
		fmt.Fprintln(os.Stderr, "error: --project is required")
		return 2
	}
	c, err := newStreamClient()
	if err != nil {
		return die(err)
	}
	defer c.Disconnect()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ch, cancel, err := c.ProjectDiscoverWorkflowsAllRepos(ctx, handle, nil)
	if err != nil {
		return die(err)
	}
	defer cancel()
	return drainDiscoveryStream(ctx, ch)
}

func drainDiscoveryStream(ctx context.Context, ch <-chan service.DiscoveryProgressEvent) int {
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return 0
			}
			if ev.Err != "" {
				fmt.Fprintf(os.Stderr, "error: %s\n", ev.Err)
				return 1
			}
			if ev.Done {
				return 0
			}
			fmt.Printf("[%d/%d] %s\n", ev.Found, ev.Total, ev.RepoName)
		case <-ctx.Done():
			return 0
		}
	}
}

func runProjectStatus(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("project-status", flag.ContinueOnError)
	project := fs.String("project", "", "project handle (see: singl project list)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	handle := service.ProjectHandle(*project)
	if handle == "" {
		fmt.Fprintln(os.Stderr, "error: --project is required (use `singl project list` to see available handles)")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	st, err := c.ProjectStatus(tctx, handle)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(st)
	}
	md := fmt.Sprintf("## Project Status: %s (%d repos)\n\n", st.Name, st.RepoCount)
	if st.DirtyCount > 0 {
		md += fmt.Sprintf("**%d dirty**, %d errors  \n\n", st.DirtyCount, st.ErrorCount)
	}
	for _, r := range st.Repos {
		dirty := ""
		if r.IsDirty {
			dirty = " *(dirty)*"
		}
		errNote := ""
		if r.Error != "" {
			errNote = fmt.Sprintf(" — **error:** %s", r.Error)
		}
		md += fmt.Sprintf("**%s** `%s`%s%s  \n", r.Name, r.Path, dirty, errNote)
		md += fmt.Sprintf("Branch: `%s` (default: `%s`) — HEAD: `%s`  \n\n",
			r.CurrentBranch, r.DefaultBranch, r.HEAD)
	}
	return renderMarkdown(md)
}
