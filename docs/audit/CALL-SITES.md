# View → Backend Call-Site Audit

Catalog of every call from `internal/app/*.go` and `internal/app/views/*.go`
into `internal/git`, `internal/engine`, `internal/project`, `internal/jira`.
Input for the service-interface architect (Phase A3).

Scope: production code only — `_test.go` files excluded.

---

## 1. Summary table

| Target package | Capability | Call sites |
|----------------|------------|-----------:|
| git            | repo (open/find/info)              | 11 |
| git            | branches (list/create/checkout/delete) | 11 |
| git            | diff (branch/workdir/file/deep/hunks)  | 24 |
| git            | commit (history/files/diff/cherry/amend/reset/suggest) | 11 |
| git            | stash (list/get/create/apply/drop/clear) | 12 |
| git            | rebase (plan/status/continue/skip/abort/onto-main) | 18 |
| git            | worktree (list/create/remove/lock/prune/checkout) | 17 |
| git            | sync/upstream (status/fetch/pull/push/rebase) | 27 |
| git            | pipeline (statuses/retry/format/enums)   | 28 |
| git            | mr/pr (auth/title/desc/create/detect/cli) | 11 |
| git            | forge (auth detect, provider detect)     | 4 |
| git            | clipboard (CopyToClipboard)              | 2 |
| engine         | agent (start/resume/send/kill/remove/list/get/stats) | 18 |
| engine         | engine lifecycle (New/Shutdown/Prune/Sound/OnAgentUpdate/MaxAgents) | 8 |
| engine         | agent state enums + output entries      | 50+ |
| project        | loader (NewLoader, ConfigPath, NewProject, RepoDef, ProjectDef) | 9 |
| project        | repo/status (RepoStatus, ProjectStatus, BranchExistence, Repo) | 14 |
| project        | workflows (Discover/Load/Save/NewFeatureWorkflow + state enums) | 17 |
| project        | bulk ops (ListAllStashes, StashAllRepos, ApplyStashAllRepos)    | 4  |
| jira          | client (NewClient, Issue, SearchResult)  | 12 |
| jira          | AI workflows (RefineTicket, CreateStories, RefineProposalWithContext, ReviewTickets) | 8 |
| jira          | actions (JiraAction, ParseJiraActions)   | 13 |

Total enumerated call sites (excluding pure type references and switch-case enum arms): **~280**.
Including every line that mentions a target identifier (types, enum arms): **~480**.

Files touched (production):
- `internal/app/app.go`, `internal/app/async.go`, `internal/app/ws.go`, `internal/app/layout.go`
- `internal/app/views/`: `agent.go`, `approval.go`, `branch_comparison.go`, `branch_diff.go`,
  `branches.go`, `commit.go`, `diff.go`, `jira.go`, `jira_picker.go`, `log.go`, `overview.go`,
  `pipeline.go`, `pr.go`, `project.go`, `project_diff.go`, `project_stash.go`, `project_sync.go`,
  `rebase.go`, `stash.go`, `sync.go`, `workflow_diff.go`, `workflows.go`, `worktree.go`

---

## 2. Per-capability breakdown

### 2.1 repo (git)

**Functions/types used**
- `git.OpenRepo(path) (*RepoInfo, error)`
- `git.FindRepo(path) (string, error)`
- `git.RepoInfo` (type — held in view state, also crosses WS boundary)

**Call sites**
- `internal/app/app.go:175 — FindRepo to locate repo root from CWD`
- `internal/app/app.go:184 — OpenRepo on m.repoPath at startup`
- `internal/app/app.go:404 — OpenRepo on defaultRepoPath fallback`
- `internal/app/app.go:503 — OpenRepo when switching project repo`
- `internal/app/async.go:350 — OpenRepo (async loader)`
- `internal/app/async.go:374 — OpenRepo (branches loader)`
- `internal/app/async.go:386 — OpenRepo (additional async path)`
- `internal/app/views/overview.go:62 — OpenRepo for overview header`
- `internal/app/views/pr.go:83 — OpenRepo before PR creation`
- `internal/app/views/pipeline.go:63 — OpenRepo before pipeline list`
- `internal/app/views/diff.go:108 — OpenRepo before workdir diff`
- `internal/app/views/branches.go:84 — OpenRepo before branch list`
- `internal/app/views/branch_comparison.go:54 — OpenRepo for comparison view`
- `internal/app/views/log.go:126 — OpenRepo before commit log`
- `internal/app/views/rebase.go:80 — OpenRepo before rebase plan`
- `internal/app/views/stash.go:70 — OpenRepo before stash list`
- `internal/app/views/worktree.go:92 — OpenRepo before worktree list`

Mutating? **no**. Streaming? **no** (one-shot).

---

### 2.2 branches (git)

**Functions/types used**
- `git.BranchInfo` (type — held in view state, returned by list)
- `git.CreateBranch(repoPath, name, from) error`
- `git.Checkout(repoPath, branch) error`
- `git.CheckoutDetached(path) error`
- `git.CheckoutDetachedAt(path, sha) error`
- `git.GetHEAD(path) (string, error)`
- `git.ResolveRef(path, ref) string`

