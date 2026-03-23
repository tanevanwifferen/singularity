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

func TestParseHunks_SingleHunk(t *testing.T) {
	rawDiff := `diff --git a/file.go b/file.go
index abc1234..def5678 100644
--- a/file.go
+++ b/file.go
@@ -1,5 +1,7 @@
 package main

+import "fmt"
+
 func main() {
-	println("hello")
+	fmt.Println("hello")
 }
`

	hunks := ParseHunks(rawDiff)
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}

	h := hunks[0]
	if h.OldStart != 1 || h.OldCount != 5 {
		t.Errorf("expected old range 1,5 got %d,%d", h.OldStart, h.OldCount)
	}
	if h.NewStart != 1 || h.NewCount != 7 {
		t.Errorf("expected new range 1,7 got %d,%d", h.NewStart, h.NewCount)
	}

	// Count additions and deletions
	adds, dels, ctx := 0, 0, 0
	for _, l := range h.Lines {
		switch l.LineType {
		case "+":
			adds++
		case "-":
			dels++
		case " ":
			ctx++
		}
	}
	if adds != 3 {
		t.Errorf("expected 3 additions, got %d", adds)
	}
	if dels != 1 {
		t.Errorf("expected 1 deletion, got %d", dels)
	}
	if ctx != 3 {
		t.Errorf("expected 3 context lines, got %d", ctx)
	}
}

func TestParseHunks_MultipleHunks(t *testing.T) {
	rawDiff := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -1,3 +1,4 @@
 package main

+import "fmt"
 func main() {
@@ -10,3 +11,3 @@
 // end
-	println("bye")
+	fmt.Println("bye")
 }
`

	hunks := ParseHunks(rawDiff)
	if len(hunks) != 2 {
		t.Fatalf("expected 2 hunks, got %d", len(hunks))
	}

	if hunks[0].OldStart != 1 {
		t.Errorf("hunk 0: expected old start 1, got %d", hunks[0].OldStart)
	}
	if hunks[1].OldStart != 10 {
		t.Errorf("hunk 1: expected old start 10, got %d", hunks[1].OldStart)
	}
}

func TestParseHunkHeader(t *testing.T) {
	tests := []struct {
		header   string
		oldStart int
		oldCount int
		newStart int
		newCount int
	}{
		{"@@ -1,5 +1,7 @@", 1, 5, 1, 7},
		{"@@ -10,3 +11,3 @@ func main()", 10, 3, 11, 3},
		{"@@ -1 +1,2 @@", 1, 1, 1, 2},
		{"@@ -0,0 +1,10 @@", 0, 0, 1, 10},
	}

	for _, tc := range tests {
		var hunk DiffHunk
		parseHunkHeader(tc.header, &hunk)
		if hunk.OldStart != tc.oldStart || hunk.OldCount != tc.oldCount {
			t.Errorf("header %q: old got %d,%d want %d,%d",
				tc.header, hunk.OldStart, hunk.OldCount, tc.oldStart, tc.oldCount)
		}
		if hunk.NewStart != tc.newStart || hunk.NewCount != tc.newCount {
			t.Errorf("header %q: new got %d,%d want %d,%d",
				tc.header, hunk.NewStart, hunk.NewCount, tc.newStart, tc.newCount)
		}
	}
}

func TestBuildPatch(t *testing.T) {
	hunk := DiffHunk{
		Header:   "@@ -1,3 +1,4 @@",
		OldStart: 1,
		OldCount: 3,
		NewStart: 1,
		NewCount: 4,
		Lines: []DiffLine{
			{Content: " package main", LineType: " "},
			{Content: "+import \"fmt\"", LineType: "+"},
			{Content: " ", LineType: " "},
			{Content: " func main() {", LineType: " "},
		},
	}

	patch := buildPatch("file.go", hunk)

	expected := []string{
		"diff --git a/file.go b/file.go",
		"--- a/file.go",
		"+++ b/file.go",
		"@@ -1,3 +1,4 @@",
		"+import \"fmt\"",
	}

	for _, want := range expected {
		found := false
		for _, line := range splitLines(patch) {
			if line == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("patch missing expected line: %q", want)
		}
	}
}

func TestParseHunks_NoNewlineAtEnd(t *testing.T) {
	rawDiff := `diff --git a/file.txt b/file.txt
--- a/file.txt
+++ b/file.txt
@@ -1,2 +1,2 @@
 hello
-world
+universe
\ No newline at end of file
`

	hunks := ParseHunks(rawDiff)
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}

	// Should have context, deletion, addition, and no-newline marker
	hasNoNewline := false
	for _, l := range hunks[0].Lines {
		if l.LineType == "\\" {
			hasNoNewline = true
		}
	}
	if !hasNoNewline {
		t.Error("expected to find 'no newline at end of file' marker")
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func TestStageAndUnstageHunk(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Create a file and commit it
	createFileTree(t, tmpDir, "test.txt", "line1\nline2\nline3\nline4\nline5\n")
	runGitTree(t, tmpDir, "add", ".")
	runGitTree(t, tmpDir, "commit", "-m", "Initial")

	// Modify the file (add a line)
	createFileTree(t, tmpDir, "test.txt", "line1\nline2\nnew line\nline3\nline4\nline5\n")

	// Get the unstaged diff
	rawDiff, err := GetUnstagedFileDiff(tmpDir, "test.txt")
	if err != nil {
		t.Fatalf("GetUnstagedFileDiff failed: %v", err)
	}

	hunks := ParseHunks(rawDiff)
	if len(hunks) == 0 {
		t.Fatal("expected at least one hunk")
	}

	// Stage the first hunk
	if err := StageHunk(tmpDir, "test.txt", hunks[0]); err != nil {
		t.Fatalf("StageHunk failed: %v", err)
	}

	// Verify something is staged
	stagedDiff, err := GetStagedFileDiff(tmpDir, "test.txt")
	if err != nil {
		t.Fatalf("GetStagedFileDiff failed: %v", err)
	}
	if stagedDiff == "" {
		t.Error("expected staged diff to be non-empty after staging hunk")
	}

	// Parse staged hunks and unstage
	stagedHunks := ParseHunks(stagedDiff)
	if len(stagedHunks) == 0 {
		t.Fatal("expected staged hunks")
	}

	if err := UnstageHunk(tmpDir, "test.txt", stagedHunks[0]); err != nil {
		t.Fatalf("UnstageHunk failed: %v", err)
	}

	// Verify nothing is staged now
	stagedDiff2, err := GetStagedFileDiff(tmpDir, "test.txt")
	if err != nil {
		t.Fatalf("GetStagedFileDiff after unstage failed: %v", err)
	}
	if stagedDiff2 != "" {
		t.Errorf("expected empty staged diff after unstaging, got: %s", stagedDiff2)
	}
}
