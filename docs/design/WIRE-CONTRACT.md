# Wire Contract — daemon ↔ TUI

Phase B deliverable. One row per service-interface method (across all 16 interfaces in `internal/service`), specifying its HTTP endpoint (or WS stream subscription topic), the request / response Go types in `internal/api`, and the sentinel error code(s) the operation may return.

Every JSON payload uses snake_case keys. Every `POST` request body is `application/json`. Every JSON response is wrapped in `api.APIResponse{success, data, error, code}` — the "Response type" column below is the Go type that lives inside `data`. Every reachable HTTP error includes a stable string `code` from §1 mapped to the HTTP status code listed there.

---

## 1. Error code → HTTP status mapping

Defined as Go constants in `internal/api/errors.go`. The remote client decodes a `code` field on every non-2xx response and maps it back to the matching sentinel from `internal/service`. Views can therefore use `errors.Is(err, service.ErrXyz)` end-to-end.

| Code (`api.ErrCodeXxx`)   | HTTP status | Service sentinel                  |
|---------------------------|------------:|-----------------------------------|
| `MR_ALREADY_EXISTS`       | 409         | `service.ErrMRAlreadyExists`      |
| `NOT_FOUND`               | 404         | `service.ErrNotFound`             |
| `CONFLICT`                | 409         | `service.ErrConflict`             |
| `AGENT_LIMIT`             | 429         | `service.ErrAgentLimit`           |
| `NO_FORGE`                | 404         | `service.ErrNoForge`              |
| `REBASE_IN_PROGRESS`      | 409         | `service.ErrRebaseInProgress`     |
| `NO_REBASE_IN_PROGRESS`   | 409         | `service.ErrNoRebaseInProgress`   |
| `PERMISSION_DENIED`       | 401         | `service.ErrPermissionDenied`     |
| `UNAVAILABLE`             | 503         | `service.ErrUnavailable`          |
| `CANCELED`                | 499         | `service.ErrCanceled`             |
| `BAD_REQUEST`             | 400         | `service.ErrInvalidRequest`       |
| `INTERNAL`                | 500         | (no sentinel — fallback)          |

`499 Client Closed Request` is non-standard but is unambiguous (no overlap with `408 Request Timeout`) and matches nginx convention for cancellation.

---

## 2. Streaming pattern

Long-running operations (sync ops, rebase-onto-main, project discovery, agent output streams, subscriptions) use a two-step pattern:

1. Client `POST`s the operation; server returns `202 Accepted` with `{"stream_id": "..."}` (see `api.StreamStartResponse`).
2. Client opens WS and sends `{"type": "subscribe_stream", "payload": {"stream_id": "..."}}`.
3. Server pushes frames `{"type": "stream:<id>", "payload": <event>}` until a terminal frame with `"done": true` (or an `error` frame). Server then closes the stream registry entry.

For subscriptions that are inherently long-lived (`Repo.Subscribe`, `Project.Subscribe`, `Project.SubscribeWorkflows`, `Pipeline.Subscribe`, `Agent.Subscribe`, `Agent.SubscribeAll`) the same pattern is used. The first message after the `POST` carries the initial snapshot when one is available.

Stream IDs are opaque UUIDs minted by the daemon. The client may cancel a stream by sending `{"type": "cancel_stream", "payload": {"stream_id": "..."}}` — the cancel closure on the service-interface side translates to this WS message.

---

## 3. WebSocket events