**Call sites**
- `internal/app/views/project.go:378 — CreateBranch from project view`
- `internal/app/views/project.go:483 — CheckoutDetached on per-repo worktree`
- `internal/app/views/project.go:521 — CheckoutDetachedAt at HEAD of worktree`
- `internal/app/views/project.go:549 — Checkout branch in single repo`
- `internal/app/views/project.go:574 — Checkout by typed name`
- `internal/app/views/project.go:635 — Checkout on tree-node selection`
- `internal/app/views/overview.go:149 — CheckoutDetached on current repo`
- `internal/app/views/worktree.go:333 — CheckoutDetached on worktree path`
- `internal/app/views/workflows.go:781 — GetHEAD before detach`
- `internal/app/views/workflows.go:786 — CheckoutDetachedAt at HEAD`
- `internal/app/views/branch_diff.go:94 — ResolveRef for default branch`
- `internal/app/views/workflow_diff.go:122 — ResolveRef for default branch (workflow)`

Mutating? **yes** (Create/Checkout). Streaming? **no**.

---

### 2.3 diff (git)

**Functions/types used**
- `git.BranchDiff`, `git.WorkdirDiff`, `git.WorkdirStatus`, `git.FileChange`, `git.DiffHunk`, `git.FilteredDiffHunk` (types — stored in view state)
- `git.GetBranchDiff(repoPath, a, b) (*BranchDiff, error)`
- `git.GetWorkdirStatus(repoPath) (*WorkdirDiff, error)`
- `git.GetFileDiff(repoPath, a, b, path) (string, error)`
- `git.GetStagedFileDiff(repoPath, path) (string, error)`
- `git.GetUnstagedFileDiff(repoPath, path) (string, error)`
- `git.GetDeepFileDiff(repoPath, mergeBase, branch, defaultBranch, path) ([]FilteredDiffHunk, string, error)`
- `git.GetMergeBase(repoPath, a, b) (string, error)`
- `git.ParseHunks(rawDiff) []DiffHunk`
- `git.StageHunk`, `git.UnstageHunk`, `git.StageLines`, `git.UnstageLines`

**Call sites**
- `internal/app/views/diff.go:131 — GetBranchDiff (compare two branches)`
- `internal/app/views/diff.go:150 — GetWorkdirStatus`
- `internal/app/views/diff.go:158-160 — FileChange construction from WorkdirDiff`
- `internal/app/views/diff.go:292 — GetFileDiff (selected file in branch diff)`
- `internal/app/views/diff.go:305 — GetStagedFileDiff`
- `internal/app/views/diff.go:306 — GetUnstagedFileDiff`
- `internal/app/views/branch_diff.go:98 — GetMergeBase`
- `internal/app/views/branch_diff.go:102 — GetBranchDiff (vs merge base)`
- `internal/app/views/branch_diff.go:109 — GetDeepFileDiff (per-file)`
- `internal/app/views/branch_diff.go:303 — GetDeepFileDiff (on selection)`
- `internal/app/views/workflow_diff.go:127 — GetMergeBase`
- `internal/app/views/workflow_diff.go:131 — GetBranchDiff for workflow repo`
- `internal/app/views/workflow_diff.go:140 — GetDeepFileDiff (per-file)`
- `internal/app/views/workflow_diff.go:339 — GetDeepFileDiff (on selection)`
- `internal/app/views/workflow_diff.go:353/371 — uses FilteredDiffHunk as parameter type`
- `internal/app/views/project_diff.go:103 — GetWorkdirStatus per repo`
- `internal/app/views/project_diff.go:261 — GetStagedFileDiff`
- `internal/app/views/project_diff.go:262 — GetUnstagedFileDiff`
- `internal/app/views/commit.go:567 — GetStagedFileDiff`
- `internal/app/views/commit.go:571 — GetUnstagedFileDiff`
- `internal/app/views/commit.go:585 — ParseHunks (pure parser, no IO)`
- `internal/app/views/commit.go:635 — UnstageHunk (mutating)`
- `internal/app/views/commit.go:641 — StageHunk (mutating)`
- `internal/app/views/commit.go:751 — UnstageLines (mutating)`
- `internal/app/views/commit.go:756 — StageLines (mutating)`
- `internal/app/views/commit.go:807-821 — second-pass ParseHunks + reload diff`
- `internal/app/views/async.go (none — diff calls in views only)`

Mutating? **mixed** (most read-only; Stage/Unstage Hunk/Lines mutate the index).
Streaming? **no** but large payloads — server should support paginated/per-file fetch.

---

### 2.4 commit (git)

**Functions/types used**
- `git.GetCommitFiles`, `git.GetCommitFileDiff`, `git.GetCommitFullDiff`
- `git.SuggestCommitMessage(repoPath) (string, error)`
- `git.CherryPick(repoPath, hash) error`
- `git.ResetToCommit(repoPath, hash, mode) error`
- `git.AmendCommitMessage(repoPath, msg) error`
- `git.CopyToClipboard(text) error` (utility — debatably its own capability)

