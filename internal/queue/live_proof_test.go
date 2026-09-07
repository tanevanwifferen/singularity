package queue

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

	pid2 := readPid(t, pidFile)
	if pid2 == pid1 {
		t.Fatalf("pid2 == pid1 (%d): t2 did not actually get a new process", pid1)
	}
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
