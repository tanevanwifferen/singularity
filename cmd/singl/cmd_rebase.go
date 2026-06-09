package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func cmdRebase(ctx context.Context, verb string, args []string) int {
	switch verb {
	case "plan":
		return runRebasePlan(ctx, args)
	case "status":
		return runRebaseStatus(ctx, args)
	case "continue":
		return runRebaseContinue(ctx, args)
	case "skip":
		return runRebaseSkip(ctx, args)
	case "abort":
		return runRebaseAbort(ctx, args)
	case "onto-main":
		return runRebaseOntoMain(ctx, args)
	case "todo":
		return runRebaseTodo(ctx, args)
	case "context":
		return runRebaseContext(ctx, args)
	default:
		fmt.Fprintf(os.Stderr, "unknown rebase verb: %q\nverbs: plan status continue skip abort onto-main todo context\n", verb)
		return 2
	}
}

func runRebasePlan(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("rebase-plan", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	base := fs.String("base", "", "base branch/ref (required)")
	current := fs.String("current", "", "current branch/ref (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *base == "" || *current == "" {
		fmt.Fprintln(os.Stderr, "error: --repo, --base, and --current are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	commits, err := c.RebasePlan(tctx, repoPath, *base, *current)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(commits)
	}
	md := fmt.Sprintf("## Rebase plan: `%s` onto `%s`\n\n", *current, *base)
	if len(commits) == 0 {
		md += "_No commits to rebase._\n"
		return renderMarkdown(md)
	}
	md += "| Action | Hash | Author | Message |\n|--------|------|--------|---------|\n"
	for _, c := range commits {
		op := c.Operation.String()
		if op == "" {
			op = "pick"
		}
		msg := c.Message
		if len([]rune(msg)) > 60 {
			msg = string([]rune(msg)[:60]) + "…"
		}
		md += fmt.Sprintf("| %s | `%s` | %s | %s |\n", op, c.ShortSHA, c.Author, msg)
	}
	return renderMarkdown(md)
}

func runRebaseStatus(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("rebase-status", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" {
		fmt.Fprintln(os.Stderr, "error: --repo is required (or set --repo globally)")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	inProgress, commit, err := c.RebaseStatus(tctx, repoPath)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]any{"in_progress": inProgress, "commit": commit})
	}
	if !inProgress {
		return renderMarkdown("No rebase in progress.\n")
	}
	return renderMarkdown(fmt.Sprintf("## Rebase in progress\n\nCurrent commit: `%s`\n", commit))
}

func runRebaseContinue(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("rebase-continue", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" {
		fmt.Fprintln(os.Stderr, "error: --repo is required (or set --repo globally)")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.RebaseContinue(tctx, repoPath); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "continued"})
	}
	return renderMarkdown("Rebase continued.\n")
}

func runRebaseSkip(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("rebase-skip", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" {
		fmt.Fprintln(os.Stderr, "error: --repo is required (or set --repo globally)")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.RebaseSkip(tctx, repoPath); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "skipped"})
	}
	return renderMarkdown("Commit skipped.\n")
}

func runRebaseAbort(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("rebase-abort", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" {
		fmt.Fprintln(os.Stderr, "error: --repo is required (or set --repo globally)")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.RebaseAbort(tctx, repoPath); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "aborted"})
	}
	return renderMarkdown("Rebase aborted.\n")
}

func runRebaseOntoMain(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("rebase-onto-main", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" {
		fmt.Fprintln(os.Stderr, "error: --repo is required (or set --repo globally)")
		return 2
	}
	c, err := newStreamClient()
	if err != nil {
		return die(err)
	}
	defer c.Disconnect()

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	ch, cancel, err := c.RebaseOntoMain(ctx, repoPath)
	if err != nil {
		return die(err)
	}
	defer cancel()
	return drainSyncStream(ctx, ch)
}

func runRebaseTodo(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("rebase-todo", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	base := fs.String("base", "", "base branch/ref (required)")
	current := fs.String("current", "", "current branch/ref (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *base == "" || *current == "" {
		fmt.Fprintln(os.Stderr, "error: --repo, --base, and --current are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	commits, err := c.RebasePlan(tctx, repoPath, *base, *current)
	cancel()
	if err != nil {
		return die(err)
	}
	if len(commits) == 0 {
		return renderMarkdown("No commits to generate a todo for.\n")
	}
	tctx2, cancel2 := context.WithTimeout(ctx, 120*time.Second)
	defer cancel2()
	todo, err := c.RebaseGenerateTodo(tctx2, commits)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"todo": todo})
	}
	return renderMarkdown(fmt.Sprintf("## Rebase todo\n\n```\n%s\n```\n", todo))
}

func runRebaseContext(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("rebase-context", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	main := fs.String("main", "", "main/target branch (required)")
	conflicts := fs.String("conflicts", "", "comma-separated conflict file paths")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *main == "" {
		fmt.Fprintln(os.Stderr, "error: --repo and --main are required")
		return 2
	}
	var conflictFiles []string
	if *conflicts != "" {
		for _, f := range strings.Split(*conflicts, ",") {
			if f = strings.TrimSpace(f); f != "" {
				conflictFiles = append(conflictFiles, f)
			}
		}
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	ctxStr, err := c.RebaseContext(tctx, repoPath, *main, conflictFiles)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"context": ctxStr})
	}
	return renderMarkdown(fmt.Sprintf("## Rebase conflict context\n\n```\n%s\n```\n", ctxStr))
}
