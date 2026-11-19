package flow

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitIn runs git in dir for a test and returns its trimmed stdout.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// initRepo makes a repository with one commit and returns it with that commit.
func initRepo(t *testing.T) (dir, head string) {
	t.Helper()
	dir = t.TempDir()
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	return dir, gitIn(t, dir, "rev-parse", "HEAD")
}

// agentCommits stands in for a work step that commits although it was told not
// to, and returns the new HEAD.
func agentCommits(t *testing.T, dir, file string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte("work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", file)
	gitIn(t, dir, "commit", "-q", "-m", "feat: the agent committed "+file)
	return gitIn(t, dir, "rev-parse", "HEAD")
}

// startInRepo starts a flow over dir.
func startInRepo(t *testing.T, m *Manager, dir string, rounds int) Flow {
	t.Helper()
	req := startReq(t)
	req.WorkDir = dir
	req.MaxRounds = rounds
	f, err := m.Start(req)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return f
}

// TestGitHeadMatchesGit pins the in-process HEAD reader against git itself in
// every repository layout a work dir can have.
func TestGitHeadMatchesGit(t *testing.T) {
	repo, head := initRepo(t)
	sub := filepath.Join(repo, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "wt")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "side", linked)
	linkedHead := agentCommits(t, linked, "b.txt")

	packed, packedHead := initRepo(t)
	gitIn(t, packed, "pack-refs", "--all")

	detached, _ := initRepo(t)
	second := agentCommits(t, detached, "c.txt")
	gitIn(t, detached, "checkout", "-q", "--detach", "HEAD~1")
	detachedHead := gitIn(t, detached, "rev-parse", "HEAD")

	unborn := t.TempDir()
	gitIn(t, unborn, "init", "-q")

	for _, tc := range []struct {
		name, dir, want string
	}{
		{"plain repo", repo, head},
		{"subdirectory of a repo", sub, head},
		{"linked worktree", linked, linkedHead},
		{"subdirectory of a linked worktree", mkSub(t, linked), linkedHead},
		{"refs only in packed-refs", packed, packedHead},
		{"detached HEAD", detached, detachedHead},
		{"no commit yet", unborn, ""},
		{"not a repository", t.TempDir(), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := gitHead(tc.dir); got != tc.want {
				t.Errorf("gitHead(%s) = %q, want %q", tc.dir, got, tc.want)
			}
		})
	}
	if detachedHead == second {
		t.Fatal("test setup: detached HEAD did not move off the tip")
	}
}

func mkSub(t *testing.T, dir string) string {
	t.Helper()
	sub := filepath.Join(dir, "x", "y")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	return sub
}

