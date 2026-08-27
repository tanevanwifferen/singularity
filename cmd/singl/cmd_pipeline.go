package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
)

func cmdPipeline(ctx context.Context, verb string, args []string) int {
	switch verb {
	case "status":
		return runPipelineStatus(ctx, args)
	default:
		return nounHelp("pipeline", verb)
	}
}

func runPipelineStatus(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("pipeline-status", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	branch := fs.String("branch", "", "specific branch (empty = all tracked branches)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	repoPath := repoArg(*repo)
	if repoPath == "" {
		fmt.Fprintln(os.Stderr, "error: --repo is required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()

	// Fetch all branches for pipeline statuses.
	allBranches, err := c.BranchList(tctx, repoPath)
	if err != nil {
		return die(err)
	}

	// Filter to requested branch if set.
	bList := allBranches
	if *branch != "" {
		bList = nil
		for _, b := range allBranches {
			if b.Name == *branch {
				bList = append(bList, b)
				break
			}
		}
	}

	pipelines, err := c.PipelineStatuses(tctx, repoPath, bList)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(pipelines)
	}
	md := fmt.Sprintf("## Pipelines for `%s`\n\n", repoPath)
	keys := make([]string, 0, len(pipelines))
	for k := range pipelines {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, branchName := range keys {
		info := pipelines[branchName]
		status := "unknown"
		if info != nil {
			status = string(info.Status)
			if !info.HasPipeline {
				status = "none"
			}
		}
		md += fmt.Sprintf("- `%s` — %s  \n", branchName, status)
	}
	if len(pipelines) == 0 {
		md += "_No pipeline data._\n"
	}
	return renderMarkdown(md)
}
