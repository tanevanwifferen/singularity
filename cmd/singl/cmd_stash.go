package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

func cmdStash(ctx context.Context, verb string, args []string) int {
	switch verb {
	case "list":
		return runStashList(ctx, args)
	case "create":
		return runStashCreate(ctx, args)
	case "apply":
		return runStashApply(ctx, args)
	case "pop":
		return runStashPop(ctx, args)
	case "drop":
		return runStashDrop(ctx, args)
	case "get":
		return runStashGet(ctx, args)
	case "clear":
		return runStashClear(ctx, args)
	case "list-all":
		return runStashListAll(ctx, args)
	case "all":
		return runStashAll(ctx, args)
	case "apply-all":
		return runStashApplyAll(ctx, args)
	default:
		fmt.Fprintf(os.Stderr, "unknown stash verb: %q\nverbs: list create apply pop drop get clear list-all all apply-all\n", verb)
		return 2
	}
}

func runStashList(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("stash-list", flag.ContinueOnError)
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
	entries, err := c.StashList(tctx, repoPath)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(entries)
	}
	md := fmt.Sprintf("## Stashes for `%s`\n\n", repoPath)
	for _, e := range entries {
		md += fmt.Sprintf("**stash@{%d}** — %s  \n", e.Index, e.Message)
		md += fmt.Sprintf("Author: %s — %s  \n\n", e.Author, e.Date.Format("2006-01-02 15:04"))
	}
	if len(entries) == 0 {
		md += "_No stashes._\n"
	}
	return renderMarkdown(md)
}

func runStashCreate(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("stash-create", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	message := fs.String("message", "", "stash message")
	untracked := fs.Bool("untracked", false, "include untracked files")
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
	idx, err := c.StashCreate(tctx, repoPath, *message, *untracked)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]int{"index": idx})
	}
	return renderMarkdown(fmt.Sprintf("Stash created.\n\nIndex: `%d`\n", idx))
}

func runStashApply(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("stash-apply", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path (required)")
	index := fs.Int("index", -1, "stash index (required)")
	pop := fs.Bool("pop", false, "pop (apply and drop) instead of just apply")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *index < 0 {
		fmt.Fprintln(os.Stderr, "error: --repo and --index are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.StashApply(tctx, repoPath, *index, *pop); err != nil {
		return die(err)
	}
	verb := "applied"
	if *pop {
		verb = "popped"
	}
	if globals.json {
		return printJSON(map[string]any{"status": verb, "index": *index})
	}
	return renderMarkdown(fmt.Sprintf("Stash@{%d} %s.\n", *index, verb))
}

func runStashPop(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("stash-pop", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path (required)")
	index := fs.Int("index", -1, "stash index (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *index < 0 {
		fmt.Fprintln(os.Stderr, "error: --repo and --index are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.StashApply(tctx, repoPath, *index, true); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]any{"status": "popped", "index": *index})
	}
	return renderMarkdown(fmt.Sprintf("Stash@{%d} popped.\n", *index))
}

func runStashGet(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("stash-get", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path (required)")
	index := fs.Int("index", -1, "stash index (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *index < 0 {
		fmt.Fprintln(os.Stderr, "error: --repo and --index are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	entry, err := c.StashGet(tctx, repoPath, *index)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(entry)
	}
	md := fmt.Sprintf("## Stash@{%d}\n\n", entry.Index)
	md += fmt.Sprintf("Message: %s  \nAuthor: %s  \nDate: %s  \n",
		entry.Message, entry.Author, entry.Date.Format("2006-01-02 15:04"))
	return renderMarkdown(md)
}

func runStashClear(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("stash-clear", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path (required)")
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
	if err := c.StashClear(tctx, repoPath); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"status": "cleared"})
	}
	return renderMarkdown("All stashes cleared.\n")
}

func runStashListAll(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("stash-list-all", flag.ContinueOnError)
	project := fs.String("project", "", "project handle (required)")
	if err := fs.Parse(args); err != nil {
		return 2
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
	repos, err := c.StashListAllRepos(tctx, service.ProjectHandle(*project))
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(repos)
	}
	md := "## Stashes — all repos\n\n"
	for _, r := range repos {
		md += fmt.Sprintf("### `%s` (`%s`)\n\n", r.RepoName, r.Path)
		if r.Error != "" {
			md += fmt.Sprintf("_Error: %s_\n\n", r.Error)
			continue
		}
		if len(r.Entries) == 0 {
			md += "_No stashes._\n\n"
			continue
		}
		for _, e := range r.Entries {
			md += fmt.Sprintf("- stash@{%d}: %s  \n", e.Index, e.Message)
		}
		md += "\n"
	}
	return renderMarkdown(md)
}

func runStashAll(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("stash-all", flag.ContinueOnError)
	project := fs.String("project", "", "project handle (required)")
	message := fs.String("message", "", "stash message")
	untracked := fs.Bool("untracked", false, "include untracked files")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *project == "" {
		fmt.Fprintln(os.Stderr, "error: --project is required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	results, err := c.StashAllRepos(tctx, service.ProjectHandle(*project), *message, *untracked)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(results)
	}
	md := "## Stash all repos\n\n"
	for _, r := range results {
		if r.Skipped {
			md += fmt.Sprintf("- `%s`: skipped (nothing to stash)  \n", r.RepoName)
		} else if r.Error != "" {
			md += fmt.Sprintf("- `%s`: **error** — %s  \n", r.RepoName, r.Error)
		} else {
			md += fmt.Sprintf("- `%s`: stashed at index `%d`  \n", r.RepoName, r.StashIndex)
		}
	}
	return renderMarkdown(md)
}

func runStashApplyAll(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("stash-apply-all", flag.ContinueOnError)
	project := fs.String("project", "", "project handle (required)")
	pop := fs.Bool("pop", false, "pop (apply and drop) instead of just apply")
	message := fs.String("message", "", "match by stash message (optional)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *project == "" {
		fmt.Fprintln(os.Stderr, "error: --project is required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	results, err := c.StashApplyAllRepos(tctx, service.ProjectHandle(*project), *message, *pop)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(results)
	}
	verb := "applied"
	if *pop {
		verb = "popped"
	}
	md := fmt.Sprintf("## Stash %s — all repos\n\n", verb)
	for _, r := range results {
		if r.Skipped {
			md += fmt.Sprintf("- `%s`: skipped  \n", r.RepoName)
		} else if r.Error != "" {
			md += fmt.Sprintf("- `%s`: **error** — %s  \n", r.RepoName, r.Error)
		} else {
			md += fmt.Sprintf("- `%s`: %s index `%d`  \n", r.RepoName, verb, r.StashIndex)
		}
	}
	return renderMarkdown(md)
}

func runStashDrop(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("stash-drop", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path (required)")
	index := fs.Int("index", -1, "stash index (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" || *index < 0 {
		fmt.Fprintln(os.Stderr, "error: --repo and --index are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := c.StashDrop(tctx, repoPath, *index); err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]int{"dropped_index": *index})
	}
	return renderMarkdown(fmt.Sprintf("Stash@{%d} dropped.\n", *index))
}
