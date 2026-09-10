# Adversarial Review Flows

> A daemon-side primitive that drives repeated implement → review → fix rounds
> over one piece of work until a reviewer accepts it or a round cap is hit.
> Consumers: `internal/flow` (new), `internal/queue`, `internal/service`,
> `internal/server`, `internal/client`, `cmd/singl`, `internal/app/views`.
> `docs/design/WIRE-CONTRACT.md` has the JSON conventions this follows.

---

## 0. TL;DR of the decisions

- A **Flow is built on top of the queue**, not beside it. It owns one queue
  (`flow-<flow_id>`) and appends **one round's tasks at a time**, because a round
  count that depends on a verdict cannot be a DAG submitted up front.
- A **round is two tasks**: a work task (`implement` in round 1, `fix`
  afterwards) and a `review` task that depends on it, both in the flow's single
  `work_dir`. Every step is a **fresh agent** — the queue only dispatches fresh
  agents, and terminates each one when its task settles.
- The verdict is a **structured JSON file the reviewer writes**, parsed by the
  daemon. Task exit state is liveness only, never the accept/reject decision. An
  unparseable or missing verdict is a **reject, then one re-review, then
  `errored`** — never an accept, never a loop.
- Terminal states: `accepted`, `rejected` (cap reached), `errored`, `cancelled`.
  Persistence mirrors `internal/queue/store.go`.
- The driver is a **reconciler**, not an event handler, for the same reason
  `queue.Manager.tick` is: restart-safety falls out of re-deriving state from
  the queue instead of replaying events.

---

## 1. Data model

### 1.1 Flow, Round, Verdict

`internal/flow/flow.go` defines the domain types wire-first with snake_case tags
— the choice `internal/queue` made, and the reason `internal/api` can alias
queue types instead of re-projecting them.

```go
type Flow struct {
    ID        string      `json:"id"`          // "f1", "f2", …
    QueueID   string      `json:"queue_id"`    // always "flow-<id>"
    Title     string      `json:"title,omitempty"`
    Goal      string      `json:"goal"`        // the implementer's task
    ReviewGoal string     `json:"review_goal,omitempty"` // extra reviewer instructions
    WorkDir   string      `json:"work_dir"`
    MaxRounds int         `json:"max_rounds"`
    Opts      TaskOptions `json:"opts,omitempty"`          // queue.TaskOptions
    ReviewOpts TaskOptions `json:"review_opts,omitempty"`  // defaults to Opts
    State     State       `json:"state"`
    Error     string      `json:"error,omitempty"`
    Rounds    []*Round    `json:"rounds"`
    CreatedAt time.Time   `json:"created_at"`
    EndedAt   *time.Time  `json:"ended_at,omitempty"`
}

// Round{N (1-based), WorkTaskID, ReviewTaskID, ReviewAttempt (1 or 2; §3.3),
//       Verdict *Verdict, State RoundState, StartedAt, EndedAt}
// Verdict{Decision, Summary, Findings} and Finding{Severity, File, Line,
//       Detail} mirror the reviewer's JSON one-for-one; §3.2 has the schema.
```

Flow states: `pending`, `running`, `accepted`, `rejected`, `errored`,
`cancelled`, with `Terminal()`/`Valid()` predicates shaped exactly like
`queue.State`'s so list filters validate the same way. Round states: `running`,
`accepted`, `rejected`, `errored`. A `Round` deliberately stores **no step state
of its own** — a step's state is its `queue.Task`'s, read on demand. Duplicating
it would create two truths about one agent, and `queue.Manager` already
broadcasts changes to the one that matters.

### 1.2 Relationship to queue Tasks and engine Agents

```
Flow f3  (internal/flow)  → queue "flow-f3"
 ├─ Round 1 ── implement → queue.Task t11 → agent (fresh, terminated on done)
 │          └─ review    → queue.Task t12 → agent
 └─ Round 2 ── fix       → queue.Task t13 → agent
            └─ review    → queue.Task t14 → agent
```

`queue.Task` gains **no new fields**. The flow→task mapping lives in the Round
record, so the queue never learns what a flow is; the reverse direction is
recoverable from the task's `QueueID` (`flow-<id>`), which is also what
`EngineRunner.StartTask` passes to the engine as `WorkflowID` and logs into the
agent's output stream. Agent IDs come from `Task.AgentID`, which the queue keeps
set after a task finishes precisely so the transcript stays reachable.

### 1.3 On top of the queue, not beside it — decided, with the argument

A second scheduler would mean re-implementing, and keeping in sync with,
everything `internal/queue` already got right: capacity as backpressure rather
than `ErrAgentLimit` (`dispatch.go`), the one-agent-per-working-directory rule
(`WorkDirBusy`), reaping an agent's process on terminal state so the directory
is genuinely released (`EngineRunner.TerminateAgent`), claim/spawn
reconciliation across an unlocked `StartTask`, shutdown draining, and requeueing
tasks a daemon restart interrupted. A flow needs all of it and adds nothing.

