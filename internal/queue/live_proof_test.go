package queue

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
)

// TestLiveProofKillDoesNotLetASecondAgentIntoTheDirectory is the end-to-end
// version of TestReconcileTerminatesAKilledAgentBeforeDispatch (which runs
// against the fake): a real engine.Engine, a real EngineRunner, and a real
// Manager, with only the backend stubbed (sh+cat, no coding-agent spend). It
// dispatches two tasks onto the same work_dir, kills the first task's agent
// the way `singl agents kill` does, and shows with real pids that the queue
// terminates the process itself rather than leaving it soft-closed, and
// that the second task's agent never runs while the first one's pid is
// alive.
func TestLiveProofKillDoesNotLetASecondAgentIntoTheDirectory(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	eng := newStubEngine(t, pidFile)
	runner := NewEngineRunner(eng)
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	m := NewManager(runner, store)

	tasks, err := m.Add([]TaskSpec{
		{Name: "t1", Prompt: "p", WorkDir: dir},
		{Name: "t2", Prompt: "p", WorkDir: dir},
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	t1, t2 := tasks[0].ID, tasks[1].ID

	m.tick()
	pid1 := readPid(t, pidFile)
	t.Logf("t1 dispatched, pid1=%d (alive=%v)", pid1, processAlive(pid1))
	if !processAlive(pid1) {
		t.Fatalf("pid1 %d is not alive right after dispatch", pid1)
	}
	task1, _ := m.Get(t1)
	if task1.State != StateRunning {
		t.Fatalf("t1 state = %s, want running", task1.State)
	}
	task2, _ := m.Get(t2)
	if task2.State == StateRunning {
		t.Fatalf("t2 state = running while t1 (pid %d) still occupies %s — the directory guard did not hold", pid1, dir)
	}
	t.Logf("t2 correctly held back: state=%s while pid1=%d is alive in %s", task2.State, pid1, dir)

	// Equivalent of `singl agents kill --id <t1's agent>`: engine.KillAgent
	// soft-closes. The record says killed; the process, deliberately, does
	// not — it is still running the exact same pid1 above.
	if err := eng.KillAgent(task1.AgentID); err != nil {
		t.Fatalf("KillAgent: %v", err)
	}
	t.Logf("KillAgent(%s) returned; pid1=%d alive=%v (soft-close must not touch the process)", task1.AgentID, pid1, processAlive(pid1))
	if !processAlive(pid1) {
		t.Fatalf("pid1 %d died from KillAgent alone; KillAgent is documented to soft-close", pid1)
	}

	// One tick: reconcile observes `killed`, fails t1, and — this is the
	// fix — synchronously terminates pid1 before dispatch runs, in the same
	// tick. Only then does dispatch see the directory genuinely free.
	m.tick()

	task1, _ = m.Get(t1)
	task2, _ = m.Get(t2)
	t.Logf("after the tick that observed `killed`: t1.State=%s t2.State=%s", task1.State, task2.State)

	deadline := time.Now().Add(2 * time.Second)
	for processAlive(pid1) {
		if time.Now().After(deadline) {
			t.Fatalf("pid1 %d is still alive 2s after reconcile observed its agent killed — the queue left it running", pid1)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Logf("pid1=%d confirmed dead (reconcile terminated it — not merely soft-closed)", pid1)

	if task1.State != StateFailed {
		t.Fatalf("t1 state = %s, want failed", task1.State)
	}
	if task2.State != StateRunning {
		t.Fatalf("t2 state = %s, want running once t1's agent was genuinely terminated", task2.State)
	}

	pid2 := readPidExcluding(t, pidFile, pid1)
	if !processAlive(pid2) {
		t.Fatalf("pid2 %d (t2's agent) is not alive", pid2)
	}
	t.Logf("t2 dispatched a genuinely new process, pid2=%d (alive=%v), distinct from the terminated pid1=%d", pid2, processAlive(pid2), pid1)

	out1, _ := exec.Command("ps", "-o", "pid,stat,cmd", "-p", strconv.Itoa(pid1)).CombinedOutput()
	out2, _ := exec.Command("ps", "-o", "pid,stat,cmd", "-p", strconv.Itoa(pid2)).CombinedOutput()
	t.Logf("ps for pid1 (%d, expect not found — it is dead):\n%s", pid1, out1)
	t.Logf("ps for pid2 (%d, expect alive):\n%s", pid2, out2)

	pg, _ := exec.Command("pgrep", "-x", "cat").CombinedOutput()
	t.Logf("pgrep -x cat (only pid2=%d should be listed, pid1=%d must be absent):\n%s", pid2, pid1, pg)
}

// TestLiveProofCompleteDoesNotLetASecondAgentIntoTheDirectory is the
// end-to-end version of TestReconcileTerminatesACompletedAgentBeforeDispatch
// (which runs against the fake): a real engine.Engine, a real EngineRunner,
// and a real Manager, with resultThenCatBackend standing in for a real
// backend that finishes a turn while its process stays resident on stdin —
// exactly the shape both real backends have. It dispatches two tasks onto
// the same work_dir, lets the first one's agent report complete, and shows
// with real pids that the queue terminates that process itself before the
// second task is ever dispatched into the same directory.
func TestLiveProofCompleteDoesNotLetASecondAgentIntoTheDirectory(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	eng := newResultThenCatStubEngine(t, pidFile)
	runner := NewEngineRunner(eng)
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	m := NewManager(runner, store)

	tasks, err := m.Add([]TaskSpec{
		{Name: "t1", Prompt: "p", WorkDir: dir},
		{Name: "t2", Prompt: "p", WorkDir: dir},
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	t1, t2 := tasks[0].ID, tasks[1].ID

	m.tick()
	pid1 := readPid(t, pidFile)
	t.Logf("t1 dispatched, pid1=%d (alive=%v)", pid1, processAlive(pid1))
	if !processAlive(pid1) {
		t.Fatalf("pid1 %d is not alive right after dispatch", pid1)
	}
	task1, _ := m.Get(t1)
	if task1.State != StateRunning {
		t.Fatalf("t1 state = %s, want running", task1.State)
	}
	task2, _ := m.Get(t2)
	if task2.State == StateRunning {
		t.Fatalf("t2 state = running while t1 (pid %d) still occupies %s — the directory guard did not hold", pid1, dir)
	}
	t.Logf("t2 correctly held back: state=%s while pid1=%d is alive in %s", task2.State, pid1, dir)

	// t1's agent finishes its turn: resultThenCatBackend emits RESULT, which
	// ParseEvent turns into a BackendResult and handleResult turns into
	// AgentComplete — with pid1 still alive on stdin, exactly like a real
	// backend left open for a follow-up.
	deadline := time.Now().Add(2 * time.Second)
	for {
		task1, _ = m.Get(t1)
		if eng.GetAgent(task1.AgentID).Snapshot().State.String() == "complete" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("t1's agent never reached complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Logf("t1's agent reached complete; pid1=%d alive=%v (a completed agent's process does not exit on its own)", pid1, processAlive(pid1))
	if !processAlive(pid1) {
		t.Fatalf("pid1 %d died on its own once the agent reported complete", pid1)
	}

	// One tick: reconcile observes `complete`, marks t1 done, and — this is
	// the fix — synchronously terminates pid1 before dispatch runs, in the
	// same tick. Only then does dispatch see the directory genuinely free.
	m.tick()

	task1, _ = m.Get(t1)
	task2, _ = m.Get(t2)
	t.Logf("after the tick that observed `complete`: t1.State=%s t2.State=%s", task1.State, task2.State)

	deadline = time.Now().Add(2 * time.Second)
	for processAlive(pid1) {
		if time.Now().After(deadline) {
			t.Fatalf("pid1 %d is still alive 2s after reconcile observed its agent complete — the queue left it running", pid1)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Logf("pid1=%d confirmed dead (reconcile terminated it once complete, same as it does for killed)", pid1)

	if task1.State != StateDone {
		t.Fatalf("t1 state = %s, want done", task1.State)
	}
	if task2.State != StateRunning {
		t.Fatalf("t2 state = %s, want running once t1's agent was genuinely terminated", task2.State)
	}

	pid2 := readPidExcluding(t, pidFile, pid1)
	if !processAlive(pid2) {
		t.Fatalf("pid2 %d (t2's agent) is not alive", pid2)
	}
	t.Logf("t2 dispatched a genuinely new process, pid2=%d (alive=%v), distinct from the terminated pid1=%d", pid2, processAlive(pid2), pid1)

	out1, _ := exec.Command("ps", "-o", "pid,stat,cmd", "-p", strconv.Itoa(pid1)).CombinedOutput()
	out2, _ := exec.Command("ps", "-o", "pid,stat,cmd", "-p", strconv.Itoa(pid2)).CombinedOutput()
	t.Logf("ps for pid1 (%d, expect not found — it is dead):\n%s", pid1, out1)
	t.Logf("ps for pid2 (%d, expect alive):\n%s", pid2, out2)

	pg, _ := exec.Command("pgrep", "-x", "cat").CombinedOutput()
	t.Logf("pgrep -x cat (only pid2=%d should be listed, pid1=%d must be absent):\n%s", pid2, pid1, pg)
}

// TestLiveProofForeignAgentBlocksDispatch is review cycle 7 finding 1's own
// live proof, answering the question that cycle asked: the one-agent-per-
// directory invariant binds an agent the queue did not start too. A real
// engine.Engine, a real EngineRunner, a real Manager — and a foreign agent
// started directly on the engine, bypassing the queue entirely, exactly the
// shape a bare `agents spawn`, a TUI-killed agent, or a Jira AI agent has.
// It soft-closes that foreign agent (the record says killed, the process —
// deliberately, per engine.KillAgent's contract — does not), then shows a
// queued task for the same directory sits held back for as long as that pid
// is alive, and only dispatches once the process is genuinely gone.
func TestLiveProofForeignAgentBlocksDispatch(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	eng := newStubEngine(t, pidFile)
	runner := NewEngineRunner(eng)
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	m := NewManager(runner, store)

	// The foreign agent: started directly on the engine, exactly like a bare
	// `agents spawn` the queue has no record of and never dispatched.
	foreignID, err := eng.StartAgent(dir, "p", engine.AgentOptions{})
	if err != nil {
		t.Fatalf("StartAgent (foreign): %v", err)
	}
	foreignPid := readPid(t, pidFile)
	t.Logf("foreign agent %s started outside the queue, pid=%d (alive=%v)", foreignID, foreignPid, processAlive(foreignPid))

	// Equivalent of `singl agents kill --id <foreignID>`: engine.KillAgent
	// soft-closes. The label goes terminal; the process, deliberately, does
	// not.
	if err := eng.KillAgent(foreignID); err != nil {
		t.Fatalf("KillAgent: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if !processAlive(foreignPid) {
		t.Fatalf("foreign pid %d died from KillAgent alone; KillAgent is documented to soft-close", foreignPid)
	}
	if got := eng.GetAgent(foreignID).Snapshot().State; got != engine.AgentKilled {
		t.Fatalf("foreign agent state = %s, want killed", got)
	}
	t.Logf("foreign agent soft-closed: state=killed, pid=%d alive=%v", foreignPid, processAlive(foreignPid))

	tasks, err := m.Add([]TaskSpec{{Name: "t", Prompt: "p", WorkDir: dir}})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	m.tick()
	task, _ := m.Get(tasks[0].ID)
	t.Logf("after one tick, foreign pid=%d (label=killed) still alive: queued task state=%s", foreignPid, task.State)
	if task.State == StateRunning {
		t.Fatalf("task dispatched into %s while foreign pid %d is still alive — the one-agent-per-directory invariant did not hold for an agent the queue never started", dir, foreignPid)
	}

	pg, _ := exec.Command("pgrep", "-x", "cat").CombinedOutput()
	t.Logf("pgrep -x cat while the queue task is held back (foreign pid=%d must be the only one listed):\n%s", foreignPid, pg)

	// Only once the foreign process is genuinely gone — an operator running
	// `agents remove`, in this test TerminateAgent standing in for it — is
	// the directory free.
	if err := eng.TerminateAgent(foreignID); err != nil {
		t.Fatalf("TerminateAgent (foreign): %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for processAlive(foreignPid) {
		if time.Now().After(deadline) {
			t.Fatalf("foreign pid %d still alive after TerminateAgent", foreignPid)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Logf("foreign pid=%d confirmed dead", foreignPid)

	m.tick()
	task, _ = m.Get(tasks[0].ID)
	if task.State != StateRunning {
		t.Fatalf("task state = %s, want running once the foreign agent's process was gone", task.State)
	}
	t.Logf("queued task dispatched once foreign pid=%d was confirmed dead: state=%s", foreignPid, task.State)
}

// TestLiveProofFailedSpawnDoesNotWedgeTheDirectory is review cycle 8 finding
// 1's live proof: a real engine.Engine, a real EngineRunner, and a real
// Manager. t1's backend binary does not exist, so engine.StartAgent inserts
// the agent record, agent.start() fails at cmd.Start() with cmd.Process nil,
// and dispatch fails the task — all before this fix, processExited() stayed
// false forever for that agent (no waitForExit ever ran to close done), so
// WorkDirOccupied reported the directory busy for the engine's lifetime and
// t2 could never dispatch into it. Smart routing has to be off for t1: with
// it on, StartAgent returns success immediately and the spawn failure
// surfaces later, asynchronously, which is a different (already-covered)
// path — this proof needs the synchronous one, so t1 pins an explicit model.
func TestLiveProofFailedSpawnDoesNotWedgeTheDirectory(t *testing.T) {
	dir := t.TempDir()
	eng := newMissingBinaryStubEngine()
	runner := NewEngineRunner(eng)
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	m := NewManager(runner, store)

	tasks, err := m.Add([]TaskSpec{
		{Name: "t1", Prompt: "p", WorkDir: dir, Opts: TaskOptions{Model: "sonnet"}},
		{Name: "t2", Prompt: "p", WorkDir: dir, Opts: TaskOptions{Model: "sonnet"}},
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	t1, t2 := tasks[0].ID, tasks[1].ID

	m.tick()
	task1, _ := m.Get(t1)
	t.Logf("t1 after dispatch attempt: state=%s error=%q", task1.State, task1.Error)
	if task1.State != StateFailed {
		t.Fatalf("t1 state = %s, want failed (missing binary must fail the spawn)", task1.State)
	}
	if got := eng.WorkDirOccupied(dir); got {
		t.Fatalf("WorkDirOccupied(%s) = true right after a failed spawn with no subprocess ever created", dir)
	}
	t.Logf("WorkDirOccupied(%s) = false after t1's spawn failed — directory correctly reported free", dir)

	m.tick()
	task2, _ := m.Get(t2)
	t.Logf("t2 after a second tick: state=%s", task2.State)
	if task2.State != StateRunning && task2.State != StateFailed {
		t.Fatalf("t2 state = %s, want it to have been dispatched (running or itself failed on the same missing binary) rather than wedged in ready", task2.State)
	}
	if task2.State == StateReady {
		t.Fatalf("t2 is still ready %d ticks after t1's failed spawn — the directory is wedged", 2)
	}
	t.Logf("t2 was dispatched rather than wedged: state=%s", task2.State)
}

func readPid(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 0 {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid file %s never appeared", pidFile)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// readPidExcluding polls pidFile until it holds a pid other than exclude.
// review cycle 7 finding 2: both live-proof tests dispatch a second agent
// into the same pidFile as the first, and dispatch marks the task running
// as soon as StartTask returns — before the newly spawned agent's own
// backend process has actually overwritten the file, since smart routing is
// on by default for a queued task and the agent sits in AgentRouting for a
// classifier round trip first. Plain readPid only waits for the file to
// exist, which it already does (with the first agent's pid), so it can
// return the first agent's pid as the second's — a false FAILURE ("pid2 ==
// pid1") today, but the same weakness could just as easily produce a false
// PASS if the timing ever tipped the other way. Waiting for the content to
// actually change removes the race outright.
func readPidExcluding(t *testing.T, pidFile string, exclude int) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 0 && pid != exclude {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid file %s never held a pid other than %d", pidFile, exclude)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