| Event name (`api.WSEventXxx`) | Direction | Payload Go type            | Notes |
|-------------------------------|-----------|----------------------------|-------|
| `repo_update`                 | S→C       | `*git.RepoInfo`            | Existing. |
| `branch_update`               | S→C       | `api.BranchUpdatePayload`  | Existing — keyed `{branch}`. |
| `pipeline_update`             | S→C       | `service.PipelineEvent`    | NEW emission; broadcast on engine pipeline tick. |
| `project_update`              | S→C       | `api.ProjectUpdatePayload` | Fix mismatch: server now emits `{status, repo_name?}` envelope. |
| `agent_started`               | S→C       | `api.AgentStartedPayload`  | Fix mismatch: now `{agent_id, task, work_dir}`. |
| `agent_output`                | S→C       | `api.AgentOutputPayload`   | NEW emission; one frame per new output line. |
| `agent_complete`              | S→C       | `api.AgentCompletePayload` | NEW emission. |
| `agent_resumed`               | S→C       | `api.AgentResumedPayload`  | Emitted when an agent already reported terminal goes non-terminal again (follow-up message to a finished agent). |
| `agent_error`                 | S→C       | `api.AgentErrorPayload`    | NEW emission. |
| `workflow_updated`            | S→C       | `service.WorkflowEvent`    | NEW; replaces TUI polling tick. |
| `sync_progress`               | S→C       | `service.SyncProgressEvent`| NEW; piggybacks on `stream:<id>` envelope. |
| `discovery_progress`          | S→C       | `service.DiscoveryProgressEvent` | NEW; same envelope. |
| `queue_task_changed`          | S→C       | `api.QueueTaskChangedPayload` | NEW; one frame per task state change, broadcast to every client (the daemon wires it to `queue.Manager.OnChange`). Carries the whole task, so a view needs no follow-up fetch. |
| `flow_updated`                | S→C       | `api.FlowUpdatedPayload`      | NEW; one frame per flow state change, broadcast to every client (the daemon wires `Server.FlowChangeHook` to `flow.Manager.OnFlowChange`). Carries the whole flow, rounds and verdicts included, so a view needs no follow-up fetch. The flow's tasks keep producing `queue_task_changed` frames unchanged. |
| `error`                       | S→C       | `api.ErrorPayload`         | Existing — `{error, code?}`. |
| `subscribed`                  | S→C       | `api.SubscribedPayload`    | Ack reply. |
| `stream:<id>`                 | S→C       | dynamic                    | Generic stream frame; one envelope per active stream. |
| `subscribe`                   | C→S       | none                       | Existing. |
| `refresh_repo`                | C→S       | `api.RefreshRepoPayload`   | Existing — extended to carry optional `path`. |
| `subscribe_stream`            | C→S       | `api.SubscribeStreamPayload` | NEW. |
| `cancel_stream`               | C→S       | `api.CancelStreamPayload`  | NEW. |

---

## 4. Endpoint table (one row per service method)

All endpoints live under `/api`. Streaming operations are marked **stream**: response is `202 + api.StreamStartResponse`; subsequent frames arrive over WS on topic `stream:<id>`. Where the same endpoint already exists in legacy form (`/api/repo/info` etc.) we keep the path and merely formalize the request/response shape.