The one thing the queue cannot express is the loop: `validateDeps` rejects
cycles and a flow's round count is not knowable at submit time. So the flow is a
**generator**, not a graph — it appends round N's two tasks when round N-1
settles, and each individual round *is* a legal two-node DAG, which is all the
queue has to understand.

Three consequences, all accepted deliberately: a flow's tasks are visible to
`singl queue list` and steerable with `queue cancel`/`retry`/`pause` (that is
transparency, not leakage — a flow that goes wrong is debuggable with the tools
`prime.md` already documents); `queue wait --queue flow-f3` works with no
flow-specific code; and an operator cancelling a flow's task out from under it
is a real event the reconciler must handle (§4), not an impossibility.

The flow manager talks to the queue through a narrow interface it defines
itself — `Add`, `Get`, `List`, `Cancel` — the same discipline
`queue.AgentRunner` uses to stay testable without an engine.

---

## 2. Round semantics

**What runs, in what order.** Round N submits two tasks in one atomic
`queue.Manager.Add` batch: the work task, and a review task with
`After: []string{"<work>"}` by batch-local name. Round 1's work task is the
implementer, given the flow's `Goal`; every later round's is a fixer, given the
same `Goal` plus the previous round's findings. Task titles are
`f3 r2 fix` / `f3 r2 review`, which become the agents' summaries so
`agents list` and `queue list` stay readable.

**Where.** Every task in every round runs in the flow's single `WorkDir` with
`Opts.UseWorktree` forced **false**. Worktree isolation is wrong here twice over:
the engine rewrites an isolated agent's `WorkDir` to a private checkout
(`setupWorktree`), so the reviewer would review a different tree than the
implementer wrote, and each isolated agent merges back independently
(`mergeWorktreeBack`), so a rejected round's work would already be merged.
Isolation is the caller's job, done before the flow starts — create a workflow
and point the flow at that worktree, as `prime.md` step 1 prescribes;
`flow start` refuses `use_worktree` with `BAD_REQUEST`. A side benefit of one
shared `WorkDir`: the one-agent-per-directory rule serialises the two tasks
independently of the `after` edge.

**How the fix step receives the findings.** By prompt text, composed daemon-side
in `internal/flow/prompt.go`: the original `Goal` verbatim, the previous round's
`Verdict` as a numbered findings list (severity, file:line, detail), and a
one-line summary of every earlier round's verdict so the fixer does not undo a
fix from two rounds ago. The reviewer's transcript is *not* injected —
`prime.md`'s rule that subagents inherit nothing holds here, and a transcript is
unbounded where a verdict is not.

**Fresh agents, not resumed ones.** Partly forced —
`queue.AgentRunner.StartTask` only spawns, and the scheduler terminates a queued
task's agent on terminal state so its directory frees up — and partly wanted: a
fresh reviewer cannot anchor on the verdict it wrote last round, and a fresh
fixer does not carry a round's worth of tool output into the next one. The cost
is each fixer re-reading the code it is about to change; that is the right trade
at these round counts, and `engine.ResumeWithHistory` stays available if a
measurement later says otherwise.

---

## 3. Verdict

### 3.1 The three candidates

**(b) Reviewer task exit state driving `on_failure`** does not work, and why is
worth stating precisely. `engine.handleResult` moves an agent to `AgentError`
only when the backend reports `IsResultError` — a reviewer that finds ten
blockers still exits `complete`, and no prompt can make `claude` or `pi` pick a
process exit code. Even if it could, `queue.failLocked` would spend `MaxRetries`
re-running the rejection first and `FailBlock` would skip the dependents; "the
reviewer rejected the work" and "the reviewer crashed" would be one signal, and
the flow must treat them oppositely. **(a) A structured verdict parsed by the
daemon** is the only mechanism that carries a decision *and* the findings the
next round needs. So: **(c) hybrid, chosen** — the verdict file is the sole
accept path, and the task's terminal state is the liveness signal saying when to
look for it. `done` means "the reviewer ran to completion, go read its verdict";
`failed`/`cancelled` mean it never concluded anything, and the flow errors or
cancels without inventing a verdict.

### 3.2 Format and location

The reviewer is told, in its prompt, to write exactly this JSON to an absolute
path the daemon supplies:

```
<state-dir>/flows/<flow_id>/r<N>-a<attempt>-verdict.json
```

```json
{
  "verdict": "reject",
  "summary": "Retry-After parsing ignores the HTTP-date form.",
  "findings": [
    {"severity": "blocker", "file": "internal/http/retry.go", "line": 88,
     "detail": "strconv.Atoi on a Retry-After that may be an HTTP-date: returns 0 and retries immediately."},
    {"severity": "minor", "file": "internal/http/retry_test.go", "detail": "No test covers the date form."}
  ]
}
```

