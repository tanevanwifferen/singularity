package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareBranchesByTree(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Create feature branch with changes
	runGit(t, tmpDir, "checkout", "-b", "feature")
	createFile(t, tmpDir, "feature.txt", "Feature content")
	runGit(t, tmpDir, "add", ".")
	runGit(t, tmpDir, "commit", "-m", "Add feature")

	// Go back to master and add different changes
	runGit(t, tmpDir, "checkout", "master")
	createFile(t, tmpDir, "master.txt", "Master content")
	runGit(t, tmpDir, "add", ".")
	runGit(t, tmpDir, "commit", "-m", "Add master content")

	// Trees should be different
	result, err := CompareBranchesByTree(tmpDir, "master", "feature")
	if err != nil {
		t.Fatalf("CompareBranchesByTree failed: %v", err)
	}

	if !result.TreeDiverged {
		t.Error("Expected treeDiverged=true")
	}
	if !result.CommitDiverged {
		t.Error("Expected commitDiverged=true")
	}
	if result.SquashDetected {
		t.Error("Expected squashDetected=false")
	}
}

func TestCompareBranchesByTreeSquash(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Create feature branch
	runGit(t, tmpDir, "checkout", "-b", "feature")
	createFile(t, tmpDir, "feature.txt", "Feature content")
	runGit(t, tmpDir, "add", ".")
	runGit(t, tmpDir, "commit", "-m", "Add feature")

	// Squash merge back to master
	runGit(t, tmpDir, "checkout", "master")
	runGit(t, tmpDir, "merge", "--squash", "feature")
	runGit(t, tmpDir, "commit", "-m", "Squash merge feature")

	// Commits are different but trees should be the same
	result, err := CompareBranchesByTree(tmpDir, "master", "feature")
	if err != nil {
		t.Fatalf("CompareBranchesByTree failed: %v", err)
	}

	if result.TreeDiverged {
		t.Error("Expected treeDiverged=false after squash")
	}
	if result.CommitDiverged {
		t.Error("Expected commitDiverged=true (different commits)")
	}
	if !result.SquashDetected {
		t.Error("Expected squashDetected=true")
	}
}

func TestAreTreesEqual(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Same branch should have equal trees
	equal, err := AreTreesEqual(tmpDir, "master", "master")
	if err != nil {
		t.Fatalf("AreTreesEqual failed: %v", err)
	}
	if !equal {
		t.Error("Expected trees to be equal")
	}

	// Create divergent branch
	runGit(t, tmpDir, "checkout", "-b", "feature")
	createFile(t, tmpDir, "feature.txt", "Feature")
	runGit(t, tmpDir, "add", ".")
	runGit(t, tmpDir, "commit", "-m", "Add feature")

	// Different branches should have different trees
	equal, err = AreTreesEqual(tmpDir, "master", "feature")
	if err != nil {
		t.Fatalf("AreTreesEqual failed: %v", err)
	}
	if equal {
		t.Error("Expected trees to be different")
	}
}

// Helper functions
func setupTestRepo(t *testing.T) string {
	tmpDir, err := os.MkdirTemp("", "git-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	runGit(t, tmpDir, "init")
	runGit(t, tmpDir, "config", "user.name", "Test User")
	runGit(t, tmpDir, "config", "user.email", "test@example.com")

	createFile(t, tmpDir, "README.md", "# Test")
	runGit(t, tmpDir, "add", ".")
	runGit(t, tmpDir, "commit", "-m", "Initial commit")

	return tmpDir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func createFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}
}
