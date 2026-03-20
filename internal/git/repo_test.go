package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRepo(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	repo, err := OpenRepo(tmpDir)
	if err != nil {
		t.Fatalf("OpenRepo failed: %v", err)
	}

	if repo.Path != tmpDir {
		t.Errorf("Expected path=%s, got %s", tmpDir, repo.Path)
	}
	if repo.CurrentBranch != "master" {
		t.Errorf("Expected currentBranch=master, got %s", repo.CurrentBranch)
	}
	if repo.HEAD == "" {
		t.Error("Expected HEAD to be set")
	}
	if repo.IsBare {
		t.Error("Expected IsBare=false")
	}
}

func TestOpenRepoNotGit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "non-git-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	_, err = OpenRepo(tmpDir)
	if err == nil {
		t.Error("Expected error for non-git directory")
	}
}

func TestFindRepo(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Find from repo root
	found, err := FindRepo(tmpDir)
	if err != nil {
		t.Fatalf("FindRepo failed: %v", err)
	}
	if found != tmpDir {
		t.Errorf("Expected found=%s, got %s", tmpDir, found)
	}

	// Find from subdirectory
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("Failed to create subdir: %v", err)
	}

	found, err = FindRepo(subDir)
	if err != nil {
		t.Fatalf("FindRepo from subdir failed: %v", err)
	}
	if found != tmpDir {
		t.Errorf("Expected found=%s, got %s", tmpDir, found)
	}
}

func TestFindRepoNotFound(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "non-git-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	_, err = FindRepo(tmpDir)
	if err == nil {
		t.Error("Expected error when no git repo found")
	}
}

func TestIsDirty(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Clean repo should not be dirty
	dirty, err := isDirty(tmpDir)
	if err != nil {
		t.Fatalf("isDirty failed: %v", err)
	}
	if dirty {
		t.Error("Expected clean repo to not be dirty")
	}

	// Add uncommitted changes
	createFile(t, tmpDir, "newfile.txt", "content")

	dirty, err = isDirty(tmpDir)
	if err != nil {
		t.Fatalf("isDirty failed: %v", err)
	}
	if !dirty {
		t.Error("Expected dirty repo to be dirty")
	}
}

func TestGetRemotes(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Add a remote
	runGit(t, tmpDir, "remote", "add", "origin", "https://example.com/repo.git")

	remotes, err := getRemotes(tmpDir)
	if err != nil {
		t.Fatalf("getRemotes failed: %v", err)
	}

	if len(remotes) != 1 {
		t.Fatalf("Expected 1 remote, got %d", len(remotes))
	}

	if remotes[0].Name != "origin" {
		t.Errorf("Expected remote name=origin, got %s", remotes[0].Name)
	}
	if remotes[0].URL != "https://example.com/repo.git" {
		t.Errorf("Expected remote URL=https://example.com/repo.git, got %s", remotes[0].URL)
	}
}

func TestGetBranches(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Create a new branch
	runGit(t, tmpDir, "checkout", "-b", "feature")
	runGit(t, tmpDir, "checkout", "master")

	branches, err := getBranches(tmpDir)
	if err != nil {
		t.Fatalf("getBranches failed: %v", err)
	}

	foundMaster := false
	foundFeature := false
	for _, branch := range branches {
		if branch.Name == "master" {
			foundMaster = true
		}
		if branch.Name == "feature" {
			foundFeature = true
		}
	}

	if !foundMaster {
		t.Error("Expected to find master branch")
	}
	if !foundFeature {
		t.Error("Expected to find feature branch")
	}
}
