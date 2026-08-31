package service

import (
	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/jira"
	"gitlab.com/tanevanwifferen1/singularity/internal/project"
)

// Re-exported DTO types. Views import these via internal/service only and
// never reach into internal/git/internal/engine/internal/project/internal/jira
// directly. Type aliases keep JSON marshaling identical to the existing
// definitions (tags travel with the underlying type).
//
// Operations move to the daemon; data shapes stay where they are.

// --- from internal/git ---

type (
	// RepoInfo is the canonical repo descriptor (path, branches, remotes,
	// HEAD, dirty bit). Held in view state, also serialized over WS via
	// "repo_update" events.
	RepoInfo = git.RepoInfo

	// BranchInfo describes a single branch (name, upstream, ahead/behind).
	BranchInfo = git.BranchInfo

	// RemoteInfo describes a git remote.
	RemoteInfo = git.RemoteInfo

	// BranchComparison summarizes ahead/behind/divergence between two branches.
	BranchComparison = git.BranchComparison

	// TreeComparison is a tree-level comparison (path + status), distinct
	// from BranchComparison's commit-level view.
	TreeComparison = git.TreeComparison

	// BranchDiff is the full textual diff between two branches.
	BranchDiff = git.BranchDiff

	// WorkdirDiff is the staged+unstaged working-tree diff snapshot.
	WorkdirDiff = git.WorkdirDiff

	// WorkdirStatus is the per-file status portion of WorkdirDiff.
	WorkdirStatus = git.WorkdirStatus

	// FileChange describes one file change (path, status, +/-).
	FileChange = git.FileChange

	// DiffHunk is a single hunk parsed out of a raw diff string.
	DiffHunk = git.DiffHunk

	// FilteredDiffHunk is a hunk annotated as "new in this branch" vs.
	// "already in the merge base", used by deep file diff views.
	FilteredDiffHunk = git.FilteredDiffHunk

	// UpstreamStatus is the ahead/behind/last-fetch summary versus upstream.
	UpstreamStatus = git.UpstreamStatus

	// PipelineInfo is the CI pipeline state for a branch.
	PipelineInfo = git.PipelineInfo

	// PipelineStatus enum value.
	PipelineStatus = git.PipelineStatus

	// StashEntry is one entry from `git stash list`.
	StashEntry = git.StashEntry

	// Worktree describes one git worktree (path, branch, locked, prunable).
	Worktree = git.Worktree

	// RebaseCommit is one commit in an interactive-rebase plan.
	RebaseCommit = git.RebaseCommit

	// RebaseOperation enum value (pick/reword/edit/squash/fixup/drop).
	RebaseOperation = git.RebaseOperation

	// ForgeAuth is the detected forge credential set. Whole struct is
	// exposed for backwards compat; ForgeService.Detect returns the lean
	// ForgeInfo DTO instead for use by clients that only need provider/has-auth.
	ForgeAuth = git.ForgeAuth

	// ForgeType enum value (GitHub/GitLab/Unknown).
	ForgeType = git.ForgeType

	// RemoteProvider enum value (used by CLI MR fallback).
	RemoteProvider = git.RemoteProvider

	// MergeRequest is the created MR/PR descriptor.
	MergeRequest = git.MergeRequest

	// MRResult is the CLI-fallback MR creation result.
	MRResult = git.MRResult

	// CommitMessage is a structured commit message (type/scope/subject/body).
	CommitMessage = git.CommitMessage
)

// --- from internal/engine ---

type (
	// AgentSnapshot is the read-only DTO view of an agent (id, state,
	// work dir, task, timestamps, error, exit code).
	AgentSnapshot = engine.AgentSnapshot

	// OutputEntry is one line of agent stdout/stderr with source + timestamp.
	OutputEntry = engine.OutputEntry

	// AgentOptions is the request DTO for StartAgent / ResumeWithHistory.
	AgentOptions = engine.AgentOptions

	// AgentState enum value (Idle/Routing/Starting/Running/Complete/Error/Killed).
	AgentState = engine.AgentState

	// EngineStats is the aggregate engine snapshot (running/idle/total counts).
	EngineStats = engine.EngineStats
)

// --- from internal/project ---

type (
	// RepoDef is the static config definition of one repo inside a project.
	RepoDef = project.RepoDef

	// ProjectDef is the static config definition of a project.
	ProjectDef = project.ProjectDef

	// Repo is the runtime per-repo handle inside a loaded project. Note:
	// kept as an alias for parity with existing views, but new code should
	// prefer fetching RepoInfo via RepoService since *project.Repo is
	// daemon-only state.
	Repo = project.Repo

	// RepoStatus is the per-repo status snapshot (branch, dirty, ahead/behind).
	RepoStatus = project.RepoStatus

	// ProjectStatus is the aggregated multi-repo status snapshot.
	ProjectStatus = project.ProjectStatus

	// BranchExistence reports which repos in a project carry a given branch.
	BranchExistence = project.BranchExistence

	// FeatureWorkflow is the multi-repo feature workflow state.
	FeatureWorkflow = project.FeatureWorkflow

	// WorkflowRepo is the per-repo slice of a FeatureWorkflow.
	WorkflowRepo = project.WorkflowRepo

	// WorkflowState enum value (Initializing/Active/PushingAll/CreatingMRs/CleaningUp/Done).
	WorkflowState = project.WorkflowState

	// WorkflowStatus is the aggregate progress snapshot of a workflow.
	WorkflowStatus = project.WorkflowStatus

	// RepoStashList is the per-repo stash list from project.ListAllStashes.
	RepoStashList = project.RepoStashList

	// RepoStashResult is the per-repo outcome of a bulk stash op.
	RepoStashResult = project.RepoStashResult
)

