package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/git"
)

// WorkflowState represents the current phase of a feature workflow
type WorkflowState int

const (
	WorkflowInitializing WorkflowState = iota
	WorkflowActive
	WorkflowPushingAll
	WorkflowCreatingMRs
	WorkflowCleaningUp
	WorkflowDone
)

// String returns a human-readable name for the workflow state
func (s WorkflowState) String() string {
	switch s {
	case WorkflowInitializing:
		return "initializing"
	case WorkflowActive:
		return "active"
	case WorkflowPushingAll:
		return "pushing"
	case WorkflowCreatingMRs:
		return "creating_mrs"
	case WorkflowCleaningUp:
		return "cleaning_up"
	case WorkflowDone:
		return "done"
	default:
		return "unknown"
	}
}

// WorkflowRepo tracks worktree and CI state for a single repo within a workflow
type WorkflowRepo struct {
	RepoName        string `json:"repo_name"`
	OriginalPath    string `json:"original_path"`
	WorktreePath    string `json:"worktree_path"`
	DefaultBranch   string `json:"default_branch"`
	WorktreeCreated bool   `json:"worktree_created"`
	Pushed          bool   `json:"pushed"`
	MRURL           string `json:"mr_url,omitempty"`
	MRTitle         string `json:"mr_title,omitempty"`
	Error           string `json:"error,omitempty"`

	// Runtime branch status (not persisted)
	AheadDefault  int  `json:"-"`
	BehindDefault int  `json:"-"`
	AheadRemote   int  `json:"-"`
	BehindRemote  int  `json:"-"`
	HasRemote     bool `json:"-"`
}

// FeatureWorkflow orchestrates a cross-repo feature branch lifecycle
type FeatureWorkflow struct {
	BranchName string                   `json:"branch_name"`
	BaseDir    string                   `json:"base_dir"`
	Repos      map[string]*WorkflowRepo `json:"repos"`
	State      WorkflowState            `json:"state"`
	CreatedAt  time.Time                `json:"created_at"`
	Error      string                   `json:"error,omitempty"`
	AgentID    string                   `json:"agent_id,omitempty"`
	JiraURL    string                   `json:"jira_url,omitempty"`

	project *Project
	mu      sync.RWMutex
}

// WorkflowStatus is a snapshot summary of the workflow
type WorkflowStatus struct {
	BranchName       string        `json:"branch_name"`
	State            WorkflowState `json:"state"`
	TotalRepos       int           `json:"total_repos"`
	WorktreesCreated int           `json:"worktrees_created"`
	Pushed           int           `json:"pushed"`
	MRsCreated       int           `json:"mrs_created"`
	Errors           int           `json:"errors"`
	Error            string        `json:"error,omitempty"`
	AgentID          string        `json:"agent_id,omitempty"`
	HasAgent         bool          `json:"has_agent"`
}

// countRepoErrors returns the number of repos that have a non-empty error string.
// Callers must hold fw.mu or operate on a stable snapshot.
func countRepoErrors(repos map[string]*WorkflowRepo) int {
	count := 0
	for _, wr := range repos {
		if wr.Error != "" {
			count++
		}
	}
	return count
}

// sanitizeBranchForPath replaces characters that are problematic in filesystem paths
func sanitizeBranchForPath(branch string) string {
	return strings.ReplaceAll(branch, "/", "-")
}

// DefaultWorkflowBaseDir returns the default root under which a project's
// workflow worktrees live: ~/.worktrees/<project>/<branch>/<repo>. Callers that
// let the user pick a base dir should fall back to this when it is empty, so
// the layout stays identical between the TUI and the CLI.
func DefaultWorkflowBaseDir(projectName string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	if projectName == "" {
		return filepath.Join(home, ".worktrees")
	}
	return filepath.Join(home, ".worktrees", projectName)
}