| # | Service.Method          | HTTP                                                | Request type                            | Response type                              | Errors |
|---|-------------------------|-----------------------------------------------------|-----------------------------------------|---------------------------------------------|--------|
| **Repo** ||||||
|  1 | `Repo.Open`              | `POST /api/repo/open`                              | `api.RepoOpenRequest`                   | `*api.RepoInfo` (alias of `*git.RepoInfo`)  | `NOT_FOUND`, `BAD_REQUEST` |
|  2 | `Repo.Find`              | `GET  /api/repo/find?path=`                        | —                                       | `api.RepoFindResponse`                      | `NOT_FOUND` |
|  3 | `Repo.Subscribe`         | `POST /api/repo/subscribe` **stream**              | `api.RepoSubscribeRequest`              | `api.StreamStartResponse`                   | `NOT_FOUND` |
| **Branch** ||||||
|  4 | `Branch.List`            | `GET  /api/branch/list?repo_path=`                 | —                                       | `api.BranchListResponse`                    | `NOT_FOUND` |
|  5 | `Branch.Create`          | `POST /api/branch/create`                          | `api.BranchCreateRequest`               | —                                           | `CONFLICT`, `NOT_FOUND` |
|  6 | `Branch.Checkout`        | `POST /api/branch/checkout`                        | `api.BranchCheckoutRequest`             | —                                           | `CONFLICT`, `NOT_FOUND` |
|  7 | `Branch.CheckoutDetached`| `POST /api/branch/checkout_detached`               | `api.BranchCheckoutDetachedRequest`     | —                                           | `NOT_FOUND` |
|  8 | `Branch.CheckoutDetachedAt`| `POST /api/branch/checkout_detached_at`          | `api.BranchCheckoutDetachedAtRequest`   | —                                           | `NOT_FOUND` |
|  9 | `Branch.Delete`          | `POST /api/branch/delete`                          | `api.BranchDeleteRequest`               | —                                           | `NOT_FOUND`, `CONFLICT` |
| 10 | `Branch.HEAD`            | `GET  /api/branch/head?repo_path=`                 | —                                       | `api.BranchHEADResponse`                    | `NOT_FOUND` |
| 11 | `Branch.ResolveRef`      | `GET  /api/branch/resolve?repo_path=&ref=`         | —                                       | `api.BranchResolveRefResponse`              | `NOT_FOUND` |
| 12 | `Branch.Compare`         | `POST /api/branch/compare`                         | `api.BranchComparisonRequest`           | `*api.BranchComparison`                     | `NOT_FOUND` |
| 13 | `Branch.CompareByTree`   | `POST /api/branch/compare_tree`                    | `api.BranchComparisonRequest`           | `*api.TreeComparison`                       | `NOT_FOUND` |
| **Diff** ||||||
| 14 | `Diff.BranchDiff`        | `POST /api/diff/branch`                            | `api.BranchDiffRequest`                 | `*api.BranchDiff`                           | `NOT_FOUND` |
| 15 | `Diff.WorkdirStatus`     | `GET  /api/diff/workdir?repo_path=`                | —                                       | `*api.WorkdirDiff`                          | `NOT_FOUND` |
| 16 | `Diff.FileDiff`          | `POST /api/diff/file`                              | `api.FileDiffRequest`                   | `api.FileDiffResponse`                      | `NOT_FOUND` |
| 17 | `Diff.StagedFileDiff`    | `POST /api/diff/file_staged`                       | `api.SingleFileDiffRequest`             | `api.FileDiffResponse`                      | `NOT_FOUND` |
| 18 | `Diff.UnstagedFileDiff`  | `POST /api/diff/file_unstaged`                     | `api.SingleFileDiffRequest`             | `api.FileDiffResponse`                      | `NOT_FOUND` |
| 19 | `Diff.DeepFileDiff`      | `POST /api/diff/file_deep`                         | `api.DeepFileDiffRequest`               | `api.DeepFileDiffResponse`                  | `NOT_FOUND` |
| 20 | `Diff.MergeBase`         | `POST /api/diff/merge_base`                        | `api.MergeBaseRequest`                  | `api.MergeBaseResponse`                     | `NOT_FOUND` |
| 21 | `Diff.StageHunk`         | `POST /api/diff/stage_hunk`                        | `api.HunkRequest`                       | —                                           | `CONFLICT` |
| 22 | `Diff.UnstageHunk`       | `POST /api/diff/unstage_hunk`                      | `api.HunkRequest`                       | —                                           | `CONFLICT` |
| 23 | `Diff.StageLines`        | `POST /api/diff/stage_lines`                       | `api.HunkLinesRequest`                  | —                                           | `CONFLICT` |
| 24 | `Diff.UnstageLines`      | `POST /api/diff/unstage_lines`                     | `api.HunkLinesRequest`                  | —                                           | `CONFLICT` |
| 25 | `Diff.DiffAllRepos`      | `POST /api/diff/all_repos`                         | `api.ProjectHandleRequest`              | `api.DiffAllReposResponse`                  | `UNAVAILABLE` |
| **Commit** ||||||
| 26 | `Commit.SuggestMessage`  | `POST /api/commit/suggest`                         | `api.RepoPathRequest`                   | `api.CommitSuggestResponse`                 | `NOT_FOUND` |
| 27 | `Commit.Files`           | `GET  /api/commit/files?repo_path=&hash=`          | —                                       | `api.CommitFilesResponse`                   | `NOT_FOUND` |
| 28 | `Commit.FileDiff`        | `POST /api/commit/file_diff`                       | `api.CommitFileDiffRequest`             | `api.FileDiffResponse`                      | `NOT_FOUND` |
| 29 | `Commit.FullDiff`        | `POST /api/commit/full_diff`                       | `api.CommitFullDiffRequest`             | `api.FileDiffResponse`                      | `NOT_FOUND` |
| 30 | `Commit.CherryPick`      | `POST /api/commit/cherry_pick`                     | `api.CommitHashRequest`                 | —                                           | `CONFLICT`, `NOT_FOUND` |
| 31 | `Commit.Reset`           | `POST /api/commit/reset`                           | `api.CommitResetRequest`                | —                                           | `NOT_FOUND` |
| 32 | `Commit.AmendMessage`    | `POST /api/commit/amend`                           | `api.CommitAmendRequest`                | —                                           | `NOT_FOUND` |
| 33 | `Commit.GenerateMessage` | `POST /api/commit/message`                         | `api.CommitMessageRequest` (legacy)     | `*api.CommitMessage`                        | `NOT_FOUND` |
| **Stash** ||||||
| 34 | `Stash.List`             | `GET  /api/stash/list?repo_path=`                  | —                                       | `api.StashListResponse`                     | `NOT_FOUND` |
| 35 | `Stash.Get`              | `GET  /api/stash/get?repo_path=&index=`            | —                                       | `*api.StashEntry`                           | `NOT_FOUND` |
| 36 | `Stash.Create`           | `POST /api/stash/create`                           | `api.StashCreateRequest`                | `api.StashCreateResponse`                   | `NOT_FOUND` |
| 37 | `Stash.Apply`            | `POST /api/stash/apply`                            | `api.StashApplyRequest`                 | —                                           | `CONFLICT`, `NOT_FOUND` |
| 38 | `Stash.Drop`             | `POST /api/stash/drop`                             | `api.StashDropRequest`                  | —                                           | `NOT_FOUND` |
| 39 | `Stash.Clear`            | `POST /api/stash/clear`                            | `api.RepoPathRequest`                   | —                                           | `NOT_FOUND` |
| 40 | `Stash.ListAllRepos`     | `POST /api/stash/list_all`                         | `api.ProjectHandleRequest`              | `api.StashListAllResponse`                  | `UNAVAILABLE` |
| 41 | `Stash.StashAllRepos`    | `POST /api/stash/all`                              | `api.StashAllRequest`                   | `api.StashBulkResponse`                     | `UNAVAILABLE` |
| 42 | `Stash.ApplyStashAllRepos`| `POST /api/stash/apply_all`                       | `api.StashApplyAllRequest`              | `api.StashBulkResponse`                     | `UNAVAILABLE` |
| **Rebase** ||||||
| 43 | `Rebase.Plan`            | `POST /api/rebase/plan`                            | `api.RebasePlanRequest`                 | `api.RebasePlanResponse`                    | `NOT_FOUND` |
| 44 | `Rebase.Status`          | `GET  /api/rebase/status?repo_path=`               | —                                       | `api.RebaseStatusResponse`                  | `NOT_FOUND` |
| 45 | `Rebase.GenerateTodo`    | `POST /api/rebase/todo`                            | `api.RebaseTodoRequest`                 | `api.RebaseTodoResponse`                    | `BAD_REQUEST` |
| 46 | `Rebase.Continue`        | `POST /api/rebase/continue`                        | `api.RepoPathRequest`                   | —                                           | `NO_REBASE_IN_PROGRESS`, `CONFLICT` |
| 47 | `Rebase.Skip`            | `POST /api/rebase/skip`                            | `api.RepoPathRequest`                   | —                                           | `NO_REBASE_IN_PROGRESS` |
| 48 | `Rebase.Abort`           | `POST /api/rebase/abort`                           | `api.RepoPathRequest`                   | —                                           | `NO_REBASE_IN_PROGRESS` |
| 49 | `Rebase.OntoMain`        | `POST /api/rebase/onto_main` **stream**            | `api.RepoPathRequest`                   | `api.StreamStartResponse`                   | `REBASE_IN_PROGRESS`, `CONFLICT` |
| 50 | `Rebase.Context`         | `POST /api/rebase/context`                         | `api.RebaseContextRequest`              | `api.RebaseContextResponse`                 | `NOT_FOUND` |
| **Worktree** ||||||
| 51 | `Worktree.List`          | `GET  /api/worktree/list?repo_path=`               | —                                       | `api.WorktreeListResponse`                  | `NOT_FOUND` |
| 52 | `Worktree.Create`        | `POST /api/worktree/create`                        | `api.WorktreeCreateRequest`             | —                                           | `CONFLICT`, `NOT_FOUND` |
| 53 | `Worktree.Remove`        | `POST /api/worktree/remove`                        | `api.WorktreeRemoveRequest`             | —                                           | `NOT_FOUND`, `CONFLICT` |
| 54 | `Worktree.Prune`         | `POST /api/worktree/prune`                         | `api.RepoPathRequest`                   | —                                           | `NOT_FOUND` |
| 55 | `Worktree.Lock`          | `POST /api/worktree/lock`                          | `api.WorktreePathRequest`               | —                                           | `NOT_FOUND` |
| 56 | `Worktree.Unlock`        | `POST /api/worktree/unlock`                        | `api.WorktreePathRequest`               | —                                           | `NOT_FOUND` |
| **Sync** ||||||
| 57 | `Sync.UpstreamStatus`    | `GET  /api/sync/upstream?repo_path=`               | —                                       | `*api.UpstreamStatus`                       | `NOT_FOUND` |
| 58 | `Sync.LastFetchTime`     | `GET  /api/sync/last_fetch?repo_path=`             | —                                       | `api.LastFetchResponse`                     | `NOT_FOUND` |
| 59 | `Sync.Fetch`             | `POST /api/sync/fetch` **stream**                  | `api.SyncFetchRequest`                  | `api.StreamStartResponse`                   | `NOT_FOUND` |
| 60 | `Sync.Pull`              | `POST /api/sync/pull` **stream**                   | `api.RepoPathRequest`                   | `api.StreamStartResponse`                   | `CONFLICT`, `NOT_FOUND` |
| 61 | `Sync.Push`              | `POST /api/sync/push` **stream**                   | `api.SyncPushRequest`                   | `api.StreamStartResponse`                   | `NOT_FOUND` |
| 62 | `Sync.PullRebase`        | `POST /api/sync/pull_rebase` **stream**            | `api.RepoPathRequest`                   | `api.StreamStartResponse`                   | `CONFLICT`, `NOT_FOUND` |
| 63 | `Sync.SetUpstreamAndPush`| `POST /api/sync/set_upstream` **stream**           | `api.SyncSetUpstreamRequest`            | `api.StreamStartResponse`                   | `NOT_FOUND` |
| 64 | `Sync.SyncAllRepos`      | `POST /api/sync/all` **stream**                    | `api.SyncAllRequest`                    | `api.StreamStartResponse`                   | `UNAVAILABLE` |
| **Pipeline** ||||||
| 65 | `Pipeline.Statuses`      | `POST /api/pipeline/statuses`                      | `api.PipelineStatusesRequest`           | `api.PipelineStatusesResponse`              | `NOT_FOUND` |
| 66 | `Pipeline.Retry`         | `POST /api/pipeline/retry`                         | `api.PipelineRetryRequest`              | —                                           | `NOT_FOUND` |
| 67 | `Pipeline.Subscribe`     | `POST /api/pipeline/subscribe` **stream**          | `api.RepoPathRequest`                   | `api.StreamStartResponse`                   | `NOT_FOUND` |
| **MR** ||||||
| 68 | `MR.GenerateTitle`       | `POST /api/mr/title`                               | `api.MRGenerateRequest`                 | `api.MRTextResponse`                        | `NOT_FOUND` |
| 69 | `MR.GenerateDescription` | `POST /api/mr/description`                         | `api.MRGenerateRequest`                 | `api.MRTextResponse`                        | `NOT_FOUND` |
| 70 | `MR.Create`              | `POST /api/mr/create`                              | `api.MRRequest` (legacy)                | `*api.MergeRequest`                         | `MR_ALREADY_EXISTS`, `NO_FORGE` |
| 71 | `MR.CreateCLI`           | `POST /api/mr/create_cli`                          | `api.MRCreateCLIRequest`                | `*api.MRResult`                             | `NOT_FOUND` |
| **Forge** ||||||
| 72 | `Forge.DetectAuth`       | `GET  /api/forge/auth`                             | —                                       | `*api.ForgeAuth`                            | `NO_FORGE` |
| 73 | `Forge.Detect`           | `GET  /api/forge/info`                             | —                                       | `*api.ForgeInfo` (alias of `service.ForgeInfo`) | `NO_FORGE` |
| 74 | `Forge.DetectProvider`   | `GET  /api/forge/provider?repo_path=`              | —                                       | `api.ForgeProviderResponse`                 | `NOT_FOUND` |
| **Project** ||||||
| 75 | `Project.List`           | `GET  /api/project/list`                           | —                                       | `api.ProjectListResponse`                   | — |
| 76 | `Project.Load`           | `POST /api/project/load`                           | `api.ProjectLoadRequest`                | `*api.ProjectInfo`                          | `UNAVAILABLE`, `NOT_FOUND` |
| 77 | `Project.Info`           | `GET  /api/project/info?handle=`                   | —                                       | `*api.ProjectInfo`                          | `NOT_FOUND` |
| 78 | `Project.Status`         | `GET  /api/project/status?handle=`                 | —                                       | `*api.ProjectStatus`                        | `NOT_FOUND` |
| 79 | `Project.Refresh`        | `POST /api/project/refresh`                        | `api.ProjectHandleRequest`              | `*api.ProjectStatus`                        | `NOT_FOUND` |
| 80 | `Project.BranchExists`   | `POST /api/project/branch/check`                   | `api.ProjectBranchRequest`              | `*api.BranchExistence`                      | `NOT_FOUND` |
| 81 | `Project.ContextSummary` | `GET  /api/project/context?handle=`                | —                                       | `api.ProjectContextResponse`                | `NOT_FOUND` |
| 82 | `Project.DefaultConfigPath`| `GET /api/project/config_path`                   | —                                       | `api.ProjectConfigPathResponse`             | `UNAVAILABLE` |
| 83 | `Project.Subscribe`      | `POST /api/project/subscribe` **stream**           | `api.ProjectHandleRequest`              | `api.StreamStartResponse`                   | `NOT_FOUND` |
| 84 | `Project.CreateWorkflow` | `POST /api/project/workflow/create`                | `api.WorkflowCreateRequest`             | `*api.FeatureWorkflow`                      | `NOT_FOUND` |
| 85 | `Project.LoadWorkflows`  | `GET  /api/project/workflow/list?handle=`          | —                                       | `api.WorkflowListResponse`                  | `NOT_FOUND` |
| 86 | `Project.SaveWorkflows`  | `POST /api/project/workflow/save`                  | `api.WorkflowSaveRequest`               | —                                           | `NOT_FOUND` |
| 87 | `Project.DiscoverWorkflowsAllRepos` | `POST /api/project/workflow/discover` **stream** | `api.WorkflowDiscoverRequest`     | `api.StreamStartResponse`                   | `NOT_FOUND` |
| 88 | `Project.SubscribeWorkflows` | `POST /api/project/workflow/subscribe` **stream** | `api.ProjectHandleRequest`         | `api.StreamStartResponse`                   | `NOT_FOUND` |
| **Agent** ||||||
| 89 | `Agent.Start`            | `POST /api/agent/start`                            | `api.AgentStartRequest`                 | `api.AgentStartResponse`                    | `AGENT_LIMIT`, `BAD_REQUEST` |
| 90 | `Agent.Resume`           | `POST /api/agent/resume`                           | `api.AgentResumeRequest`                | `api.AgentStartResponse`                    | `AGENT_LIMIT`, `NOT_FOUND` |
| 91 | `Agent.SendInput`        | `POST /api/agent/input`                            | `api.AgentInputRequest`                 | —                                           | `NOT_FOUND` |
| 92 | `Agent.Kill`             | `POST /api/agent/kill`                             | `api.AgentQueryRequest`                 | —                                           | `NOT_FOUND` |
| 93 | `Agent.Remove`           | `POST /api/agent/remove`                           | `api.AgentQueryRequest`                 | —                                           | — |
| 94 | `Agent.List`             | `GET  /api/agent/list`                             | —                                       | `api.AgentListResponse`                     | — |
| 95 | `Agent.Get`              | `GET  /api/agent/get?agent_id=`                    | —                                       | `*api.AgentSnapshot`                        | `NOT_FOUND` |
| 96 | `Agent.Output`           | `GET  /api/agent/output?agent_id=&offset=`         | —                                       | `api.AgentOutputResponse`                   | `NOT_FOUND` |
| 97 | `Agent.Stats`            | `GET  /api/agent/stats`                            | —                                       | `*api.EngineStats`                          | — |
| 98 | `Agent.MaxAgents`        | `GET  /api/agent/max`                              | —                                       | `api.AgentMaxResponse`                      | — |
| 99 | `Agent.Subscribe`        | `POST /api/agent/subscribe` **stream**             | `api.AgentSubscribeRequest`             | `api.StreamStartResponse`                   | `NOT_FOUND` |
|100 | `Agent.SubscribeAll`     | `POST /api/agent/subscribe_all` **stream**         | —                                       | `api.StreamStartResponse`                   | — |
| **Queue** ||||||
<!-- Every `/api/queue/*` route answers `UNAVAILABLE` when the daemon has
     no queue manager (state directory unreadable): local.New accepts a nil
     manager and every method degrades rather than panicking. `Queue.Answer`
     is wired but inert — the engine never reports an agent asking a
     question, so no task reaches `waiting_human` and it always returns
     `CONFLICT`. -->
