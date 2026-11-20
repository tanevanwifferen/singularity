package project

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/git"
)

// BranchExistsError is returned when a workflow's branch already exists in
// one or more of the project's repos. A workflow force-deletes its branch
// (locally and on origin) when it is removed, so it must only ever own a
// branch it created itself; an existing branch is therefore refused rather
// than adopted. Service layers map it to CONFLICT.
type BranchExistsError struct {
	Branch string
	// Repos lists "<repo> (<where>)" for every repo that has the branch,
	// sorted by repo name.
	Repos []string
}

func (e *BranchExistsError) Error() string {
	return fmt.Sprintf("branch %q already exists in %d repo(s): %s; choose a new branch name",
		e.Branch, len(e.Repos), strings.Join(e.Repos, ", "))
}

// branchConflict reports where branch already exists in the repo — locally,
// on origin, or as the repo's default branch — or "" when it is free.
func branchConflict(repoPath, defaultBranch, branch string) string {
	var where []string
	if git.BranchExists(repoPath, branch) {
		where = append(where, "local")
	}
	if git.RefExists(repoPath, "refs/remotes/origin/"+branch) {
		where = append(where, "origin")
	}
	if len(where) == 0 && branch == defaultBranchOrMain(defaultBranch) {
		where = append(where, "default branch")
	}
	return strings.Join(where, "+")
}

// CheckBranchAvailable verifies fw's branch exists in none of its repos,
// locally or on origin. It changes nothing and returns a *BranchExistsError
// naming every offending repo. Callers run it before persisting or listing a
// new workflow so a refused create leaves no trace.
func (fw *FeatureWorkflow) CheckBranchAvailable() error {
	repos := fw.repoSnapshot()
	var taken []string
	for _, wr := range repos {
		if where := branchConflict(wr.OriginalPath, wr.DefaultBranch, fw.BranchName); where != "" {
			taken = append(taken, fmt.Sprintf("%s (%s)", wr.RepoName, where))
		}
	}
	if len(taken) == 0 {
		return nil
	}
	sort.Strings(taken)
	return &BranchExistsError{Branch: fw.BranchName, Repos: taken}
}

// reattachWorktree restores the worktree of a repo this workflow already
// tracks (its create failed or the worktree was lost) on the branch the
// workflow created there earlier: an existing worktree on the branch is
// returned as is, a free local branch gets a worktree attached. Only
// SyncRepos uses it, and never for a repo new to the workflow — there an
// existing branch cannot be ours, so ensureWorktree refuses it. The default
// branch is never reattached, and a branch that only exists on origin is not
// ours either; both fall through to ensureWorktree's refusal.
func reattachWorktree(repoPath, worktreePath, branch, defaultBranch string) (worktreeResult, error) {
	if branch == defaultBranchOrMain(defaultBranch) || !git.BranchExists(repoPath, branch) {
		return ensureWorktree(repoPath, worktreePath, branch, defaultBranch)
	}
	if worktrees, err := git.GetWorktrees(repoPath); err == nil {
		for _, wt := range worktrees {
			if wt.Path != repoPath && wt.Branch == branch {
				return worktreeResult{Path: wt.Path}, nil
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return worktreeResult{}, fmt.Errorf("create worktree parent: %w", err)
	}
	if err := git.CreateWorktree(repoPath, worktreePath, branch, false, ""); err != nil {
		return worktreeResult{}, err
	}
	return worktreeResult{Path: worktreePath}, nil
}