// NewFeatureWorkflow creates a workflow that tracks a feature branch across all repos in a project
func NewFeatureWorkflow(proj *Project, branchName, baseDir string) *FeatureWorkflow {
	proj.mu.RLock()
	defer proj.mu.RUnlock()

	sanitized := sanitizeBranchForPath(branchName)
	repos := make(map[string]*WorkflowRepo, len(proj.Repos))

	for _, r := range proj.Repos {
		worktreePath := filepath.Join(baseDir, sanitized, r.Name)
		repos[r.Name] = &WorkflowRepo{
			RepoName:      r.Name,
			OriginalPath:  r.Path,
			WorktreePath:  worktreePath,
			DefaultBranch: r.DefaultBranch,
		}
	}

	return &FeatureWorkflow{
		BranchName: branchName,
		BaseDir:    baseDir,
		Repos:      repos,
		State:      WorkflowInitializing,
		CreatedAt:  time.Now(),
		project:    proj,
	}
}

// ensureWorktree brings a single repo to the desired state: a worktree of
// OriginalPath checked out on the workflow branch. It is idempotent — an
// existing worktree for the branch is adopted (and its real path recorded)
// instead of failing, so re-running a workflow create is safe.
//
// Returns the adopted/created path, or an error. Callers hold no lock.
func ensureWorktree(repoPath, worktreePath, branch, defaultBranch string) (string, error) {
	// Already have a worktree on this branch? Adopt it wherever it lives.
	if worktrees, err := git.GetWorktrees(repoPath); err == nil {
		for _, wt := range worktrees {
			if wt.Path == repoPath {
				continue // main worktree, never a workflow worktree
			}
			if wt.Branch == branch {
				return wt.Path, nil
			}
		}
	}

	if defaultBranch == "" {
		defaultBranch = "main"
	}

	// The branch exists but is not checked out anywhere: attach a worktree to it
	// rather than trying to create it again.
	if git.BranchExists(repoPath, branch) {
		if err := git.CreateWorktree(repoPath, worktreePath, branch, false, ""); err != nil {
			return "", err
		}
		return worktreePath, nil
	}

	// Fresh branch. Prefer origin/<default> so the worktree starts from the
	// remote tip; fall back to the local default branch when there is no
	// remote-tracking ref (never fetched, or a local-only repo).
	startPoint := "origin/" + defaultBranch
	if !git.RefExists(repoPath, startPoint) {
		if git.RefExists(repoPath, defaultBranch) {
			startPoint = defaultBranch
		} else {
			startPoint = "" // let git use HEAD
		}
	}
	if err := git.CreateWorktree(repoPath, worktreePath, branch, true, startPoint); err != nil {
		return "", err
	}
	return worktreePath, nil
}

// CreateAllWorktrees creates worktrees for all repos in the project concurrently
// — the workflow is per project, so every repo always gets its own worktree on
// the same branch. Each repo's error is tracked independently, and repos that
// already have a worktree on the branch are adopted rather than re-created.
func (fw *FeatureWorkflow) CreateAllWorktrees() error {
	fw.mu.Lock()
	fw.State = WorkflowInitializing
	fw.mu.Unlock()

	var wg sync.WaitGroup
	for _, wr := range fw.Repos {
		wg.Add(1)
		go func(wr *WorkflowRepo) {
			defer wg.Done()

			path, err := ensureWorktree(wr.OriginalPath, wr.WorktreePath, fw.BranchName, wr.DefaultBranch)

			fw.mu.Lock()
			defer fw.mu.Unlock()
			if err != nil {
				wr.Error = fmt.Sprintf("create worktree: %v", err)
			} else {
				wr.WorktreePath = path
				wr.WorktreeCreated = true
				wr.Error = ""
			}
		}(wr)
	}
	wg.Wait()

	fw.mu.Lock()
	defer fw.mu.Unlock()

	// Check if any repo had errors
	errCount := countRepoErrors(fw.Repos)

	if errCount == len(fw.Repos) {
		fw.Error = "all worktree creations failed"
		return fmt.Errorf("all %d worktree creations failed", errCount)
	}

	fw.State = WorkflowActive
	if errCount > 0 {
		return fmt.Errorf("%d of %d worktree creations failed", errCount, len(fw.Repos))
	}
	return nil
}