|101 | `Queue.Add`              | `POST /api/queue/add`                              | `api.QueueAddRequest`                   | `api.QueueAddResponse`                      | `BAD_REQUEST`, `UNAVAILABLE` |
|102 | `Queue.List`             | `GET  /api/queue/list?queue=&state=`               | —                                       | `api.QueueListResponse`                     | `NOT_FOUND`, `BAD_REQUEST`, `UNAVAILABLE` |
|103 | `Queue.Get`              | `GET  /api/queue/get?task_id=`                     | —                                       | `*api.Task`                                 | `NOT_FOUND`, `BAD_REQUEST`, `UNAVAILABLE` |
|104 | `Queue.Queues`           | `GET  /api/queue/queues`                           | —                                       | `api.QueueQueuesResponse`                   | `UNAVAILABLE` |
|105 | `Queue.QueueInfo`        | (no endpoint — client selects from `queues`)        | —                                       | `*api.QueueInfo`                            | `NOT_FOUND` |
|106 | `Queue.Graph`            | `GET  /api/queue/graph?queue=`                     | —                                       | `api.QueueGraphResponse`                    | `NOT_FOUND`, `BAD_REQUEST`, `UNAVAILABLE` |
|107 | `Queue.Cancel`           | `POST /api/queue/cancel`                           | `api.QueueTaskRequest`                  | —                                           | `NOT_FOUND`, `BAD_REQUEST`, `UNAVAILABLE` |
|108 | `Queue.CancelQueue`      | `POST /api/queue/cancel_queue`                     | `api.QueueIDRequest`                    | —                                           | `NOT_FOUND`, `BAD_REQUEST`, `UNAVAILABLE` |
|109 | `Queue.Retry`            | `POST /api/queue/retry`                            | `api.QueueTaskRequest`                  | —                                           | `NOT_FOUND`, `CONFLICT`, `UNAVAILABLE` |
|110 | `Queue.Answer` *(inert)* | `POST /api/queue/answer`                           | `api.QueueAnswerRequest`                | —                                           | `NOT_FOUND`, `CONFLICT`, `BAD_REQUEST`, `UNAVAILABLE` |
|111 | `Queue.Pause`            | `POST /api/queue/pause`                            | `api.QueueIDRequest`                    | —                                           | `NOT_FOUND`, `BAD_REQUEST`, `UNAVAILABLE` |
|112 | `Queue.Resume`           | `POST /api/queue/resume`                           | `api.QueueIDRequest`                    | —                                           | `NOT_FOUND`, `BAD_REQUEST`, `UNAVAILABLE` |
|113 | `Queue.RemoveQueue`      | `POST /api/queue/remove`                           | `api.QueueIDRequest`                    | —                                           | `NOT_FOUND`, `BAD_REQUEST`, `UNAVAILABLE` |
| **Jira** ||||||
|114 | `Jira.SearchIssues`      | `POST /api/jira/search`                            | `api.JiraSearchRequest`                 | `*api.SearchResult`                         | `UNAVAILABLE` |
|115 | `Jira.GetIssue`          | `GET  /api/jira/issue?key=`                        | —                                       | `*api.Issue`                                | `NOT_FOUND`, `UNAVAILABLE` |
|116 | `Jira.GetMyIssues`       | `GET  /api/jira/my?project=`                       | —                                       | `*api.SearchResult`                         | `UNAVAILABLE` |
|117 | `Jira.UpdateFields`      | `POST /api/jira/update`                            | `api.JiraUpdateRequest`                 | —                                           | `NOT_FOUND` |
|118 | `Jira.AddComment`        | `POST /api/jira/comment`                           | `api.JiraCommentRequest`                | —                                           | `NOT_FOUND` |
|119 | `Jira.CreateIssue`       | `POST /api/jira/create`                            | `api.JiraCreateRequest`                 | `*api.Issue`                                | `UNAVAILABLE` |
|120 | `Jira.LinkIssues`        | `POST /api/jira/link`                              | `api.JiraLinkRequest`                   | —                                           | `NOT_FOUND` |
|121 | `Jira.ParseActions`      | `GET  /api/jira/actions?path=`                     | —                                       | `api.JiraActionsResponse`                   | `NOT_FOUND` |
|122 | `Jira.RefineTicket`      | `POST /api/jira/ai/refine`                         | `api.JiraRefineTicketRequest`           | `api.AgentStartResponse`                    | `AGENT_LIMIT`, `UNAVAILABLE` |
|123 | `Jira.CreateStories`     | `POST /api/jira/ai/stories`                        | `api.JiraCreateStoriesRequest`          | `api.AgentStartResponse`                    | `AGENT_LIMIT`, `UNAVAILABLE` |
|124 | `Jira.RefineProposalWithContext` | `POST /api/jira/ai/refine_proposal`        | `api.JiraRefineProposalRequest`         | `api.AgentStartResponse`                    | `AGENT_LIMIT`, `UNAVAILABLE` |
|125 | `Jira.ReviewTickets`     | `POST /api/jira/ai/review`                         | `api.JiraReviewTicketsRequest`          | `api.AgentStartResponse`                    | `AGENT_LIMIT`, `UNAVAILABLE` |
| **Flow** ||||||
|126 | `Flow.Start`             | `POST /api/flow/start`                             | `api.FlowStartRequest`                  | `api.FlowStartResponse`                     | `BAD_REQUEST`, `UNAVAILABLE` |
|127 | `Flow.Continue`          | `POST /api/flow/continue`                          | `api.FlowContinueRequest`               | `api.FlowContinueResponse`                  | `NOT_FOUND`, `CONFLICT`, `BAD_REQUEST`, `UNAVAILABLE` |
|128 | `Flow.List`              | `GET  /api/flow/list?state=`                       | —                                       | `api.FlowListResponse`                      | `BAD_REQUEST`, `UNAVAILABLE` |
|129 | `Flow.Get`               | `GET  /api/flow/get?flow_id=`                      | —                                       | `*api.Flow`                                 | `NOT_FOUND`, `BAD_REQUEST`, `UNAVAILABLE` |
|130 | `Flow.Tree`              | `GET  /api/flow/tree?flow_id=`                     | —                                       | `api.FlowTreeResponse`                      | `NOT_FOUND`, `BAD_REQUEST`, `UNAVAILABLE` |
|131 | `Flow.Cancel`            | `POST /api/flow/cancel`                            | `api.FlowIDRequest`                     | —                                           | `NOT_FOUND`, `BAD_REQUEST`, `UNAVAILABLE` |
|132 | `Flow.Remove`            | `POST /api/flow/remove`                            | `api.FlowIDRequest`                     | —                                           | `NOT_FOUND`, `CONFLICT`, `BAD_REQUEST`, `UNAVAILABLE` |

