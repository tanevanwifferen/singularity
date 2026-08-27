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

- **Isolation is per project, not per repo.** `project workflows create` makes a
  worktree for *every* repo in the project on the same branch — even repos you
  think you won't touch. Cross-repo changes are the norm here, and a workflow
  whose repos are half-isolated cannot be pushed or MR'd as one unit.
- Layout: `<base-dir>/<branch>/<repo>`, base-dir defaults to
  `~/.worktrees/<project-slug>` — the slug is the lowercased project name with
  non-alphanumerics collapsed to `-` ("PBD Development" → `pbd-development`),
  so worktree paths never contain spaces. A legacy directory named after the
  raw project name is reused if it already exists. Slashes in the branch name
  become dashes in the directory name.
- Project handles are `proj-<key>` (the bare key also works); agent IDs are opaque strings.
- One agent per directory. Never point two agents at the same workdir.
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
singl --json project workflows create --project proj-<key> --branch feature/x
# optional: --base-dir ~/worktrees   (default ~/.worktrees/<project-slug>)
# → {"branch_name":"feature/x","base_dir":"...","repos":{
#      "api":{"worktree_path":"~/.worktrees/<project>/feature-x/api","worktree_created":true}, ...}}
```

Branches are cut from `origin/<default-branch>` per repo, the workflow is
persisted (so `project workflows list` and the TUI see it), and re-running the
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

**3 — observe.** For unattended waiting, `agents wait` blocks until the
agent(s) reach a terminal state and prints only the final snapshot — no
polling loop needed:

```
singl --json agents wait --id <id> [--id <id2> ...] [--timeout SECS] [--any]
# one id → a compact agent object; multiple → {"agents":[...],"timed_out":bool}
# exit 0: all completed; exit 1: an agent errored/was killed, or the timeout hit
```

`--id` is repeatable (or comma-separated); `--any` returns as soon as the
first agent finishes; `--interval` tunes the poll rate (default 2s). On
timeout the snapshot is still printed with a non-terminal state.

For point-in-time checks:

```
singl --json agents get    --id <id>                # state: idle routing starting running complete error killed
singl --json agents output --id <id> --offset <n>   # incremental; n = entries already consumed
singl --json agents list
singl --json agents stats                           # active/max — check capacity before spawning
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
singl --json commit stage     --repo <worktree> --all        # or --file a --file b
singl --json commit suggest   --repo <worktree>              # AI commit message from the diff
singl --json commit create    --repo <worktree> --message "..."   # → {"status":"committed","hash":...}
singl sync push               --repo <worktree>
singl --json mr title  --repo <worktree> --source feature/x --target main
singl --json mr create --repo <worktree> --source feature/x --target main --title "..." --desc "..."
```

`commit suggest`/`generate` use the staged diff and fall back to the unstaged
diff when nothing is staged; they only fail when the working tree is fully
clean. Typical unattended flow: `commit stage --all` → `commit suggest --json`
→ `commit create --message ...`.

Forge credentials for `mr create` and `pipeline status` are resolved per host:
the gh CLI, glab's config file (`~/.config/glab-cli/config.yml`, per-host
`token:` entries — self-hosted GitLab instances work), and the
GITHUB_TOKEN / GITLAB_TOKEN env vars, preferring whatever matches the repo's
origin host. When nothing is found the error lists every source checked and
how to fix it.

**6 — clean up.** There is no one-shot workflow teardown in this build.
When the MRs are merged (or the work is abandoned), remove each repo's
worktree and delete the feature branch yourself:

```
singl worktrees remove --repo <repo> --path <worktree> [--force]
singl branches delete  --repo <repo> --branch feature/x [--force]
```

Branch deletion is not undoable from here — only do it after the work landed.

## Command surface

| Noun | Verbs | Key flags |
|---|---|---|
| `status` | — | — |
| `agents` | list get spawn resume kill remove output input wait watch watch-all chat stats | `--id` `--workdir` `--prompt` `--message` `--offset` `--tail` `--last` `--full` `--model` `--effort` `--smart-route` `--max-turns` `--timeout` `--interval` `--any` `--backend` |
| `project` | list status load info refresh branch-check context workflows | `--name` (load) `--project` (handle) `--branch` |
| `project workflows` | list create discover | `--project` `--branch` `--base-dir` (create makes a worktree per repo) |
| `worktrees` | list create remove lock unlock prune | `--repo` `--path` `--branch` `--create-branch` `--start-point` `--force` |
| `branches` | list checkout create delete head compare | `--repo` `--branch` `--start-point` `--base` `--head` `--force` |
| `diff` | workdir branch file staged unstaged merge-base all-repos | `--repo` `--base` `--head` `--file` `--project` |
| `commit` | suggest generate stage create files diff file-diff cherry-pick reset amend | `--repo` `--file` (repeatable) `--all` `--message` `--hash` `--mode soft\|mixed\|hard` |
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
`sync fetch|pull|push|pull-rebase|set-upstream|all`, `rebase onto-main`,
`project workflows discover`. They do not support `--json`.

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
- Wait with `agents wait --id <id> --timeout <secs>` — no `sleep`-and-poll
  loops. Its exit code already distinguishes success from error/killed/timeout;
  then drain `agents output` (or `agents get --last N`) before acting on a result.
- Review a subagent's diff yourself (`diff workdir`) before committing or pushing it.
- Never `remove` an agent you still want to talk to — `kill` keeps it addressable.

## Known gaps in this build

- `agents spawn` exposes no context-file injection or allowed-tool restriction,
  though the daemon API supports both.
- No `project workflows remove`: tearing a workflow down (worktrees + local and
  remote branches) is a per-repo manual job — see step 6.
- `singl prime` is not wired up yet: this file is not embedded in the binary,
  so nothing appends live daemon state to it.

## Improving the tool

The singularity source lives in the project list — friction you hit with
`singl` can be fixed in `singl` itself: edit under `cmd/singl/`, rebuild with
`go build ./...`, restart the daemon, and keep this primer in sync with every
CLI change.
