package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/oneshot"
)

// setupMRTestRepo creates a repo with an "origin" remote holding the initial
// commit, and one additional local commit ahead of origin/<baseBranch> — the
// shape GenerateMRContent needs to take the real (non-fallback) path.
func setupMRTestRepo(t *testing.T) (repoDir, baseBranch string) {
	t.Helper()

	originDir, err := os.MkdirTemp("", "git-mr-origin-*")
	if err != nil {
		t.Fatalf("failed to create origin dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(originDir) })
	runGitTree(t, originDir, "init", "--bare")

	repoDir, err = os.MkdirTemp("", "git-mr-repo-*")
	if err != nil {
		t.Fatalf("failed to create repo dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(repoDir) })

	runGitTree(t, repoDir, "init")
	runGitTree(t, repoDir, "config", "user.name", "Test User")
	runGitTree(t, repoDir, "config", "user.email", "test@example.com")
	runGitTree(t, repoDir, "remote", "add", "origin", originDir)

	createFileTree(t, repoDir, "README.md", "# Test")
	runGitTree(t, repoDir, "add", ".")
	runGitTree(t, repoDir, "commit", "-m", "Initial commit")

	baseBranch = currentBranchTree(t, repoDir)
	runGitTree(t, repoDir, "push", "origin", baseBranch)

	createFileTree(t, repoDir, "feature.go", "package main\nfunc main() {}\n")
	runGitTree(t, repoDir, "add", ".")
	runGitTree(t, repoDir, "commit", "-m", "Add feature")

	return repoDir, baseBranch
}

func currentBranchTree(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil {
		t.Fatalf("failed to determine current branch: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// stubOneShotCounting is like stubOneShot but also counts how many times the
// seam was invoked, so tests can assert on the number of real LLM calls.
func stubOneShotCounting(t *testing.T, fn func(prompt string) (string, error)) *int {
	t.Helper()
	GetGlobalCache().Clear()
	t.Cleanup(func() { GetGlobalCache().Clear() })

	prev := oneShotPrompt
	t.Cleanup(func() { oneShotPrompt = prev })

	calls := 0
	oneShotPrompt = func(_ context.Context, req oneshot.Request) (string, error) {
		calls++
		return fn(req.Prompt)
	}
	return &calls
}

func TestMRContentCacheKey(t *testing.T) {
	k1 := mrContentCacheKey("/repo", "main", "commits A", "stat A")
	k2 := mrContentCacheKey("/repo", "main", "commits B", "stat A")
	k3 := mrContentCacheKey("/repo", "main", "commits A", "stat A")

	if k1 == k2 {
		t.Error("different commit logs should produce different cache keys")
	}
	if k1 != k3 {
		t.Error("same inputs should produce the same cache key")
	}
	if !strings.HasPrefix(k1, "mrcontent:") {
		t.Errorf("cache key should have 'mrcontent:' prefix, got %q", k1)
	}
}

// TestGenerateMRContentCachesSecondCall asserts the second GenerateMRContent
// call for the same branch state reuses the first call's answer instead of
// issuing a second oneshot LLM request.
func TestGenerateMRContentCachesSecondCall(t *testing.T) {
	repoDir, baseBranch := setupMRTestRepo(t)

	calls := stubOneShotCounting(t, func(string) (string, error) {
		return `{"title":"Add feature","description":"## Summary\n\n- adds a feature"}`, nil
	})

	first, err := GenerateMRContent(repoDir, "", baseBranch)
	if err != nil {
		t.Fatalf("GenerateMRContent (first call): %v", err)
	}
	second, err := GenerateMRContent(repoDir, "", baseBranch)
	if err != nil {
		t.Fatalf("GenerateMRContent (second call): %v", err)
	}

	if *calls != 1 {
		t.Errorf("oneShotPrompt called %d times, want 1 (second call should be a cache hit)", *calls)
	}
	if *first != *second {
		t.Errorf("first and second calls returned different content: %+v vs %+v", first, second)
	}
}

// TestGenerateMRTitleAndDescriptionShareOneGeneration is the regression test
// for the reported bug: GenerateMRTitle and GenerateMRDescription, called
// back-to-back as real callers do, must draw the title and description from
// one generated MRContent (one oneshot call) rather than two independent,
// potentially divergent generations.
func TestGenerateMRTitleAndDescriptionShareOneGeneration(t *testing.T) {
	repoDir, baseBranch := setupMRTestRepo(t)
	// The repo's checked-out branch is baseBranch itself (with one local
	// commit ahead of origin/baseBranch), so passing it as the explicit
	// source matches what's actually checked out.
	sourceBranch := currentBranchTree(t, repoDir)

	calls := stubOneShotCounting(t, func(string) (string, error) {
		return `{"title":"Add feature","description":"## Summary\n\n- adds a feature\n\n## Changes\n\n- feature.go"}`, nil
	})

	title, err := GenerateMRTitle(repoDir, sourceBranch, baseBranch)
	if err != nil {
		t.Fatalf("GenerateMRTitle: %v", err)
	}
	desc, err := GenerateMRDescription(repoDir, sourceBranch, baseBranch)
	if err != nil {
		t.Fatalf("GenerateMRDescription: %v", err)
	}

	if *calls != 1 {
		t.Errorf("oneShotPrompt called %d times across GenerateMRTitle+GenerateMRDescription, want 1", *calls)
	}
	if title != "Add feature" {
		t.Errorf("title = %q, want %q", title, "Add feature")
	}
	if !strings.Contains(desc, "adds a feature") {
		t.Errorf("description = %q, want it to contain the generated summary", desc)
	}
}

// setupMRTestRepoWithDetachedSource creates a repo where HEAD is checked out
// on baseBranch (with nothing ahead of origin/baseBranch), while a separate
// local branch, sourceBranch, carries the actual new commit. This is the
// shape of the PR TUI's branch picker: selecting a source branch never
// checks it out, so HEAD and the requested source branch diverge.
func setupMRTestRepoWithDetachedSource(t *testing.T) (repoDir, sourceBranch, baseBranch string) {
	t.Helper()

	originDir, err := os.MkdirTemp("", "git-mr-origin-*")
	if err != nil {
		t.Fatalf("failed to create origin dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(originDir) })
	runGitTree(t, originDir, "init", "--bare")

	repoDir, err = os.MkdirTemp("", "git-mr-repo-*")
	if err != nil {
		t.Fatalf("failed to create repo dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(repoDir) })

	runGitTree(t, repoDir, "init")
	runGitTree(t, repoDir, "config", "user.name", "Test User")
	runGitTree(t, repoDir, "config", "user.email", "test@example.com")
	runGitTree(t, repoDir, "remote", "add", "origin", originDir)

	createFileTree(t, repoDir, "README.md", "# Test")
	runGitTree(t, repoDir, "add", ".")
	runGitTree(t, repoDir, "commit", "-m", "Initial commit")

	baseBranch = currentBranchTree(t, repoDir)
	runGitTree(t, repoDir, "push", "origin", baseBranch)

	sourceBranch = "other-feature"
	runGitTree(t, repoDir, "checkout", "-b", sourceBranch)
	createFileTree(t, repoDir, "feature.go", "package main\nfunc main() {}\n")
	runGitTree(t, repoDir, "add", ".")
	runGitTree(t, repoDir, "commit", "-m", "Add feature")

	// Leave HEAD back on baseBranch, with nothing new versus origin: only
	// sourceBranch has the commit.
	runGitTree(t, repoDir, "checkout", baseBranch)

	return repoDir, sourceBranch, baseBranch
}

// TestGenerateMRContentUsesExplicitSourceBranch is the regression test for
// finding 1: GenerateMRContent must diff the caller-supplied sourceBranch
// against baseBranch, not whatever happens to be checked out at HEAD.
func TestGenerateMRContentUsesExplicitSourceBranch(t *testing.T) {
	repoDir, sourceBranch, baseBranch := setupMRTestRepoWithDetachedSource(t)

	calls := stubOneShotCounting(t, func(prompt string) (string, error) {
		if !strings.Contains(prompt, "Add feature") {
			t.Errorf("prompt does not mention the source branch's commit, got: %s", prompt)
		}
		return `{"title":"Add feature","description":"## Summary\n\n- adds a feature"}`, nil
	})

	content, err := GenerateMRContent(repoDir, sourceBranch, baseBranch)
	if err != nil {
		t.Fatalf("GenerateMRContent: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("oneShotPrompt called %d times, want 1 (HEAD, which is at baseBranch, has nothing to describe)", *calls)
	}
	if content.Title != "Add feature" {
		t.Errorf("title = %q, want %q", content.Title, "Add feature")
	}
}

// TestFallbackMRContentUsesExplicitSourceBranch is the regression test for
// finding 2: when the LLM call is unavailable, the fallback title/description
// must name the caller-supplied sourceBranch, not repoPath's checked-out HEAD.
func TestFallbackMRContentUsesExplicitSourceBranch(t *testing.T) {
	repoDir, sourceBranch, baseBranch := setupMRTestRepoWithDetachedSource(t)

	stubOneShotCounting(t, func(string) (string, error) {
		return "", fmt.Errorf("agent unavailable")
	})

	content, err := GenerateMRContent(repoDir, sourceBranch, baseBranch)
	if err != nil {
		t.Fatalf("GenerateMRContent: %v", err)
	}
	if !strings.Contains(content.Title, "other feature") {
		t.Errorf("fallback title = %q, want it to name sourceBranch %q, not HEAD (%q)", content.Title, sourceBranch, baseBranch)
	}
	if !strings.Contains(content.Description, sourceBranch) {
		t.Errorf("fallback description = %q, want it to mention sourceBranch %q", content.Description, sourceBranch)
	}
}
