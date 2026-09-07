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

- **You are an orchestrator, not an implementer.** Your job is to decide,
  delegate, observe and land; writing the code is the agents' job.
- **Isolation is per project, not per repo.** `workflows create` makes a
  worktree for *every* repo in the project on the same branch — even repos you
  think you won't touch. Cross-repo changes are the norm here, and a workflow
  whose repos are half-isolated cannot be pushed or MR'd as one unit.
- Layout: `<base-dir>/<branch>/<repo>`, base-dir defaults to
  `~/.worktrees/<project-slug>` — the slug is the lowercased project name with
  non-alphanumerics collapsed to `-` ("PBD Development" → `pbd-development`),
  so worktree paths never contain spaces. A legacy directory named after the
  raw project name is reused if it already exists. Slashes in the branch name
  become dashes in the directory name.
- Project handles are `proj-<key>` (the bare key also works); agent IDs, task IDs
  and queue IDs are opaque strings.
- One agent per directory. The **queue scheduler enforces this**: a task whose
  `workdir` already has a live agent is not dispatched until that agent is gone,
  even when its dependencies are satisfied. Tasks with `use_worktree` are exempt —
  the engine gives each of them its own worktree, so they cannot collide.
  `agents spawn` is not policed: there, never point two agents at the same workdir.
- An agent spawned through `singl` runs **directly in `--workdir`** — it gets no
  implicit isolation. Create the workflow first, or the agent edits your live tree.

## Output contract

- Default output is markdown: rendered on a TTY, raw when piped.
- `--json` works on every non-streaming command. **Always use it when parsing.**
  All JSON uses snake_case field names (`work_dir`, `total_staged_adds`, ...).
- Exit codes: `0` ok, `1` error (message on stderr), `2` usage error. Check the code.
- Every noun answers `--help`, `-h`, `help`, or a bare invocation
  (`singl agents`) with its verb + flag reference; every verb answers `--help`
  with its flag defaults (`singl agents wait --help`). Explicit help exits 0.
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

**Deciding isolation is your job, before you spawn anything:**

- **Unrelated changes ⇒ separate workflows.** Each gets its own branch and its own
  worktree per repo, so it can be reviewed, landed and reverted independently.
- **One change ⇒ one workflow.** Then either a single agent, or several agents on
  *different* repo worktrees of that same workflow. Never two agents in one directory.
- **Never spawn an agent in the user's live checkout for feature work.** A bare
  `agents spawn --workdir <repo>` edits the tree the user is sitting in; that is only
  for read-only inspection or an explicitly throwaway quick fix.

Four unrelated changes = four `workflows create` calls on four branches, one agent
per worktree — not one agent fed four messages:

```
singl --json workflows create --project proj-x --branch feat/agents-wait
singl --json workflows create --project proj-x --branch feat/wait-all
singl --json workflows create --project proj-x --branch feat/smart-route-default
singl --json workflows create --project proj-x --branch fix/prompt-logging
# then one agents spawn per worktree, one task each
```

**2 — queue the work as a DAG.** Submit every task in one call and let the
daemon sequence them; you do not stay alive to babysit the chain.

```
cat > /tmp/tasks.json <<'JSON'
{"queue": "feature-x",
 "tasks": [
   {"name": "api", "title": "api: implement X",
    "workdir": "/home/me/.worktrees/<project>/feature-x/api",
    "prompt": "Implement X in this worktree. Run the tests. Report what you changed.",
    "opts": {"effort": "medium", "timeout_secs": 1800}},
   {"name": "web", "title": "web: call the new endpoint",
    "workdir": "/home/me/.worktrees/<project>/feature-x/web",
    "prompt": "Call the new endpoint. Run the tests. Report what you changed.",
    "opts": {"effort": "medium", "timeout_secs": 1800}},
   {"name": "review", "title": "cross-repo review", "after": ["api", "web"],
    "workdir": "/home/me/.worktrees/<project>/feature-x",
    "prompt": "Review both diffs against the task. Report problems; do not fix them.",
    "on_failure": "continue"}
 ]}
JSON
singl --json queue add --file /tmp/tasks.json
# → {"queue_id":"feature-x","tasks":[{"id":"t1","state":"ready",...},...]}
```