// RemoveAllWorktrees removes worktrees for all repos concurrently.
// Missing or already-removed worktrees are handled gracefully.
func (fw *FeatureWorkflow) RemoveAllWorktrees() error {
	fw.mu.Lock()
	fw.State = WorkflowCleaningUp
	fw.mu.Unlock()

	var wg sync.WaitGroup
	for _, wr := range fw.Repos {
		if !wr.WorktreeCreated {
			continue
		}
		wg.Add(1)
		go func(wr *WorkflowRepo) {
			defer wg.Done()

			err := git.RemoveWorktree(wr.OriginalPath, wr.WorktreePath, true)

			fw.mu.Lock()
			defer fw.mu.Unlock()
			if err != nil {
				// Not fatal -- worktree may already be gone
				wr.Error = fmt.Sprintf("remove worktree: %v", err)
			} else {
				wr.WorktreeCreated = false
				wr.Error = ""
			}

			// Delete the remote branch before the local one (tracking info needed to find remote)
			if delErr := git.DeleteRemoteBranch(wr.OriginalPath, "origin", fw.BranchName); delErr != nil {
				// Not fatal
				if wr.Error == "" {
					wr.Error = fmt.Sprintf("delete remote branch: %v", delErr)
				}
			}

			// Delete the local branch (force in case it's unmerged)
			if delErr := git.DeleteBranch(wr.OriginalPath, fw.BranchName, true); delErr != nil {
				// Not fatal -- branch may not exist or may be checked out
				if wr.Error == "" {
					wr.Error = fmt.Sprintf("delete branch: %v", delErr)
				}
			}
		}(wr)
	}
	wg.Wait()

	// Remove the workflow directory (baseDir/sanitized-branch/) now that
	// all repo worktrees inside it have been removed.  os.Remove only
	// succeeds if the directory is empty, so this is safe.  If a repo
	// worktree removal failed and left files behind, this will be a no-op.
	workflowDir := filepath.Join(fw.BaseDir, sanitizeBranchForPath(fw.BranchName))
	os.Remove(workflowDir)

	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.State = WorkflowDone
	return nil
}

// HasOpenMRs returns true if any repo in this workflow has an MR URL set,
// indicating there may be an open merge request that hasn't been merged yet.
func (fw *FeatureWorkflow) HasOpenMRs() bool {
	fw.mu.RLock()
	defer fw.mu.RUnlock()
	for _, wr := range fw.Repos {
		if wr.MRURL != "" {
			return true
		}
	}
	return false
}

// HasUnmergedBranches returns true if any repo's branch has commits ahead of
// the default branch. Uses the runtime AheadDefault field populated by RefreshBranchStatuses.
func (fw *FeatureWorkflow) HasUnmergedBranches() bool {
	fw.mu.RLock()
	defer fw.mu.RUnlock()
	for _, wr := range fw.Repos {
		if wr.AheadDefault > 0 {
			return true
		}
	}
	return false
}

// ReposNeedingPush returns the names of repos that have commits to push.
// Uses the same logic as PushAll to determine eligibility.
func (fw *FeatureWorkflow) ReposNeedingPush() []string {
	fw.mu.RLock()
	repos := make([]*WorkflowRepo, 0, len(fw.Repos))
	names := make(map[*WorkflowRepo]string, len(fw.Repos))
	for name, wr := range fw.Repos {
		if wr.WorktreeCreated {
			repos = append(repos, wr)
			names[wr] = name
		}
	}
	fw.mu.RUnlock()

	var mu sync.Mutex
	var result []string
	var wg sync.WaitGroup
	for _, wr := range repos {
		wg.Add(1)
		go func(wr *WorkflowRepo, name string) {
			defer wg.Done()
			status, err := git.GetUpstreamStatus(wr.WorktreePath)
			if err != nil {
				// Include on error so PushAll can surface the error
				mu.Lock()
				result = append(result, name)
				mu.Unlock()
				return
			}
			if status.Upstream != "" {
				if status.Ahead > 0 {
					mu.Lock()
					result = append(result, name)
					mu.Unlock()
				}
			} else {
				base := wr.DefaultBranch
				if base == "" {
					base = "main"
				}
				ahead, _, cmpErr := git.CompareBranchesSimple(wr.WorktreePath, base, "HEAD")
				if cmpErr == nil && ahead > 0 {
					mu.Lock()
					result = append(result, name)
					mu.Unlock()
				}
			}
		}(wr, names[wr])
	}
	wg.Wait()
	return result
}