The file lives in the **daemon state directory, not the work tree**: writing it
into the repo — the `.jira-actions-*.json` precedent in
`internal/jira/prompts.go` — would put an artifact in a tree an agent has been
told to commit, and `commit stage --all` would land it. The path is per round
*and* per review attempt, so no attempt can read another's file; that is also
what keeps §5's restart story independent of wiring order.

Parsing rules, enforced in `internal/flow/verdict.go` as a pure function over
bytes so they are testable without a daemon: unknown top-level keys are
tolerated (a model will add commentary) and unknown `severity` values normalise
to `major`; `verdict` must be exactly `accept` or `reject` after trimming and
lowercasing; `reject` with zero findings is **unparseable**, because a rejection
that gives the fix step nothing to work from is not a usable verdict; `accept`
carrying a `blocker` or `major` finding is read as **reject**, because a
self-contradicting verdict resolves against the work and never for it; and a
file that does not exist when the review task reaches `done` is treated exactly
like an unparseable one.

### 3.3 Unparseable or missing — the fail-open we refuse

An LLM will eventually emit garbage. The rule:

1. First unparseable/missing verdict in a round: the round is recorded with a
   synthetic reject verdict whose single finding is
   `{"severity":"major","detail":"the reviewer did not produce a parseable verdict at <path>: <parse error>"}`,
   `ReviewAttempt` becomes 2, and the flow submits **one more review task for
   the same round** (depending on nothing — the work is already done).
2. Second unparseable/missing verdict in the same round: the flow terminates as
   `errored` with `Error: "unparseable reviewer verdict after 2 attempts"`.

This deadlocks nowhere (attempts capped at 2 per round, rounds at `MaxRounds`)
and accepts nothing silently. A round that consumed a re-review still counts as
one round against the cap: the cap bounds review cycles over the work, and a
reviewer that cannot write JSON has not reviewed anything.

---

## 4. Termination

| Terminal state | Cause | What it means to a caller |
|---|---|---|
| `accepted` | a round's verdict was `accept` | the work in `work_dir` passed review; diff it, commit it, land it |
| `rejected` | round `MaxRounds` ended in a reject | the work is on disk and unaccepted; read `rounds[last].verdict.findings` and decide by hand |
| `errored` | a task `failed` after its retries, an agent timed out, `work_dir` vanished, or §3.3 fired | the flow machinery gave up; `error` says why, the task IDs say where |
| `cancelled` | the operator cancelled the flow, or cancelled one of its tasks directly | nothing more will run |

Non-terminal: `pending` (record created, round 1 not yet submitted — one
reconciler pass wide, so a flow is never observed stateless) and `running`.

Four cases worth spelling out. **A reviewer that keeps rejecting** is bounded
only by the cap, by design: any "close enough" heuristic is the fail-open §3
refuses. `MaxRounds` defaults to 3, validated to 1..20. **Agent error and
timeout** are not the flow's to police — `Opts.TimeoutSecs` reaches
`engine.AgentOptions.Timeout`, a timed-out agent lands `killed`, the queue's
`reconcile` fails the task after spending `MaxRetries`, and the flow errors when
it sees `failed`. **`Flow.Cancel`** marks the flow `cancelled` and then calls
`Queue.Cancel` on each of its own recorded non-terminal task IDs, individually
and *not* via `CancelQueue`: the flow must never cancel a task it did not
create, and `Manager.Add` adopts a pre-existing queue named `flow-f3` rather
than refusing it. **A task cancelled outside the flow** terminates the flow as
`cancelled` too — continuing would mean reviewing work someone deliberately
stopped. `Flow.Remove` deletes the record and its verdict directory and is
refused with `CONFLICT` while the flow is non-terminal, the same rule and reason
as `queue.Manager.RemoveQueue`; it leaves the underlying queue alone, which
`queue remove` handles.

> **Added later:** three of these four terminal states turned out not to be the
> end of the flow. `Flow.Continue` (§11, an addition after this design) takes a
> `rejected`, `errored` or `cancelled` flow back to `running` with a raised cap.
> Everything above still describes how a flow *arrives* at a terminal state.

---

## 5. Persistence

`internal/flow/store.go` is `internal/queue/store.go` with `Flow` in place of
`queueState`, copied on purpose so there is one persistence idiom in the daemon
to learn: one file per flow at `<state-dir>/flows/<flow_id>.json` holding the
whole record including every round; `Save` writes a temp file in the same
directory, chmods `0600` and renames, so a crash mid-write leaves the previous
version intact; `Load` returns `([]persisted, []error)` so a corrupt file is
logged and skipped rather than stopping the daemon from serving the rest; a zero
`Store` (`Dir == ""`) is a valid no-op for tests and for a daemon whose state
directory is unreadable. `daemon.Paths` gains `Flows string // <Dir>/flows`
alongside `Queues`.