`after` entries name other tasks in the same file (batch-local `name` keys,
resolved to task IDs daemon-side) or already-assigned task IDs. The batch is
accepted or rejected **as one unit** — an unknown name, a cycle or a missing
prompt/workdir refuses the whole submission, never half of it. A top-level
`"queue"` applies to every task that does not set its own; with no queue named
anywhere the daemon mints one and returns its ID.

Per-task keys: `name`, `title`, `workdir` (`work_dir` accepted too), `prompt`,
`after`, `priority` (higher dispatches first among ready tasks), `max_retries`,
`on_failure` (`block` — the default, dependents are skipped — `continue`, or
`abort-queue`), and `opts`: `model`, `effort`, `timeout_secs`, `backend`,
`use_worktree`, `max_turns`, `context_files`, `allowed_tools`. Unknown keys are
an error, so a typo is reported instead of silently submitting an empty prompt.

One task at a time takes the same options as flags:

```
singl --json queue add --workdir <dir> --prompt "..." --title "..." \
  --after t1,t2 --queue feature-x --effort medium --timeout 1800 --use-worktree \
  --context-file ./NOTES.md --allowed-tools Read,Edit,Bash --max-retries 1 \
  --on-failure continue --priority 1
```

Two things the queue now guarantees, which you used to have to remember:

- **Capacity is backpressure, not an error.** A queued task waits for a free
  agent slot instead of failing with an agent-limit error, so you can submit a
  20-task DAG against a pool of 4 and stop checking `agents stats` first.
- **One agent per working directory is enforced.** Two tasks pointed at the same
  `workdir` are serialised even if nothing links them in the DAG. Tasks with
  `"use_worktree": true` are exempt: the engine gives each its own worktree.

  **Nobody reclaims that worktree automatically** — not when the agent is
  removed (`agents remove`), not on daemon shutdown, not when the task
  reaches `done`. Its checkout under `~/.worktrees/<repo>/agent-<id>` and its
  `agent/<id>/<branch>` branch sit in the source repo until an operator
  clears them: the TUI's Worktrees view ('w'), or `git worktree remove` /
  `git worktree prune` by hand. A large `use_worktree` DAG accumulates one
  checkout and one branch per task — plan to sweep them afterwards.

**3 — wait for the queue, then read the results.** `queue wait` blocks by
polling the daemon (no streaming) and fully supports `--json`:

```
singl --json queue wait   --queue feature-x --timeout 3600 --interval 5
singl --json queue list   --queue feature-x [--state failed,skipped]
singl --json queue show   --id <task-id>            # includes agent_id
singl queue graph  --queue feature-x                # ascii dependency tree (--json for the DAG)
singl --json queue queues                           # every queue with its state tallies
```

Task states: `blocked` → `ready` → `running` → `done`, plus `failed`,
`cancelled` and `skipped` (a dependency failed under `on_failure: block`).
The wire enum also carries `waiting_human`, but nothing in this build ever
produces it — see Known gaps.

`queue wait` exit codes — the whole point of the verb, so check them:

| Exit | Meaning |
|---|---|
| `0` | the queue drained and every task is `done` |
| `1` | the queue drained but at least one task `failed`, was `cancelled` or was `skipped` |
| `1` | `--timeout` expired (JSON carries `"timed_out": true` and the last tallies) |

Without `--queue` it waits for every queue on the daemon at once. Default poll
`--interval` is 5s; `--timeout 0` (the default) waits forever, so always pass a
timeout for unattended work.

Each task carries the `agent_id` of its most recent attempt, so the transcript
stays reachable after the task finishes: `singl --json agents output --id
<agent_id> --offset <n>`. The agent's process, however, is not: the scheduler
terminates a queued task's agent the moment the task reaches a terminal state
(`done`, `failed`, `cancelled`) so its working directory is genuinely free for
the next task. `agents input`/`agents kill` on that agent_id after the fact
are no-ops or errors — a correction to a finished queued task is a new queued
task, not a follow-up message.

Steering a queue in flight:

```
singl queue retry  --id <task-id>                   # requeue a failed/cancelled/skipped task
singl queue cancel --id <task-id>                   # or --queue <id> for the whole queue
singl queue pause  --queue <id>                     # stop dispatching new tasks; running ones continue
singl queue resume --queue <id>
singl queue remove --queue <id>                     # forget a drained queue + delete its state file
```