// TestWorkStepThatMovedHeadIsRecordedNotSilent covers an implement agent that
// commits although it was told not to: the reviewer is pointed at the commit
// range, the round records the moved HEAD, the agent's commit is left alone,
// and the commit task treats "already committed, tree clean" as normal.
func TestWorkStepThatMovedHeadIsRecordedNotSilent(t *testing.T) {
	for _, tc := range []struct {
		name      string
		agentWork func(t *testing.T, dir string)
		wantMoved bool
	}{
		{"work step commits", func(t *testing.T, dir string) { agentCommits(t, dir, "a.txt") }, true},
		{"work step leaves the diff uncommitted", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("work\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, base := initRepo(t)
			m, q := newDriven(t)
			f := startInRepo(t, m, dir, 0)
			m.tick()

			batch := q.batches[0]
			if !strings.Contains(batch[0].Prompt, "Do NOT run `git commit`, whatever the goal says") {
				t.Errorf("implement prompt does not state the no-commit rule:\n%s", batch[0].Prompt)
			}
			if !strings.Contains(batch[1].Prompt, "git diff "+base) {
				t.Errorf("review prompt does not name the base commit %s:\n%s", base, batch[1].Prompt)
			}
			if r := currentRound(t, m, f.ID); r.BaseHead != base {
				t.Fatalf("round BaseHead = %q, want %q", r.BaseHead, base)
			}

			tc.agentWork(t, dir)
			head := gitIn(t, dir, "rev-parse", "HEAD")
			finishSteps(t, m, q, f.ID)
			writeVerdict(t, m, f.ID, 1, 1, acceptVerdict)
			m.tick()

			got, err := m.Get(f.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.State != StateAccepted {
				t.Fatalf("flow state = %s, want accepted", got.State)
			}
			r := got.Rounds[0]
			if tc.wantMoved {
				if r.CommittedHead != head || head == base {
					t.Errorf("CommittedHead = %q, want the moved HEAD %q (base %q)", r.CommittedHead, head, base)
				}
			} else if r.CommittedHead != "" {
				t.Errorf("CommittedHead = %q, want empty when HEAD did not move", r.CommittedHead)
			}
			// The agent's commit is never reset or rewritten.
			if now := gitIn(t, dir, "rev-parse", "HEAD"); now != head {
				t.Errorf("HEAD = %s after the flow, want the agent's %s untouched", now, head)
			}
			commit := q.batches[1][0].Prompt
			if commit != CommitPrompt(&got) || !strings.Contains(commit, "is therefore a normal outcome") {
				t.Errorf("commit prompt does not treat an already-committed clean tree as normal:\n%s", commit)
			}
		})
	}
}

// TestFixRoundAfterACommittingWorkStep: round 2's reviewer and the commit task
// still measure from the flow's original base, so round 1's commit is in the
// range, while round 2 records its own base and its own moved HEAD.
func TestFixRoundAfterACommittingWorkStep(t *testing.T) {
	dir, base := initRepo(t)
	m, q := newDriven(t)
	f := startInRepo(t, m, dir, 2)
	m.tick()

	c1 := agentCommits(t, dir, "a.txt")
	finishSteps(t, m, q, f.ID)
	writeVerdict(t, m, f.ID, 1, 1, rejectVerdict("needs more"))
	m.tick() // settles round 1 and submits the fix round

	if len(q.batches) != 2 {
		t.Fatalf("submitted %d batches, want round 1 and the fix round", len(q.batches))
	}
	r2 := currentRound(t, m, f.ID)
	if r2.N != 2 || r2.BaseHead != c1 {
		t.Fatalf("round 2 = %+v, want N 2 with BaseHead %s (HEAD when its fixer started)", r2, c1)
	}
	fix := q.batches[1]
	if !strings.Contains(fix[0].Prompt, "Do NOT run `git commit`, whatever the goal says") {
		t.Errorf("fix prompt does not state the no-commit rule:\n%s", fix[0].Prompt)
	}
	if !strings.Contains(fix[1].Prompt, "git diff "+base) || strings.Contains(fix[1].Prompt, "git diff "+c1) {
		t.Errorf("round 2's review prompt must name the flow's base %s, not round 1's commit:\n%s", base, fix[1].Prompt)
	}

	c2 := agentCommits(t, dir, "b.txt")
	finishSteps(t, m, q, f.ID)
	writeVerdict(t, m, f.ID, 2, 1, acceptVerdict)
	m.tick()

	got, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateAccepted {
		t.Fatalf("flow state = %s, want accepted", got.State)
	}
	if got.Rounds[0].CommittedHead != c1 || got.Rounds[1].CommittedHead != c2 {
		t.Errorf("CommittedHead = %q, %q; want %q, %q", got.Rounds[0].CommittedHead, got.Rounds[1].CommittedHead, c1, c2)
	}
	if commit := q.batches[2][0].Prompt; !strings.Contains(commit, "commits already on top of "+base) {
		t.Errorf("commit prompt must name the flow's base %s:\n%s", base, commit)
	}
}

// TestRetryOfRoundOneKeepsTheFlowsBase: a retried round 1 starts from a HEAD
// its first attempt already moved, but the base every reviewer and the commit
// task measure from stays the original one, so the first attempt's commit is
// not dropped from the range.
func TestRetryOfRoundOneKeepsTheFlowsBase(t *testing.T) {
	dir, base := initRepo(t)
	m, q := newDriven(t)
	f := startInRepo(t, m, dir, 1)
	m.tick()

	c1 := agentCommits(t, dir, "a.txt")
	r1 := finishSteps(t, m, q, f.ID)
	writeVerdict(t, m, f.ID, 1, 1, rejectVerdict("wrong"))
	m.tick()

	out, err := m.RetryStep(f.ID, r1.WorkTaskID)
	if err != nil {
		t.Fatalf("RetryStep: %v", err)
	}
	if r := out.CurrentRound(); r.BaseHead != base || r.CommittedHead != "" {
		t.Fatalf("round after retry = base %q committed %q, want the original base %q and no committed head yet",
			r.BaseHead, r.CommittedHead, base)
	}
	review := q.batches[len(q.batches)-1][1].Prompt
	if !strings.Contains(review, "git diff "+base) || strings.Contains(review, "git diff "+c1) {
		t.Errorf("retried review prompt must name the original base %s:\n%s", base, review)
	}

	finishSteps(t, m, q, f.ID)
	writeVerdict(t, m, f.ID, 1, 1, acceptVerdict)
	m.tick()
	got, err := m.Get(f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateAccepted || got.Rounds[0].CommittedHead != c1 {
		t.Errorf("flow %s, CommittedHead %q; want accepted with the first attempt's commit %s",
			got.State, got.Rounds[0].CommittedHead, c1)
	}
	if commit := q.batches[len(q.batches)-1][0].Prompt; !strings.Contains(commit, "commits already on top of "+base) {
		t.Errorf("commit prompt must name the original base %s:\n%s", base, commit)
	}
}
