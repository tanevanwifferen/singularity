package service

// Services aggregates every capability into a single value the TUI accepts
// in view constructors. Both implementation subpackages (local, remote)
// expose a constructor returning *Services so call sites stay symmetric:
//
//	// daemon side
//	svc := local.NewServices(engine, loader, ...)
//	// TUI side
//	svc := remote.NewServices(httpClient)
//
// All fields are required — nil entries indicate a broken implementation,
// not an opt-out. Views may take the whole Services or just the interface
// they need; passing the whole struct keeps constructors compact and lets
// us add capabilities without churning every view signature.
type Services struct {
	Repo     RepoService
	Branch   BranchService
	Diff     DiffService
	Commit   CommitService
	Stash    StashService
	Rebase   RebaseService
	Worktree WorktreeService
	Sync     SyncService
	Pipeline PipelineService
	MR       MRService
	Forge    ForgeService
	Project  ProjectService
	Agent    AgentService
	Jira     JiraService
}
