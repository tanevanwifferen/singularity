package engine

import (
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// runGitOrFatal runs a git command in dir and fails the test on error,
// including the command's combined output for diagnosis.
func runGitOrFatal(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// TestLockRepoSerializesConcurrentCallers guards the fix for Shutdown's
// concurrency regression: agent.terminate() now runs in a goroutine per
// agent (engine.go Shutdown), and every terminate() ends by calling
// cleanupWorktree against a.sourceRepoPath. Multiple agents commonly share a
// sourceRepoPath, so without a per-repo lock their `git worktree remove` /
// `worktree prune` / `branch -D` invocations would run concurrently against
// the same .git directory. This asserts lockRepo actually excludes
// concurrent holders for the same path.
func TestLockRepoSerializesConcurrentCallers(t *testing.T) {
	repoPath := t.TempDir()

	const n = 8
	var active int32
	var maxActive int32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l := lockRepo(repoPath)
			l.Lock()
			defer l.Unlock()

			cur := atomic.AddInt32(&active, 1)
			for {
				prev := atomic.LoadInt32(&maxActive)
				if cur <= prev || atomic.CompareAndSwapInt32(&maxActive, prev, cur) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			atomic.AddInt32(&active, -1)
		}()
	}
	wg.Wait()

	if maxActive != 1 {
		t.Errorf("lockRepo allowed %d concurrent holders for the same repo path, want 1 (serialized)", maxActive)
	}
}

// TestCleanupWorktreeConcurrentSameRepo exercises cleanupWorktree itself
// (not just the lock primitive) the way parallel Shutdown does: several
// worktrees of the same source repo torn down concurrently. Before the
// per-repo lock, concurrent `git worktree remove`/`prune`/`branch -D`
// against one .git directory could race on git's own lock files and
// intermittently leave a worktree or branch behind; since every *Cmd.Run()
// error is swallowed, the only observable symptom is leftover state, which
// is what this test checks for directly.
func TestCleanupWorktreeConcurrentSameRepo(t *testing.T) {
	repoPath := t.TempDir()
	runGitOrFatal(t, repoPath, "init", "-b", "main")
	runGitOrFatal(t, repoPath, "config", "user.email", "test@example.com")
	runGitOrFatal(t, repoPath, "config", "user.name", "Test")
	runGitOrFatal(t, repoPath, "commit", "--allow-empty", "-m", "init")

	const n = 5
	type wt struct {
		path   string
		branch string
	}
	worktrees := make([]wt, n)
	for i := 0; i < n; i++ {
		wtPath := t.TempDir()
		branch := "agent/test-" + string(rune('a'+i))
		runGitOrFatal(t, repoPath, "worktree", "add", "-b", branch, wtPath)
		worktrees[i] = wt{path: wtPath, branch: branch}
	}

	var wg sync.WaitGroup
	for _, w := range worktrees {
		wg.Add(1)
		go func(w wt) {
			defer wg.Done()
			cleanupWorktree(repoPath, w.path, w.branch)
		}(w)
	}
	wg.Wait()

	remaining := runGitOrFatal(t, repoPath, "worktree", "list", "--porcelain")
	for _, w := range worktrees {
		if strings.Contains(remaining, w.path) {
			t.Errorf("worktree %s was not removed after concurrent cleanup", w.path)
		}
	}

	branches := runGitOrFatal(t, repoPath, "branch", "--list")
	for _, w := range worktrees {
		if strings.Contains(branches, w.branch) {
			t.Errorf("branch %s was not deleted after concurrent cleanup", w.branch)
		}
	}
}