`remove` is refused while any task is still `running`; cancel or wait first.

**4 — escape hatch: one agent, right now.** `agents spawn` is the single-shot
path — a read-only inspection, a throwaway fix, or work with no dependencies
worth declaring. It bypasses the queue entirely: no dependency ordering, no
backpressure (it fails with an agent-limit error once the pool is full) and no
one-agent-per-directory check.

```
singl --json agents spawn --workdir ~/.worktrees/<project>/feature-x/api \
  --prompt "Implement X in this worktree. Run the tests. Report what you changed." \
  --effort medium --timeout 1800
# → {"agent_id": "a1b2c3"}
```

Flags: `--model`, `--effort low|medium|high`, `--max-turns`, `--timeout <secs>`,
`--backend claude|pi`, `--smart-route[=bool]`, `--no-smart-route`.

Smart routing is **on by default**: a Haiku classifier reads the prompt and
picks the model (planning → opus, implementation → sonnet) and effort
(low/medium/high). Passing `--model` or `--effort` overrides the corresponding
part of routing — an explicit `--model` disables the classifier entirely, an
explicit `--effort` is never overwritten by it. Pass `--no-smart-route` (or
`--smart-route=false`) to disable routing and use backend defaults. If the
classifier fails, the agent starts on backend defaults and an `error` output
entry says so. `--max-turns` is claude-only — pi has no turn limit and says so
in the agent output; use `--timeout` there. Model short names
(`sonnet`/`opus`/`haiku`) are mapped per backend by
`~/.config/singularity/models.json`.

**One discrete task = one agent.** Never grow an agent's scope after spawning; a
new task means a new task in the queue (and, if unrelated, a new workflow).

Observe a spawned agent by polling, not streaming:

```
singl --json agents get    --id <id>                # state: idle routing starting running complete error killed
singl --json agents output --id <id> --offset <n>    # incremental; n = entries already consumed
singl --json agents list
singl --json agents stats                           # active/max — only matters for bare spawns
singl --json agents wait     --id <id> [--id <id2> ...] [--timeout <secs>] [--interval <secs>] [--any]
singl --json agents wait-all [--timeout <secs>] [--interval <secs>]
```

`get`/`list` JSON is a **compact** snapshot: `id`, `state`, `work_dir`,
`summary`, timestamps, `duration_secs`, `total_cost_usd`, `merge_result`.
`exit_code` appears **only** once the state is terminal — while an agent runs
the field is absent, never a lying `0`. The full task prompt (can be
kilobytes) is only included with `--full`; `get --last N` appends the last N
output entries. Output entries carry a `type` discriminator (`text | tool_use
| tool_result | system | error | result | user_input`, with `source` as a
legacy alias) plus `tool_name`/`tool_id`/`is_error` for tool events;
`output --tail N` keeps only the last N entries (applied after `--offset`).

Keep your own per-agent output cursor and advance `--offset`.
The output stream includes the prompts: the initial task and every follow-up
appear as `user_input` entries (rendered `[prompt]`; very long prompts are
truncated with an explicit elision marker — `agents get` has the full task),
so the log reads as a complete conversation.

`agents wait` takes one or more ids (repeat `--id` or comma-separate); with
multiple ids `--any` returns as soon as the first agent finishes instead of
waiting for all of them. `wait-all` snapshots the currently active agents and
waits for those — agents spawned later don't extend the wait. Exit `0` only
when every waited agent ended `complete`; `1` on `error`/`killed` or timeout.

`agents watch --id <id>` and `agents watch-all` stream live to stdout and **block
until the agent stops** — use them only when a human is watching; `queue wait`,
`agents wait` and `wait-all` are the non-streaming, `--json`-capable counterparts.

**5 — chat, correct, clear.**

```
singl agents input  --id <id> --message "..."       # non-blocking follow-up; works even after complete
singl agents chat   --id <id> --message "..."       # sends, then streams the reply (blocks)
singl agents kill   --id <id>                       # soft close: ends the turn, process stays alive for follow-ups
                                                     # (bare spawn only — on a queue-dispatched task the scheduler
                                                     # reaps this within one tick: process terminated. Its worktree,
                                                     # if any, is NOT — see the use_worktree note in step 2;
                                                     # use `queue cancel --id <task>` for a queued task instead)
singl agents remove --id <id>                       # terminates the process and drops the agent
singl --json agents resume --id <id> --message "..." # NEW agent seeded with the old one's history (crash recovery)
```