// PushAll pushes all repos that have worktrees concurrently.
// Repos without an upstream get one set automatically via SetUpstreamAndPush.
// When force is true, --force-with-lease is used for repos that already have an upstream.
func (fw *FeatureWorkflow) PushAll(force bool) error {
	fw.mu.Lock()
	fw.State = WorkflowPushingAll
	fw.mu.Unlock()

	var wg sync.WaitGroup
	for _, wr := range fw.Repos {
		if !wr.WorktreeCreated {
			continue
		}
		wg.Add(1)
		go func(wr *WorkflowRepo) {
			defer wg.Done()

			// Check upstream status to decide push strategy
			status, err := git.GetUpstreamStatus(wr.WorktreePath)
			if err != nil {
				fw.mu.Lock()
				wr.Error = fmt.Sprintf("push: upstream check: %v", err)
				fw.mu.Unlock()
				return
			}

			base := wr.DefaultBranch
			if base == "" {
				base = "main"
			}

			// Treat upstream as absent if it points to the default branch — this
			// happens when a worktree was created from origin/<default> and git
			// auto-set the tracking branch.  In that case we must push the feature
			// branch to its own remote ref, not to the default branch.
			upstreamIsDefault := status.Upstream == "origin/"+base
			if status.Upstream != "" && !upstreamIsDefault {
				// Has a proper feature-branch upstream: only push if ahead (or force)
				if status.Ahead == 0 && !force {
					return // nothing to push
				}
				_, err = git.Push(wr.WorktreePath, force)
			} else {
				// No upstream (or upstream is just the default branch): check
				// commits vs default and push to a new remote feature branch.
				ahead, _, cmpErr := git.CompareBranchesSimple(wr.WorktreePath, base, "HEAD")
				if cmpErr != nil || ahead == 0 {
					return // nothing to push
				}
				_, err = git.SetUpstreamAndPush(wr.WorktreePath, "origin")
			}

			fw.mu.Lock()
			defer fw.mu.Unlock()
			if err != nil {
				wr.Error = fmt.Sprintf("push: %v", err)
			} else {
				wr.Pushed = true
			}
		}(wr)
	}
	wg.Wait()

	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.State = WorkflowActive

	errCount := countRepoErrors(fw.Repos)
	if errCount > 0 {
		return fmt.Errorf("%d of %d pushes failed", errCount, len(fw.Repos))
	}
	return nil
}

// CreateAllMRs creates merge requests sequentially (to respect API rate limits).
// It detects the remote provider per repo and calls the appropriate CLI.
func (fw *FeatureWorkflow) CreateAllMRs() error {
	fw.mu.Lock()
	fw.State = WorkflowCreatingMRs
	fw.mu.Unlock()

	for _, wr := range fw.Repos {
		if !wr.Pushed {
			continue
		}

		provider := git.DetectRemoteProvider(wr.WorktreePath)
		if provider == git.ProviderUnknown {
			// Fall back to detecting from the original repo path
			provider = git.DetectRemoteProvider(wr.OriginalPath)
		}

		base := wr.DefaultBranch
		if base == "" {
			base = "main"
		}
		result, err := git.CreateMergeRequestCLI(wr.WorktreePath, provider, base)

		fw.mu.Lock()
		if err != nil {
			wr.Error = fmt.Sprintf("create MR: %v", err)
		} else {
			wr.MRURL = result.URL
			if result.Content != nil {
				wr.MRTitle = result.Content.Title
			}
		}
		fw.mu.Unlock()
	}

	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.State = WorkflowActive

	errCount := countRepoErrors(fw.Repos)
	if errCount > 0 {
		return fmt.Errorf("%d MR creations failed", errCount)
	}
	return nil
}

// Status returns a snapshot summary of the workflow
func (fw *FeatureWorkflow) Status() *WorkflowStatus {
	fw.mu.RLock()
	defer fw.mu.RUnlock()

	s := &WorkflowStatus{
		BranchName: fw.BranchName,
		State:      fw.State,
		TotalRepos: len(fw.Repos),
		Error:      fw.Error,
		AgentID:    fw.AgentID,
		HasAgent:   fw.AgentID != "",
	}

	for _, wr := range fw.Repos {
		if wr.WorktreeCreated {
			s.WorktreesCreated++
		}
		if wr.Pushed {
			s.Pushed++
		}
		if wr.MRURL != "" {
			s.MRsCreated++
		}
		if wr.Error != "" {
			s.Errors++
		}
	}

	return s
}

