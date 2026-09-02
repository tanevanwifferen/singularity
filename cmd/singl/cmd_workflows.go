package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// cmdWorkflows handles "singl workflows" — the project-level unit of
// isolation: one feature branch, one worktree per repo, for every repo in
// the project. Also reachable as "singl project workflows".
func cmdWorkflows(ctx context.Context, verb string, args []string) int {
	switch verb {
	case "list":
		return runWorkflowList(ctx, args)
	case "create":
		return runWorkflowCreate(ctx, args)
	case "remove":
		return runWorkflowRemove(ctx, args)
	case "discover":
		return runWorkflowDiscover(ctx, args)
	default:
		return nounHelp("workflows", verb)
	}
}

func runWorkflowList(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("workflow-list", flag.ContinueOnError)
	project := fs.String("project", "", "project handle (required)")
	if err := fs.Parse(args); err != nil {
		return 2
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
	baseDir := fs.String("base-dir", "", "base directory for worktrees (default ~/.worktrees/<project>)")
	if err := fs.Parse(args); err != nil {
		return 2
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
		if code := printJSON(wf); code != 0 {
			return code
		}
		return workflowExitCode(wf)
	}
	md := fmt.Sprintf("## Workflow created: `%s`\n\nState: `%s`  \nBase dir: `%s`  \nRepos: %d — one worktree per repo  \n\n",
		wf.BranchName, wf.State, wf.BaseDir, len(wf.Repos))
	for _, name := range sortedRepoNames(wf) {
		wr := wf.Repos[name]
		if wr.Error != "" {
			md += fmt.Sprintf("- `%s` — **failed**: %s  \n", name, wr.Error)
			continue
		}
		md += fmt.Sprintf("- `%s` — `%s`  \n", name, wr.WorktreePath)
	}
	if code := renderMarkdown(md); code != 0 {
		return code
	}
	return workflowExitCode(wf)
}

// runWorkflowRemove tears a workflow down across every repo: worktrees
// removed, local + remote feature branches deleted, workflow dropped from
// persistence once fully clean.
func runWorkflowRemove(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("workflow-remove", flag.ContinueOnError)
	project := fs.String("project", "", "project handle (required)")
	branch := fs.String("branch", "", "feature branch name (required)")
	if err := fs.Parse(args); err != nil {
		return 2
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
	wf, err := c.ProjectRemoveWorkflow(tctx, handle, *branch)
	if err != nil {
		return die(err)
	}
	if globals.json {
		if code := printJSON(wf); code != 0 {
			return code
		}
		return workflowExitCode(wf)
	}
	md := fmt.Sprintf("## Workflow removed: `%s`\n\n", wf.BranchName)
	for _, name := range sortedRepoNames(wf) {
		wr := wf.Repos[name]
		if wr.Error != "" {
			md += fmt.Sprintf("- `%s` — **failed**: %s  \n", name, wr.Error)
			continue
		}
		md += fmt.Sprintf("- `%s` — cleaned  \n", name)
	}
	if code := renderMarkdown(md); code != 0 {
		return code
	}
	return workflowExitCode(wf)
}

// sortedRepoNames returns the workflow's repo names in a stable order, so the
// rendered output does not shuffle between runs (map iteration).
func sortedRepoNames(wf *api.FeatureWorkflow) []string {
	names := make([]string, 0, len(wf.Repos))
	for name := range wf.Repos {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// workflowExitCode reports 1 when any repo in the workflow failed, so scripts
// notice a partially-created workflow instead of treating it as success.
func workflowExitCode(wf *api.FeatureWorkflow) int {
	for _, wr := range wf.Repos {
		if wr.Error != "" {
			fmt.Fprintln(os.Stderr, "error: one or more repos failed to get a worktree")
			return 1
		}
	}
	return 0
}

func runWorkflowDiscover(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("workflow-discover", flag.ContinueOnError)
	project := fs.String("project", "", "project handle (required)")
	if err := fs.Parse(args); err != nil {
		return 2
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