**Call sites**
- `internal/app/views/commit.go:400 — SuggestCommitMessage (AI-generated)`
- `internal/app/views/log.go:249 — GetCommitFiles`
- `internal/app/views/log.go:270 — GetCommitFileDiff`
- `internal/app/views/log.go:299 — GetCommitFullDiff`
- `internal/app/views/log.go:559 — CopyToClipboard (commit hash)`
- `internal/app/views/log.go:575 — CherryPick`
- `internal/app/views/log.go:762 — ResetToCommit (soft/mixed/hard)`
- `internal/app/views/log.go:785 — AmendCommitMessage`
- `internal/app/views/workflows.go:809 — CopyToClipboard (workflow text)`

Mutating? **yes** for cherry-pick / reset / amend. Streaming? **no**.
Note: clipboard is OS-local — on a remote daemon scenario it must execute on the *client*.
Recommend: keep clipboard out of the service contract; views call it directly via a small local helper.

---

### 2.5 stash (git + project)

**Functions/types used**
- `git.StashEntry` (type)
- `git.GetStashList`, `git.GetStash`, `git.CreateStash`
- `git.ApplyStash(repoPath, idx, pop bool)`, `git.DropStash`, `git.ClearStash`
- `project.RepoStashList`, `project.RepoStashResult` (types)
- `project.ListAllStashes(proj) []RepoStashList`
- `project.StashAllRepos(proj, name, includeUntracked)`
- `project.ApplyStashAllRepos(proj, name, pop)`

**Call sites**
- `internal/app/views/stash.go:78 — GetStashList`
- `internal/app/views/stash.go:313 — GetStash (preview a single entry)`
- `internal/app/views/stash.go:323 — ApplyStash (apply)`
- `internal/app/views/stash.go:334 — ApplyStash (pop)`
- `internal/app/views/stash.go:345 — DropStash`
- `internal/app/views/stash.go:356 — ClearStash`
- `internal/app/views/stash.go:367 — CreateStash`
- `internal/app/views/overview.go:78 — GetStashList`
- `internal/app/views/project_stash.go:73 — project.ListAllStashes`
- `internal/app/views/project_stash.go:195 — project.StashAllRepos`
- `internal/app/views/project_stash.go:221 — project.ApplyStashAllRepos (apply)`
- `internal/app/views/project_stash.go:238 — project.ApplyStashAllRepos (pop)`

Mutating? **yes** for create/apply/drop/clear. Streaming? **no**.

---

### 2.6 rebase (git)

**Functions/types used**
- `git.RebaseCommit`, `git.RebaseOperation` (type)
- Enum constants: `git.RebasePick/Reword/Edit/Squash/Fixup/Drop` (used in 8+ switch arms)
- `git.GetRebasePlan(repoPath, base, current) ([]RebaseCommit, error)`
- `git.GetRebaseStatus(repoPath) (inProgress bool, _, err)`
- `git.GenerateTodoList(commits) string`
- `git.ContinueRebase`, `git.SkipRebase`, `git.AbortRebase`
- `git.RebaseOntoMain(path) (mainBranch, conflictFiles, _, err)`
- `git.GetRebaseContext(path, mainBranch, conflictFiles)`

**Call sites**
- `internal/app/views/rebase.go:91 — GetRebasePlan`
- `internal/app/views/rebase.go:321-336 — RebaseOperation switch (cycle through ops)`
- `internal/app/views/rebase.go:365 — GetRebaseStatus`
- `internal/app/views/rebase.go:386 — GenerateTodoList`
- `internal/app/views/rebase.go:480 — ContinueRebase`
- `internal/app/views/rebase.go:509 — SkipRebase`
- `internal/app/views/rebase.go:539 — AbortRebase`
- `internal/app/views/rebase.go:557-629 — enum arms (rendering icons / item state)`
- `internal/app/views/worktree.go:393 — RebaseOntoMain (rebase a worktree onto main)`
- `internal/app/views/worktree.go:402 — GetRebaseContext (for conflict resolution)`

Mutating? **yes** (continue/skip/abort/rebase). Streaming? **no** (but RebaseOntoMain is long-running; consider async event).

---

### 2.7 worktree (git)

**Functions/types used**
- `git.Worktree` (type — held in view state)
- `git.GetWorktrees(repoPath) ([]Worktree, error)`
- `git.CreateWorktree(repoPath, path, branch, createBranch, startPoint) error`
- `git.RemoveWorktree(repoPath, path, force) error`
- `git.PruneWorktrees(repoPath) error`
- `git.LockWorktree`, `git.UnlockWorktree`

**Call sites**
- `internal/app/views/worktree.go:100 — GetWorktrees (primary list)`
- `internal/app/views/worktree.go:536 — CreateWorktree`
- `internal/app/views/worktree.go:547 — RemoveWorktree`
- `internal/app/views/worktree.go:558 — PruneWorktrees`
- `internal/app/views/worktree.go:569 — LockWorktree`
- `internal/app/views/worktree.go:580 — UnlockWorktree`
- `internal/app/views/overview.go:86 — GetWorktrees`
- `internal/app/views/project.go:477 — GetWorktrees`
- `internal/app/views/project.go:507 — GetWorktrees`
- `internal/app/views/jira.go:708 — GetWorktrees (existingWTs prefetch)`
- `internal/app/views/jira.go:858 — CreateWorktree (start workflow for ticket)`

