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

**2b — when acceptance is uncertain, use a flow.** The rule that chooses
between the two: **a DAG is for work whose *shape* is known; a flow is for work
whose *acceptance* is not.** If you can write down the steps and their edges up
front, `queue add --file` is right. If the number of steps depends on whether a
reviewer is satisfied — "make this correct", "harden this until it holds" — you
cannot express that as a DAG, because the round count does not exist when you
submit. A flow is the daemon-side loop for it: implement → review → fix →
review, one round at a time, until a reviewer accepts or the cap is hit.

Each round is two fresh agents in the *same* work dir: a work task (`implement`
in round 1, `fix` afterwards) and a `review` task that depends on it. The
reviewer writes a structured JSON verdict the daemon parses; the task's exit
state is liveness only, never the accept/reject decision. An unparseable or
missing verdict is a reject plus exactly one re-review, then `errored` — never
an accept.

```
singl --json flow start --workdir ~/.worktrees/<project>/feature-x/api \
  --title "retry-after handling" \
  --prompt "Implement retry-after handling in this worktree. Run the tests." \
  --review-prompt "Focus on error paths and the tests." \
  --max-rounds 3 --effort medium --timeout 1800
# → the flow record: {"id":"f3","queue_id":"flow-f3","state":"pending","max_rounds":3,...}

singl --json flow wait --id f3 --timeout 3600 --interval 5
```

`flow wait` exit codes — the whole point of the verb, same contract as
`queue wait`:

| Exit | Meaning |
|---|---|
| `0` | `accepted` — a reviewer accepted the work in the flow's work dir |
| `1` | `rejected` — the round cap was reached with the reviewer still rejecting |
| `1` | `errored` or `cancelled` |
| `1` | `--timeout` expired with the flow still live (JSON carries `"timed_out": true`, plus `rounds` and `max_rounds`) |

`rejected` is a designed outcome, not a malfunction — but it still exits `1`,
because the work must not be landed on the strength of it. Read the findings
(`flow show`) and decide by hand.

**When the cap is the problem, continue the flow — do not start another one.**
A `rejected` flow means one thing only: the reviewer was still rejecting when
the round cap ran out. Nothing was wrong with the goal, and the rounds that ran
are the most valuable thing you have — every verdict, every finding, and the
tree the fixer has been working in. `flow continue` is how an orchestrator
authorises more rounds against that same record:

```
singl --json flow continue --id f3 [--rounds N]
# → {"flow":{...},"flow_id":"f3","max_rounds":6,"next_round":4}
```

Round numbering carries on where it stopped (a flow that ended at round 3 opens
round 4), the cap is *raised by* `--rounds` (default `3` more, exactly as
`--max-rounds` defaults to 3), and every round already recorded keeps its
verdict and findings — which is the point, because the next round's fix prompt
is composed from them. A new flow against the same work dir would throw all of
that away and hand its first reviewer a tree it has no account of.

Nothing else is re-specifiable: goal, `--review-prompt`, `--workdir` and both
option blocks stay the flow's own. If the *goal* was wrong, a continue is the
wrong verb — start a better-specified flow instead.

- **Continuable states: `rejected`, `errored`, `cancelled`.** An `accepted`
  flow is refused with CONFLICT (its work passed review; there is nothing to
  fix), and so is a `pending` or `running` one — that has not finished, so
  wait for it or `flow cancel` it first.