`Restore` re-reads every record, recovers the `f<n>` sequence from the filenames
(as `queue.Manager.Restore` does with `taskIDPattern`), and then **does nothing
else** — no event replay. The next reconciler pass re-derives each non-terminal
flow's position from its tasks, which the queue has already restored on its own
terms (a task recorded `running` comes back `blocked` with its attempt
refunded). Two restart cases then fall out for free. If the daemon died between
a review task reaching `done` and the flow reacting, a handler-only design loses
that edge forever while the reconciler just reads `done` and parses the verdict
next pass. If it died mid-review, the requeued review task re-runs and writes to
`r<N>-a<attempt>-verdict.json` — same attempt, same path — truncating whatever
the dead attempt left, so no stale file can be read as this attempt's verdict
and no ordering between `flowMgr.Restore()` and `taskQueue.Start()` is
load-bearing.

---

## 6. Wire contract

New `internal/api/flow.go` aliases the domain types rather than re-projecting
them — they carry snake_case tags already, the same rationale as
`internal/api/queue.go`: `Flow`, `FlowRound`, `FlowState`, `FlowVerdict` and
`FlowTree` alias the `service` names, which alias the `internal/flow` ones via
`service/types.go`.

| # | Service method | Endpoint | Request | Response | Codes |
|---|---|---|---|---|---|
| 126 | `Flow.Start` | `POST /api/flow/start` | `api.FlowStartRequest` | `api.FlowStartResponse` | `BAD_REQUEST`, `UNAVAILABLE` |
| 127 | `Flow.List` | `GET /api/flow/list?state=` | — | `api.FlowListResponse` | `BAD_REQUEST`, `UNAVAILABLE` |
| 128 | `Flow.Get` | `GET /api/flow/get?flow_id=` | — | `*api.Flow` | `NOT_FOUND`, `BAD_REQUEST`, `UNAVAILABLE` |
| 129 | `Flow.Tree` | `GET /api/flow/tree?flow_id=` | — | `api.FlowTreeResponse` | `NOT_FOUND`, `BAD_REQUEST`, `UNAVAILABLE` |
| 130 | `Flow.Cancel` | `POST /api/flow/cancel` | `api.FlowIDRequest` | — | `NOT_FOUND`, `BAD_REQUEST`, `UNAVAILABLE` |
| 131 | `Flow.Remove` | `POST /api/flow/remove` | `api.FlowIDRequest` | — | `NOT_FOUND`, `CONFLICT`, `UNAVAILABLE` |

`FlowStartRequest` is `{title, goal, review_goal, work_dir, max_rounds, opts,
review_opts}`, with `work_dir` cleaned by `Server.validateRepoPath` before the
service sees it exactly as `handleQueueAdd` does. Every `/api/flow/*` route
answers `UNAVAILABLE` when the daemon has no flow manager, mirroring the queue's
nil-manager degradation.

One new WS event, S→C: `flow_updated` with `api.FlowUpdatedPayload{Flow}`, one
frame per flow state change carrying the whole flow so a view needs no follow-up
fetch — the same reasoning as `queue_task_changed`.
`api.WSEventFlowUpdated = "flow_updated"` joins the constants in
`internal/api/ws.go`; the flow's tasks keep producing `queue_task_changed`
frames unchanged. No streaming endpoints: a flow outlives the connection that
started it, the same argument `service.QueueService`'s doc comment makes.

`Flow.Tree` returns a flat, parent-linked node list rather than a nested one,
matching `queue.Graph`'s nodes+edges shape and rendering without recursion. A
`FlowTreeNode` is `{id ("f3", "f3/r2", "f3/r2/review"), parent_id, kind
("flow"|"round"|"step"), label, state (the flow/round state, or the task's for a
step), round, task_id, agent_id, agent_state, verdict}`. The tree is derivable
client-side from `Flow` + `queue list` + `agent list`, but that is three round
trips and a join every client would get subtly wrong; the daemon owns it.

---

## 7. CLI surface

New noun `flow`, dispatched from `cmd/singl/main.go`, split as `cmd_queue_*.go`
is: `cmd_flow.go` (dispatch, start, cancel, remove), `cmd_flow_view.go`
(list/show/tree renderers), `cmd_flow_wait.go` (poll loop).

```
singl flow start  --workdir <dir> --prompt <goal> [--review-prompt <text>]
                  [--max-rounds N] [--title T]
                  [--model M] [--effort low|medium|high] [--timeout SECS]
                  [--backend claude|pi|herdr] [--context-file P ...] [--allowed-tools a,b]
                  [--max-retries N] [--reviewer-model M] [--reviewer-effort E]
                  [--smart-route[=bool]] [--no-smart-route]
singl flow list   [--state s1,s2]
singl flow show   --id <flow-id>          # rounds, verdicts, findings, agent ids
singl flow tree   --id <flow-id>          # ascii tree (--json for the node list)
singl flow wait   --id <flow-id> [--timeout SECS] [--interval SECS]
singl flow cancel --id <flow-id>
singl flow remove --id <flow-id>
```