// --- from internal/jira ---

type (
	// Issue is the canonical Jira issue DTO.
	Issue = jira.Issue

	// SearchResult is the paginated search response from Jira.
	SearchResult = jira.SearchResult

	// JiraAction is one entry from the action-list JSON file, passed
	// through to ApprovalView.
	JiraAction = jira.JiraAction
)

// --- Re-exported enum constants ---
//
// Go type aliases only re-export the type itself, not its constants. The
// constants below mirror the originals so view code can reference them via
// the service package the same way it references the types. Adding new ones
// requires they be exposed through some service interface method/event.

// PipelineStatus values mirroring internal/git.
const (
	PipelinePending  = git.PipelinePending
	PipelineRunning  = git.PipelineRunning
	PipelineSuccess  = git.PipelineSuccess
	PipelineFailed   = git.PipelineFailed
	PipelineCanceled = git.PipelineCanceled
	PipelineSkipped  = git.PipelineSkipped
)

// RebaseOperation values mirroring internal/git.
const (
	RebasePick   = git.RebasePick
	RebaseReword = git.RebaseReword
	RebaseEdit   = git.RebaseEdit
	RebaseSquash = git.RebaseSquash
	RebaseFixup  = git.RebaseFixup
	RebaseDrop   = git.RebaseDrop
)

// AgentState values mirroring internal/engine.
const (
	AgentIdle     = engine.AgentIdle
	AgentRouting  = engine.AgentRouting
	AgentStarting = engine.AgentStarting
	AgentRunning  = engine.AgentRunning
	AgentComplete = engine.AgentComplete
	AgentError    = engine.AgentError
	AgentKilled   = engine.AgentKilled
)

// WorkflowState values mirroring internal/project.
const (
	WorkflowInitializing = project.WorkflowInitializing
	WorkflowActive       = project.WorkflowActive
	WorkflowPushingAll   = project.WorkflowPushingAll
	WorkflowCreatingMRs  = project.WorkflowCreatingMRs
	WorkflowCleaningUp   = project.WorkflowCleaningUp
	WorkflowDone         = project.WorkflowDone
)

// ForgeType values mirroring internal/git.
const (
	ForgeGitHub  = git.ForgeGitHub
	ForgeGitLab  = git.ForgeGitLab
	ForgeGitea   = git.ForgeGitea
	ForgeUnknown = git.ForgeUnknown
)

// RemoteProvider values mirroring internal/git.
const (
	ProviderGitHub  = git.ProviderGitHub
	ProviderGitLab  = git.ProviderGitLab
	ProviderGitea   = git.ProviderGitea
	ProviderUnknown = git.ProviderUnknown
)

// --- Transitional aliases for opaque-ish types still held by views ---
//
// These types are held by views during Phase D. Long-term, project views
// hold a ProjectHandle + ProjectInfo and call the daemon for operations
// (see CALL-SITES §5 note 4); workflows views hold WorkflowKey + status.
// Keeping the aliases here keeps the build green while the deeper refactor
// is deferred. TODO(phase-D-followup): remove these once views consume
// ProjectInfo+handle instead of *Project.
type (
	// Project is the loaded multi-repo project handle. Held directly by
	// views as an opaque-ish type today; method calls (proj.Refresh,
	// proj.Repos, proj.Status, proj.BranchExistsAcross) flow through.
	Project = project.Project

	// Loader is the project config loader (parses projects.json and
	// instantiates *Project values).
	Loader = project.Loader
)

// ProjectHandle is an opaque identifier the daemon uses to refer to a loaded
// project. Views treat *project.Project as opaque (per CALL-SITES §5 note 4);
// after migration they hold only a handle plus a lean ProjectInfo DTO they
// can render. The daemon resolves the handle to the real *project.Project.
//
// Conventionally the handle is the project key from daemon config, but
// callers MUST treat it as opaque — do not parse it.
type ProjectHandle string

// ProjectInfo is the lean projection of a Project that the client actually
// renders: identification + per-repo summary. Sufficient for picker views and
// status bars without exposing the full *project.Project graph.
//
// The full ProjectStatus (re-exported above) is still returned by
// ProjectService.Status when views need the aggregated data.
type ProjectInfo struct {
	Handle ProjectHandle `json:"handle"`
	Key    string        `json:"key"`
	Name   string        `json:"name"`
	Repos  []RepoSummary `json:"repos"`
	Loaded bool          `json:"loaded"`
	// ContextFiles are the project's configured agent-context file paths.
	// Carried over the wire so a client rebuilding a *Project from this
	// DTO keeps cross-repo context injection working.
	ContextFiles []string `json:"context_files,omitempty"`
	Context      string   `json:"context_summary,omitempty"`
}

// RepoSummary is the per-repo slice of ProjectInfo — just what overview /
// picker rendering needs without pulling the full RepoInfo over the wire.
type RepoSummary struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	CurrentBranch string `json:"current_branch"`
	DefaultBranch string `json:"default_branch"`
	Dirty         bool   `json:"dirty"`
}
