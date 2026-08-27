package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
)

func cmdMR(ctx context.Context, verb string, args []string) int {
	switch verb {
	case "title":
		return runMRTitle(ctx, args)
	case "desc":
		return runMRDesc(ctx, args)
	case "create":
		return runMRCreate(ctx, args)
	case "cli":
		return runMRCLI(ctx, args)
	default:
		return nounHelp("mr", verb)
	}
}

func runMRTitle(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("mr-title", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	source := fs.String("source", "", "source branch (required)")
	target := fs.String("target", "", "target branch (required)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *source == "" || *target == "" {
		fmt.Fprintln(os.Stderr, "error: --repo, --source, and --target are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	title, err := c.MRGenerateTitle(tctx, repoPath, *source, *target)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"title": title})
	}
	return renderMarkdown(fmt.Sprintf("## Suggested MR title\n\n> %s\n", title))
}

func runMRDesc(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("mr-desc", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	source := fs.String("source", "", "source branch (required)")
	target := fs.String("target", "", "target branch (required)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *source == "" || *target == "" {
		fmt.Fprintln(os.Stderr, "error: --repo, --source, and --target are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	desc, err := c.MRGenerateDescription(tctx, repoPath, *source, *target)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"description": desc})
	}
	return renderMarkdown(fmt.Sprintf("## Suggested MR description\n\n%s\n", desc))
}

func runMRCreate(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("mr-create", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	source := fs.String("source", "", "source branch (required)")
	target := fs.String("target", "", "target branch (required)")
	title := fs.String("title", "", "MR title (required)")
	desc := fs.String("desc", "", "MR description")
	reviewers := fs.String("reviewers", "", "comma-separated reviewer usernames")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *source == "" || *target == "" || *title == "" {
		fmt.Fprintln(os.Stderr, "error: --repo, --source, --target, and --title are required")
		return 2
	}
	var reviewerList []string
	if *reviewers != "" {
		for _, r := range strings.Split(*reviewers, ",") {
			if r = strings.TrimSpace(r); r != "" {
				reviewerList = append(reviewerList, r)
			}
		}
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	mr, err := c.MRCreate(tctx, repoPath, *source, *target, *title, *desc, reviewerList)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(mr)
	}
	url := mr.URL
	if url == "" {
		url = mr.WebURL
	}
	md := fmt.Sprintf("## MR created\n\n**%s**  \n", mr.Title)
	md += fmt.Sprintf("Branch: `%s` → `%s`  \n", mr.SourceBranch, mr.TargetBranch)
	if mr.Number > 0 {
		md += fmt.Sprintf("Number: `#%d`  \n", mr.Number)
	}
	if url != "" {
		md += fmt.Sprintf("URL: %s  \n", url)
	} else {
		md += "URL: _(not returned by forge)_  \n"
	}
	return renderMarkdown(md)
}

func runMRCLI(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("mr-cli", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	base := fs.String("base", "", "base branch (optional)")
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
	provider, err := c.ForgeDetectProvider(tctx, repoPath)
	if err != nil {
		return die(err)
	}
	res, err := c.MRCreateCLI(tctx, repoPath, provider, *base)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(res)
	}
	md := "## MR created via CLI\n\n"
	if res.URL != "" {
		md += fmt.Sprintf("URL: %s  \n", res.URL)
	}
	if res.Content != nil {
		md += fmt.Sprintf("Title: %s  \n", res.Content.Title)
	}
	return renderMarkdown(md)
}
