// Package fake provides no-op service.Services implementations for tests.
// Every method returns the zero value of its result type plus
// service.ErrUnavailable so callers that care about the wire-up surface
// without actually exercising the daemon get a stable response.
//
// Tests that need richer fakes can construct New() then overwrite individual
// service fields with hand-rolled mocks before passing into the SUT.
package fake

import (
	"context"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// New returns a *service.Services where every capability is a no-op stub.
func New() *service.Services {
	return &service.Services{
		Repo:     repoStub{},
		Branch:   branchStub{},
		Diff:     diffStub{},
		Commit:   commitStub{},
		Stash:    stashStub{},
		Rebase:   rebaseStub{},
		Worktree: worktreeStub{},
		Sync:     syncStub{},
		Pipeline: pipelineStub{},
		MR:       mrStub{},
		Forge:    forgeStub{},
		Project:  projectStub{},
		Agent:    agentStub{},
		Jira:     jiraStub{},
	}
}

func unavail() error { return service.ErrUnavailable }

// repoStub ---------------------------------------------------------------------

type repoStub struct{}

func (repoStub) Open(context.Context, string) (*service.RepoInfo, error) {
	return nil, unavail()
}
func (repoStub) Find(context.Context, string) (string, error) { return "", unavail() }
func (repoStub) Subscribe(context.Context, string) (<-chan *service.RepoInfo, func(), error) {
	ch := make(chan *service.RepoInfo)
	close(ch)
	return ch, func() {}, nil
}

// branchStub -------------------------------------------------------------------

type branchStub struct{}

func (branchStub) List(context.Context, string) ([]service.BranchInfo, error) { return nil, unavail() }
func (branchStub) Create(context.Context, string, string, string) error       { return unavail() }
func (branchStub) Checkout(context.Context, string, string) error             { return unavail() }
func (branchStub) CheckoutDetached(context.Context, string) error             { return unavail() }
func (branchStub) CheckoutDetachedAt(context.Context, string, string) error   { return unavail() }
func (branchStub) Delete(context.Context, string, string, bool) error         { return unavail() }
func (branchStub) HEAD(context.Context, string) (string, error)               { return "", unavail() }
func (branchStub) ResolveRef(context.Context, string, string) (string, error) { return "", unavail() }
func (branchStub) Compare(context.Context, string, string, string) (*service.BranchComparison, error) {
	return nil, unavail()
}
func (branchStub) CompareByTree(context.Context, string, string, string) (*service.TreeComparison, error) {
	return nil, unavail()
}

// diffStub ---------------------------------------------------------------------

type diffStub struct{}

func (diffStub) BranchDiff(context.Context, string, string, string) (*service.BranchDiff, error) {
	return nil, unavail()
}
func (diffStub) WorkdirStatus(context.Context, string) (*service.WorkdirDiff, error) {
	return nil, unavail()
}
func (diffStub) FileDiff(context.Context, string, string, string, string) (string, error) {
	return "", unavail()
}
func (diffStub) StagedFileDiff(context.Context, string, string) (string, error) { return "", unavail() }
func (diffStub) UnstagedFileDiff(context.Context, string, string) (string, error) {
	return "", unavail()
}
func (diffStub) DeepFileDiff(context.Context, string, string, string, string, string) ([]service.FilteredDiffHunk, string, error) {
	return nil, "", unavail()
}
func (diffStub) MergeBase(context.Context, string, string, string) (string, error) {
	return "", unavail()
}
func (diffStub) StageHunk(context.Context, string, string, service.DiffHunk) error { return unavail() }
func (diffStub) UnstageHunk(context.Context, string, string, service.DiffHunk) error {
	return unavail()
}
func (diffStub) StageLines(context.Context, string, string, service.DiffHunk, []int) error {
	return unavail()
}
func (diffStub) UnstageLines(context.Context, string, string, service.DiffHunk, []int) error {
	return unavail()
}
func (diffStub) DiffAllRepos(context.Context, service.ProjectHandle) (map[string]*service.WorkdirDiff, error) {
	return nil, unavail()
}

// commitStub -------------------------------------------------------------------

type commitStub struct{}

func (commitStub) SuggestMessage(context.Context, string) (string, error) { return "", unavail() }
func (commitStub) Files(context.Context, string, string) ([]service.FileChange, error) {
	return nil, unavail()
}
func (commitStub) FileDiff(context.Context, string, string, string) (string, error) {
	return "", unavail()
}
func (commitStub) FullDiff(context.Context, string, string) (string, error) { return "", unavail() }
func (commitStub) CherryPick(context.Context, string, string) error         { return unavail() }
func (commitStub) Reset(context.Context, string, string, string) error      { return unavail() }
func (commitStub) AmendMessage(context.Context, string, string) error       { return unavail() }
func (commitStub) GenerateMessage(context.Context, string) (*service.CommitMessage, error) {
	return nil, unavail()
}

// stashStub --------------------------------------------------------------------

type stashStub struct{}

func (stashStub) List(context.Context, string) ([]service.StashEntry, error) { return nil, unavail() }
func (stashStub) Get(context.Context, string, int) (*service.StashEntry, error) {
	return nil, unavail()
}
func (stashStub) Create(context.Context, string, string, bool) (int, error) { return 0, unavail() }
func (stashStub) Apply(context.Context, string, int, bool) error            { return unavail() }
func (stashStub) Drop(context.Context, string, int) error                   { return unavail() }
func (stashStub) Clear(context.Context, string) error                       { return unavail() }
func (stashStub) ListAllRepos(context.Context, service.ProjectHandle) ([]service.RepoStashList, error) {
	return nil, unavail()
}
func (stashStub) StashAllRepos(context.Context, service.ProjectHandle, string, bool) ([]service.RepoStashResult, error) {
	return nil, unavail()
}
func (stashStub) ApplyStashAllRepos(context.Context, service.ProjectHandle, string, bool) ([]service.RepoStashResult, error) {
	return nil, unavail()
}

// rebaseStub -------------------------------------------------------------------

type rebaseStub struct{}

func (rebaseStub) Plan(context.Context, string, string, string) ([]service.RebaseCommit, error) {
	return nil, unavail()
}
func (rebaseStub) Status(context.Context, string) (bool, string, error) { return false, "", unavail() }
func (rebaseStub) GenerateTodo(context.Context, []service.RebaseCommit) (string, error) {
	return "", unavail()
}
func (rebaseStub) Continue(context.Context, string) error { return unavail() }
func (rebaseStub) Skip(context.Context, string) error     { return unavail() }
func (rebaseStub) Abort(context.Context, string) error    { return unavail() }
func (rebaseStub) OntoMain(context.Context, string) (<-chan service.SyncProgressEvent, func(), error) {
	ch := make(chan service.SyncProgressEvent)
	close(ch)
	return ch, func() {}, unavail()
}
func (rebaseStub) Context(context.Context, string, string, []string) (string, error) {
	return "", unavail()
}

// worktreeStub -----------------------------------------------------------------

type worktreeStub struct{}

func (worktreeStub) List(context.Context, string) ([]service.Worktree, error) { return nil, unavail() }
func (worktreeStub) Create(context.Context, string, string, string, bool, string) error {
	return unavail()
}
func (worktreeStub) Remove(context.Context, string, string, bool) error { return unavail() }
func (worktreeStub) Prune(context.Context, string) error                { return unavail() }
func (worktreeStub) Lock(context.Context, string, string) error         { return unavail() }
func (worktreeStub) Unlock(context.Context, string, string) error       { return unavail() }

// syncStub ---------------------------------------------------------------------

type syncStub struct{}

func (syncStub) UpstreamStatus(context.Context, string) (*service.UpstreamStatus, error) {
	return nil, unavail()
}
func (syncStub) LastFetchTime(context.Context, string) (time.Time, error) {
	return time.Time{}, unavail()
}

func emptySyncProgress() (<-chan service.SyncProgressEvent, func(), error) {
	ch := make(chan service.SyncProgressEvent)
	close(ch)
	return ch, func() {}, unavail()
}

func (syncStub) Fetch(context.Context, string, string) (<-chan service.SyncProgressEvent, func(), error) {
	return emptySyncProgress()
}
func (syncStub) Pull(context.Context, string) (<-chan service.SyncProgressEvent, func(), error) {
	return emptySyncProgress()
}
func (syncStub) Push(context.Context, string, bool) (<-chan service.SyncProgressEvent, func(), error) {
	return emptySyncProgress()
}
func (syncStub) PullRebase(context.Context, string) (<-chan service.SyncProgressEvent, func(), error) {
	return emptySyncProgress()
}
func (syncStub) SetUpstreamAndPush(context.Context, string, string) (<-chan service.SyncProgressEvent, func(), error) {
	return emptySyncProgress()
}
func (syncStub) SyncAllRepos(context.Context, service.ProjectHandle, bool) (<-chan service.SyncProgressEvent, func(), error) {
	return emptySyncProgress()
}

// pipelineStub -----------------------------------------------------------------

type pipelineStub struct{}

func (pipelineStub) Statuses(context.Context, string, []service.BranchInfo) (map[string]*service.PipelineInfo, error) {
	return nil, unavail()
}
func (pipelineStub) Retry(context.Context, string, string) error { return unavail() }
func (pipelineStub) Subscribe(context.Context, string) (<-chan service.PipelineEvent, func(), error) {
	ch := make(chan service.PipelineEvent)
	close(ch)
	return ch, func() {}, nil
}

// mrStub -----------------------------------------------------------------------

type mrStub struct{}

func (mrStub) GenerateTitle(context.Context, string, string, string) (string, error) {
	return "", unavail()
}
func (mrStub) GenerateDescription(context.Context, string, string, string) (string, error) {
	return "", unavail()
}
func (mrStub) Create(context.Context, string, string, string, string, string, []string) (*service.MergeRequest, error) {
	return nil, unavail()
}
func (mrStub) CreateCLI(context.Context, string, service.RemoteProvider, string) (*service.MRResult, error) {
	return nil, unavail()
}

// forgeStub --------------------------------------------------------------------

type forgeStub struct{}

func (forgeStub) DetectAuth(context.Context) (*service.ForgeAuth, error) { return nil, unavail() }
func (forgeStub) Detect(context.Context) (*service.ForgeInfo, error)     { return nil, unavail() }
func (forgeStub) DetectProvider(context.Context, string) (service.RemoteProvider, error) {
	return service.ProviderUnknown, unavail()
}
func (forgeStub) ProviderInfo(context.Context, string) (*service.ForgeProviderInfo, error) {
	return nil, unavail()
}

// projectStub ------------------------------------------------------------------

type projectStub struct{}

func (projectStub) List(context.Context) ([]string, error) { return nil, unavail() }
func (projectStub) Load(context.Context, string) (*service.ProjectInfo, error) {
	return nil, unavail()
}
func (projectStub) Info(context.Context, service.ProjectHandle) (*service.ProjectInfo, error) {
	return nil, unavail()
}
func (projectStub) Status(context.Context, service.ProjectHandle) (*service.ProjectStatus, error) {
	return nil, unavail()
}
func (projectStub) Refresh(context.Context, service.ProjectHandle) (*service.ProjectStatus, error) {
	return nil, unavail()
}
func (projectStub) BranchExists(context.Context, service.ProjectHandle, string) (*service.BranchExistence, error) {
	return nil, unavail()
}
func (projectStub) ContextSummary(context.Context, service.ProjectHandle) (string, error) {
	return "", unavail()
}
func (projectStub) DefaultConfigPath(context.Context) (string, error) { return "", unavail() }
func (projectStub) Subscribe(context.Context, service.ProjectHandle) (<-chan service.ProjectEvent, func(), error) {
	ch := make(chan service.ProjectEvent)
	close(ch)
	return ch, func() {}, nil
}
func (projectStub) CreateWorkflow(context.Context, service.ProjectHandle, string, string) (*service.FeatureWorkflow, error) {
	return nil, unavail()
}
func (projectStub) RemoveWorkflow(context.Context, service.ProjectHandle, string) (*service.FeatureWorkflow, error) {
	return nil, unavail()
}
func (projectStub) LoadWorkflows(context.Context, service.ProjectHandle) ([]*service.FeatureWorkflow, error) {
	return nil, unavail()
}
func (projectStub) SaveWorkflows(context.Context, service.ProjectHandle, []*service.FeatureWorkflow) error {
	return unavail()
}
func (projectStub) DiscoverWorkflowsAllRepos(context.Context, service.ProjectHandle, map[string]bool) (<-chan service.DiscoveryProgressEvent, func(), error) {
	ch := make(chan service.DiscoveryProgressEvent)
	close(ch)
	return ch, func() {}, unavail()
}
func (projectStub) SubscribeWorkflows(context.Context, service.ProjectHandle) (<-chan service.WorkflowEvent, func(), error) {
	ch := make(chan service.WorkflowEvent)
	close(ch)
	return ch, func() {}, nil
}

// agentStub --------------------------------------------------------------------

type agentStub struct{}

func (agentStub) Start(context.Context, string, string, service.AgentOptions) (string, error) {
	return "", unavail()
}
func (agentStub) Resume(context.Context, string, string, service.AgentOptions) (string, error) {
	return "", unavail()
}
func (agentStub) SendInput(context.Context, string, string) error       { return unavail() }
func (agentStub) Kill(context.Context, string) error                    { return unavail() }
func (agentStub) Remove(context.Context, string) error                  { return unavail() }
func (agentStub) List(context.Context) ([]service.AgentSnapshot, error) { return nil, unavail() }
func (agentStub) Get(context.Context, string) (*service.AgentSnapshot, error) {
	return nil, unavail()
}
func (agentStub) Output(context.Context, string, int) ([]service.OutputEntry, error) {
	return nil, unavail()
}
func (agentStub) Stats(context.Context) (service.EngineStats, error) {
	return service.EngineStats{}, unavail()
}
func (agentStub) MaxAgents(context.Context) (int, error) { return 0, unavail() }
func (agentStub) Subscribe(context.Context, string) (<-chan service.AgentEvent, func(), error) {
	ch := make(chan service.AgentEvent)
	close(ch)
	return ch, func() {}, nil
}
func (agentStub) SubscribeAll(context.Context) (<-chan service.AgentEvent, func(), error) {
	ch := make(chan service.AgentEvent)
	close(ch)
	return ch, func() {}, nil
}

// jiraStub ---------------------------------------------------------------------

type jiraStub struct{}

func (jiraStub) SearchIssues(context.Context, string, int) (*service.SearchResult, error) {
	return nil, unavail()
}
func (jiraStub) GetIssue(context.Context, string) (*service.Issue, error) { return nil, unavail() }
func (jiraStub) GetMyIssues(context.Context, string) (*service.SearchResult, error) {
	return nil, unavail()
}
func (jiraStub) UpdateFields(context.Context, string, map[string]any) error { return unavail() }
func (jiraStub) AddComment(context.Context, string, string) error           { return unavail() }
func (jiraStub) CreateIssue(context.Context, string, string, string, string, string) (*service.Issue, error) {
	return nil, unavail()
}
func (jiraStub) LinkIssues(context.Context, string, string, string) error { return unavail() }
func (jiraStub) ParseActions(context.Context, string) ([]service.JiraAction, error) {
	return nil, unavail()
}
func (jiraStub) RefineTicket(context.Context, *service.Issue, string, string, string) (string, error) {
	return "", unavail()
}
func (jiraStub) CreateStories(context.Context, *service.Issue, string, string, string, string) (string, error) {
	return "", unavail()
}
func (jiraStub) RefineProposalWithContext(context.Context, *service.Issue, []service.JiraAction, string, string, string) (string, error) {
	return "", unavail()
}
func (jiraStub) ReviewTickets(context.Context, []service.Issue, string, string, string) (string, error) {
	return "", unavail()
}