- **The 20-round ceiling still holds.** The raised cap must land inside the
  same 1..20 every flow is capped by, so a flow at 20 is refused whatever you
  ask for, and a smaller ask than you made is named for you ("ask for at most N
  more"). Both come back as BAD_REQUEST, as does a `--workdir` that has been
  removed since the flow started — the commonest case being a workflow whose
  worktrees were torn down.
- A trailing round that never reached a verdict (the usual shape of a
  `cancelled` flow's last one) is settled `errored` with a synthetic reject
  before round N+1 opens, and never reopened — so the next fixer is told the
  tree may hold a half-finished round. Check what is actually in there before
  building on it. A round that *did* reach a verdict is left as it is: that
  rejection is what the next round is for.

Flow states: `pending` → `running`, then one of `accepted`, `rejected`,
`errored`, `cancelled` — and `flow continue` is the one edge back out, from
any of the three non-accepted terminals to `running`. Verbs:

```
singl --json flow list  [--state running,accepted]
singl --json flow show  --id f3    # rounds, verdicts, findings, task and agent IDs
singl flow tree  --id f3           # ascii tree of flow → rounds → steps (--json for the node list)
singl --json flow continue --id f3 [--rounds N]   # more rounds, same goal, same tree
singl flow cancel --id f3          # marks the flow cancelled, stops the tasks it created
singl flow remove --id f3          # refused with CONFLICT while the flow is non-terminal
```

`flow start` flags: `--workdir` and `--prompt` are required; then
`--review-prompt`, `--max-rounds N` (1..20, default 3), `--title`, `--model`,
`--effort low|medium|high`, `--timeout <secs>`, `--backend claude|pi`,
`--context-file <p>` (repeatable), `--allowed-tools a,b`, `--reviewer-model`,
`--reviewer-effort`, `--smart-route[=bool]`, `--no-smart-route`. The reviewer
inherits the work options and `--reviewer-*` overrides only what it names.
Routing works exactly as it does for `queue add` and `agents spawn`, resolved
per block — so `--reviewer-model opus` still lets the classifier pick the
reviewer's effort.

Two flags that are deliberately *not* there:

- **`--use-worktree` does not exist, and `use_worktree` in the opts is
  refused** (BAD_REQUEST), not silently cleared. An isolated agent gets its
  own private checkout, so the reviewer would review a different tree than the
  implementer wrote, and each isolated agent merges back on its own, so a
  rejected round's work would already be merged. Isolation is *your* job,
  before the flow starts — create the workflow and point the flow at its
  worktree.
- **`--max-retries` is registered but rejected** with exit `2`: no per-task
  retry count reaches the daemon. A flow's liveness bound is `--timeout` and
  its round bound is `--max-rounds`.

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
picks the model (planning → opus, implementation → sonnet), the effort
(low/medium/high) and the one-line summary shown in `agents list` and the TUI.
Passing `--model` or `--effort` overrides only the corresponding part of
routing — the classifier still runs and still supplies the rest. It is skipped
only when you pin both, leaving it nothing to decide. Pass `--no-smart-route`
(or `--smart-route=false`) to disable routing and use backend defaults. If the
classifier fails or is skipped, the agent starts on backend defaults, an
`error` output entry says so where it failed, and the summary is generated by a
separate cheap one-shot call instead. `--max-turns` is claude-only — pi has no turn limit and says so
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
| `flow` | start continue list show tree wait cancel remove | `--workdir` `--prompt` `--review-prompt` `--max-rounds` `--rounds` (continue: extra rounds, default 3) `--title` `--id` `--state` `--model` `--effort` `--timeout` `--interval` `--backend` `--context-file` `--allowed-tools` `--reviewer-model` `--reviewer-effort` `--smart-route` `--no-smart-route` (no `--use-worktree`; `--max-retries` is rejected) |
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
`queue wait`, `flow wait`, `agents wait` and `agents wait-all` block too, but
poll instead of stream and fully support `--json` — they are the scripted
counterparts of watch/watch-all.

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
- **Aim a flow at a workflow worktree, never the live checkout.** A flow runs
  every round of every agent in the one `--workdir` you gave it, with worktree
  isolation refused, so it will happily rewrite the tree the user is sitting in.
  `workflows create` first, then `flow start --workdir <that worktree>`.
- Debug a flow through its queue, not by guessing: `singl queue list --queue
  flow-<id>` shows the round's two tasks and their states, `singl flow tree
  --id <id>` joins them to their agents, and `singl agents output --id
  <agent_id> --offset <n>` is the transcript. `flow show --id <id>` has the
  verdicts and findings.
- **Never `agents input` a flow's agent.** The fix round *is* the correction
  mechanism: the reviewer's findings are fed to the next round's fixer
  automatically. Steering a step by hand puts work into the tree that no
  verdict accounts for — and the agent is terminated the moment its task
  settles anyway, exactly as for any queued task. If the flow is heading the
  wrong way, `flow cancel` it and start a better-specified one.
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
- The TUI has a **Flows view** — `F6` in repo mode, `F7` in project mode (it sits
  right after Agents) — with the flow list on the left and the selected flow's
  round/step tree on the right: `tab` switches pane, `j`/`k` and arrows move,
  `l`/`enter` expand, `h` collapse, `n` starts a flow (workflow, goal, review
  focus, max rounds), `C` continues a `rejected`/`errored`/`cancelled` flow
  with more rounds (it shows the new cap and the round it resumes at; on an
  accepted or still-running flow it says why instead), `c` cancels behind a
  confirm, `a` opens the selected step's agent in the Agents view, `r`
  refreshes, `/` filters.
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
- **Flows have no cost cap.** N rounds is roughly 2N agents, and nothing
  aggregates their `TotalCostUSD` — not `flow show`, not `flow list`. The only
  bounds are `--max-rounds` (1..20, default 3) and the per-step
  `--timeout`/`opts.timeout_secs`. Set both, deliberately, before you start one.
  `flow continue` raises the cap on purpose, so it buys roughly 2 more agents
  per round asked for, with the 20-round ceiling as the only hard stop — a
  number to choose, not to default past.
- **Nothing stops a reviewer rejecting cosmetically until the cap.** There is no
  daemon-side "good enough" rule — that would be the fail-open the verdict
  design refuses — so the cap is the whole convergence guarantee and `rejected`
  is a first-class outcome, not an error. Narrow the reviewer with
  `--review-prompt` if you see a flow burning rounds on style; the cap itself is
  yours to raise with `flow continue` when the findings say the work is
  converging, which is a judgement no daemon-side rule makes for you.
- **Flows inherit the `waiting_human` gap.** A flow cannot stop to ask a
  question, because nothing in this build ever reaches that state; a stuck step
  shows up as a task still `running`, bounded only by its timeout.
- **`flow start --max-retries N` exits `2`.** The flag is registered only so it
  can say why: no per-task retry count reaches the daemon for a flow. Use
  `--timeout` for a step and `--max-rounds` for the flow.
- `use_worktree` worktrees are never reclaimed automatically — see step 2.
  `agents remove`, `queue cancel`, a completed task and `daemon stop` all end
  the agent's process but leave its checkout and branch on disk. Clean them
  up yourself with the TUI's Worktrees view or `git worktree remove`/`prune`.

## Improving the tool

Run `singl prime --debug` to get the self-improvement primer: it locates the
singularity source in the project list and explains the fix → rebuild →
daemon-restart loop, so friction you hit with `singl` can be fixed in `singl`.
