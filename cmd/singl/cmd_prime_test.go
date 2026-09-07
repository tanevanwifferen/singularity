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
		"queue",
	}
	for _, noun := range nouns {
		if !strings.Contains(primeGuide, "`"+noun+"`") {
			t.Errorf("primer does not mention noun %q", noun)
		}
	}

	agentVerbs := []string{
		"list", "get", "spawn", "resume", "kill", "remove", "output", "input",
		"wait", "wait-all", "watch", "watch-all", "chat", "stats",
	}
	for _, verb := range agentVerbs {
		if !strings.Contains(primeGuide, verb) {
			t.Errorf("primer does not mention agents verb %q", verb)
		}
	}

	queueVerbs := []string{
		"queue add", "queue list", "queue show", "queue graph", "queue wait",
		"queue cancel", "queue retry", "queue answer", "queue pause",
		"queue resume", "queue queues", "queue remove",
	}
	for _, verb := range queueVerbs {
		if !strings.Contains(primeGuide, verb) {
			t.Errorf("primer does not mention %q", verb)
		}
	}
}

// TestPrimeGuideTeachesQueueFirst guards the delegation paradigm the queue
// introduced: submit a DAG and wait for it, rather than spawn-then-poll. The
// primer is the only place an orchestrating agent learns this, so the claims
// it has to make are pinned here.
func TestPrimeGuideTeachesQueueFirst(t *testing.T) {
	for _, want := range []string{
		"queue add --file",       // the recommended submit path
		"queue wait --queue",     // the recommended block path
		"backpressure",           // capacity no longer errors
		"waiting_human",          // ...documented as a gap, not as live behaviour
		"use_worktree",           // the one-agent-per-directory exemption
		"scheduler enforces",     // ...which the scheduler now polices
		"escape hatch",           // agents spawn is still documented
		"agents spawn --workdir", // ...with its example intact
	} {
		if !strings.Contains(primeGuide, want) {
			t.Errorf("primer missing %q", want)
		}
	}

	// The old advice to poll a spawned agent until it settles must not be the
	// documented way to block on unattended work any more.
	if strings.Contains(primeGuide, "observe by polling, not streaming") {
		t.Error("primer still frames spawn-then-poll as the main observation step")
	}

	// waiting_human must be described as a gap, not as a branch to write.
	// The engine cannot report a stalled agent in this build, so an agent
	// primed to answer one writes a path it can never take and reaches for
	// a verb that always returns CONFLICT.
	if !strings.Contains(primeGuide, "Human escalation is not implemented") {
		t.Error("primer does not disclose that waiting_human is unreachable in this build")
	}
	if strings.Contains(primeGuide, "it returns **early** with a notice naming the task ID") {
		t.Error("primer still documents the waiting_human early return as live queue wait behaviour")
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
