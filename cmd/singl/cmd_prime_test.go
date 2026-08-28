package main

import (
	"strings"
	"testing"
)

// TestPrimeGuideCoversDispatcher guards against the primer drifting away from
// the CLI: every noun main() dispatches on must appear in the primer, and every
// verb `singl agents` accepts must be documented — an orchestrating agent only
// knows what prime tells it.
func TestPrimeGuideCoversDispatcher(t *testing.T) {
	nouns := []string{
		"status", "workflows", "agents", "branches", "repos", "stash", "sync",
		"pipeline", "project", "diff", "commit", "mr", "rebase", "forge", "jira",
	}
	for _, noun := range nouns {
		if !strings.Contains(primeGuide, "`"+noun+"`") {
			t.Errorf("primer does not mention noun %q", noun)
		}
	}

	agentVerbs := []string{
		"list", "get", "spawn", "resume", "kill", "remove", "output", "input",
		"watch", "watch-all", "chat", "stats",
	}
	for _, verb := range agentVerbs {
		if !strings.Contains(primeGuide, verb) {
			t.Errorf("primer does not mention agents verb %q", verb)
		}
	}
}

// TestPrimeGuideStructure checks the primer keeps the sections an agent reads
// it for: the delegation flow, the JSON contract, and the orchestration rules.
func TestPrimeGuideStructure(t *testing.T) {
	for _, want := range []string{
		"## Mental model",
		"## Output contract",
		"## Bootstrap",
		"## Delegating work",
		"## Command surface",
		"## Orchestration rules",
		"agents spawn --workdir",
		"--project proj-<key>",
	} {
		if !strings.Contains(primeGuide, want) {
			t.Errorf("primer missing %q", want)
		}
	}
}

// TestPrimeGuideTeachesProjectWorktrees guards the isolation paradigm: an
// orchestrating agent must learn that a workflow creates a worktree for every
// repo in the project, and it must not be told to hand-roll per-repo worktrees
// as the primary path.
func TestPrimeGuideTeachesProjectWorktrees(t *testing.T) {
	for _, want := range []string{
		"workflows create",
		"workflows remove",
		"<base-dir>/<branch>/<repo>",
		"~/.worktrees/<project>",
		"every repo in the project",
	} {
		if !strings.Contains(primeGuide, want) {
			t.Errorf("primer missing %q", want)
		}
	}

	// The old known-gap wording claimed workflows create nothing. If that text
	// ever comes back while the daemon does create worktrees, agents will
	// hand-roll worktrees again.
	for _, unwanted := range []string{
		"does **not** create the worktrees",
		"singl worktrees", // the per-repo worktrees noun was removed in favor of workflows
	} {
		if strings.Contains(primeGuide, unwanted) {
			t.Errorf("primer still contains stale claim %q", unwanted)
		}
	}
}

// TestFmtPrimeLive renders both branches of the live snapshot.
func TestFmtPrimeLive(t *testing.T) {
	down := fmtPrimeLive(&primeLive{Note: "No daemon running."})
	if !strings.Contains(down, "No daemon running.") {
		t.Errorf("unreachable snapshot lost its note: %q", down)
	}

	up := fmtPrimeLive(&primeLive{
		Reachable: true,
		Endpoint:  "unix:///tmp/d.sock",
		Service:   "singularity-api",
		Version:   "0.0.1",
		Projects:  []string{"pbd"},
	})
	for _, want := range []string{"unix:///tmp/d.sock", "proj-pbd", "_No agents._"} {
		if !strings.Contains(up, want) {
			t.Errorf("live snapshot missing %q:\n%s", want, up)
		}
	}
}

// TestFmtPrimeDebug checks the --debug section adapts to whether the
// singularity source is a configured project.
func TestFmtPrimeDebug(t *testing.T) {
	with := fmtPrimeDebug(&primeLive{Projects: []string{"pbd", "singularity"}})
	for _, want := range []string{"proj-singularity", "daemon stop", "prime.md", "go test"} {
		if !strings.Contains(with, want) {
			t.Errorf("debug primer (project configured) missing %q", want)
		}
	}

	without := fmtPrimeDebug(&primeLive{Projects: []string{"pbd"}})
	if !strings.Contains(without, "No singularity project is configured") {
		t.Errorf("debug primer without project should explain how to locate the source:\n%s", without)
	}
	if fmtPrimeDebug(nil) == "" {
		t.Error("debug primer must render even with no live snapshot")
	}
}