// RefreshBranchStatuses updates the runtime ahead/behind counts for all repos concurrently.
// It compares the feature branch against the default branch and the remote tracking branch.
func (fw *FeatureWorkflow) RefreshBranchStatuses() {
	fw.mu.RLock()
	branchName := fw.BranchName
	repos := make([]*WorkflowRepo, 0, len(fw.Repos))
	for _, wr := range fw.Repos {
		repos = append(repos, wr)
	}
	fw.mu.RUnlock()

	var wg sync.WaitGroup
	for _, wr := range repos {
		wg.Add(1)
		go func(wr *WorkflowRepo) {
			defer wg.Done()

			repoPath := wr.OriginalPath

			defaultBranch := wr.DefaultBranch
			if defaultBranch == "" {
				defaultBranch = "main"
			}

			// Ahead/behind vs default branch
			aheadDef, behindDef, err := git.CompareBranchesSimple(repoPath, defaultBranch, branchName)

			// Ahead/behind vs remote tracking branch (origin/<branch>)
			remoteBranch := "origin/" + branchName
			aheadRem, behindRem, remErr := git.CompareBranchesSimple(repoPath, remoteBranch, branchName)

			fw.mu.Lock()
			if err == nil {
				wr.AheadDefault = aheadDef
				wr.BehindDefault = behindDef
			}
			if remErr == nil {
				wr.AheadRemote = aheadRem
				wr.BehindRemote = behindRem
				wr.HasRemote = true
			} else {
				wr.HasRemote = false
			}
			fw.mu.Unlock()
		}(wr)
	}
	wg.Wait()
}

// GetRepo returns the WorkflowRepo for a given repo name, or nil
func (fw *FeatureWorkflow) GetRepo(name string) *WorkflowRepo {
	fw.mu.RLock()
	defer fw.mu.RUnlock()
	return fw.Repos[name]
}

// SetWorkflowAgentID records which agent is working on this workflow
func (fw *FeatureWorkflow) SetWorkflowAgentID(agentID string) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.AgentID = agentID
}

// GetWorkflowAgentID returns the agent ID for this workflow
func (fw *FeatureWorkflow) GetWorkflowAgentID() string {
	fw.mu.RLock()
	defer fw.mu.RUnlock()
	return fw.AgentID
}

// WorkflowDir returns the full path to this workflow's worktree directory
// (baseDir/sanitized-branch/), where all repo worktrees are subdirectories.
func (fw *FeatureWorkflow) WorkflowDir() string {
	fw.mu.RLock()
	defer fw.mu.RUnlock()
	return filepath.Join(fw.BaseDir, sanitizeBranchForPath(fw.BranchName))
}

// SetProject re-associates the workflow with a project (needed after loading from disk).
func (fw *FeatureWorkflow) SetProject(proj *Project) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.project = proj
}

// workflowStatePath returns the path to the workflow state file for a project.
func workflowStatePath(projectKey string) string {
	dir := filepath.Dir(GetDefaultConfigPath())
	return filepath.Join(dir, fmt.Sprintf("workflows-%s.json", projectKey))
}