`--use-worktree` is deliberately absent (§2). `--smart-route` reuses
`smartRouteFlags`/`smartRoute` so a flow's tasks route exactly as the same
prompt would from `queue add` or `agents spawn` — the precedence bug
`TaskOptions.RouteEnabled` documents must not be reintroduced at a third call
site. Reviewer options default to the work options; `--reviewer-*` overrides only
what it names. `flow wait` polls and supports `--json`, mirroring `queue wait`:
exit `0` when the flow reached `accepted`, `1` on `rejected`, `errored`,
`cancelled` or `--timeout` (JSON then carries `"timed_out": true` and the round
count) — that exit code is the whole point of the verb. `cmd_help.go` gains
`nounUsage["flow"]`, and `printUsage` a `flow` line.

### prime.md changes

A new step **2b — when acceptance is uncertain, use a flow**, between "queue the
work as a DAG" and "wait for the queue", carrying the rule that chooses between
them (a DAG is for work whose *shape* is known; a flow for work whose
*acceptance* is not), a worked `flow start` + `flow wait` pair and the exit-code
table. Plus a `flow` row in the command-surface table; orchestration rules — aim
a flow at a workflow worktree, never the live checkout; debug one with
`queue list --queue flow-<id>` and `agents output --id <agent_id>`; never
`agents input` a flow's agent, since the fix round is the correction mechanism —
and known-gaps entries for the absent cost cap, the inherited `waiting_human`
gap, and cosmetic rejection bounded only by `--max-rounds`.

---

## 8. TUI

**New view `internal/app/views/flow.go`** (`FlowsView`), registered in
`registerCommonViews` so it exists in both repo and project mode, immediately
after `Agents` and taking the next F-key — which shifts `Config` by one (F2–F7
in repo mode, F3–F8 in project mode). That renumbering is the only change to
`internal/app/app.go` besides the registration.

Two panes, `tab` toggling focus. Left is a `components.Filter[FlowInfo]` list —
one row per flow: id, title, state, **`round 2/3`**, and the basename of
`work_dir`. Right is the tree of the selected flow:

```
▾ f3  retry-after handling                 running   round 2/3
  ▾ round 1                                rejected  3 findings
      implement  t11  done     agent-1712-4  complete
      review     t12  done     agent-1712-5  complete   reject: 3 findings
  ▾ round 2                                running
      fix        t13  running  agent-1712-9  running
      review     t14  blocked  —             —
```

Nodes are the three `Flow.Tree` kinds: the flow root, one per round, one per step
carrying its task state, agent id and agent state. Keys: `j`/`k` and ↑/↓ move,
`l`/→/`enter` expand, `h`/← collapse, `a` opens the selected step's agent in the
Agents view, `n` starts a flow, `c` cancels behind a `components.ConfirmPrompt`,
`r` refreshes, `/` filters; `KeyBindings()`/`ShortHelp()` as every other view.

**Starting a flow** (`n`) opens a modal in the `components.TextInput` +
`ConfirmPrompt` idiom `WorkflowsView`'s workflow-start modal already uses: work
dir (prefilled from `v.repoPath`, or the selected workflow's worktree in project
mode), goal, optional review focus, max rounds. Confirm calls
`v.services.Flow.Start`; refusals land in the flash-message line, like
`workflowStatusMsg`. **Round count** appears in three places: the list row
(`round 2/3`), the tree root, and the selected flow's pane header.

**Live state** arrives with no new plumbing: the view refreshes on the app-level
`views.StreamTickMsg` chain (2s, already driving `AgentView.AgentTickCmd` and
surviving view switches) and on `views.AgentUpdateMsg`, which `Model.Run`'s
`Agent.SubscribeAll` subscription delivers on every agent event — and every flow
transition is caused by one. A `Flow.Subscribe` stream is deliberately not
added: no view consumes WS frames today, and `flow_updated` exists on the wire
for clients that will. Jumping to an agent needs one new message,
`views.OpenAgentMsg{AgentID}` in `internal/app/views/messages.go`, handled in
`Model.Update` beside `OpenPRForBranchMsg`.

---

## 9. Implementation plan

Thirteen units, each one agent's task, self-contained and testable; the
dependency column is the DAG to submit. Units 1 and 3 are roots.

