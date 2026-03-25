package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetStagedDiff(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Create and stage a file
	createFileTree(t, tmpDir, "staged.txt", "staged content")
	runGitTree(t, tmpDir, "add", "staged.txt")

	diff, err := GetStagedDiff(tmpDir)
	if err != nil {
		t.Fatalf("GetStagedDiff failed: %v", err)
	}

	if !strings.Contains(diff, "staged.txt") {
		t.Error("Expected diff to contain staged.txt")
	}
}

func TestGetStagedDiffEmpty(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// No staged changes
	diff, err := GetStagedDiff(tmpDir)
	if err != nil {
		t.Fatalf("GetStagedDiff failed unexpectedly: %v", err)
	}

	if diff != "" {
		t.Error("Expected empty diff for no staged changes")
	}
}

func TestGetUnstagedDiff(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Modify an existing tracked file (don't stage the modification)
	os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# Modified\nNew content here"), 0644)

	diff, err := GetUnstagedDiff(tmpDir)
	if err != nil {
		t.Fatalf("GetUnstagedDiff failed: %v", err)
	}

	if diff == "" {
		t.Error("Expected non-empty diff for modified file")
	}
}

func TestGenerateCommitMessage(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Stage a new file
	createFileTree(t, tmpDir, "newfeature.go", "package main\nfunc main() {}")
	runGitTree(t, tmpDir, "add", "newfeature.go")

	msg, err := GenerateCommitMessage(tmpDir)
	if err != nil {
		t.Fatalf("GenerateCommitMessage failed: %v", err)
	}

	if msg == nil {
		t.Fatal("Expected commit message, got nil")
	}

	if msg.Type == "" {
		t.Error("Expected non-empty Type")
	}

	if msg.Subject == "" {
		t.Error("Expected non-empty Subject")
	}
}

func TestGenerateCommitMessageNoStaged(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Don't stage anything
	_, err := GenerateCommitMessage(tmpDir)
	if err == nil {
		t.Error("Expected error for no staged changes")
	}
}

func TestDetectCommitType(t *testing.T) {
	tests := []struct {
		diff     string
		expected string
	}{
		{"+++ b/internal/git/repo_test.go", "test"},
		{"+++ b/README.md", "docs"},
		{"+++ b/config.yml", "chore"},
		{"+++ b/fix_bug.go", "fix"},
		{"+++ b/new_feature.go", "feat"},
	}

	for _, tt := range tests {
		result := detectCommitType(tt.diff)
		if result != tt.expected {
			t.Errorf("detectCommitType(%q) = %q, want %q", tt.diff, result, tt.expected)
		}
	}
}

func TestDetectScope(t *testing.T) {
	tests := []struct {
		diff     string
		expected string
	}{
		{"+++ b/internal/git/repo.go", "git"},
		{"+++ b/internal/app/app.go", "app"},
		{"+++ b/cmd/singularity/main.go", "cli"},
	}

	for _, tt := range tests {
		result := detectScope(tt.diff)
		if result != tt.expected {
			t.Errorf("detectScope(%q) = %q, want %q", tt.diff, result, tt.expected)
		}
	}
}

func TestExtractChangedFiles(t *testing.T) {
	diff := `diff --git a/internal/git/repo.go b/internal/git/repo.go
--- a/internal/git/repo.go
+++ b/internal/git/repo.go
diff --git a/internal/git/branch.go b/internal/git/branch.go
--- a/internal/git/branch.go
+++ b/internal/git/branch.go`

	files := extractChangedFiles(diff)

	if len(files) != 2 {
		t.Errorf("Expected 2 files, got %d", len(files))
	}

	if !strings.Contains(files[0], "repo.go") {
		t.Errorf("Expected repo.go, got %s", files[0])
	}
}

func TestFormatConventionalCommit(t *testing.T) {
	msg := &CommitMessage{
		Type:    "feat",
		Scope:   "git",
		Subject: "add new functionality",
		Body:    "This is the body",
	}

	formatted := FormatConventionalCommit(msg)

	if !strings.Contains(formatted, "feat(git):") {
		t.Error("Expected feat(git): in formatted message")
	}

	if !strings.Contains(formatted, "add new functionality") {
		t.Error("Expected subject in formatted message")
	}
}

func TestFormatConventionalCommitWithFooters(t *testing.T) {
	msg := &CommitMessage{
		Type:    "feat",
		Scope:   "",
		Subject: "major changes",
		Body:    "",
		Footers: []string{"BREAKING CHANGE: removed old API"},
	}

	formatted := FormatConventionalCommit(msg)

	if !strings.Contains(formatted, "BREAKING CHANGE: removed old API") {
		t.Error("Expected footer in formatted message")
	}
}

func TestFormatConventionalCommitNil(t *testing.T) {
	formatted := FormatConventionalCommit(nil)
	if formatted != "" {
		t.Error("Expected empty string for nil message")
	}
}

func TestCommitMsgCacheKey(t *testing.T) {
	key1 := commitMsgCacheKey("diff content A")
	key2 := commitMsgCacheKey("diff content B")
	key3 := commitMsgCacheKey("diff content A")

	if key1 == key2 {
		t.Error("different diffs should produce different cache keys")
	}
	if key1 != key3 {
		t.Error("same diff should produce the same cache key")
	}
	if !strings.HasPrefix(key1, "commitmsg:") {
		t.Errorf("cache key should have 'commitmsg:' prefix, got %q", key1)
	}
}

func TestIsValidCommitType(t *testing.T) {
	valid := []string{"feat", "fix", "docs", "style", "refactor", "test", "chore", "perf", "ci", "build"}
	for _, v := range valid {
		if !isValidCommitType(v) {
			t.Errorf("isValidCommitType(%q) = false, want true", v)
		}
	}

	invalid := []string{"", "feature", "bugfix", "update", "FEAT", "Fix"}
	for _, v := range invalid {
		if isValidCommitType(v) {
			t.Errorf("isValidCommitType(%q) = true, want false", v)
		}
	}
}

func TestSuggestCommitMessage(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Stage a file
	createFileTree(t, tmpDir, "test.go", "package main")
	runGitTree(t, tmpDir, "add", "test.go")

	suggested, err := SuggestCommitMessage(tmpDir)
	if err != nil {
		t.Fatalf("SuggestCommitMessage failed: %v", err)
	}

	if suggested == "" {
		t.Error("Expected non-empty suggestion")
	}
}
