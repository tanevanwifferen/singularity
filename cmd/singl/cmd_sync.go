package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

func cmdSync(ctx context.Context, verb string, args []string) int {
	switch verb {
	case "fetch":
		return runSyncFetch(ctx, args)
	case "pull":
		return runSyncPull(ctx, args)
	case "push":
		return runSyncPush(ctx, args)
	case "pull-rebase":
		return runSyncPullRebase(ctx, args)
	case "set-upstream":
		return runSyncSetUpstream(ctx, args)
	case "upstream-status":
		return runSyncUpstreamStatus(ctx, args)
	case "last-fetch":
		return runSyncLastFetch(ctx, args)
	case "all":
		return runSyncAll(ctx, args)
	default:
		fmt.Fprintf(os.Stderr, "unknown sync verb: %q\nverbs: fetch pull push pull-rebase set-upstream upstream-status last-fetch all\n", verb)
		return 2
	}
}

func runSyncFetch(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("sync-fetch", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	remote := fs.String("remote", "", "remote name (empty = origin)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" {
		fmt.Fprintln(os.Stderr, "error: --repo is required")
		return 2
	}
	c, err := newStreamClient()
	if err != nil {
		return die(err)
	}
	defer c.Disconnect()

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	ch, cancel, err := c.SyncFetch(ctx, repoPath, *remote)
	if err != nil {
		return die(err)
	}
	defer cancel()
	return drainSyncStream(ctx, ch)
}

func runSyncPull(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("sync-pull", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" {
		fmt.Fprintln(os.Stderr, "error: --repo is required")
		return 2
	}
	c, err := newStreamClient()
	if err != nil {
		return die(err)
	}
	defer c.Disconnect()

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	ch, cancel, err := c.SyncPull(ctx, repoPath)
	if err != nil {
		return die(err)
	}
	defer cancel()
	return drainSyncStream(ctx, ch)
}

func runSyncPush(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("sync-push", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	force := fs.Bool("force", false, "force push")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" {
		fmt.Fprintln(os.Stderr, "error: --repo is required")
		return 2
	}
	c, err := newStreamClient()
	if err != nil {
		return die(err)
	}
	defer c.Disconnect()

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	ch, cancel, err := c.SyncPush(ctx, repoPath, *force)
	if err != nil {
		return die(err)
	}
	defer cancel()
	return drainSyncStream(ctx, ch)
}

func runSyncPullRebase(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("sync-pull-rebase", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" {
		fmt.Fprintln(os.Stderr, "error: --repo is required")
		return 2
	}
	c, err := newStreamClient()
	if err != nil {
		return die(err)
	}
	defer c.Disconnect()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ch, cancel, err := c.SyncPullRebase(ctx, repoPath)
	if err != nil {
		return die(err)
	}
	defer cancel()
	return drainSyncStream(ctx, ch)
}

func runSyncSetUpstream(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("sync-set-upstream", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	remote := fs.String("remote", "", "remote name (empty = origin)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repoPath := repoArg(*repo)
	if repoPath == "" {
		fmt.Fprintln(os.Stderr, "error: --repo is required")
		return 2
	}
	c, err := newStreamClient()
	if err != nil {
		return die(err)
	}
	defer c.Disconnect()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ch, cancel, err := c.SyncSetUpstreamAndPush(ctx, repoPath, *remote)
	if err != nil {
		return die(err)
	}
	defer cancel()
	return drainSyncStream(ctx, ch)
}

func runSyncUpstreamStatus(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("sync-upstream-status", flag.ContinueOnError)
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
	st, err := c.SyncUpstreamStatus(tctx, repoPath)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(st)
	}
	md := fmt.Sprintf("## Upstream status: `%s`\n\n", repoPath)
	md += fmt.Sprintf("Branch: `%s`  \nUpstream: `%s`  \nAhead: %d  Behind: %d  \n",
		st.Branch, st.Upstream, st.Ahead, st.Behind)
	if st.IsDirty {
		md += "Working tree: *dirty*  \n"
	}
	return renderMarkdown(md)
}

func runSyncLastFetch(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("sync-last-fetch", flag.ContinueOnError)
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
	t, err := c.SyncLastFetchTime(tctx, repoPath)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"last_fetch": t.Format("2006-01-02T15:04:05Z07:00")})
	}
	if t.IsZero() {
		return renderMarkdown("Last fetch: _never_\n")
	}
	return renderMarkdown(fmt.Sprintf("Last fetch: `%s`\n", t.Format("2006-01-02 15:04:05")))
}

func runSyncAll(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("sync-all", flag.ContinueOnError)
	project := fs.String("project", "", "project handle (required)")
	force := fs.Bool("force", false, "force push")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *project == "" {
		fmt.Fprintln(os.Stderr, "error: --project is required (use `singl project list` for handles)")
		return 2
	}
	c, err := newStreamClient()
	if err != nil {
		return die(err)
	}
	defer c.Disconnect()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ch, cancel, err := c.SyncAllRepos(ctx, service.ProjectHandle(*project), *force)
	if err != nil {
		return die(err)
	}
	defer cancel()
	return drainSyncStreamPrefixed(ctx, ch)
}

// drainSyncStreamPrefixed is like drainSyncStream but prefixes each line
// with the repo path from the event, for multi-repo sync output.
func drainSyncStreamPrefixed(ctx context.Context, ch <-chan service.SyncProgressEvent) int {
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return 0
			}
			repoName := filepath.Base(ev.RepoPath)
			if repoName == "" || repoName == "." {
				repoName = ev.RepoPath
			}
			if ev.Err != "" {
				fmt.Fprintf(os.Stderr, "[%s] error: %s\n", repoName, ev.Err)
				return 1
			}
			if ev.Line != "" {
				fmt.Printf("[%s] %s\n", repoName, ev.Line)
			}
			if ev.Done {
				return 0
			}
		case <-ctx.Done():
			return 0
		}
	}
}

// drainSyncStream reads SyncProgressEvents and prints each line until done.
// Lines are printed raw (not buffered into markdown) since they arrive
// incrementally from git output.
func drainSyncStream(ctx context.Context, ch <-chan service.SyncProgressEvent) int {
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
			if ev.Line != "" {
				fmt.Println(ev.Line)
			}
			if ev.Done {
				return 0
			}
		case <-ctx.Done():
			return 0
		}
	}
}