`agents input` is for correcting or unblocking the task the agent already has — it
is **not** a queue for the next task. Queueing unrelated work onto a running agent
produces one tangled diff across unrelated concerns that cannot be reviewed, landed
or reverted separately. New task ⇒ new agent.

"Clear a subagent" = `remove`, then `spawn` a fresh one on the same worktree.

**6 — land the work.** The daemon does the git plumbing; don't shell out to git.

```
singl --json diff workdir     --repo <worktree>
singl --json commit stage     --repo <worktree> --all             # or --file a --file b
singl --json commit suggest   --repo <worktree>     # AI commit message from the current diff
singl --json commit create    --repo <worktree> --message "..."   # → {"status":"committed","hash":...}
singl sync push               --repo <worktree>
singl --json mr title  --repo <worktree> --source feature/x --target main
singl --json mr create --repo <worktree> --source feature/x --target main --title "..." --desc "..."
```

`commit suggest` and `mr title/create` generate text with a cheap one-shot prompt on
the provider from `ai.provider` (claude or pi); both fall back to heuristics if it fails.
`commit suggest`/`generate` use the staged diff and fall back to the unstaged
diff when nothing is staged, so they only fail when the working tree is fully
clean.

Forge credentials for `mr create` and `pipeline status` are resolved per host:
the gh CLI, glab's config file (`~/.config/glab-cli/config.yml`, per-host
`token:` entries — self-hosted GitLab instances work), the
GITHUB_TOKEN / GITLAB_TOKEN env vars, and finally `tea` for Gitea/Forgejo,
preferring whatever matches the repo's origin host. When nothing is found the
error lists every source checked and how to fix it.

**7 — clean up.** One command tears the whole workflow down: every repo's
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
| `queue` | add list show graph wait cancel retry answer pause resume queues remove | `--file` `--workdir` `--prompt` `--title` `--after` `--queue` `--id` `--state` `--message` `--model` `--effort` `--timeout` `--interval` `--backend` `--use-worktree` `--context-file` `--allowed-tools` `--max-retries` `--on-failure` `--priority` `--smart-route` `--no-smart-route` |
| `agents` | list get spawn resume kill remove output input wait wait-all watch watch-all chat stats | `--id` `--workdir` `--prompt` `--message` `--offset` `--tail` `--last` `--full` `--model` `--effort` `--smart-route` `--max-turns` `--timeout` `--interval` `--any` `--backend` |
| `project` | list status load info refresh branch-check context workflows | `--name` (load) `--project` (handle) `--branch` |
| `workflows` | list create remove discover | `--project` `--branch` `--base-dir` (create makes a worktree per repo; remove tears the whole workflow down) |
| `branches` | list checkout create delete head compare | `--repo` `--branch` `--start-point` `--base` `--head` `--force` |
| `diff` | workdir branch file staged unstaged merge-base all-repos | `--repo` `--base` `--head` `--file` `--project` |
| `commit` | suggest generate stage create files diff file-diff cherry-pick reset amend | `--repo` `--hash` `--file` (repeatable) `--all` `--message` `--mode soft\|mixed\|hard` |
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
`queue wait`, `agents wait` and `agents wait-all` block too, but poll instead of
stream and fully support `--json` — they are the scripted counterparts of
watch/watch-all.

<!-- gitea-forge -->
The forge layer drives **github** (`gh`), **gitlab** (`glab`) and **gitea**/Forgejo (`tea`).
Provider is detected from the origin URL; for a self-hosted instance on a neutral domain,
pin it with `git config singularity.forge gitea` (or `SINGULARITY_FORGE=gitea`).
`singl --json forge provider --repo <path>` reports whether the CLI is installed and logged
in for that host, and prints the exact `tea logins add` command when it is not.
<!-- /gitea-forge -->

## Orchestration rules

- Prefer `queue add` over `agents spawn`. Capacity is backpressure for queued
  work: a task waits for a free slot instead of failing, so you do not check
  `agents stats` first and do not handle an agent-limit error. Only a bare
  `agents spawn` still fails once the pool's hard cap is reached.
- Subagents inherit **nothing** from your context. Put everything in `--prompt`:
  absolute paths, the definition of done, and "report a summary of what changed".