// SaveWorkflows persists active workflows to disk.
func SaveWorkflows(projectKey string, workflows []*FeatureWorkflow) error {
	// Only save active workflows (not done/cleaning up)
	var toSave []*FeatureWorkflow
	for _, wf := range workflows {
		wf.mu.RLock()
		state := wf.State
		wf.mu.RUnlock()
		if state != WorkflowDone {
			toSave = append(toSave, wf)
		}
	}

	path := workflowStatePath(projectKey)

	if len(toSave) == 0 {
		// Remove the state file if no active workflows
		os.Remove(path)
		return nil
	}

	data, err := json.MarshalIndent(toSave, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal workflows: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

// DiscoverWorkflows scans existing worktrees across all project repos and groups
// them by branch name into FeatureWorkflow objects. This allows importing workflows
// that were created outside the app (e.g., manually via git worktree add).
// The skip map contains branch names of workflows already tracked, to avoid duplicates.
func DiscoverWorkflows(proj *Project, skip map[string]bool) ([]*FeatureWorkflow, error) {
	proj.mu.RLock()
	defer proj.mu.RUnlock()

	// branch -> repo name -> worktree info
	type repoWorktree struct {
		repoName      string
		originalPath  string
		worktreePath  string
		defaultBranch string
	}
	byBranch := make(map[string][]repoWorktree)

	for _, r := range proj.Repos {
		worktrees, err := git.GetWorktrees(r.Path)
		if err != nil {
			continue // skip repos that fail
		}
		for _, wt := range worktrees {
			// Skip the main worktree (path == repo path) and bare/detached
			if wt.Path == r.Path || wt.Branch == "" {
				continue
			}
			// Skip default branches
			if wt.Branch == r.DefaultBranch {
				continue
			}
			// Skip already tracked workflows
			if skip[wt.Branch] {
				continue
			}
			byBranch[wt.Branch] = append(byBranch[wt.Branch], repoWorktree{
				repoName:      r.Name,
				originalPath:  r.Path,
				worktreePath:  wt.Path,
				defaultBranch: r.DefaultBranch,
			})
		}
	}

	var workflows []*FeatureWorkflow
	for branch, rws := range byBranch {
		repos := make(map[string]*WorkflowRepo, len(rws))
		// Infer base dir from the first worktree path
		var baseDir string
		for _, rw := range rws {
			repos[rw.repoName] = &WorkflowRepo{
				RepoName:        rw.repoName,
				OriginalPath:    rw.originalPath,
				WorktreePath:    rw.worktreePath,
				DefaultBranch:   rw.defaultBranch,
				WorktreeCreated: true,
			}
			if baseDir == "" {
				// worktreePath is typically baseDir/sanitized-branch/repoName
				// go up two levels to get baseDir
				baseDir = filepath.Dir(filepath.Dir(rw.worktreePath))
			}
		}

		wf := &FeatureWorkflow{
			BranchName: branch,
			BaseDir:    baseDir,
			Repos:      repos,
			State:      WorkflowActive,
			CreatedAt:  time.Now(),
			project:    proj,
		}
		workflows = append(workflows, wf)
	}

	return workflows, nil
}

// LoadWorkflows loads persisted workflows from disk and re-associates them with the project.
func LoadWorkflows(projectKey string, proj *Project) ([]*FeatureWorkflow, error) {
	path := workflowStatePath(projectKey)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read workflows: %w", err)
	}

	var workflows []*FeatureWorkflow
	if err := json.Unmarshal(data, &workflows); err != nil {
		return nil, fmt.Errorf("unmarshal workflows: %w", err)
	}

	for _, wf := range workflows {
		wf.SetProject(proj)
	}

	return workflows, nil
}

// RepoMergeStatus describes whether a single repo of the workflow can have its
// feature branch merged into its local default branch, and why not if it can't.
type RepoMergeStatus struct {
	RepoName      string `json:"repo_name"`
	DefaultBranch string `json:"default_branch"`
	Ahead         int    `json:"ahead"`
	Eligible      bool   `json:"eligible"`
	Reason        string `json:"reason,omitempty"`
}

// RepoMergeResult is the outcome of merging one repo's feature branch into its
// local default branch.
type RepoMergeResult struct {
	RepoName    string   `json:"repo_name"`
	Merged      bool     `json:"merged"`
	FastForward bool     `json:"fast_forward"`
	Skipped     bool     `json:"skipped"`
	Reason      string   `json:"reason,omitempty"`
	Conflicts   []string `json:"conflicts,omitempty"`
}

// repoSnapshot returns a stable, name-sorted copy of the repo pointers so the
// merge helpers can work without holding fw.mu across git calls.
func (fw *FeatureWorkflow) repoSnapshot() []*WorkflowRepo {
	fw.mu.RLock()
	defer fw.mu.RUnlock()
	names := make([]string, 0, len(fw.Repos))
	for name := range fw.Repos {
		names = append(names, name)
	}
	sort.Strings(names)
	repos := make([]*WorkflowRepo, 0, len(names))
	for _, name := range names {
		repos = append(repos, fw.Repos[name])
	}
	return repos
}

// mergeStatusFor computes merge eligibility for one repo. The merge happens in
// the repo's original checkout (OriginalPath), which must already be on the
// default branch and clean — we never move someone's HEAD or stash for them.
func (fw *FeatureWorkflow) mergeStatusFor(wr *WorkflowRepo, branch string) RepoMergeStatus {
	base := wr.DefaultBranch
	if base == "" {
		base = "main"
	}
	st := RepoMergeStatus{RepoName: wr.RepoName, DefaultBranch: base}

	if !wr.WorktreeCreated {
		st.Reason = "no worktree"
		return st
	}
	if !git.BranchExists(wr.OriginalPath, branch) {
		st.Reason = fmt.Sprintf("branch %s not found", branch)
		return st
	}
	current, err := git.CurrentBranch(wr.OriginalPath)
	if err != nil {
		st.Reason = "detached HEAD in main checkout"
		return st
	}
	if current != base {
		st.Reason = fmt.Sprintf("main checkout is on %s, not %s", current, base)
		return st
	}
	if dirty, err := git.IsDirty(wr.OriginalPath); err != nil {
		st.Reason = fmt.Sprintf("status check: %v", err)
		return st
	} else if dirty {
		st.Reason = "main checkout has uncommitted changes"
		return st
	}
	ahead, _, err := git.CompareBranchesSimple(wr.OriginalPath, base, branch)
	if err != nil {
		st.Reason = fmt.Sprintf("compare: %v", err)
		return st
	}
	st.Ahead = ahead
	if ahead == 0 {
		st.Reason = "already merged"
		return st
	}
	st.Eligible = true
	return st
}

// MergeStatuses returns per-repo merge eligibility for the workflow branch,
// sorted by repo name. Used to build the confirmation prompt before merging.
func (fw *FeatureWorkflow) MergeStatuses() []RepoMergeStatus {
	branch := fw.BranchName
	repos := fw.repoSnapshot()
	statuses := make([]RepoMergeStatus, 0, len(repos))
	for _, wr := range repos {
		statuses = append(statuses, fw.mergeStatusFor(wr, branch))
	}
	return statuses
}

// MergeAllToDefault merges the workflow branch into each repo's local default
// branch, in the repo's original checkout. Repos that aren't eligible (see
// mergeStatusFor) are skipped with a reason instead of being forced.
//
// Runs sequentially so a conflict in one repo surfaces before the next repo is
// touched, and stops at the first conflict — leaving the conflicted repo in its
// merging state for the user to resolve (or `git merge --abort`).
func (fw *FeatureWorkflow) MergeAllToDefault(noFastForward bool) []RepoMergeResult {
	branch := fw.BranchName
	repos := fw.repoSnapshot()

	results := make([]RepoMergeResult, 0, len(repos))
	conflicted := false
	for _, wr := range repos {
		if conflicted {
			results = append(results, RepoMergeResult{
				RepoName: wr.RepoName, Skipped: true,
				Reason: "not attempted (earlier repo conflicted)",
			})
			continue
		}

		st := fw.mergeStatusFor(wr, branch)
		if !st.Eligible {
			results = append(results, RepoMergeResult{
				RepoName: wr.RepoName, Skipped: true, Reason: st.Reason,
			})
			continue
		}

		res, err := git.Merge(wr.OriginalPath, branch, git.MergeOptions{
			NoFastForward: noFastForward,
		})
		if err != nil {
			r := RepoMergeResult{RepoName: wr.RepoName, Reason: err.Error()}
			if res != nil && len(res.Conflicts) > 0 {
				r.Conflicts = res.Conflicts
				r.Reason = "merge conflict"
				conflicted = true
			}
			fw.mu.Lock()
			wr.Error = fmt.Sprintf("merge: %s", r.Reason)
			fw.mu.Unlock()
			results = append(results, r)
			continue
		}

		fw.mu.Lock()
		wr.Error = ""
		fw.mu.Unlock()
		results = append(results, RepoMergeResult{
			RepoName:    wr.RepoName,
			Merged:      true,
			FastForward: res != nil && res.FastForward,
		})
	}
	return results
}