**Total: 132 service methods over 131 endpoints — one each, except `Queue.QueueInfo`: the per-queue tallies are computed in memory and shipped for every queue at once, so the client selects from `/api/queue/queues` instead of the daemon serving a route that would repeat it.**

---

## 5. Conventions

- `repo_path` is always a string field in the request body or as a query param. Server-side resolution against an implicit "current repo" is removed — every call carries its repo path.
- `handle` (ProjectHandle, opaque string) appears wherever a method operates on a loaded project.
- Streaming endpoints return `202 Accepted` on success; the `stream_id` is mandatory.
- Empty/204-ish endpoints still return `200 OK` + `api.APIResponse{success:true}` (no `data` field). Consumers ignore `data` when the response Go type column is `—`.
- Queue paths carry the task or queue identifier in the body (`task_id` / `queue_id`) for mutations and as a query param (`task_id` / `queue`) for reads. The `state` list filter on `/api/queue/list` accepts both spellings: repeated (`?state=ready&state=running`) and comma-separated (`?state=ready,running`).
- The queue's domain types already carry snake_case JSON tags, so `internal/api` aliases them (`api.Task = service.Task = queue.Task`) rather than re-projecting. A tag change therefore cannot land on one side only.
- Flow paths carry the flow identifier in the body (`flow_id`, via `api.FlowIDRequest` — or `api.FlowContinueRequest`, which is its own type because a continue carries `rounds` alongside the id) for mutations and as a query param (`flow_id`) for reads. The `state` list filter on `/api/flow/list` accepts both spellings, exactly as the queue's does: repeated (`?state=running&state=accepted`) and comma-separated (`?state=running,accepted`); an unknown state is `BAD_REQUEST`, not an empty result.
- The flow domain types carry snake_case tags too, so `internal/api` aliases them all the way down (`api.Flow = service.Flow = flow.Flow`, and `api.FlowStartRequest = service.FlowStartRequest = flow.StartRequest`) rather than re-projecting. Only the four response envelopes (`FlowStartResponse`, `FlowContinueResponse`, `FlowListResponse`, `FlowTreeResponse`) and the two request types with no domain counterpart (`FlowIDRequest`, `FlowContinueRequest`) are declared in `internal/api`.
- Every `/api/flow/*` route answers `UNAVAILABLE` when the daemon has no flow manager (`Server.requireFlow`), mirroring the queue's nil-manager degradation. `Flow.Start` cleans `work_dir` through `Server.validateRepoPath` before the service sees it, exactly as `handleQueueAdd` does; an empty `work_dir` is passed through unchanged so the flow manager reports it missing rather than the server substituting the daemon's cwd.
- `Flow.Start` returns `BAD_REQUEST` for an empty goal, a `work_dir` that is not an existing directory, a `max_rounds` outside 1..20, and for `opts.use_worktree`/`review_opts.use_worktree` — which a flow refuses rather than silently clears, because every round must see the same tree. `Flow.Remove` returns `CONFLICT` while the flow is non-terminal.
- `Flow.Continue` takes only `flow_id` and `rounds` — an increment on the flow's `max_rounds`, `0` meaning the daemon's default of 3 more — because nothing else about a flow is re-specifiable. `CONFLICT` covers a flow that was `accepted` and one that has not finished (`pending`/`running`); `BAD_REQUEST` covers a raised cap that would pass the 1..20 ceiling (a flow already at 20 is refused by name, since no round count would fit) and a `work_dir` that is no longer an existing directory, which the manager re-stats because a continue can come long after the start. The handler range-checks nothing itself, so the manager's message — which names the largest increment that would have fit — is what reaches the caller. The reply is the flow as recorded: `running`, cap raised, every round it already had still on it, since opening the next round is the reconciler's job.
- `Queue.Add` is atomic: the whole batch is accepted or refused. `BAD_REQUEST` (`service.ErrInvalidRequest`) covers an unknown dependency, a cycle, a missing prompt/work dir, and a `RemoveQueue` refused because a task is still active.
- Legacy paths kept exactly (no breaking renames during this phase): `/api/status`, `/api/repo/open`, `/api/repo/info`, `/api/repo`, `/api/branch/compare`, `/api/branch/diff`, `/api/commit/message`, `/api/mr/create`, `/api/forge/auth`, `/api/project/list`, `/api/project/load`, `/api/project/status`, `/api/project/refresh`, `/api/project/branch/check`, `/api/project/branch/compare`, `/api/project/context`, `/api/agent/start`, `/api/agent/status`, `/api/agent/output`, `/api/agent/kill`, `/api/agent/input`, `/api/agent/list`, `/api/agent/stats`, `/ws`, `/health`.
- The legacy `/api/branch/diff` is kept as an alias of `/api/diff/branch`. `/api/agent/status` is kept as an alias of `/api/agent/get`. `/api/project/branch/compare` is kept (one-off cross-repo helper — no service method, no client coverage, intentionally untouched).
