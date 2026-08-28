package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"strings"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/daemon"
)

// primeGuide is the static orchestration primer: how an agent drives singl to
// delegate work to subagents. Kept as markdown so `singl prime` output is
// directly consumable by the agent that reads it.
//
//go:embed prime.md
var primeGuide string

// primeLive is the daemon snapshot appended to the primer so the reading agent
// knows what already exists before it starts spawning anything.
type primeLive struct {
	Endpoint  string                 `json:"endpoint,omitempty"`
	Reachable bool                   `json:"reachable"`
	Version   string                 `json:"version,omitempty"`
	Service   string                 `json:"service,omitempty"`
	Projects  []string               `json:"projects,omitempty"`
	Stats     *api.EngineStats       `json:"stats,omitempty"`
	Agents    []api.AgentSnapshotDTO `json:"agents,omitempty"`
	Note      string                 `json:"note,omitempty"`
}

// cmdPrime handles "singl prime".
func cmdPrime(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("prime", flag.ContinueOnError)
	noLive := fs.Bool("no-live", false, "print the primer only, without querying the daemon")
	debug := fs.Bool("debug", false, "append the self-improvement primer: dogfood singl, log friction, fix the tool itself")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	var live *primeLive
	if !*noLive {
		live = collectPrimeLive(ctx)
	}

	var debugMD string
	if *debug {
		debugMD = fmtPrimeDebug(live)
	}

	if globals.json {
		out := map[string]any{"guide": primeGuide, "live": live}
		if *debug {
			out["debug"] = debugMD
		}
		return printJSON(out)
	}
	md := primeGuide
	if debugMD != "" {
		md += "\n" + debugMD
	}
	if live != nil {
		md += "\n" + fmtPrimeLive(live)
	}
	return renderMarkdown(md)
}

// collectPrimeLive gathers the daemon snapshot on a best-effort basis. Unlike
// every other subcommand it never auto-spawns a daemon — priming is a read, and
// a primer is still useful with nothing running.
func collectPrimeLive(ctx context.Context) *primeLive {
	live := &primeLive{}

	endpoint := globals.server
	if endpoint == "" {
		paths := daemon.DefaultPaths()
		if !daemon.SocketReachable(paths.Socket) {
			live.Note = "No daemon running. Any singl command auto-spawns one on the default socket, or run `singularity daemon --detach`."
			return live
		}
		endpoint = "unix://" + paths.Socket
	}
	live.Endpoint = endpoint

	c, err := newClient()
	if err != nil {
		live.Note = fmt.Sprintf("Daemon unreachable: %v", err)
		return live
	}
	tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	st, err := c.GetStatus(tctx)
	if err != nil {
		live.Note = fmt.Sprintf("Daemon unreachable: %v", err)
		return live
	}
	live.Reachable = true
	live.Version = st.Version
	live.Service = st.Server

	// Each of these is optional: a daemon without a project config or agent
	// engine still primes fine, it just reports less.
	if projects, perr := c.ProjectList(tctx); perr == nil {
		live.Projects = projects
	}
	if stats, serr := c.AgentStats(tctx); serr == nil {
		live.Stats = &stats
	}
	if agents, aerr := c.AgentList(tctx); aerr == nil {
		live.Agents = agents
	}
	return live
}

// fmtPrimeLive renders the daemon snapshot as the primer's closing section.
func fmtPrimeLive(live *primeLive) string {
	md := "## Live state\n\n"
	if !live.Reachable {
		return md + live.Note + "\n"
	}
	md += fmt.Sprintf("Daemon `%s` — %s version %s  \n", live.Endpoint, live.Service, live.Version)

	if len(live.Projects) == 0 {
		md += "Projects: _none configured_  \n"
	} else {
		keys := make([]string, len(live.Projects))
		for i, k := range live.Projects {
			keys[i] = fmt.Sprintf("`%s` (handle `proj-%s`)", k, k)
		}
		md += "Projects: " + strings.Join(keys, ", ") + "  \n"
	}

	if live.Stats != nil {
		md += fmt.Sprintf("Agent pool: %d/%d active — %d completed, %d errored, %d killed  \n",
			live.Stats.Active, live.Stats.MaxAgents, live.Stats.Completed,
			live.Stats.Errored, live.Stats.Killed)
	}

	if len(live.Agents) == 0 {
		md += "\n_No agents._\n"
		return md
	}
	md += fmt.Sprintf("\n### Existing agents (%d)\n\n", len(live.Agents))
	for _, a := range live.Agents {
		md += fmt.Sprintf("- `%s` — state `%s`, workdir `%s`", a.ID, a.State, a.WorkDir)
		if a.Summary != "" {
			md += " — " + a.Summary
		}
		md += "  \n"
	}
	return md
}

