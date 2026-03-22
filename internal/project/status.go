package project

import (
	"fmt"
	"strings"
	"sync"

	"git-frontend/internal/git"
)

// CrossRepoComparison holds branch comparison data across repos
type CrossRepoComparison struct {
	Branch      string                    `json:"branch"`
	BaseBranch  string                    `json:"base_branch"`
	Repos       map[string]*RepoCompare   `json:"repos"`
}

// RepoCompare holds comparison data for a single repo
type RepoCompare struct {
	RepoName string `json:"repo_name"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	Diverged bool   `json:"diverged"`
	HasBranch bool  `json:"has_branch"`
	Error    string `json:"error,omitempty"`
}

// CompareBranchAcrossRepos compares a branch against the default branch in all repos
func CompareBranchAcrossRepos(proj *Project, branch string) *CrossRepoComparison {
	proj.mu.RLock()
	repos := make([]*Repo, len(proj.Repos))
	copy(repos, proj.Repos)
	proj.mu.RUnlock()

	result := &CrossRepoComparison{
		Branch: branch,
		Repos:  make(map[string]*RepoCompare),
	}

	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, r := range repos {
		wg.Add(1)
		go func(repo *Repo) {
			defer wg.Done()

			rc := &RepoCompare{RepoName: repo.Name}

			// Check if branch exists
			if repo.Info == nil {
				rc.Error = "repo not loaded"
				mu.Lock()
				result.Repos[repo.Name] = rc
				mu.Unlock()
				return
			}

			found := false
			for _, b := range repo.Info.Branches {
				if b.Name == branch {
					found = true
					break
				}
			}
			rc.HasBranch = found

			if !found {
				mu.Lock()
				result.Repos[repo.Name] = rc
				mu.Unlock()
				return
			}

			// Compare against default branch
			comparison, err := git.CompareBranches(repo.Path, branch, repo.DefaultBranch)
			if err != nil {
				rc.Error = err.Error()
			} else {
				rc.Ahead = comparison.Ahead
				rc.Behind = comparison.Behind
				rc.Diverged = comparison.Diverged
			}

			mu.Lock()
			result.Repos[repo.Name] = rc
			mu.Unlock()
		}(r)
	}

	wg.Wait()
	return result
}

// AggregateStatus collects status from all repos in a project concurrently
func AggregateStatus(proj *Project) *ProjectStatus {
	return proj.Status()
}

// FormatProjectStatus returns a human-readable status string for the project
func FormatProjectStatus(status *ProjectStatus) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Project: %s (%d repos)\n", status.Name, status.RepoCount))
	if status.DirtyCount > 0 {
		sb.WriteString(fmt.Sprintf("  Dirty repos: %d\n", status.DirtyCount))
	}
	if status.ErrorCount > 0 {
		sb.WriteString(fmt.Sprintf("  Error repos: %d\n", status.ErrorCount))
	}

	sb.WriteString("\n")

	for _, rs := range status.Repos {
		dirty := ""
		if rs.IsDirty {
			dirty = " [dirty]"
		}

		head := ""
		if len(rs.HEAD) >= 7 {
			head = rs.HEAD[:7]
		}

		if rs.Error != "" {
			sb.WriteString(fmt.Sprintf("  %s: ERROR - %s\n", rs.Name, rs.Error))
		} else {
			sb.WriteString(fmt.Sprintf("  %s: %s (%s)%s [%d branches]\n",
				rs.Name, rs.CurrentBranch, head, dirty, rs.BranchCount))
		}
	}

	return sb.String()
}

// FormatBranchExistence returns a human-readable string for branch existence
func FormatBranchExistence(be *BranchExistence) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Branch %q exists in:\n", be.Branch))
	for name, exists := range be.Repos {
		if exists {
			sb.WriteString(fmt.Sprintf("  %s: yes\n", name))
		} else {
			sb.WriteString(fmt.Sprintf("  %s: no\n", name))
		}
	}

	return sb.String()
}