Mutating? **yes** (create/remove/prune/lock/unlock). Streaming? **no**.

---

### 2.8 sync / upstream (git)

**Functions/types used**
- `git.UpstreamStatus` (type)
- `git.GetUpstreamStatus(repoPath) (*UpstreamStatus, error)`
- `git.GetLastFetchTime(repoPath) (time.Time, error)`
- `git.Fetch(repoPath, remote)`, `git.Pull(repoPath)`, `git.Push(repoPath, force)`, `git.PullRebase(repoPath)`
- `git.SetUpstreamAndPush(repoPath, remote)`

**Call sites**
- `internal/app/views/sync.go:117 — GetUpstreamStatus`
- `internal/app/views/sync.go:125 — GetLastFetchTime`
- `internal/app/views/sync.go:167 — GetLastFetchTime`
- `internal/app/views/sync.go:253 — Fetch`
- `internal/app/views/sync.go:261 — Pull`
- `internal/app/views/sync.go:269 — Push (no force)`
- `internal/app/views/sync.go:277 — Push (force)`
- `internal/app/views/sync.go:285 — PullRebase`
- `internal/app/views/sync.go:298 — SetUpstreamAndPush`
- `internal/app/views/sync.go:314 — Fetch (smart-sync flow)`
- `internal/app/views/sync.go:327 — PullRebase (smart-sync flow)`
- `internal/app/views/sync.go:340 — Push (smart-sync flow)`
- `internal/app/views/sync.go:352 — GetUpstreamStatus`
- `internal/app/views/project_sync.go:113 — GetUpstreamStatus (per repo, in goroutine)`
- `internal/app/views/project_sync.go:116 — GetLastFetchTime`
- `internal/app/views/project_sync.go:270 — Fetch (per repo)`
- `internal/app/views/project_sync.go:272 — Pull`
- `internal/app/views/project_sync.go:274 — Push`
- `internal/app/views/project_sync.go:276 — Push (force)`
- `internal/app/views/project_sync.go:278 — PullRebase`
- `internal/app/views/project_sync.go:365 — GetUpstreamStatus (refresh)`
- `internal/app/views/project_sync.go:368 — GetLastFetchTime (refresh)`

Mutating? **yes** for fetch/pull/push/rebase/set-upstream. Streaming? Operations are long-running and emit text output. **Recommend WS streaming** of `output` lines so the TUI can render progress live (current code captures output into a single string).

---

### 2.9 pipeline (git)

**Functions/types used**
- `git.PipelineInfo`, `git.PipelineStatus` (types — held in view state)
- Enum constants: `PipelineSuccess/Failed/Running/Pending/Canceled/Skipped` (used in many switch arms)
- `git.GetBranchPipelineStatuses(repoPath, branches) (map[string]*PipelineInfo, error)`
- `git.RetryPipeline(repoPath, branch) error`
- `git.FormatPipelineStatus(status) string`

**Call sites**
- `internal/app/views/pipeline.go:73 — GetBranchPipelineStatuses`
- `internal/app/views/pipeline.go:169 — PipelineFailed (enum check)`
- `internal/app/views/pipeline.go:173 — RetryPipeline`
- `internal/app/views/pipeline.go:220 — FormatPipelineStatus`
- `internal/app/views/pipeline.go:304 — FormatPipelineStatus`
- `internal/app/views/pipeline.go:326 — FormatPipelineStatus`
- `internal/app/views/pipeline.go:350 — PipelineFailed (hint)`
- `internal/app/views/pipeline.go:384 — FormatPipelineStatus (jobs)`
- `internal/app/views/pipeline.go:399 — FormatPipelineStatus (job line)`
- `internal/app/views/pipeline.go:417-427 — getStatusIcon enum switch (6 arms)`
- `internal/app/views/pipeline.go:437-447 — getJobStatusIcon enum switch (6 arms)`
- `internal/app/views/pipeline.go:458-464 — getStatusStyle enum switch (5 arms)`
- `internal/app/views/branches.go:117 — GetBranchPipelineStatuses`
- `internal/app/views/branches.go:414-429 — enum arms in branch row rendering (6)`

Mutating? **yes** for RetryPipeline only. Streaming? Pipeline statuses change over time; **server already emits `pipeline_update` WS events** (see ws.go:445). Service interface should mirror that.

---

### 2.10 mr / pr (git)

**Functions/types used**
- `git.MergeRequest` (type — created MR stored in view state)
- `git.ErrMRAlreadyExists` (sentinel error)
- `git.GenerateMRTitle(repoPath, source, target) (string, error)`
- `git.GenerateMRDescription(repoPath, source, target) (string, error)`
- `git.CreateMR(repoPath, source, target, title, desc, opts) (*MergeRequest, error)`
- `git.CreateMergeRequestCLI(path, provider, baseBranch) (Result, error)`
- `git.DetectRemoteProvider(path) Provider`

**Call sites**
- `internal/app/views/pr.go:160 — GenerateMRTitle`
- `internal/app/views/pr.go:179 — GenerateMRDescription`
- `internal/app/views/pr.go:537 — CreateMR`
- `internal/app/views/pr.go:540 — errors.Is(err, git.ErrMRAlreadyExists)`
- `internal/app/views/project.go:423 — DetectRemoteProvider`
- `internal/app/views/project.go:433 — CreateMergeRequestCLI (provider-CLI fallback)`