// findSingularityProject returns the configured project key that carries the
// singularity source tree itself, or "" if none is configured. Matched by
// name so `prime --debug` works regardless of what the user called the key.
func findSingularityProject(live *primeLive) string {
	if live == nil {
		return ""
	}
	for _, key := range live.Projects {
		if strings.Contains(strings.ToLower(key), "singularity") || strings.Contains(strings.ToLower(key), "singl") {
			return key
		}
	}
	return ""
}

// fmtPrimeDebug renders the self-improvement section for `prime --debug`:
// the reading agent is invited to treat singl/singularityd as a work item —
// log friction, patch the source, rebuild, restart the daemon, retry.
func fmtPrimeDebug(live *primeLive) string {
	md := "## Debug mode: improve the tool you are holding\n\n"
	md += "You are dogfooding `singl` and the singularity daemon. Treat every rough\n"
	md += "edge you hit as a bug report addressed to yourself:\n\n"
	md += "- A command that needed `--json` parsing gymnastics, a missing verb, a\n"
	md += "  confusing error, an exit code that lied — **write it down as you work**.\n"
	md += "- You are allowed — encouraged — to fix these in the singularity source,\n"
	md += "  rebuild, restart the daemon, and continue with the improved tool.\n\n"

	key := findSingularityProject(live)
	if key != "" {
		md += fmt.Sprintf("The singularity source is already a configured project: `%s`\n", key)
		md += fmt.Sprintf("(handle `proj-%s`). Inspect it like any other project:\n\n", key)
		md += fmt.Sprintf("```\nsingl --json project info --project proj-%s   # repo paths\nsingl --json project status --project proj-%s\n```\n\n", key, key)
		md += "For tool work, isolate it like any other change: create a workflow on\n"
		md += "it and spawn an agent there, or edit directly for a quick fix.\n\n"
	} else {
		md += "No singularity project is configured in the daemon's project list —\n"
		md += "locate the source tree yourself (the daemon binary is `singularity` on\n"
		md += "PATH; `go version -m $(command -v singularity)` reveals the module path),\n"
		md += "or add it to the project config so future sessions get this for free.\n\n"
	}

	md += "### Rebuild & restart loop\n\n"
	md += "```\n"
	md += "go build ./...                         # in the singularity repo: compile check\n"
	md += "go test ./...                          # keep the suite green\n"
	md += "make install                           # build + copy singularity & singl to $GOPATH/bin\n"
	md += "singularity daemon stop                # stop the running daemon\n"
	md += "singl status                           # any singl command auto-spawns the NEW binary\n"
	md += "```\n\n"
	md += "The daemon keeps agent state in-process — restarting it drops running\n"
	md += "agents. Check `singl --json agents stats` first and restart when the pool\n"
	md += "is idle, or accept the loss knowingly.\n\n"
	md += "### Rules of self-improvement\n\n"
	md += "- Small, verified steps: one fix, build, test, restart, retry the command\n"
	md += "  that annoyed you. Do not batch ten speculative changes into one restart.\n"
	md += "- `singl prime` is the tool's contract with agents: if you change a\n"
	md += "  command surface, update `cmd/singl/prime.md` in the same change.\n"
	md += "- Leave the tree committable: the user reviews your improvements as\n"
	md += "  ordinary diffs. Never push the singularity repo without being asked.\n"
	md += "- End your session by listing the friction you hit but did NOT fix.\n"
	return md
}
