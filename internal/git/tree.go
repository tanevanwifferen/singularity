package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// TreeComparison holds the result of comparing two branch trees
type TreeComparison struct {
	CommitDiverged bool   `json:"commit_diverged"`
	TreeDiverged   bool   `json:"tree_diverged"`
	SquashDetected bool   `json:"squash_detected"`
	BranchA        string `json:"branch_a"`
	BranchB        string `json:"branch_b"`
	TreeA          string `json:"tree_a"`
	TreeB          string `json:"tree_b"`
}

// CompareBranchesByTree compares the tree hashes of two branches
// This detects squash merges: commits may diverge but trees may match
func CompareBranchesByTree(repoPath, branchA, branchB string) (*TreeComparison, error) {
	// Get tree hash for branch A
	treeACmd := exec.Command("git", "-C", repoPath, "rev-parse", fmt.Sprintf("%s^{tree}", branchA))
	treeAOutput, err := treeACmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get tree hash for %s: %w", branchA, err)
	}
	treeA := strings.TrimSpace(string(treeAOutput))

	// Get tree hash for branch B
	treeBCmd := exec.Command("git", "-C", repoPath, "rev-parse", fmt.Sprintf("%s^{tree}", branchB))
	treeBOutput, err := treeBCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get tree hash for %s: %w", branchB, err)
	}
	treeB := strings.TrimSpace(string(treeBOutput))

	// Get commit SHAs for comparison
	commitACmd := exec.Command("git", "-C", repoPath, "rev-parse", branchA)
	commitAOutput, err := commitACmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get commit SHA for %s: %w", branchA, err)
	}
	commitA := strings.TrimSpace(string(commitAOutput))

	commitBCmd := exec.Command("git", "-C", repoPath, "rev-parse", branchB)
	commitBOutput, err := commitBCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get commit SHA for %s: %w", branchB, err)
	}
	commitB := strings.TrimSpace(string(commitBOutput))

	commitDiverged := commitA != commitB
	treeDiverged := treeA != treeB
	squashDetected := commitDiverged && !treeDiverged

	return &TreeComparison{
		CommitDiverged: commitDiverged,
		TreeDiverged:   treeDiverged,
		SquashDetected: squashDetected,
		BranchA:        branchA,
		BranchB:        branchB,
		TreeA:          treeA,
		TreeB:          treeB,
	}, nil
}

// AreTreesEqual checks if two branches have the same tree (working directory state)
func AreTreesEqual(repoPath, branchA, branchB string) (bool, error) {
	result, err := CompareBranchesByTree(repoPath, branchA, branchB)
	if err != nil {
		return false, err
	}
	return !result.TreeDiverged, nil
}