- Start every piece of work with a workflow, not a bare worktree — a project's
  repos must be isolated together or the branch cannot be landed as one change.
- Prefer several small scoped agents over one broad one; when a change spans repos,
  one agent per repo worktree, all inside the same workflow.
- One discrete task = one queued task. Decide *before* submitting whether tasks
  must be isolated (unrelated work ⇒ one workflow each) or should collaborate
  inside one workflow (same change ⇒ separate repo worktrees). Two tasks in the
  same directory are serialised by the scheduler rather than rejected, so this is
  now about reviewability, not corruption — unless you set `use_worktree`, which
  gives each task its own worktree and lifts the serialisation.
- Never use `agents input` to hand an agent a second, unrelated task — spawn a new one.
- Too big for one agent? Declare the steps as one DAG with `queue add --file`
  and let `after` sequence them — that is what the queue is for. Reserve
  "review the diff, then submit the next task" for steps whose *shape* depends on
  what the previous one produced; parallelise independent work across workflows.
- Always pass a timeout for unattended work — `opts.timeout_secs` per task and
  `queue wait --timeout` on the wait; a runaway agent otherwise runs forever.
- `queue wait --queue <id> --timeout <secs>` is the preferred way to block on
  unattended work (`watch` is for humans). Do **not** poll `queue list` or
  `agents get` in a loop for terminal state: the wait already does that
  daemon-side and its exit code is the verdict — `0` drained clean, `1` something
  failed or the timeout expired.
- After a wait settles, read the outcome from `queue list --state failed,skipped`
  and the transcripts from `agents output --id <task's agent_id> --offset <n>`.
- Review a subagent's diff yourself (`diff workdir`) before committing or pushing it.
- Never `remove` an agent you still want to talk to — `kill` keeps it addressable.
  That only holds for a bare `agents spawn`: killing a queue-dispatched task's
  agent gets it reaped by the scheduler (process terminated — its worktree is
  not, see the use_worktree note in step 2) within one tick, so it is no more
  addressable afterwards than `remove` would leave it. Use `queue cancel --id
  <task>` to stop a queued task.
- Do not edit source files yourself. Anything that changes a working tree's
  content goes through an agent — including a one-line config flip or a
  mechanical rename across files. "It's only one line" is exactly how an
  orchestrator ends up with an untracked, unreviewed diff no task accounts for.
- What you *do* touch directly, and nothing beyond it: the git plumbing the
  daemon exposes (`commit`, `sync push`, `mr create`, `workflows create|remove`),
  reading files and read-only commands to decide what to delegate, and reviewing
  diffs.
- This is context discipline, not ceremony: you hold the plan and the state of
  the fleet, agents hold the implementation detail. Every file you edit yourself
  is detail you loaded instead of overview you exist to keep.

## Known gaps in this build

- Context-file injection and allowed-tool restriction are now reachable:
  `queue add --context-file <p> --allowed-tools a,b` (or `opts.context_files` /
  `opts.allowed_tools` in a `--file` document). `agents spawn` still does not
  expose them, so use the queue when a task needs either.
- `queue add` does not expose `--max-turns` as a flag; set `opts.max_turns` in
  a `--file` document instead. (`--smart-route`/`--no-smart-route` are exposed,
  and routing is on by default just as it is for `agents spawn` — in a `--file`
  document set `"smart_route": false` per task to opt out.)
- Human escalation is not implemented. `waiting_human` exists in the task-state
  enum and the queue handles it end to end, but the agent engine has no way to
  report that an agent stopped to ask something, so no task ever reaches that
  state: `queue answer` errors with CONFLICT for every input, and `queue wait`
  never returns a question. Do not write a branch for it — a stuck agent shows
  up as a task still `running`, which is what `opts.timeout_secs` is for.
- `use_worktree` worktrees are never reclaimed automatically — see step 2.
  `agents remove`, `queue cancel`, a completed task and `daemon stop` all end
  the agent's process but leave its checkout and branch on disk. Clean them
  up yourself with the TUI's Worktrees view or `git worktree remove`/`prune`.

## Improving the tool

Run `singl prime --debug` to get the self-improvement primer: it locates the
singularity source in the project list and explains the fix → rebuild →
daemon-restart loop, so friction you hit with `singl` can be fixed in `singl`.
