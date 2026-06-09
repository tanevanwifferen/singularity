package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
)

func cmdRepos(ctx context.Context, verb string, args []string) int {
	switch verb {
	case "info":
		return runReposInfo(ctx, args)
	case "open":
		return runReposOpen(ctx, args)
	case "find":
		return runReposFind(ctx, args)
	default:
		fmt.Fprintf(os.Stderr, "unknown repos verb: %q\nverbs: info open find\n", verb)
		return 2
	}
}

func runReposInfo(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("repos-info", flag.ContinueOnError)
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
	info, err := c.RepoInfo(tctx, repoPath)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(info)
	}
	dirty := ""
	if info.IsDirty {
		dirty = " *(dirty)*"
	}
	md := fmt.Sprintf("## Repo `%s`%s\n\n", info.Path, dirty)
	md += fmt.Sprintf("Branch: `%s`  \nHEAD: `%s`  \n", info.CurrentBranch, info.HEAD)
	if len(info.Remotes) > 0 {
		names := make([]string, len(info.Remotes))
		for i, r := range info.Remotes {
			names[i] = r.Name
		}
		md += fmt.Sprintf("Remotes: %s  \n", strings.Join(names, ", "))
	}
	md += fmt.Sprintf("Branches: %d  \n", len(info.Branches))
	return renderMarkdown(md)
}

func runReposOpen(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("repos-open", flag.ContinueOnError)
	path := fs.String("path", "", "path to open (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *path == "" {
		fmt.Fprintln(os.Stderr, "error: --path is required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	info, err := c.RepoOpen(tctx, *path)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(info)
	}
	dirty := ""
	if info.IsDirty {
		dirty = " *(dirty)*"
	}
	md := fmt.Sprintf("Opened `%s`%s — branch: `%s`  \n", info.Path, dirty, info.CurrentBranch)
	return renderMarkdown(md)
}

func runReposFind(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("repos-find", flag.ContinueOnError)
	path := fs.String("path", "", "path to search from (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *path == "" {
		fmt.Fprintln(os.Stderr, "error: --path is required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	found, err := c.RepoFind(tctx, *path)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"path": found})
	}
	return renderMarkdown(fmt.Sprintf("Repo root: `%s`\n", found))
}
