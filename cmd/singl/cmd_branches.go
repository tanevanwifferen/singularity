package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func cmdBranches(ctx context.Context, verb string, args []string) int {
	switch verb {
	case "list":
		return runBranchesList(ctx, args)
	case "checkout":
		return runBranchesCheckout(ctx, args)
	case "create":
		return runBranchesCreate(ctx, args)
	case "delete":
		return runBranchesDelete(ctx, args)
	case "head":
		return runBranchesHead(ctx, args)
	case "compare":
		return runBranchesCompare(ctx, args)
	default:
		fmt.Fprintf(os.Stderr, "unknown branches verb: %q\nverbs: list checkout create delete head compare\n", verb)
		return 2
	}
}

func runBranchesList(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("branches-list", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	if err := fs.Parse(args); err != nil {
		return 2
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
	branches, err := c.BranchList(tctx, repoPath)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(branches)
	}
	md := fmt.Sprintf("## Branches for `%s`\n\n", repoPath)
	for _, b := range branches {
		upstream := ""
		if b.Upstream != "" {
			upstream = fmt.Sprintf(" (upstream: `%s`)", b.Upstream)
		}
		sync := ""
		if b.Ahead > 0 || b.Behind > 0 {
			sync = fmt.Sprintf(" ↑%d ↓%d", b.Ahead, b.Behind)
		}
		md += fmt.Sprintf("- `%s`%s%s  \n", b.Name, sync, upstream)
	}
	if len(branches) == 0 {
		md += "_No branches._\n"
	}
	return renderMarkdown(md)
}

func runBranchesCheckout(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("branches-checkout", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path (required)")
	branch := fs.String("branch", "", "branch name (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *branch == "" {
		fmt.Fprintln(os.Stderr, "error: --repo and --branch are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.BranchCheckout(tctx, repoPath, *branch); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "checked_out", "branch": *branch})
	}
	return renderMarkdown(fmt.Sprintf("Checked out `%s`.\n", *branch))
}

func runBranchesCreate(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("branches-create", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path (required)")
	branch := fs.String("branch", "", "branch name (required)")
	from := fs.String("start-point", "", "start point ref (empty = HEAD)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *branch == "" {
		fmt.Fprintln(os.Stderr, "error: --repo and --branch are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.BranchCreate(tctx, repoPath, *branch, *from); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "created", "branch": *branch})
	}
	return renderMarkdown(fmt.Sprintf("Branch `%s` created.\n", *branch))
}

func runBranchesDelete(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("branches-delete", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path (required)")
	branch := fs.String("branch", "", "branch name (required)")
	force := fs.Bool("force", false, "force delete unmerged branch")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *branch == "" {
		fmt.Fprintln(os.Stderr, "error: --repo and --branch are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.BranchDelete(tctx, repoPath, *branch, *force); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "deleted", "branch": *branch})
	}
	return renderMarkdown(fmt.Sprintf("Branch `%s` deleted.\n", *branch))
}

func runBranchesHead(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("branches-head", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	if err := fs.Parse(args); err != nil {
		return 2
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
	head, err := c.BranchHEAD(tctx, repoPath)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"head": head})
	}
	return renderMarkdown(fmt.Sprintf("HEAD: `%s`\n", head))
}

func runBranchesCompare(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("branches-compare", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path (required)")
	base := fs.String("base", "", "base branch (required)")
	head := fs.String("head", "", "head branch (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *base == "" || *head == "" {
		fmt.Fprintln(os.Stderr, "error: --repo, --base, and --head are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	cmp, err := c.BranchCompare(tctx, repoPath, *base, *head)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(cmp)
	}
	diverged := ""
	if cmp.Diverged {
		diverged = " **(diverged)**"
	}
	md := fmt.Sprintf("## Branch comparison: `%s` vs `%s`%s\n\n", cmp.BranchA, cmp.BranchB, diverged)
	md += fmt.Sprintf("`%s` is **%d ahead**, **%d behind** `%s`  \n", cmp.BranchA, cmp.Ahead, cmp.Behind, cmp.BranchB)
	return renderMarkdown(md)
}
