// Command singl is a scriptable CLI client for the singularity daemon.
// Default output is markdown: rendered via glamour on a TTY, raw on pipes.
// Use --json for machine-readable JSON output.
//
// Usage: singl [--server=<url>] [--json] [--repo=<path>] <noun> <verb> [flags]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
)

// globals holds the parsed top-level flags shared across all subcommands.
var globals struct {
	server string
	json   bool
	repo   string
}

func main() {
	cfg := loadConfig()
	flag.StringVar(&globals.server, "server", cfg.Server, "daemon endpoint (unix:///path, http://host:port, https://host:port)")
	flag.BoolVar(&globals.json, "json", cfg.JSON, "output raw JSON instead of markdown")
	flag.StringVar(&globals.repo, "repo", cfg.Repo, "default repo path (auto-detected from cwd git root if unset)")
	flag.Usage = printUsage
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		printUsage()
		os.Exit(2)
	}

	noun := args[0]
	verb := ""
	rest := []string{}
	if len(args) > 1 {
		verb = args[1]
		rest = args[2:]
	}

	ctx := context.Background()

	var code int
	switch noun {
	case "prime":
		code = cmdPrime(ctx, args[1:])
	case "status":
		code = cmdStatus(ctx)
	case "workflows":
		code = cmdWorkflows(ctx, verb, rest)
	case "agents":
		code = cmdAgents(ctx, verb, rest)
	case "branches":
		code = cmdBranches(ctx, verb, rest)
	case "repos":
		code = cmdRepos(ctx, verb, rest)
	case "stash":
		code = cmdStash(ctx, verb, rest)
	case "sync":
		code = cmdSync(ctx, verb, rest)
	case "pipeline":
		code = cmdPipeline(ctx, verb, rest)
	case "project":
		code = cmdProject(ctx, verb, rest)
	case "diff":
		code = cmdDiff(ctx, verb, rest)
	case "commit":
		code = cmdCommit(ctx, verb, rest)
	case "mr":
		code = cmdMR(ctx, verb, rest)
	case "rebase":
		code = cmdRebase(ctx, verb, rest)
	case "forge":
		code = cmdForge(ctx, verb, rest)
	case "jira":
		code = cmdJira(ctx, verb, rest)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", noun)
		printUsage()
		code = 2
	}
	os.Exit(code)
}

func printUsage() {
	fmt.Fprint(os.Stderr, `singl — scriptable CLI for the singularity daemon

Usage:
  singl [--server=<url>] [--json] [--repo=<path>] <noun> <verb> [flags]

Global flags:
  --server   daemon endpoint (unix:///path, http://host:port, https://host:port)
  --json     output raw JSON
  --repo     default repo path

Commands:
  prime      print the orchestration primer (how to drive singl + live daemon state)
  status
  workflows  list | create | remove | discover   (whole project: one worktree per repo)
  agents     list | get | spawn | resume | kill | remove | output | input | wait | wait-all | watch | watch-all | chat | stats
  branches   list | checkout | create | delete | head | compare | merge
  repos      info | open | find
  diff       workdir | branch | file | staged | unstaged | merge-base | all-repos
  commit     suggest | generate | files | diff | file-diff | cherry-pick | reset | amend
  mr         title | desc | create | cli
  rebase     plan | status | continue | skip | abort | onto-main | todo | context
  stash      list | get | create | apply | pop | drop | clear | list-all | all | apply-all
  sync       fetch | pull | push | pull-rebase | set-upstream | upstream-status | last-fetch | all
  pipeline   status
  project    list | status | load | info | refresh | branch-check | context | workflows
  forge      info | auth | provider
  jira       search | get | mine | create | update | comment | link | ai
`)
}

// cmdStatus handles "singl status".
func cmdStatus(ctx context.Context) int {
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, err := c.GetStatus(tctx)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(st)
	}
	md := "## Singularity Daemon\n\n"
	md += fmt.Sprintf("Version: %s  \n", st.Version)
	md += fmt.Sprintf("Server: `%s`  \n", st.Server)
	if st.RepoPath != "" {
		md += fmt.Sprintf("Repo: `%s`  \n", st.RepoPath)
	}
	return renderMarkdown(md)
}

// repoArg returns the --repo flag value, falling back to globals.repo.
func repoArg(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return globals.repo
}
