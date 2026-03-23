package project

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"git-frontend/internal/git"
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
	RepoName       string `json:"repo_name"`
	OriginalPath   string `json:"original_path"`
	WorktreePath   string `json:"worktree_path"`
	WorktreeCreated bool  `json:"worktree_created"`
	AgentID        string `json:"agent_id,omitempty"`
	Pushed         bool   `json:"pushed"`
	MRURL          string `json:"mr_url,omitempty"`
	Error          string `json:"error,omitempty"`
}

// FeatureWorkflow orchestrates a cross-repo feature branch lifecycle
type FeatureWorkflow struct {
	BranchName string                  `json:"branch_name"`
	BaseDir    string                  `json:"base_dir"`
	Repos      map[string]*WorkflowRepo `json:"repos"`
	State      WorkflowState           `json:"state"`
	CreatedAt  time.Time               `json:"created_at"`
	Error      string                  `json:"error,omitempty"`

	project *Project
	mu      sync.RWMutex
}

// WorkflowStatus is a snapshot summary of the workflow
type WorkflowStatus struct {
	BranchName      string        `json:"branch_name"`
	State           WorkflowState `json:"state"`
	TotalRepos      int           `json:"total_repos"`
	WorktreesCreated int          `json:"worktrees_created"`
	Pushed          int           `json:"pushed"`
	MRsCreated      int           `json:"mrs_created"`
	Errors          int           `json:"errors"`
	Error           string        `json:"error,omitempty"`
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
			RepoName:     r.Name,
			OriginalPath: r.Path,
			WorktreePath: worktreePath,
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
	var errCount int
	for _, wr := range fw.Repos {
		if wr.Error != "" {
			errCount++
		}
	}

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
		}(wr)
	}
	wg.Wait()

	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.State = WorkflowDone
	return nil
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

			if status.Upstream == "" {
				// No upstream yet -- set one
				_, err = git.SetUpstreamAndPush(wr.WorktreePath, "origin")
			} else {
				_, err = git.Push(wr.WorktreePath, false)
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

	var errCount int
	for _, wr := range fw.Repos {
		if wr.Error != "" {
			errCount++
		}
	}
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

		url, err := git.CreateMergeRequestCLI(wr.WorktreePath, provider)

		fw.mu.Lock()
		if err != nil {
			wr.Error = fmt.Sprintf("create MR: %v", err)
		} else {
			wr.MRURL = url
		}
		fw.mu.Unlock()
	}

	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.State = WorkflowActive

	var errCount int
	for _, wr := range fw.Repos {
		if wr.Error != "" {
			errCount++
		}
	}
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

// GetRepo returns the WorkflowRepo for a given repo name, or nil
func (fw *FeatureWorkflow) GetRepo(name string) *WorkflowRepo {
	fw.mu.RLock()
	defer fw.mu.RUnlock()
	return fw.Repos[name]
}

// SetAgentID records which agent is working on a given repo
func (fw *FeatureWorkflow) SetAgentID(repoName, agentID string) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if wr, ok := fw.Repos[repoName]; ok {
		wr.AgentID = agentID
	}
}
