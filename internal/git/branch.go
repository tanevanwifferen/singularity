package git

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// BranchComparison holds the result of comparing two branches
type BranchComparison struct {
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	Diverged bool   `json:"diverged"`
	BranchA  string `json:"branch_a"`
	BranchB  string `json:"branch_b"`
}

// CompareBranches compares two branches and returns ahead/behind counts
func CompareBranches(repoPath, branchA, branchB string) (*BranchComparison, error) {
	// Get commits ahead (in branchB but not in branchA)
	aheadCmd := exec.Command("git", "-C", repoPath, "rev-list", "--count", fmt.Sprintf("%s..%s", branchA, branchB))
	aheadOutput, err := aheadCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get ahead count: %w", err)
	}
	ahead, err := strconv.Atoi(strings.TrimSpace(string(aheadOutput)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse ahead count: %w", err)
	}

	// Get commits behind (in branchA but not in branchB)
	behindCmd := exec.Command("git", "-C", repoPath, "rev-list", "--count", fmt.Sprintf("%s..%s", branchB, branchA))
	behindOutput, err := behindCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get behind count: %w", err)
	}
	behind, err := strconv.Atoi(strings.TrimSpace(string(behindOutput)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse behind count: %w", err)
	}

	return &BranchComparison{
		Ahead:    ahead,
		Behind:   behind,
		Diverged: ahead > 0 && behind > 0,
		BranchA:  branchA,
		BranchB:  branchB,
	}, nil
}

// CompareBranchesSimple is a simpler version that returns just the counts
func CompareBranchesSimple(repoPath, branchA, branchB string) (ahead, behind int, err error) {
	result, err := CompareBranches(repoPath, branchA, branchB)
	if err != nil {
		return 0, 0, err
	}
	return result.Ahead, result.Behind, nil
}

// Checkout checks out an existing branch in the given repo
func Checkout(repoPath, branch string) error {
	cmd := exec.Command("git", "-C", repoPath, "checkout", branch)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("checkout failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

// DeleteBranch deletes a local branch. Use force to delete unmerged branches.
func DeleteBranch(repoPath, branch string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	cmd := exec.Command("git", "-C", repoPath, "branch", flag, branch)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("delete branch failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

// DeleteRemoteBranch deletes a branch from the remote (git push <remote> --delete <branch>).
// Returns nil if the remote branch does not exist.
func DeleteRemoteBranch(repoPath, remote, branch string) error {
	cmd := exec.Command("git", "-C", repoPath, "push", remote, "--delete", branch)
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		// Not an error if the branch simply doesn't exist on the remote
		if strings.Contains(msg, "remote ref does not exist") || strings.Contains(msg, "error: unable to delete") {
			return nil
		}
		// Not an error if the repo has no such remote at all — nothing to delete
		if strings.Contains(msg, "does not appear to be a git repository") || strings.Contains(msg, "No such remote") {
			return nil
		}
		// Not an error if the remote is archived/read-only (e.g. GitLab archived project)
		if strings.Contains(msg, "archived") || strings.Contains(msg, "returned error: 403") {
			return nil
		}
		return fmt.Errorf("delete remote branch failed: %s", msg)
	}
	return nil
}

// CheckoutDetached checks out the current HEAD in detached state
func CheckoutDetached(repoPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "checkout", "--detach", "HEAD")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("checkout detached failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

// GetHEAD returns the full SHA of HEAD in repoPath.
func GetHEAD(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("rev-parse HEAD failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// CheckoutDetachedAt checks out a specific commit as detached HEAD in repoPath.
func CheckoutDetachedAt(repoPath, commit string) error {
	cmd := exec.Command("git", "-C", repoPath, "checkout", "--detach", commit)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("checkout detached failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

// RefExists reports whether ref resolves in repoPath. Used to pick a usable
// start point for a new branch (e.g. "origin/main" may be absent in a repo
// that was never fetched).
func RefExists(repoPath, ref string) bool {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return cmd.Run() == nil
}

// BranchExists reports whether a local branch named branch exists in repoPath.
func BranchExists(repoPath, branch string) bool {
	return RefExists(repoPath, "refs/heads/"+branch)
}

// CreateBranch creates and checks out a new branch from the default branch
func CreateBranch(repoPath, branch, fromBranch string) error {
	cmd := exec.Command("git", "-C", repoPath, "checkout", "-b", branch, fromBranch)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("create branch failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

// MergeOptions holds options for git merge operations.
type MergeOptions struct {
	// FastForwardOnly restricts merge to fast-forward only (--ff-only)
	FastForwardOnly bool
	// NoFastForward always creates a merge commit (--no-ff)
	NoFastForward bool
	// Squash combines all commits into a single commit (--squash)
	Squash bool
	// Message is an optional custom merge commit message
	Message string
}

// MergeResult holds the result of a merge operation.
type MergeResult struct {
	// Success indicates if the merge completed without conflicts
	Success bool
	// FastForward indicates if the merge was a fast-forward
	FastForward bool
	// Conflicts lists files with merge conflicts
	Conflicts []string
	// Message is the merge commit message (if created)
	Message string
}

// Merge merges the given branch into the current HEAD in repoPath.
// Returns MergeResult with success status and conflict details.
func Merge(repoPath, branch string, opts MergeOptions) (*MergeResult, error) {
	args := []string{"-C", repoPath, "merge"}

	if opts.FastForwardOnly {
		args = append(args, "--ff-only")
	} else if opts.NoFastForward {
		args = append(args, "--no-ff")
	}

	if opts.Squash {
		args = append(args, "--squash")
	}

	if opts.Message != "" {
		args = append(args, "-m", opts.Message)
	}

	args = append(args, branch)

	cmd := exec.Command("git", args...)
	output, err := cmd.CombinedOutput()
	outputStr := strings.TrimSpace(string(output))

	result := &MergeResult{
		Success: err == nil,
	}

	if err != nil {
		// Check if it's a conflict error
		if strings.Contains(outputStr, "CONFLICT") || strings.Contains(outputStr, "conflict") {
			// Extract conflicting files
			conflicts, conflictErr := getConflictingFiles(repoPath)
			if conflictErr == nil {
				result.Conflicts = conflicts
			}
			return result, fmt.Errorf("merge conflict: %s", outputStr)
		}
		return result, fmt.Errorf("merge failed: %s", outputStr)
	}

	// Check if it was a fast-forward merge
	if strings.Contains(outputStr, "Fast-forward") {
		result.FastForward = true
	}

	// Extract merge commit message if available
	if !opts.Squash && !result.FastForward {
		// Get the last commit message (the merge commit)
		msgCmd := exec.Command("git", "-C", repoPath, "log", "-1", "--pretty=%s")
		if msgOut, msgErr := msgCmd.Output(); msgErr == nil {
			result.Message = strings.TrimSpace(string(msgOut))
		}
	}

	return result, nil
}

// getConflictingFiles returns a list of files with merge conflicts.
func getConflictingFiles(repoPath string) ([]string, error) {
	cmd := exec.Command("git", "-C", repoPath, "diff", "--name-only", "--diff-filter=U")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var conflicts []string
	for _, line := range lines {
		if line != "" {
			conflicts = append(conflicts, line)
		}
	}
	return conflicts, nil
}