Mutating? **yes** (create). Streaming? **no** but slow.

---

### 2.11 forge auth (git)

**Functions/types used**
- `git.ForgeAuth` (type — held in view state)
- `git.DetectForgeAuth() (*ForgeAuth, error)`

**Call sites**
- `internal/app/views/pr.go:93 — DetectForgeAuth (before MR creation)`
- `internal/app/views/branches.go:110 — DetectForgeAuth (for pipeline visibility)`
- `internal/app/views/pipeline.go:202 — DetectForgeAuth`

Mutating? **no**. Streaming? **no**.
Note: on a daemon model, "forge auth" probably means env/config the daemon owns.
Whether the *client* even needs the auth object as a type is questionable —
recommend: service returns only a `HasForge bool` / `ForgeProvider string` rather
than the full credential struct.

---

### 2.12 branch comparison (git)

**Functions/types used**
- `git.BranchComparison`, `git.TreeComparison` (types)
- `git.CompareBranches(repoPath, a, b) (*BranchComparison, error)`
- `git.CompareBranchesByTree(repoPath, a, b) (*TreeComparison, error)`

**Call sites**
- `internal/app/async.go:362 — CompareBranches (async loader)`
- `internal/app/views/branch_comparison.go:97 — CompareBranches`
- `internal/app/views/branch_comparison.go:105 — CompareBranchesByTree`
- `internal/app/views/branch_comparison.go:113 — GetBranchDiff (see diff capability)`

Mutating? **no**. Streaming? **no**.
(Folds naturally into the `diff` service.)

---

### 2.13 project loader & status (project)

**Functions/types used**
- `project.Project` (opaque type — held by app + many views)
- `project.ProjectDef`, `project.RepoDef` (constructor structs)
- `project.NewProject(def) *Project`
- `project.NewLoaderFromFile(path) (*Loader, error)`
- `project.GetDefaultConfigPath() string`
- `project.Repo`, `project.RepoStatus`, `project.ProjectStatus`, `project.BranchExistence`,
  `project.WorkflowRepo` (types)

**Call sites**
- `internal/app/app.go:335 — GetDefaultConfigPath`
- `internal/app/app.go:339 — NewLoaderFromFile`
- `internal/app/app.go:463 — discoverProject helper signature returns *Project`
- `internal/app/app.go:469-489 — assemble RepoDef[] + NewProject`
- `internal/app/views/project.go:91 — NewProjectView(proj *project.Project)` (constructor wiring)
- `internal/app/views/project.go:120 — SetProject`
- `internal/app/views/project.go:126-151 — discoverProject helper (duplicate of app.go)`
- `internal/app/views/jira.go:148 — SetProject`
- `internal/app/views/workflows.go:101 — NewWorkflowsView(proj *Project)`
- `internal/app/views/workflows.go:140 — SetProject`
- `internal/app/views/project_stash.go:54 — NewProjectStashView(proj *Project)`
- `internal/app/views/project_sync.go:78 — NewProjectSyncView(proj *Project)`
- `internal/app/views/project_diff.go:60 / 68 — NewProjectDiffView / SetProject`

Type references: `*project.Repo` used as goroutine parameter at `project_sync.go:107, 258, 359`
and `project_diff.go:101`.

Mutating? **no** (loader is read-only). Streaming? **no** initially, but project status
refresh happens via WS `repo_update` events — see cross-cutting.

---

### 2.14 project workflows (project)

**Functions/types used**
- `project.FeatureWorkflow` (type — stored extensively in WorkflowsView)
- `project.WorkflowRepo` (type — per-repo workflow state)
- Enum constants: `project.WorkflowActive/Done/Initializing/PushingAll/CreatingMRs/CleaningUp`
- `project.NewFeatureWorkflow(proj, branch, baseDir) *FeatureWorkflow`
- `project.DiscoverWorkflows(proj, skip) ([]*FeatureWorkflow, error)`
- `project.LoadWorkflows(key, proj) ([]*FeatureWorkflow, error)`
- `project.SaveWorkflows(key, workflows) error`

**Call sites**
- `internal/app/views/workflows.go:52 — []*project.FeatureWorkflow` state
- `internal/app/views/workflows.go:107 — components.NewFilter[*FeatureWorkflow]`
- `internal/app/views/workflows.go:168 — LoadWorkflows`
- `internal/app/views/workflows.go:177-193 — SaveWorkflows`
- `internal/app/views/workflows.go:198 — currentWorkflow accessor`
- `internal/app/views/workflows.go:609 — NewFeatureWorkflow`
- `internal/app/views/workflows.go:651 — startWorkflowFromJiraOnExisting(wf *FeatureWorkflow)`
- `internal/app/views/workflows.go:684 — buildWorkflowCommitInstructions(wf)`
- `internal/app/views/workflows.go:728 — NewFeatureWorkflow`
- `internal/app/views/workflows.go:823 — buildMRSummaryText(wf)`
- `internal/app/views/workflows.go:929 — DiscoverWorkflows`
- `internal/app/views/workflows.go:985-1057 — workflow tick + enum arm rendering (6 arms)`
- `internal/app/views/workflows.go:1305 — WorkflowActive check`
- `internal/app/views/workflows.go:1327 — renderRepoDetail(wf)`
- `internal/app/views/workflow_diff.go:62/83/115 — workflow + WorkflowRepo (goroutine param)`
- `internal/app/views/jira.go:829 — NewFeatureWorkflow (start from JIRA)`