| # | Unit | Files | Depends on |
|---|---|---|---|
| 1 | Flow/Round/Verdict types, state enums with `Terminal`/`Valid`, and the verdict parser with every §3.2 rule under test (contradiction, empty-reject, unknown severity, junk) | `internal/flow/flow.go`, `verdict.go`, `verdict_test.go` | — |
| 2 | Prompt composition: implement / fix / review builders, including the verdict-path instruction and the prior-rounds summary. Pure functions, golden-string tests | `internal/flow/prompt.go`, `prompt_test.go` | 1 |
| 3 | `queue.Manager.AddChangeObserver(func(Task))` — an additive observer slot alongside the single `OnChange` the daemon's WS hook owns, mirroring `engine.AddAgentObserver`. Without it the flow manager and the WS broadcast would starve each other | `internal/queue/manager.go`, `manager_test.go` | — |
| 4 | `flow.Store` + `Restore`: one file per flow, atomic write, skip-and-log on corrupt files, no-op zero store, `f<n>` sequence recovery | `internal/flow/store.go`, `restore.go`, `store_test.go` | 1 |
| 5 | `flow.Manager` read/write API — `Start`, `Get`, `List`, `Cancel`, `Remove`, `Tree` — over a `TaskQueue` interface the package defines, with a fake in tests. No round advancement yet | `internal/flow/manager.go`, `tree.go`, `manager_test.go` | 1, 2, 4 |
| 6 | The reconciler: tick + `Wake`/`Notify`, submit round 1, advance on settled tasks, parse verdicts, the §3.3 re-review, cap → `rejected`, failure → `errored`, externally-cancelled task → `cancelled`, emit changes | `internal/flow/reconcile.go`, `reconcile_test.go` | 3, 5 |
| 7 | `service.FlowService` interface + `local` implementation (nil manager → `ErrUnavailable`) + `fake` + `Services` field + `types.go` aliases | `internal/service/flow.go`, `local/flow.go`, `fake/services.go`, `services.go`, `types.go` | 5 |
| 8 | Wire types, handlers, routes, `flow_updated` event and its broadcast hook | `internal/api/flow.go`, `ws.go`, `internal/server/flow_handlers.go`, `routes.go`, `flow_handlers_test.go` | 7 |
| 9 | Client SDK + `remote` service implementation, with the error-code round-trip test the other capabilities have | `internal/client/flow.go`, `internal/service/remote/flow.go`, `client/flow_test.go` | 8 |
| 10 | Daemon wiring: `Paths.Flows`, construct store+manager, register the queue observer from unit 3, `Restore`, `Start`, `Stop` in the shutdown order, extend `local.New` | `internal/daemon/paths.go`, `cmd.go`, `internal/service/local/services.go` | 6, 7 |
| 11 | CLI: the `flow` noun, all seven verbs, `nounUsage`, `printUsage`, tests mirroring `cmd_queue_test.go` | `cmd/singl/cmd_flow.go`, `cmd_flow_view.go`, `cmd_flow_wait.go`, `main.go`, `cmd_help.go`, `cmd_flow_test.go` | 9 |
| 12 | `FlowsView`: list pane, tree pane, start modal, cancel confirm, key bindings, `OpenAgentMsg`, registration and the F-key shift | `internal/app/views/flow.go`, `flow_test.go`, `messages.go`, `internal/app/app.go` | 9 |
| 13 | Docs: `prime.md` step 2b + command table + rules + known gaps; `WIRE-CONTRACT.md` rows 126–131 and the `flow_updated` row; `architecture.md` view list and endpoint table | `cmd/singl/prime.md`, `docs/design/WIRE-CONTRACT.md`, `docs/architecture.md` | 8, 11, 12 |

Units 10–11 are the earliest end-to-end usable point; 12 is the one the request
was actually about. Units 7–9 and 12 do not need 6 to be reviewable, so the wire
and TUI halves can proceed against a manager that only submits round 1.

---

## 10. Open questions and accepted risks

**Unresolved from the code.**

*Reviewer independence.* Nothing in `internal/engine` says whether a reviewer on
the implementer's own model family rubber-stamps more than a different one would.
`--reviewer-model`/`--reviewer-effort` exist and default to the work options;
whether a forced split should be the default is empirical, and this design
cannot settle it.

*Per-round commits.* The flow does not touch git — agents commit or not per their
prompts — which leaves round-over-round diffs unreviewable in the TUI's diff
views. A per-round commit would fix that and make a rejected flow revertable
round by round; out of scope here because it changes who owns the branch.

*Writing outside the work dir.* The verdict path assumes agents can write to the
daemon state dir. `AgentOptions.AllowedTools` restricts tool *names*, not paths,
and this build has no filesystem sandbox, so that holds today. If a sandbox
lands, the path moves into the tree under `.singularity/` and gitignoring it
becomes a hard requirement.

**Deliberately accepted failure modes.**

*No convergence guarantee.* [Revised by §11 — the cap is now raisable after the
fact.] A reviewer can reject cosmetically until the cap. The
alternative — a daemon-side "good enough" rule — is exactly the fail-open §3
refuses, so the cap is the answer and `rejected` is a first-class outcome, not an
error.

*No cost cap.* [Revised by §11 — a continue buys roughly 2 more agents per round
asked for, so the bound is now what an operator re-authorises, up to 20 rounds.]
N rounds is roughly 2N agents. The engine tracks `TotalCostUSD`
per agent but nothing aggregates it; `--max-rounds` and `opts.timeout_secs` are
the only bounds. Summing round costs into `flow show` is a cheap follow-up.

