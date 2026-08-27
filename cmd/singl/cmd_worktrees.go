package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func cmdWorktrees(ctx context.Context, verb string, args []string) int {
	switch verb {
	case "list":
		return runWorktreesList(ctx, args)
	case "create":
		return runWorktreesCreate(ctx, args)
	case "remove":
		return runWorktreesRemove(ctx, args)
	case "lock":
		return runWorktreesLock(ctx, args)
	case "unlock":
		return runWorktreesUnlock(ctx, args)
	case "prune":
		return runWorktreesPrune(ctx, args)
	default:
		return nounHelp("worktrees", verb)
	}
}

func runWorktreesList(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("worktrees-list", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	if code, done := parseArgs(fs, args); done {
		return code
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
	worktrees, err := c.WorktreeList(tctx, repoPath)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(worktrees)
	}
	md := fmt.Sprintf("## Worktrees for `%s`\n\n", repoPath)
	for _, wt := range worktrees {
		md += fmt.Sprintf("**`%s`**  \n", wt.Path)
		branch := wt.Branch
		if branch == "" {
			branch = "(detached)"
		}
		md += fmt.Sprintf("Branch: `%s` — HEAD: `%s` — locked: %v  \n\n", branch, wt.HEAD, wt.Locked)
	}
	if len(worktrees) == 0 {
		md += "_No worktrees._\n"
	}
	return renderMarkdown(md)
}

func runWorktreesCreate(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("worktrees-create", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path (required)")
	path := fs.String("path", "", "worktree path (required)")
	branch := fs.String("branch", "", "branch name (required)")
	createBranch := fs.Bool("create-branch", false, "create the branch if it does not exist")
	startPoint := fs.String("start-point", "", "start point ref for new branch")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *path == "" || *branch == "" {
		fmt.Fprintln(os.Stderr, "error: --repo, --path, and --branch are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.WorktreeCreate(tctx, repoPath, *path, *branch, *createBranch, *startPoint); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "created", "path": *path, "branch": *branch})
	}
	return renderMarkdown(fmt.Sprintf("Worktree created at `%s` on branch `%s`.\n", *path, *branch))
}

func runWorktreesRemove(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("worktrees-remove", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path (required)")
	path := fs.String("path", "", "worktree path (required)")
	force := fs.Bool("force", false, "force removal even if dirty")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *path == "" {
		fmt.Fprintln(os.Stderr, "error: --repo and --path are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.WorktreeRemove(tctx, repoPath, *path, *force); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "removed", "path": *path})
	}
	return renderMarkdown(fmt.Sprintf("Worktree `%s` removed.\n", *path))
}

func runWorktreesLock(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("worktrees-lock", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path (required)")
	path := fs.String("path", "", "worktree path (required)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *path == "" {
		fmt.Fprintln(os.Stderr, "error: --repo and --path are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.WorktreeLock(tctx, repoPath, *path); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "locked", "path": *path})
	}
	return renderMarkdown(fmt.Sprintf("Worktree `%s` locked.\n", *path))
}

func runWorktreesUnlock(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("worktrees-unlock", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path (required)")
	path := fs.String("path", "", "worktree path (required)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *path == "" {
		fmt.Fprintln(os.Stderr, "error: --repo and --path are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.WorktreeUnlock(tctx, repoPath, *path); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "unlocked", "path": *path})
	}
	return renderMarkdown(fmt.Sprintf("Worktree `%s` unlocked.\n", *path))
}

func runWorktreesPrune(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("worktrees-prune", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path (required)")
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
	if err := c.WorktreePrune(tctx, repoPath); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "pruned", "repo": repoPath})
	}
	return renderMarkdown(fmt.Sprintf("Worktrees pruned for `%s`.\n", repoPath))
}