Mutating? **yes** (Save, Discover triggers init). Streaming? **state changes over time** —
worklflow tick currently polls (workflowTickCmd). For migration, prefer WS events
`workflow_updated`.

---

### 2.15 agent / engine

**Functions/types used**
- `engine.Engine` (type — held by app + agent/jira/workflow/worktree views)
- `engine.AgentSnapshot`, `engine.OutputEntry`, `engine.AgentOptions` (types)
- `engine.AgentState` (type) + enums `AgentIdle/Routing/Starting/Running/Complete/Error/Killed`
- `engine.New(maxAgents int) *Engine`
- `Engine.SetSoundConfig`, `Engine.PruneStaleWorktrees(path)`
- `Engine.Shutdown()`
- `Engine.OnAgentUpdate(func(agentID string))` (callback registration — **cross-cutting**)
- `Engine.StartAgent(workDir, prompt, opts) (id string, err)`
- `Engine.ResumeWithHistory(agentID, msg, opts) (id, err)`
- `Engine.SendInput(agentID, msg) error`
- `Engine.KillAgent(id)`, `Engine.RemoveAgent(id)`
- `Engine.ListAgents() []AgentSnapshot`
- `Engine.GetAgent(id) *AgentSnapshot`
- `Engine.GetOutputEntries(id, since) ([]OutputEntry, error)`
- `Engine.Stats()` (counts)
- `Engine.MaxAgents() int`

**Call sites — lifecycle**
- `internal/app/app.go:287 — engine.New(10)`
- `internal/app/app.go:289 — SetSoundConfig`
- `internal/app/app.go:290 — PruneStaleWorktrees`
- `internal/app/app.go:366 — engine.New (alt code path)`
- `internal/app/app.go:368 — SetSoundConfig`
- `internal/app/app.go:371 — PruneStaleWorktrees (per repo)`
- `internal/app/app.go:548 — Shutdown`
- `internal/app/app.go:918 — OnAgentUpdate (registers tea.Cmd push)`

**Call sites — agent ops**
- `internal/app/views/agent.go:238 — ListAgents`
- `internal/app/views/agent.go:379 — GetOutputEntries`
- `internal/app/views/agent.go:607 — StartAgent`
- `internal/app/views/agent.go:651 — ResumeWithHistory`
- `internal/app/views/agent.go:660 — SendInput`
- `internal/app/views/agent.go:825 — KillAgent`
- `internal/app/views/agent.go:884 — RemoveAgent`
- `internal/app/views/agent.go:1095 — MaxAgents`
- `internal/app/views/agent.go:1432 — StartAgent (from Jira)`
- `internal/app/views/worktree.go:370 — StartAgent (worktree path)`
- `internal/app/views/worktree.go:424 — StartAgent (alt path)`
- `internal/app/views/workflows.go:233 — Stats`
- `internal/app/views/workflows.go:244 — StartAgent`
- `internal/app/views/workflows.go:634 — StartAgent`
- `internal/app/views/workflows.go:668 — StartAgent`
- `internal/app/views/workflows.go:971 — GetAgent`
- `internal/app/views/workflows.go:1006 — GetAgent`
- `internal/app/views/workflows.go:1064 — GetAgent`

**Call sites — state enum arms (informational only)**
- `internal/app/views/agent.go:342, 641, 770, 783, 821, 849, 872, 932, 948, 972-990, 1026-1090, 1109, 1229, 1256-1290`
- `internal/app/views/workflows.go:976, 1068-1072`
- `internal/app/views/jira.go:675`

Mutating? **yes** (Start/Resume/Send/Kill/Remove). Streaming? **YES** — `agent_output` WS event
already streams output. Service must expose a subscription, not just `GetOutputEntries`.

---

### 2.16 jira (client + AI workflows)

**Functions/types used**
- `jira.Client` (type — held by JiraView, JiraPickerState, AgentView)
- `jira.Issue`, `jira.SearchResult` (types — passed around the UI)
- `jira.JiraAction` (type — passed across views; ApprovalView state)
- `jira.NewClient(baseURL, email, token) *Client`
- `jira.ParseJiraActions(path) ([]JiraAction, error)`
- `jira.RefineTicket(eng, issue, repoPath, focus, actionsFile) (id, err)`
- `jira.CreateStories(eng, issue, text, project, repoPath, actionsFile) (id, err)`
- `jira.RefineProposalWithContext(eng, issue, existing, feedback, repoPath, relFile) (id, err)`
- `jira.ReviewTickets(eng, selected, repoPath, instruction, actionsFile) (id, err)`

