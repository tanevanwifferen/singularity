package git

import (
	"os"
	"testing"
)

func TestGetBranchDiff(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Create feature branch with changes
	runGitTree(t, tmpDir, "checkout", "-b", "feature")
	createFileTree(t, tmpDir, "feature.txt", "Feature content")
	runGitTree(t, tmpDir, "add", ".")
	runGitTree(t, tmpDir, "commit", "-m", "Add feature")

	// Go back to master and add different changes
	runGitTree(t, tmpDir, "checkout", "master")
	createFileTree(t, tmpDir, "master.txt", "Master content")
	runGitTree(t, tmpDir, "add", ".")
	runGitTree(t, tmpDir, "commit", "-m", "Add master content")

	// Get diff between master and feature
	result, err := GetBranchDiff(tmpDir, "master", "feature")
	if err != nil {
		t.Fatalf("GetBranchDiff failed: %v", err)
	}

	if result.FilesChanged == 0 {
		t.Error("Expected at least one file changed")
	}

	if result.TotalAdditions == 0 && result.TotalDeletions == 0 {
		t.Error("Expected some additions or deletions")
	}
}

func TestGetBranchDiffNoChanges(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Compare identical branches
	result, err := GetBranchDiff(tmpDir, "master", "master")
	if err != nil {
		t.Fatalf("GetBranchDiff failed: %v", err)
	}

	if result.FilesChanged != 0 {
		t.Errorf("Expected 0 files changed, got %d", result.FilesChanged)
	}
}

func TestGetBranchDiffNewBranch(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Create new branch with a file
	runGitTree(t, tmpDir, "checkout", "-b", "new-feature")
	createFileTree(t, tmpDir, "new.txt", "new content")
	runGitTree(t, tmpDir, "add", ".")
	runGitTree(t, tmpDir, "commit", "-m", "Add new file")

	result, err := GetBranchDiff(tmpDir, "master", "new-feature")
	if err != nil {
		t.Fatalf("GetBranchDiff failed: %v", err)
	}

	if result.FilesChanged != 1 {
		t.Errorf("Expected 1 file changed, got %d", result.FilesChanged)
	}
}

func TestGetChangedFiles(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Create feature branch with changes
	runGitTree(t, tmpDir, "checkout", "-b", "feature")
	createFileTree(t, tmpDir, "feature.txt", "Feature content")
	createFileTree(t, tmpDir, "another.txt", "Another content")
	runGitTree(t, tmpDir, "add", ".")
	runGitTree(t, tmpDir, "commit", "-m", "Add feature files")

	// Go back to master and add different changes
	runGitTree(t, tmpDir, "checkout", "master")
	createFileTree(t, tmpDir, "master.txt", "Master content")
	runGitTree(t, tmpDir, "add", ".")
	runGitTree(t, tmpDir, "commit", "-m", "Add master content")

	files, err := GetChangedFiles(tmpDir, "master", "feature")
	if err != nil {
		t.Fatalf("GetChangedFiles failed: %v", err)
	}

	if len(files) == 0 {
		t.Error("Expected at least one changed file")
	}
}

func TestGetChangedFilesNoChanges(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	files, err := GetChangedFiles(tmpDir, "master", "master")
	if err != nil {
		t.Fatalf("GetChangedFiles failed: %v", err)
	}

	if len(files) != 0 {
		t.Errorf("Expected 0 files, got %d", len(files))
	}
}

func TestFileChangeStatus(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Create a new file
	createFileTree(t, tmpDir, "newfile.txt", "new content")
	runGitTree(t, tmpDir, "add", ".")
	runGitTree(t, tmpDir, "commit", "-m", "Add new file")

	// Modify the file
	createFileTree(t, tmpDir, "newfile.txt", "modified content")
	runGitTree(t, tmpDir, "add", ".")
	runGitTree(t, tmpDir, "commit", "-m", "Modify new file")

	// Get diff
	result, err := GetBranchDiff(tmpDir, "HEAD~1", "HEAD")
	if err != nil {
		t.Fatalf("GetBranchDiff failed: %v", err)
	}

	if len(result.Files) == 0 {
		t.Error("Expected at least one file")
	}
}

func TestBranchDiffFields(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Create feature branch
	runGitTree(t, tmpDir, "checkout", "-b", "test-branch")
	createFileTree(t, tmpDir, "test.txt", "test content")
	runGitTree(t, tmpDir, "add", ".")
	runGitTree(t, tmpDir, "commit", "-m", "Add test file")

	result, err := GetBranchDiff(tmpDir, "master", "test-branch")
	if err != nil {
		t.Fatalf("GetBranchDiff failed: %v", err)
	}

	// Check that branch names are set correctly
	if result.BranchA != "master" {
		t.Errorf("Expected BranchA=master, got %s", result.BranchA)
	}
	if result.BranchB != "test-branch" {
		t.Errorf("Expected BranchB=test-branch, got %s", result.BranchB)
	}
}
