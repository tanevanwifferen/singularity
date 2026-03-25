package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// CreateAllWorktrees creates worktrees for all repos concurrently.
// Each repo gets its own error tracked independently.
func (fw *FeatureWorkflow) CreateAllWorktrees() error {
	fw.mu.Lock()
	fw.State = WorkflowInitializing
	fw.mu.Unlock()

	var wg sync.WaitGroup
	for _, wr := range fw.Repos {
		wg.Add(1)
		go func(wr *WorkflowRepo) {
			defer wg.Done()

			err := git.CreateWorktree(wr.OriginalPath, wr.WorktreePath, fw.BranchName, true)

			fw.mu.Lock()
			defer fw.mu.Unlock()
			if err != nil {
				wr.Error = fmt.Sprintf("create worktree: %v", err)
			} else {
				wr.WorktreeCreated = true
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
func (fw *FeatureWorkflow) PushAll() error {
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

			if status.Upstream != "" {
				// Has upstream: only push if there are commits ahead
				if status.Ahead == 0 {
					return // nothing to push
				}
				_, err = git.Push(wr.WorktreePath, false)
			} else {
				// No upstream: check if branch has commits vs default branch
				base := wr.DefaultBranch
				if base == "" {
					base = "main"
				}
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