*Operator interference through the queue.* `queue retry` on a flow's review task
re-runs it and the flow parses whatever verdict lands. That is a feature — a
manual re-review is legitimate — but it means `ReviewAttempt` is not the only
thing that can produce a second verdict for a round. The reconciler always reads
the file for the *current* attempt, so the retried task's output is what it sees.
Relatedly, if a queue named `flow-f3` somehow already exists, `Manager.Add`
adopts it rather than refusing; the flow only ever addresses task IDs it recorded
itself (§4), so the worst case is two unrelated task sets sharing one queue's
pause flag and tallies.

*No human escalation.* `queue.StateWaitingHuman` is inert in this build, so a
flow cannot stop to ask a question; a stuck agent shows up as a task still
`running`, bounded by `opts.timeout_secs`. That is the queue's documented gap,
inherited unchanged rather than worked around. Forbidding `use_worktree` (§2)
incidentally keeps flows clear of the never-reclaimed-worktree gap too.

---

## 11. Continuing a flow — an addition after the original design

**This section was not part of the design above.** Sections 0–10 were written and
implemented first, and they treat `rejected` as the end of a flow: read the
findings and decide by hand. In use that turned out to be the wrong shape for
the commonest outcome. A `rejected` flow is not a failed flow — it is a flow
whose reviewer was still rejecting when the cap ran out — and the only move the
design left was to start another flow against the same work dir, which throws
away every verdict and finding and hands a fresh reviewer a tree it has no
account of. `Flow.Continue` (`internal/flow/continue.go`) was added afterwards to
close that. It is recorded here, separately, so this document stays a record of
what was decided when rather than reading as though it was always here.

**Decision: a continue extends the same flow; it does not seed a new one.** The
alternative considered was a `flow start --from <id>` that copied the goal and
options into a new record and linked back to the old one. Extending won on the
argument that makes flows worth having at all: `FixPrompt` composes round N+1's
work from the rounds before it, so a continued flow's fixer sees the history a
new flow's implementer would have to be told about by hand. Round numbering
therefore carries on (a flow that stopped at round 3 opens round 4), every round
already recorded keeps its state, verdict and findings, and `flow show` stays one
contiguous account of the work instead of a chain of records an operator has to
reassemble. Nothing but the round count is re-specifiable: goal, review goal,
`work_dir`, `Opts` and `ReviewOpts` stay the flow's own, because rounds recorded
against one goal would stop meaning anything under another. If the goal was
wrong, the answer is still a new flow.

**Continuable states.** `rejected`, `errored` and `cancelled` — the three
terminals that are not an acceptance. `accepted` is refused (`ErrNotContinuable`
→ `CONFLICT`): its work passed review, so there is nothing to fix. `pending` and
`running` are refused by the same sentinel, on the grounds that the request is
well formed and it is the flow's state that says no: a flow that has not finished
is not something to continue but something to wait for or cancel. The state is
re-checked under `m.mu` after the filesystem and queue work, because a second
continue — or a reconciler pass on the flow the first one revived — can land in
between.

**The rule for a trailing half-run round.** A `cancelled` flow's last round is
usually mid-flight: its work task done and its review cancelled, or a step still
recorded non-terminal because the daemon died before `Cancel` reached the queue.
The rule chosen — and it is the one part of a continue that is not obvious — is
that **a trailing round which never reached a verdict is settled `errored`,
given §3.3's synthetic reject as its verdict, and never reopened.** Reopening it
would mean a round with two work tasks. Leaving it non-terminal would hand the
reconciler a "current round" it would go on polling, and `advanceRound` would
either read a cancelled step and terminate the flow the operator has just
continued, or wait forever on a step nothing will dispatch. Settling it means the
next pass takes `afterSettledRound`, whose only question is whether the cap
leaves room for round N+1. The synthetic reject carries a `major` finding saying
the tree may hold a half-finished round, so the next fixer is told rather than
left to discover it — and a round that *did* reach a verdict is left exactly as
it is, because a rejection at the cap is precisely what the next round is for. A
verdict file a killed reviewer happened to leave on disk is likewise not read:
that attempt was stopped, and reading a decision out of an interrupted process is
how a fail-open gets in (§3.3).

Before any of that, `Continue` stops the flow's own recorded task IDs — the same
authority `Cancel` has (§4), never `CancelQueue` — while the flow is still
terminal and the reconciler is therefore still skipping it. A half-run round can
leave a task the queue would still dispatch, and that agent would be writing into
the very tree the new round is about to fix.

**The ceiling.** `rounds` is an increment on `MaxRounds`, defaulting to 3 (the
same number `MaxRounds` itself defaults to), and the result must still satisfy
the 1..20 §4 validates everywhere else. Two refusals, both `ErrInvalid` →
`BAD_REQUEST`: a flow already at 20 is refused *by name* rather than with a range
complaint, because no round count would have worked and the operator needs to
know that instead of trying a smaller one; an increment that would overshoot
names the largest one that would have fit. The `work_dir` is re-stat'ed too — it
was checked at start, but a continue can come hours later, and the commonest case
is a flow aimed at a workflow's worktree that has since been torn down.