**Call sites — client / data**
- `internal/app/views/jira.go:74-178 — client + issue list state + NewClient`
- `internal/app/views/jira_picker.go:25-307 — picker holds Client, Issue, returns *Issue`
- `internal/app/views/agent.go:108-179 — AgentView holds jiraClient, NewClient`
- `internal/app/views/agent.go:329, 771, 933 — ParseJiraActions (load action json)`
- `internal/app/views/jira.go:681 — ParseJiraActions`
- `internal/app/views/agent.go:1520 — ParseJiraActions`

**Call sites — AI workflows (these take *engine.Engine!)**
- `internal/app/views/jira.go:618 — jira.RefineTicket`
- `internal/app/views/jira.go:627 — jira.CreateStories`
- `internal/app/views/jira.go:646 — jira.RefineProposalWithContext`
- `internal/app/views/jira.go:1028 — jira.CreateStories (free-text mode)`
- `internal/app/views/jira.go:1060 — jira.ReviewTickets`
- `internal/app/views/agent.go:1452 — jira.RefineTicket`
- `internal/app/views/agent.go:1486 — jira.CreateStories`
- `internal/app/views/agent.go:1526 — jira.RefineProposalWithContext`

**Call sites — actions / approval**
- `internal/app/views/approval.go:17/31/54/69-98 — ApprovalView wraps []JiraAction + Client`
- `internal/app/views/approval.go:355/472/575 — render/apply individual action`

Mutating? **partial** — the AI workflows mutate engine state (they spawn agents) but
fundamentally read-only against the Jira REST API. ApprovalView eventually mutates the
Jira side. Streaming? **no** for fetches; the agent they spawn streams via the agent
capability.

---

## 3. Cross-cutting concerns

### 3.1 Engine update callback

`internal/app/app.go:918`

```go
m.engine.OnAgentUpdate(func(agentID string) { /* push tea.Cmd to TUI */ })
```

This is the single biggest "doesn't fit RPC" hook. The engine pushes notifications
when any agent's state/output changes. In the daemon model this becomes a WS event
(`agent_updated` / `agent_output`). The client SDK must let views register a callback
the same way; under the hood it subscribes to the WS stream.

Already partially modelled — `internal/app/ws.go` defines `agent_output`, `agent_started`
WS handlers. So the surface area is known; we just need to plumb them through the
service interface (a channel- or callback-based subscribe method).

### 3.2 Per-repo goroutine fan-out (project_sync, project_diff, workflow_diff)

`project_sync.go:107, 258, 359`, `project_diff.go:101`, `workflow_diff.go:115` all spin
goroutines per repo to do work in parallel.

Under daemon-mode the work moves server-side. Two options:
(a) Service exposes a `*All` bulk method that returns aggregated results.
(b) Service exposes per-repo methods; client orchestrates concurrency.

Recommend (a) for `*All*` flows — already prefigured by `project.ListAllStashes`,
`project.StashAllRepos`, `project.ApplyStashAllRepos`. Encode the same pattern for
fetch/pull/push/workdir-diff.

### 3.3 Workflow polling tick

`workflows.go:951 workflowTickCmd` polls every N seconds for workflow state. Replace
with WS `workflow_updated` push events to avoid wasted RPCs.

### 3.4 OS-local utilities

`git.CopyToClipboard` (log.go:559, workflows.go:809) operates on the client's
clipboard. **Do not** route through the daemon; views should call a local helper
directly (or the service can no-op on the remote side and the TUI keeps a tiny local
clipboard module).

### 3.5 Sentinel errors crossing the boundary

`git.ErrMRAlreadyExists` is matched via `errors.Is` at `pr.go:540`. The remote service
must preserve sentinel error identity — serialize as a typed error code in JSON and
have the client SDK map back to the same sentinel.

### 3.6 Process lifecycle coupling

