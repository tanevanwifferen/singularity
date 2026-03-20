package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareBranches(t *testing.T) {
	// Create a temporary git repo for testing
	tmpDir, err := os.MkdirTemp("", "git-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Initialize git repo
	runGit(t, tmpDir, "init")
	runGit(t, tmpDir, "config", "user.name", "Test User")
	runGit(t, tmpDir, "config", "user.email", "test@example.com")

	// Create initial commit
	createFile(t, tmpDir, "README.md", "# Test")
	runGit(t, tmpDir, "add", ".")
	runGit(t, tmpDir, "commit", "-m", "Initial commit")

	// Create feature branch with additional commits
	runGit(t, tmpDir, "checkout", "-b", "feature")
	createFile(t, tmpDir, "feature.txt", "Feature content")
	runGit(t, tmpDir, "add", ".")
	runGit(t, tmpDir, "commit", "-m", "Add feature")

	// Go back to main and add different commit
	runGit(t, tmpDir, "checkout", "master")
	createFile(t, tmpDir, "main.txt", "Main content")
	runGit(t, tmpDir, "add", ".")
	runGit(t, tmpDir, "commit", "-m", "Add main content")

	// Test comparison
	result, err := CompareBranches(tmpDir, "master", "feature")
	if err != nil {
		t.Fatalf("CompareBranches failed: %v", err)
	}

	if result.Ahead != 1 {
		t.Errorf("Expected ahead=1, got %d", result.Ahead)
	}
	if result.Behind != 1 {
		t.Errorf("Expected behind=1, got %d", result.Behind)
	}
	if !result.Diverged {
		t.Error("Expected diverged=true")
	}
}

func TestCompareBranchesEqual(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "git-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runGit(t, tmpDir, "init")
	runGit(t, tmpDir, "config", "user.name", "Test User")
	runGit(t, tmpDir, "config", "user.email", "test@example.com")

	createFile(t, tmpDir, "README.md", "# Test")
	runGit(t, tmpDir, "add", ".")
	runGit(t, tmpDir, "commit", "-m", "Initial commit")

	// Compare branch with itself
	result, err := CompareBranches(tmpDir, "master", "master")
	if err != nil {
		t.Fatalf("CompareBranches failed: %v", err)
	}

	if result.Ahead != 0 {
		t.Errorf("Expected ahead=0, got %d", result.Ahead)
	}
	if result.Behind != 0 {
		t.Errorf("Expected behind=0, got %d", result.Behind)
	}
	if result.Diverged {
		t.Error("Expected diverged=false")
	}
}

func TestCompareBranchesSimple(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "git-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runGit(t, tmpDir, "init")
	runGit(t, tmpDir, "config", "user.name", "Test User")
	runGit(t, tmpDir, "config", "user.email", "test@example.com")

	createFile(t, tmpDir, "README.md", "# Test")
	runGit(t, tmpDir, "add", ".")
	runGit(t, tmpDir, "commit", "-m", "Initial commit")

	runGit(t, tmpDir, "checkout", "-b", "feature")
	createFile(t, tmpDir, "feature.txt", "Feature")
	runGit(t, tmpDir, "add", ".")
	runGit(t, tmpDir, "commit", "-m", "Add feature")

	ahead, behind, err := CompareBranchesSimple(tmpDir, "master", "feature")
	if err != nil {
		t.Fatalf("CompareBranchesSimple failed: %v", err)
	}

	if ahead != 1 {
		t.Errorf("Expected ahead=1, got %d", ahead)
	}
	if behind != 0 {
		t.Errorf("Expected behind=0, got %d", behind)
	}
}

// Helper functions
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
