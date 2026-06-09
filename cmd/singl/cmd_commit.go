package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
)

func cmdCommit(ctx context.Context, verb string, args []string) int {
	switch verb {
	case "suggest":
		return runCommitSuggest(ctx, args)
	case "generate":
		return runCommitGenerate(ctx, args)
	case "files":
		return runCommitFiles(ctx, args)
	case "diff":
		return runCommitDiff(ctx, args)
	case "file-diff":
		return runCommitFileDiff(ctx, args)
	case "cherry-pick":
		return runCommitCherryPick(ctx, args)
	case "reset":
		return runCommitReset(ctx, args)
	case "amend":
		return runCommitAmend(ctx, args)
	default:
		fmt.Fprintf(os.Stderr, "unknown commit verb: %q\nverbs: suggest generate files diff file-diff cherry-pick reset amend\n", verb)
		return 2
	}
}

func runCommitSuggest(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("commit-suggest", flag.ContinueOnError)
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
	msg, err := c.CommitSuggestMessage(tctx, repoPath)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"message": msg})
	}
	return renderMarkdown(fmt.Sprintf("## Suggested commit message\n\n> %s\n", msg))
}

func runCommitGenerate(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("commit-generate", flag.ContinueOnError)
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
	msg, err := c.CommitGenerateMessage(tctx, repoPath)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(msg)
	}
	md := "## Generated commit message\n\n"
	header := msg.Subject
	if msg.Type != "" {
		if msg.Scope != "" {
			header = fmt.Sprintf("%s(%s): %s", msg.Type, msg.Scope, msg.Subject)
		} else {
			header = fmt.Sprintf("%s: %s", msg.Type, msg.Subject)
		}
	}
	md += fmt.Sprintf("**%s**\n\n", header)
	if msg.Body != "" {
		md += fmt.Sprintf("> %s\n\n", strings.ReplaceAll(msg.Body, "\n", "\n> "))
	}
	if len(msg.Footers) > 0 {
		md += strings.Join(msg.Footers, "  \n") + "\n"
	}
	if msg.Full != "" {
		md += fmt.Sprintf("\n```\n%s\n```\n", msg.Full)
	}
	return renderMarkdown(md)
}

func runCommitFiles(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("commit-files", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	hash := fs.String("hash", "", "commit hash (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *hash == "" {
		fmt.Fprintln(os.Stderr, "error: --repo and --hash are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	files, err := c.CommitFiles(tctx, repoPath, *hash)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(files)
	}
	md := fmt.Sprintf("## Files in `%s`\n\n", *hash)
	if len(files) == 0 {
		md += "_No files changed._\n"
		return renderMarkdown(md)
	}
	for _, f := range files {
		switch f.Status {
		case "R":
			md += fmt.Sprintf("`R` `%s` → `%s`  \n", f.OldPath, f.NewPath)
		case "A", "M", "D", "C":
			path := f.NewPath
			if path == "" {
				path = f.OldPath
			}
			stat := ""
			if f.Additions > 0 || f.Deletions > 0 {
				stat = fmt.Sprintf(" (+%d/-%d)", f.Additions, f.Deletions)
			}
			md += fmt.Sprintf("`%s` `%s`%s  \n", f.Status, path, stat)
		default:
			path := f.NewPath
			if path == "" {
				path = f.OldPath
			}
			md += fmt.Sprintf("`?` `%s`  \n", path)
		}
	}
	return renderMarkdown(md)
}

func runCommitDiff(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("commit-diff", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	hash := fs.String("hash", "", "commit hash (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *hash == "" {
		fmt.Fprintln(os.Stderr, "error: --repo and --hash are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	diff, err := c.CommitFullDiff(tctx, repoPath, *hash)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"diff": diff})
	}
	if diff == "" {
		return renderMarkdown(fmt.Sprintf("No diff for commit `%s`.\n", *hash))
	}
	return renderMarkdown(fmt.Sprintf("## Diff: `%s`\n\n```diff\n%s\n```\n", *hash, diff))
}

func runCommitFileDiff(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("commit-file-diff", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	hash := fs.String("hash", "", "commit hash (required)")
	file := fs.String("file", "", "file path (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *hash == "" || *file == "" {
		fmt.Fprintln(os.Stderr, "error: --repo, --hash, and --file are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	diff, err := c.CommitFileDiff(tctx, repoPath, *hash, *file)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"diff": diff})
	}
	if diff == "" {
		return renderMarkdown(fmt.Sprintf("No diff for `%s` in commit `%s`.\n", *file, *hash))
	}
	return renderMarkdown(fmt.Sprintf("## `%s` in `%s`\n\n```diff\n%s\n```\n", *file, *hash, diff))
}

func runCommitCherryPick(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("commit-cherry-pick", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	hash := fs.String("hash", "", "commit hash (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *hash == "" {
		fmt.Fprintln(os.Stderr, "error: --repo and --hash are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.CommitCherryPick(tctx, repoPath, *hash); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "cherry-picked", "hash": *hash})
	}
	return renderMarkdown(fmt.Sprintf("Cherry-picked `%s`.\n", *hash))
}

func runCommitReset(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("commit-reset", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	hash := fs.String("hash", "", "commit hash (required)")
	mode := fs.String("mode", "mixed", "reset mode: soft|mixed|hard")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *hash == "" {
		fmt.Fprintln(os.Stderr, "error: --repo and --hash are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.CommitReset(tctx, repoPath, *hash, *mode); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "reset", "hash": *hash, "mode": *mode})
	}
	return renderMarkdown(fmt.Sprintf("Reset to `%s` (%s).\n", *hash, *mode))
}

func runCommitAmend(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("commit-amend", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	message := fs.String("message", "", "new commit message (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *message == "" {
		fmt.Fprintln(os.Stderr, "error: --repo and --message are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.CommitAmend(tctx, repoPath, *message); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "amended"})
	}
	return renderMarkdown("Commit amended.\n")
}
