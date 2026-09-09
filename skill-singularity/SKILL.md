---
name: singularity
description: Interact with the singularity git daemon via the `singl` CLI. Use when spawning or managing AI agents in git worktrees/workflows, checking repo/branch/diff state, creating MRs, working with Jira issues, or orchestrating multi-repo project operations. Requires the singularity daemon (auto-spawns if not running).
---

# Singularity skill

`singl` is a binary CLI for the singularity daemon (`singularityd`): the daemon
owns git state, the project registry and the agent pool. Running `singl` makes
you an **orchestrator, not an implementer**: you decide, delegate, observe and
land — you do not edit source files yourself, not even one line. The primer's
orchestration rules define the exceptions; follow them, not your instincts.

This skill file is deliberately thin — **the authoritative, always-up-to-date
reference is the primer that ships with the binary**, so read that instead of
trusting a hand-maintained command list here.

## Step 1 — always: load the primer

Start every singularity session with:

```bash
singl prime
```

That prints the full orchestration primer (mental model, output contract, delegation
workflow, complete command surface, orchestration rules, known gaps) **plus live
daemon state**: endpoint, configured projects with their handles, and the current
agent pool.

Variants:

```bash
singl prime --debug     # primer + self-improvement primer (dogfood/fix singl itself)
singl prime --no-live   # primer only, no daemon query
```

Use `--debug` whenever you're doing any work. This allows you to also file bugs
on the singularity source itself (project`singularity`, handle
`proj-singularity`) — it explains the fix → `make install` →
`singularityd daemon stop` → restart loop and the rules for self-improvement.

If `singl` is not on PATH:

```bash
cd /home/owner/code/personal/singularity && make install   
# installs singularity + singl to $GOPATH/bin
```

## Step 2 — work from the primer, not from memory

The primer is the contract between `singl` and agents. Rules of thumb that survive
version changes:

- Parse with `--json` on non-streaming commands; streaming commands (`agents watch`,
  `agents chat`, `sync *`, `workflows discover`) block and reject `--json`.
- Exit codes: `0` ok, `1` error, `2` usage error — check them.
- `--repo` defaults to the git root of cwd; `.singl.json` / `SINGL_SERVER` /
  `SINGL_REPO` / `SINGL_FORMAT` override in that precedence order below CLI flags.
- Isolate before delegating: create a **workflow** (one branch, one worktree per
  repo in the project), then spawn agents on those worktrees. A bare
  `agents spawn` runs directly in `--workdir` with no isolation.
- Poll (`agents get` / `agents output --offset`) instead of streaming for unattended
  work; always pass `--timeout`.
- Subagents inherit nothing from your context — put absolute paths, definition
  of done and "report what changed" in the prompt.
- Review a subagent's diff (`diff workdir`) before committing or pushing.
- Every working-tree change goes through an agent; your own hands are for git
  plumbing, read-only inspection and diff review.

## Step 3 — if the primer is wrong

If a command in the primer no longer matches the binary's behaviour, that is a
bug in `cmd/singl/prime.md`. Fix it in the singularity repo in the same change
as the command surface change (see `singl prime --debug`), rather than patching
this skill file.