**What the record looks like afterwards.** `Error` and `EndedAt` are cleared, so a
continued flow does not carry the reason it stopped as though it were still true;
the rounds before it keep theirs. The state goes to `running` and `Continue`
submits nothing itself, for the reason `Start` leaves round 1 to the reconciler
(§5): the submit path a restart takes must be the path a continue takes, or the
two disagree exactly when the daemon dies between them. It does call `Wake`,
because unlike a fresh flow a continue is an operator watching for something to
happen. No change callback fires — that slot reports the reconciler's work, not
the API's, exactly as `Start`'s pending record and `Cancel`'s terminal one go
unemitted.

**Surfaces.** `POST /api/flow/continue` (`api.FlowContinueRequest` →
`api.FlowContinueResponse`; row 127 of `docs/design/WIRE-CONTRACT.md`),
`FlowService.Continue`, `Client.FlowContinue`, `singl flow continue --id <id>
[--rounds N]`, and `C` in the TUI's Flows view — which offers it only for a
continuable flow and otherwise says why in the flash line, and shows the current
round count, the cap it would reach and the round it resumes at before acting.

## 12. A planning phase before round 1 — a second addition after the original design

**Like §11, this was not part of the design above.** A flow's round 1 goes
straight from `Goal` to an implementer. In practice a goal is often written
before anyone has read the code it touches, and the implementer is the cheapest
model in the loop — the one place a wrong assumption is most expensive to make
and least likely to be caught, because nothing downstream of it re-derives the
approach; a reviewer only judges the diff against the goal, not the plan the
diff should have followed. `EnablePlanning` (`internal/flow/plan.go`) adds an
optional refinement step, on a model the caller can point at something bigger
than the implementer, between `Start` and round 1.

**Decision: one task, not a round.** A round (§1.1) is an implement/fix →
review cycle with its own accept/reject verdict; planning has neither a review
step nor a decision to record; it either produces a plan or the flow errors.
Giving it a `Round` would mean inventing a verdict for something nobody is
asked to grade. Instead its liveness lives directly on `Flow` — `PlanTaskID`
while the task is in flight, `Plan` once it is read — the same way a round's
task IDs live on `Round`, one level up.

**The gate in `advanceOnce`.** A flow with `EnablePlanning` set and `Plan == ""`
is gated before the `len(Rounds) == 0` check that opens round 1: `PlanTaskID ==
""` submits the plan task, and a `PlanTaskID` already recorded polls it. Once
`Plan` is non-empty the gate never fires again — the plan is written once, the
way `Goal` is fixed at `Start` and repeated verbatim rather than re-fetched —
and every later pass falls through to the ordinary round logic unchanged. A
flow that does not set `EnablePlanning` never enters this branch at all, so its
behaviour is exactly what it was before this section existed.

**Output contract.** `PlanPrompt` tells the planner to explore the codebase —
explicitly not to modify it — and write free-form markdown to a path beside the
verdict files (`Store.VerdictDir(id)/plan.md`), the same "not the work tree"
reasoning `verdictPath` follows, for the same reason: an agent told to commit
its work must not also be told to commit the state the daemon reads back. There
is no schema to parse, unlike a verdict — the planner's output is prose, not a
decision — so the only failure this fails closed on is the file being missing
or empty when the task reaches `done`; a missing plan errors the flow rather
than proceeding with an empty one, the same "never infer success from a task
exiting cleanly" rule §3.2 states for a verdict. A plan task that fails,
is cancelled, or is skipped is handled exactly as a round's step is (`step` in
`reconcile_helpers.go`), with the same three outcomes.

**Folding the plan in.** `ImplementPrompt` and `FixPrompt` both render `## Plan`
right after `## Goal` when `Plan` is non-empty (`writePlan`), so an implementer
and every later fixer read the identical refinement — the same verbatim
repetition `writeGoal` already does for the goal itself.

**Options.** `PlanOpts` defaults to `Opts` when left entirely unset, the rule
`ReviewOpts` already follows, and is refused with `use_worktree` set for the
reason every step's options are (§2). The CLI adds `--planning` (off by
default — an existing flow's behaviour is unchanged unless asked),
`--planner-model` and `--planner-effort`, composed the same three-way way
`--reviewer-*` is (`planOptsFromFlags` beside `composeFlowOpts`,
`cmd/singl/cmd_flow.go`).

**Surfaces.** `Flow.EnablePlanning` / `Flow.PlanOpts` / `Flow.PlanTaskID` /
`Flow.Plan` on the existing `api.Flow`/`api.FlowStartRequest` aliases — no new
route, since planning rides `POST /api/flow/start` (row 126) like every other
per-flow option. `PlanPrompt` (`internal/flow/prompt.go`), and a `plan` step
node in `Flow.Tree`, parented directly under the flow root rather than under a
round, since it runs before any round exists.
