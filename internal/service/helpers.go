package service

import (
	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/project"
)

// Pure rendering / parsing helpers re-exported so view code never needs to
// import internal/git directly. These functions do no I/O and have no
// daemon-vs-client distinction; routing them through the service interface
// would add overhead for no benefit.
//
// Keep this list short and add entries only when a view genuinely needs
// one (e.g. ParseHunks is used in the commit view's diff splitter).

// ParseHunks parses raw textual diff output into hunks. Pure parser, no I/O.
// Mirrors git.ParseHunks; view code calls this via service.ParseHunks(...).
func ParseHunks(rawDiff string) []DiffHunk {
	return git.ParseHunks(rawDiff)
}

// FormatPipelineStatus renders a PipelineStatus enum value as a short
// human-readable label. Pure formatter, no I/O.
func FormatPipelineStatus(status PipelineStatus) string {
	return git.FormatPipelineStatus(status)
}

// NewFeatureWorkflow constructs a multi-repo feature workflow. Transitional
// re-export of project.NewFeatureWorkflow during Phase D — views call this
// to build a workflow value they then pass to project-level service methods.
//
// TODO(phase-D-followup): views should request a workflow through
// ProjectService.CreateWorkflow (already in the interface) rather than
// constructing one client-side; this re-export keeps the build green while
// the workflows view is moved off direct project package use.
func NewFeatureWorkflow(proj *Project, branch, baseDir string) *FeatureWorkflow {
	return project.NewFeatureWorkflow(proj, branch, baseDir)
}

// DefaultWorkflowBaseDir returns the default worktree base directory for a
// project (~/.worktrees/<slug>, reusing a pre-existing legacy raw-name dir).
// Pure path computation plus one Stat; views call this instead of duplicating
// the slug/fallback rules.
func DefaultWorkflowBaseDir(projectName string) string {
	return project.DefaultWorkflowBaseDir(projectName)
}

// NewProject constructs a runtime project from a config def. Transitional
// re-export for the auto-discover code path in app.go; should be removed
// once project loading is fully daemon-side.
func NewProject(def ProjectDef) *Project {
	return project.NewProject(def)
}

// NewProjectFromInfo rebuilds a runtime *Project from the lean ProjectInfo
// DTO the daemon returns for ProjectService.Load. The TUI needs a *Project
// because the project-mode views still hold one directly (see the
// phase-D-followup TODOs above); this is the client-side counterpart of
// buildProjectInfo in internal/service/local.
//
// The returned project is unrefreshed — the caller (or the project view's
// Init) triggers the first Refresh.
func NewProjectFromInfo(info *ProjectInfo) *Project {
	if info == nil {
		return nil
	}
	def := ProjectDef{
		Name:         info.Name,
		ContextFiles: info.ContextFiles,
	}
	for _, r := range info.Repos {
		def.Repos = append(def.Repos, RepoDef{
			Name:          r.Name,
			Path:          r.Path,
			DefaultBranch: r.DefaultBranch,
		})
	}
	return project.NewProject(def)
}

// LoadWorkflows / SaveWorkflows are transitional re-exports used by the
// workflows view's local persistence path. TODO(phase-D-followup): route
// through ProjectService.LoadWorkflows / SaveWorkflows on a handle.
func LoadWorkflows(key string, proj *Project) ([]*FeatureWorkflow, error) {
	return project.LoadWorkflows(key, proj)
}

func SaveWorkflows(key string, workflows []*FeatureWorkflow) error {
	return project.SaveWorkflows(key, workflows)
}

// DiscoverWorkflows is the transitional re-export used during Phase D.
// TODO(phase-D-followup): switch workflows view to use
// ProjectService.DiscoverWorkflowsAllRepos streaming method.
func DiscoverWorkflows(proj *Project, skip map[string]bool) ([]*FeatureWorkflow, error) {
	return project.DiscoverWorkflows(proj, skip)
}

// ListAllStashes / StashAllRepos / ApplyStashAllRepos are transitional
// re-exports of the project-package bulk stash helpers used by
// project_stash.go. TODO(phase-D-followup): route through StashService
// bulk methods on a ProjectHandle.
func ListAllStashes(proj *Project) []RepoStashList {
	return project.ListAllStashes(proj)
}

func StashAllRepos(proj *Project, message string, includeUntracked bool) []RepoStashResult {
	return project.StashAllRepos(proj, message, includeUntracked)
}

func ApplyStashAllRepos(proj *Project, message string, pop bool) []RepoStashResult {
	return project.ApplyStashAllRepos(proj, message, pop)
}

// ResetRepoToMain is a transitional re-export. The audit (CALL-SITES §2.2 and
// §2.7) didn't surface this operation, so it never made it into any service
// interface; it lives in only one call site (project view's "reset all").
//
// TODO(phase-D-followup): the service interface architect should add a
// Branch.ResetToRemote (or similar) so this re-export can be removed.
func ResetRepoToMain(repoPath, defaultBranch string) error {
	return git.ResetRepoToMain(repoPath, defaultBranch)
}
