# singl — orchestration primer

`singl` is a binary CLI, not an MCP server. It talks to the **singularity daemon**
(`singularityd`) over a unix socket or HTTP; the daemon owns all git state, the
project registry, and the agent pool. You are the main agent: you delegate work by
creating a workflow (one branch, one worktree per repo in the project), spawning
subagents on those worktrees, then steering them — all through `singl`.

## Mental model

```
project   set of related repos, configured in ~/.config/singularity/projects.json
  └─ workflow    one feature branch, one worktree per repo — created for ALL repos at once
       └─ worktree   isolated checkout of a single repo, at <base-dir>/<branch>/<repo>
            └─ agent      coding-agent subprocess, --workdir on a worktree or on the workflow dir
```

- **Isolation is per project, not per repo.** `workflows create` makes a
  worktree for *every* repo in the project on the same branch — even repos you
  think you won't touch. Cross-repo changes are the norm here, and a workflow
  whose repos are half-isolated cannot be pushed or MR'd as one unit.
- Layout: `<base-dir>/<branch>/<repo>`, base-dir defaults to `~/.worktrees/<project>`.
  Slashes in the branch name become dashes in the directory name.
- Project handles are `proj-<key>` (the bare key also works); agent IDs are opaque strings.
- One agent per directory. Never point two agents at the same workdir.
- An agent spawned through `singl` runs **directly in `--workdir`** — it gets no
  implicit isolation. Create the workflow first, or the agent edits your live tree.

## Output contract

- Default output is markdown: rendered on a TTY, raw when piped.
- `--json` works on every non-streaming command. **Always use it when parsing.**
- Exit codes: `0` ok, `1` error (message on stderr), `2` usage error. Check the code.
- Global flags: `--server <url>`, `--json`, `--repo <path>`.
  Env: `SINGL_SERVER`, `SINGL_REPO`, `SINGL_FORMAT=json`. File: `.singl.json` in cwd or a parent.
- `--repo` defaults to the git root of the cwd, so most repo commands work bare.

## Bootstrap

```
singl status                       # daemon version + endpoint; spawns a local daemon if none runs
singl project list                 # configured project keys
singl project status --project proj-<key>
```

Every configured project is addressable immediately: `--project proj-<key>` (or
just `--project <key>`) loads it on first use, daemon-side. There is no load
step — `project load --name <key>` merely warms the cache and returns the lean
info in one call. Commands taking a project handle: all `project *`,
`diff all-repos`, `sync all`, `stash list-all|all|apply-all`.

## Delegating work

**1 — isolate.** One command, worktrees for every repo in the project:

```
singl --json workflows create --project proj-<key> --branch feature/x
# optional: --base-dir ~/worktrees   (default ~/.worktrees/<project>)
# → {"branch_name":"feature/x","base_dir":"...","repos":{
#      "api":{"worktree_path":"~/.worktrees/<project>/feature-x/api","worktree_created":true}, ...}}
```

Branches are cut from `origin/<default-branch>` per repo, the workflow is
persisted (so `workflows list` and the TUI see it), and re-running the
same create is idempotent — existing worktrees on that branch are adopted.
Exit code is `1` if any repo failed; check each repo's `error` field, the other
worktrees are real and already tracked.

**2 — spawn.**

Spawn one agent per repo worktree for scoped work, or one agent on the workflow
directory (`<base-dir>/<branch>/`) when the change genuinely spans repos.

```
singl --json agents spawn --workdir ~/.worktrees/<project>/feature-x/api \
  --prompt "Implement X in this worktree. Run the tests. Report what you changed." \
  --effort medium --timeout 1800
# → {"agent_id": "a1b2c3"}
```

Flags: `--model`, `--effort low|medium|high`, `--smart-route` (Haiku picks model +
effort from the prompt), `--max-turns`, `--timeout <secs>`, `--backend claude|pi`.

**3 — observe by polling, not streaming.**

```
singl --json agents get    --id <id>                # state: idle routing starting running complete error killed
singl --json agents output --id <id> --offset <n>    # incremental; n = entries already consumed
singl --json agents list
singl --json agents stats                           # active/max — check capacity before spawning
```

Keep your own per-agent output cursor and advance `--offset`.
`agents watch --id <id>` and `agents watch-all` stream live to stdout and **block
until the agent stops** — use them only when a human is watching.

**4 — chat, correct, clear.**

```
singl agents input  --id <id> --message "..."       # non-blocking follow-up; works even after complete
singl agents chat   --id <id> --message "..."       # sends, then streams the reply (blocks)
singl agents kill   --id <id>                       # soft close: ends the turn, process stays alive for follow-ups
singl agents remove --id <id>                       # terminates the process and drops the agent
singl --json agents resume --id <id> --message "..." # NEW agent seeded with the old one's history (crash recovery)
```