`engine.New(10)` is invoked in `app.go:287, 366` — the client currently *owns* the
engine. After migration, the client gets a handle to a remote engine; lifecycle calls
(`Shutdown`, `SetSoundConfig`, `PruneStaleWorktrees`) need to be either:
- moved fully daemon-side (daemon's startup applies config),
- or exposed as admin RPCs (`POST /admin/shutdown` etc).

Recommend: `SetSoundConfig` and `PruneStaleWorktrees` happen daemon-side at startup; `Shutdown` becomes "daemon stop" (separate CLI command), not a TUI action.

### 3.7 Layout/status bar receives RepoInfo directly

`layout.go:134, 217` accept `*git.RepoInfo`. Pure rendering — fine to keep as long as
the type is importable on the client. Listed under §4.

---

## 4. Types crossing the boundary

These struct/enum types are passed around in view state, function parameters, or
returned from the service. They must remain importable on the *client* side, which
under the migration plan means leaving them in their current packages (per invariant §2.5
of the plan, "types stay shared").

### From `internal/git`

- `RepoInfo` (used in app.go, layout.go, async.go, ws.go, overview.go, pr.go, pipeline.go, diff.go, log.go, stash.go, branches.go, branch_comparison.go, worktree.go, rebase.go, project.go)
- `BranchInfo` (async.go, overview.go, pipeline.go, branches.go, branch_comparison.go, rebase.go, worktree.go)
- `BranchComparison`, `TreeComparison` (async.go, branch_comparison.go)
- `BranchDiff` (diff.go, workflow_diff.go, branch_comparison.go)
- `WorkdirDiff`, `WorkdirStatus`, `FileChange` (diff.go, project_diff.go)
- `DiffHunk`, `FilteredDiffHunk` (commit.go, workflow_diff.go)
- `UpstreamStatus` (sync.go, project_sync.go)
- `PipelineInfo`, `PipelineStatus` + 6 enum constants (pipeline.go, branches.go)
- `StashEntry` (stash.go, overview.go)
- `Worktree` (worktree.go, overview.go, project.go, jira.go)
- `RebaseCommit`, `RebaseOperation` + 7 enum constants (rebase.go)
- `ForgeAuth` (pr.go, branches.go, pipeline.go) — see note in §2.11 about hiding behind a smaller DTO
- `MergeRequest` (pr.go)
- `ErrMRAlreadyExists` (pr.go) — sentinel error

### From `internal/engine`

- `Engine` — should **not** cross the boundary. Views currently hold `*engine.Engine`;
  after migration they hold an `AgentService` interface.
- `AgentSnapshot` (workflows.go) — yes, data DTO
- `OutputEntry` (agent.go, jira.go) — yes, data DTO
- `AgentOptions` (agent.go, jira.go, workflows.go, worktree.go) — yes, request DTO; also
  appears as a parameter to `jira.RefineTicket` etc. (which themselves need to be
  re-homed under the agent/jira service)
- `AgentState` + 7 enum constants (AgentIdle/Routing/Starting/Running/Complete/Error/Killed)

### From `internal/project`

- `Project` — **opaque type**, currently held by views. Need to decide:
  treat as a *handle* (string key the daemon resolves) **or** keep as a DTO with
  enough fields for client rendering. The fact that views call `proj.Repos` etc. through
  workflow helpers and pass it to `jira.CreateStories` suggests we need a `ProjectInfo`
  DTO that views read from, and the daemon owns the real `*project.Project`.
- `Repo` — DTO, passed to goroutines.
- `RepoStatus`, `ProjectStatus`, `BranchExistence` — DTO (used in project.go state)
- `RepoDef`, `ProjectDef` — constructor structs. Probably stay daemon-side; client
  never builds them after migration (daemon loads from config file).
- `FeatureWorkflow`, `WorkflowRepo` — DTO + state. Held by WorkflowsView.
- `WorkflowState` + 6 enum constants (Active/Done/Initializing/PushingAll/CreatingMRs/CleaningUp)
- `RepoStashList`, `RepoStashResult` — bulk-op result DTOs

### From `internal/jira`

- `Client` — should **not** cross the boundary. Views currently hold `*jira.Client`;
  becomes `JiraService` interface.
- `Issue`, `SearchResult` — DTOs (already plain structs, JSON-friendly)
- `JiraAction` — DTO (passed to ApprovalView)

---

## 5. Notes for the architect

1. **No view holds raw filesystem paths as authoritative state** — every call passes
   `repoPath` (string). Good news: the service interfaces can keep paths as the
   identifier and the daemon resolves them to repos / repo caches.

2. **`*engine.Engine` is the most-shared object** — it threads through agent, jira,
   workflows, worktree views. The migration's `AgentService` interface needs to
   satisfy all of them; consider splitting into `AgentService` (start/list/get/kill/output)
   and `JiraAIService` (Refine/Create/Review/RefineProposal) so the dependency on the
   engine pointer disappears from jira-side helpers.

3. **The `jira.RefineTicket`/`CreateStories`/`RefineProposalWithContext`/`ReviewTickets`
   functions accept `*engine.Engine` directly** — these are essentially "spawn a
   pre-baked agent" recipes. In the daemon model they become a single service method:
   `JiraAIService.RefineTicket(ctx, IssueRef, RepoRef, Focus)` returning an `agent_id`,
   which the client then subscribes to via `AgentService`.

4. **`*project.Project` as opaque vs DTO** — currently views pass it through to helpers
   (`NewFeatureWorkflow`, `ListAllStashes`, etc.). After migration, *those helpers run
   daemon-side*, so the client only needs the projection of `Project` it actually renders:
   a list of repos (name + path + currentBranch + status). Design accordingly — don't
   expose the full Project type on the client.

5. **WS already carries some events** (`repo_update`, `branch_update`, `pipeline_update`,
   `agent_output`, `agent_started` — see ws.go:439-457). Use these as the basis for the
   subscription methods on each service interface; add `workflow_updated`,
   `sync_progress`, and `mr_created` as missing pieces.

6. **Long-running operations** (Push, Pull, RebaseOntoMain, CreateMR, DiscoverWorkflows)
   currently block in goroutines until done, then return one result blob. Decide for each
   whether the service stays blocking-RPC or becomes `start_op` + WS progress events.
   Likely candidates for streaming: push/pull/rebase/discover; the rest can stay
   request/response.

7. **Sentinel errors** must round-trip. Define an error-code scheme in `internal/api`
   (`{"code":"MR_ALREADY_EXISTS"}`) and have the remote client map it back to
   `git.ErrMRAlreadyExists`.
