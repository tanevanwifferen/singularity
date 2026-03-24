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
		return fmt.Errorf("delete remote branch failed: %s", msg)
	}
	return nil
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