"Clear a subagent" = `remove`, then `spawn` a fresh one on the same worktree.

**5 — land the work.** The daemon does the git plumbing; don't shell out to git.

```
singl --json diff workdir     --repo <worktree>
singl --json commit suggest   --repo <worktree>     # AI commit message from the current diff
singl sync push               --repo <worktree>
singl --json mr title  --repo <worktree> --source feature/x --target main
singl --json mr create --repo <worktree> --source feature/x --target main --title "..." --desc "..."
```

`commit suggest` and `mr title/create` generate text with a cheap one-shot prompt on
the provider from `ai.provider` (claude or pi); both fall back to heuristics if it fails.

**6 — clean up.** One command tears the whole workflow down: every repo's
worktree removed, local **and remote** feature branches deleted, workflow
dropped from persistence. Only run it after the MRs are merged (or the work is
abandoned) — the branch deletion is not undoable from here.

```
singl --json workflows remove --project proj-<key> --branch feature/x
```

Exit code `1` means some repos failed to clean up; the workflow stays tracked
with per-repo `error` fields — fix the cause and re-run, the command is
idempotent for the repos that already cleaned.

## Command surface

| Noun | Verbs | Key flags |
|---|---|---|
| `status` | — | — |
| `agents` | list get spawn resume kill remove output input watch watch-all chat stats | `--id` `--workdir` `--prompt` `--message` `--offset` `--model` `--effort` `--smart-route` `--max-turns` `--timeout` `--backend` |
| `project` | list status load info refresh branch-check context workflows | `--name` (load) `--project` (handle) `--branch` |
| `workflows` | list create remove discover | `--project` `--branch` `--base-dir` (create makes a worktree per repo; remove tears the whole workflow down) |
| `branches` | list checkout create delete head compare | `--repo` `--branch` `--start-point` `--base` `--head` `--force` |
| `diff` | workdir branch file staged unstaged merge-base all-repos | `--repo` `--base` `--head` `--file` `--project` |
| `commit` | suggest generate files diff file-diff cherry-pick reset amend | `--repo` `--hash` `--file` `--message` `--mode soft\|mixed\|hard` |
| `mr` | title desc create cli | `--repo` `--source` `--target` `--title` `--desc` `--reviewers` `--base` |
| `sync` | fetch pull push pull-rebase set-upstream upstream-status last-fetch all | `--repo` `--remote` `--force` `--project` |
| `rebase` | plan status continue skip abort onto-main todo context | `--repo` `--base` `--current` `--main` `--conflicts` |
| `stash` | list get create apply pop drop clear list-all all apply-all | `--repo` `--index` `--message` `--untracked` `--pop` `--project` |
| `repos` | info open find | `--repo` `--path` |
| `pipeline` | status | `--repo` `--branch` |
| `forge` | info auth provider | `--repo` |
| `jira` | search get mine create update comment link ai | `--jql` `--key` `--project` `--type` `--summary` `--desc` `--field` `--value` `--body` `--from` `--to` |
| `jira ai` | refine stories review | `--key` `--repo` `--focus` `--instruction` `--project` |

Streaming (blocking) commands: `agents watch`, `agents watch-all`, `agents chat`,
`sync fetch|pull|push|pull-rebase|all`, `workflows discover`. They reject `--json`.

## Orchestration rules

- Check `agents stats` before spawning — the pool has a hard concurrency cap and
  `spawn` fails once it's reached.
- Subagents inherit **nothing** from your context. Put everything in `--prompt`:
  absolute paths, the definition of done, and "report a summary of what changed".
- Start every piece of work with a workflow, not a bare worktree — a project's
  repos must be isolated together or the branch cannot be landed as one change.
- Prefer several small scoped agents over one broad one; when a change spans repos,
  one agent per repo worktree, all inside the same workflow.
- Always pass `--timeout` for unattended work; a runaway agent otherwise runs forever.
- Poll `agents get` for terminal state (`complete`, `error`, `killed`) and drain
  `agents output --offset` before acting on a result.
- Review a subagent's diff yourself (`diff workdir`) before committing or pushing it.
- Never `remove` an agent you still want to talk to — `kill` keeps it addressable.

## Known gaps in this build

- `agents spawn` exposes no context-file injection or allowed-tool restriction,
  though the daemon API supports both.

## Improving the tool

Run `singl prime --debug` to get the self-improvement primer: it locates the
singularity source in the project list and explains the fix → rebuild →
daemon-restart loop, so friction you hit with `singl` can be fixed in `singl`.
