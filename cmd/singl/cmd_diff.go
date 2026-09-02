package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

func cmdDiff(ctx context.Context, verb string, args []string) int {
	switch verb {
	case "workdir":
		return runDiffWorkdir(ctx, args)
	case "branch":
		return runDiffBranch(ctx, args)
	case "file":
		return runDiffFile(ctx, args)
	case "staged":
		return runDiffStaged(ctx, args)
	case "unstaged":
		return runDiffUnstaged(ctx, args)
	case "merge-base":
		return runDiffMergeBase(ctx, args)
	case "all-repos":
		return runDiffAllRepos(ctx, args)
	default:
		return nounHelp("diff", verb)
	}
}

func runDiffWorkdir(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("diff-workdir", flag.ContinueOnError)
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
	d, err := c.DiffWorkdir(tctx, repoPath)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(d)
	}
	return renderMarkdown(fmtWorkdirDiff(repoPath, d))
}

func fmtWorkdirDiff(repoPath string, d *service.WorkdirDiff) string {
	md := fmt.Sprintf("## Working tree: `%s`\n\n", repoPath)
	md += fmt.Sprintf("Staged: +%d/-%d  Unstaged: +%d/-%d  Files: %d\n\n",
		d.TotalStagedAdds, d.TotalStagedDels,
		d.TotalUnstagedAdds, d.TotalUnstagedDels,
		len(d.Files))
	if len(d.Files) == 0 {
		md += "_Clean working tree._\n"
		return md
	}
	md += "| File | Staged | Unstaged |\n|------|--------|----------|\n"
	for _, f := range d.Files {
		staged := f.StagedStatus
		if staged == "" {
			staged = "-"
		}
		unstaged := f.UnstagedStatus
		if unstaged == "" {
			unstaged = "-"
		}
		md += fmt.Sprintf("| `%s` | %s (+%d/-%d) | %s (+%d/-%d) |\n",
			f.Path, staged, f.StagedAdditions, f.StagedDeletions,
			unstaged, f.UnstagedAdditions, f.UnstagedDeletions)
	}
	return md
}

func runDiffBranch(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("diff-branch", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	base := fs.String("base", "", "base branch/ref (required)")
	head := fs.String("head", "", "head branch/ref (required)")
	if code, done := parseArgs(fs, args); done {
		return code
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
	d, err := c.DiffBranch(tctx, repoPath, *base, *head)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(d)
	}
	md := fmt.Sprintf("## Branch diff: `%s`..`%s`\n\n", *base, *head)
	md += fmt.Sprintf("Files changed: %d  Additions: +%d  Deletions: -%d\n\n",
		d.FilesChanged, d.TotalAdditions, d.TotalDeletions)
	md += "_(file-level statistics only — use `singl diff file --repo=… --base=… --head=… --file=…` for a text patch)_\n\n"
	if len(d.Files) > 0 {
		md += "| File | Status | +/- |\n|------|--------|-----|\n"
		for _, f := range d.Files {
			path := f.NewPath
			if path == "" {
				path = f.OldPath
			}
			md += fmt.Sprintf("| `%s` | %s | +%d/-%d |\n", path, f.Status, f.Additions, f.Deletions)
		}
	}
	return renderMarkdown(md)
}

func runDiffFile(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("diff-file", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	base := fs.String("base", "", "base ref (required)")
	head := fs.String("head", "", "head ref (required)")
	file := fs.String("file", "", "file path (required)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *base == "" || *head == "" || *file == "" {
		fmt.Fprintln(os.Stderr, "error: --repo, --base, --head, and --file are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	diff, err := c.DiffFile(tctx, repoPath, *base, *head, *file)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"diff": diff})
	}
	if diff == "" {
		return renderMarkdown(fmt.Sprintf("No diff for `%s` between `%s` and `%s`.\n", *file, *base, *head))
	}
	return renderMarkdown(fmt.Sprintf("## `%s`: `%s`..`%s`\n\n```diff\n%s\n```\n", *file, *base, *head, diff))
}

func runDiffStaged(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("diff-staged", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	file := fs.String("file", "", "file path (required)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *file == "" {
		fmt.Fprintln(os.Stderr, "error: --repo and --file are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	diff, err := c.DiffStagedFile(tctx, repoPath, *file)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"diff": diff})
	}
	if diff == "" {
		return renderMarkdown(fmt.Sprintf("No staged changes for `%s`.\n", *file))
	}
	return renderMarkdown(fmt.Sprintf("## Staged: `%s`\n\n```diff\n%s\n```\n", *file, diff))
}

func runDiffUnstaged(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("diff-unstaged", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	file := fs.String("file", "", "file path (required)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *file == "" {
		fmt.Fprintln(os.Stderr, "error: --repo and --file are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	diff, err := c.DiffUnstagedFile(tctx, repoPath, *file)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"diff": diff})
	}
	if diff == "" {
		return renderMarkdown(fmt.Sprintf("No unstaged changes for `%s`.\n", *file))
	}
	return renderMarkdown(fmt.Sprintf("## Unstaged: `%s`\n\n```diff\n%s\n```\n", *file, diff))
}

func runDiffMergeBase(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("diff-merge-base", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	base := fs.String("base", "", "base ref (required)")
	head := fs.String("head", "", "head ref (required)")
	if code, done := parseArgs(fs, args); done {
		return code
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
	sha, err := c.DiffMergeBase(tctx, repoPath, *base, *head)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"merge_base": sha})
	}
	return renderMarkdown(fmt.Sprintf("## Merge base: `%s` and `%s`\n\nCommon ancestor: `%s`\n", *base, *head, sha))
}

func runDiffAllRepos(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("diff-all-repos", flag.ContinueOnError)
	project := fs.String("project", "", "project handle (required)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if *project == "" {
		fmt.Fprintln(os.Stderr, "error: --project is required (use `singl project list` for handles)")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	repos, err := c.DiffAllRepos(tctx, service.ProjectHandle(*project))
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(repos)
	}
	if len(repos) == 0 {
		return renderMarkdown("No repos found.\n")
	}
	keys := make([]string, 0, len(repos))
	for k := range repos {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	md := fmt.Sprintf("## Working tree — all repos (%d)\n\n", len(repos))
	for _, k := range keys {
		d := repos[k]
		if d == nil {
			continue
		}
		md += fmt.Sprintf("### `%s`\n\n", k)
		md += fmtWorkdirDiff(k, d)
		md += "\n"
	}
	return renderMarkdown(md)
}
