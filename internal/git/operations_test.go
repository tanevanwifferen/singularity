package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initOpsTestRepo creates a temporary git repo with one initial commit.
func initOpsTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	cmds := [][]string{
		{"git", "init", dir},
		{"git", "-C", dir, "config", "user.email", "test@test.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup %v failed: %s %v", args, out, err)
		}
	}

	// Create initial commit
	f := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(f, []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "-C", dir, "add", "."},
		{"git", "-C", dir, "commit", "-m", "initial commit"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup %v failed: %s %v", args, out, err)
		}
	}

	return dir
}

func opsGetHEAD(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func opsGetHEADMessage(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "log", "-1", "--format=%s").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestAmendCommitMessage(t *testing.T) {
	dir := initOpsTestRepo(t)

	err := AmendCommitMessage(dir, "amended message")
	if err != nil {
		t.Fatalf("AmendCommitMessage failed: %v", err)
	}

	msg := opsGetHEADMessage(t, dir)
	if msg != "amended message" {
		t.Errorf("expected 'amended message', got %q", msg)
	}
}

func TestAmendCommitMessageEmpty(t *testing.T) {
	dir := initOpsTestRepo(t)

	err := AmendCommitMessage(dir, "")
	if err == nil {
		t.Fatal("expected error for empty message")
	}
}

func TestCherryPick(t *testing.T) {
	dir := initOpsTestRepo(t)

	// Create a second commit on a branch
	cmd := exec.Command("git", "-C", dir, "checkout", "-b", "feature")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout -b feature failed: %s %v", out, err)
	}

	f := filepath.Join(dir, "feature.txt")
	if err := os.WriteFile(f, []byte("feature\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "-C", dir, "add", "."},
		{"git", "-C", dir, "commit", "-m", "feature commit"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup %v failed: %s %v", args, out, err)
		}
	}

	featureHash := opsGetHEAD(t, dir)

	// Switch back to the default branch and cherry-pick
	// Try common default branch names
	switched := false
	for _, branch := range []string{"master", "main"} {
		cmd := exec.Command("git", "-C", dir, "checkout", branch)
		if _, err := cmd.CombinedOutput(); err == nil {
			switched = true
			break
		}
	}
	if !switched {
		t.Fatal("could not switch back to default branch")
	}

	err := CherryPick(dir, featureHash)
	if err != nil {
		t.Fatalf("CherryPick failed: %v", err)
	}

	msg := opsGetHEADMessage(t, dir)
	if msg != "feature commit" {
		t.Errorf("expected 'feature commit', got %q", msg)
	}
}

func TestResetToCommitSoft(t *testing.T) {
	dir := initOpsTestRepo(t)
	initialHash := opsGetHEAD(t, dir)

	// Create a second commit
	f := filepath.Join(dir, "second.txt")
	if err := os.WriteFile(f, []byte("second\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "-C", dir, "add", "."},
		{"git", "-C", dir, "commit", "-m", "second commit"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup %v failed: %s %v", args, out, err)
		}
	}

	err := ResetToCommit(dir, initialHash, "soft")
	if err != nil {
		t.Fatalf("ResetToCommit soft failed: %v", err)
	}

	currentHash := opsGetHEAD(t, dir)
	if currentHash != initialHash {
		t.Errorf("expected HEAD at %s, got %s", initialHash, currentHash)
	}
}

func TestResetToCommitInvalidMode(t *testing.T) {
	dir := initOpsTestRepo(t)
	hash := opsGetHEAD(t, dir)

	err := ResetToCommit(dir, hash, "invalid")
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
}
