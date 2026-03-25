package project

import (
	"strings"
	"sync"

	"gitlab.com/tanevanwifferen1/singularity/internal/git"
)

// RepoStashResult is the result of a stash push or apply operation on one repo.
type RepoStashResult struct {
	RepoName   string
	Skipped    bool   // true if repo had nothing to stash or no matching stash to apply
	Error      string // non-empty on failure
	StashIndex int    // index of the created/applied stash (-1 if skipped or error)
}

// RepoStashList holds the stash entries for one repo.
type RepoStashList struct {
	RepoName string
	Path     string
	Entries  []git.StashEntry
	Error    string // non-empty if listing failed
}

// StashAllRepos creates a stash in every repo that has local changes.
// Repos with no local changes are marked Skipped=true and not treated as errors.
func StashAllRepos(proj *Project, message string, includeUntracked bool) []RepoStashResult {
	proj.mu.RLock()
	repos := make([]*Repo, len(proj.Repos))
	copy(repos, proj.Repos)
	proj.mu.RUnlock()

	results := make([]RepoStashResult, len(repos))
	var wg sync.WaitGroup

	for i, r := range repos {
		wg.Add(1)
		go func(idx int, repo *Repo) {
			defer wg.Done()
			res := RepoStashResult{RepoName: repo.Name, StashIndex: -1}

			info, err := git.OpenRepo(repo.Path)
			if err != nil {
				res.Error = err.Error()
				results[idx] = res
				return
			}
			if !info.IsDirty && !includeUntracked {
				res.Skipped = true
				results[idx] = res
				return
			}

			idx2, err := git.CreateStash(repo.Path, message, includeUntracked)
			if err != nil {
				msg := err.Error()
				if strings.Contains(msg, "No local changes") || strings.Contains(msg, "nothing to stash") {
					res.Skipped = true
				} else {
					res.Error = msg
				}
			} else {
				res.StashIndex = idx2
			}
			results[idx] = res
		}(i, r)
	}

	wg.Wait()
	return results
}

// ListAllStashes returns all stash entries for every repo in the project.
func ListAllStashes(proj *Project) []RepoStashList {
	proj.mu.RLock()
	repos := make([]*Repo, len(proj.Repos))
	copy(repos, proj.Repos)
	proj.mu.RUnlock()

	results := make([]RepoStashList, len(repos))
	var wg sync.WaitGroup

	for i, r := range repos {
		wg.Add(1)
		go func(idx int, repo *Repo) {
			defer wg.Done()
			res := RepoStashList{RepoName: repo.Name, Path: repo.Path}

			entries, err := git.GetStashList(repo.Path)
			if err != nil {
				res.Error = err.Error()
			} else {
				res.Entries = entries
			}
			results[idx] = res
		}(i, r)
	}

	wg.Wait()
	return results
}

// ApplyStashAllRepos finds and applies the most-recent stash whose message
// contains the given message string (case-insensitive) in each repo.
// Repos with no matching stash are marked Skipped=true.
func ApplyStashAllRepos(proj *Project, message string, dropAfter bool) []RepoStashResult {
	proj.mu.RLock()
	repos := make([]*Repo, len(proj.Repos))
	copy(repos, proj.Repos)
	proj.mu.RUnlock()

	results := make([]RepoStashResult, len(repos))
	lower := strings.ToLower(message)
	var wg sync.WaitGroup

	for i, r := range repos {
		wg.Add(1)
		go func(idx int, repo *Repo) {
			defer wg.Done()
			res := RepoStashResult{RepoName: repo.Name, StashIndex: -1}

			entries, err := git.GetStashList(repo.Path)
			if err != nil {
				res.Error = err.Error()
				results[idx] = res
				return
			}

			// Find most-recent matching stash (entries are ordered newest-first)
			found := -1
			for _, e := range entries {
				if strings.Contains(strings.ToLower(e.Message), lower) {
					found = e.Index
					break
				}
			}
			if found == -1 {
				res.Skipped = true
				results[idx] = res
				return
			}

			if err := git.ApplyStash(repo.Path, found, dropAfter); err != nil {
				res.Error = err.Error()
			} else {
				res.StashIndex = found
			}
			results[idx] = res
		}(i, r)
	}

	wg.Wait()
	return results
}
