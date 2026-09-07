package engine

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// processAlive reports whether pid is still a live process. Signal 0 is the
// portable existence probe: it validates the target without delivering
// anything.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func startStubAgent(t *testing.T, e *Engine) (string, int) {
	t.Helper()
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not available")
	}
	id, err := e.StartAgent(t.TempDir(), "task", AgentOptions{Backend: stubBackend{}})
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	a := e.GetAgent(id)
	if a == nil {
		t.Fatalf("agent %s not registered", id)
	}
	a.mu.Lock()
	pid := 0
	if a.cmd != nil && a.cmd.Process != nil {
		pid = a.cmd.Process.Pid
	}
	a.mu.Unlock()
	if pid == 0 {
		t.Fatal("agent has no subprocess")
	}
	return id, pid
}

// TestKillAgentLeavesTheProcessAlive pins the soft-close contract the TUI
// depends on, so the difference from TerminateAgent is documented by a test
// rather than only by a comment.
func TestKillAgentLeavesTheProcessAlive(t *testing.T) {
	e := New(2)
	id, pid := startStubAgent(t, e)
	t.Cleanup(func() { _ = e.RemoveAgent(id) })

	if err := e.KillAgent(id); err != nil {
		t.Fatalf("KillAgent: %v", err)
	}
	// No wait needed: softClose does not touch the process, so if it were
	// going to die it would already be dying.
	time.Sleep(50 * time.Millisecond)
	if !processAlive(pid) {
		t.Fatalf("pid %d is gone: KillAgent must soft-close, leaving the process addressable", pid)
	}
	if got := e.GetAgent(id).Snapshot().State; got != AgentKilled {
		t.Errorf("state after KillAgent = %s, want killed", got)
	}
}

// TestTerminateAgentKillsTheProcess is the queue's requirement: after the
// call the OS process is really gone, so the agent no longer occupies its
// working directory, while the record survives for its transcript.
func TestTerminateAgentKillsTheProcess(t *testing.T) {
	e := New(2)
	id, pid := startStubAgent(t, e)

	if err := e.TerminateAgent(id); err != nil {
		t.Fatalf("TerminateAgent: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("pid %d still alive 2s after TerminateAgent", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}

	a := e.GetAgent(id)
	if a == nil {
		t.Fatal("TerminateAgent dropped the agent record; the transcript has to stay reachable")
	}
	if got := a.Snapshot().State; got != AgentKilled {
		t.Errorf("state after TerminateAgent = %s, want killed", got)
	}
	if a.IsActive() {
		t.Error("a terminated agent must not report as active")
	}
	if len(e.ActiveAgents()) != 0 {
		t.Errorf("ActiveAgents = %d, want 0", len(e.ActiveAgents()))
	}
	if _, err := os.Stat(a.Snapshot().WorkDir); err != nil {
		t.Errorf("work dir should still exist: %v", err)
	}
}

// TestTerminateAgentReleasesARoutingAgent covers the case kill() alone
// cannot: an agent cancelled while the classifier is still choosing its
// model has no subprocess yet, and leaving its state untouched would keep
// IsActive true forever — an engine slot and a working directory held by an
// agent that will never run.
func TestTerminateAgentReleasesARoutingAgent(t *testing.T) {
	routing := newAgent("a-routing", t.TempDir(), "task", AgentOptions{}, stubBackend{})
	routing.setState(AgentRouting)
	if !routing.IsActive() {
		t.Fatal("a routing agent is expected to count as active")
	}
	if err := routing.terminate(); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if routing.IsActive() {
		t.Error("terminate left a routing agent active")
	}
	if got := routing.Snapshot().State; got != AgentKilled {
		t.Errorf("state after terminate = %s, want killed", got)
	}
	// The routing goroutine calls start() when the classifier returns;
	// having been terminated, the agent must refuse to launch.
	if err := routing.start(); err == nil {
		t.Error("start() succeeded on a terminated agent")
	}

	// softClose, by contrast, cannot move a routing agent out of active.
	// That asymmetry is why the queue needs its own call rather than
	// reusing the TUI's.
	soft := newAgent("a-soft", t.TempDir(), "task", AgentOptions{}, stubBackend{})
	soft.setState(AgentRouting)
	soft.softClose()
	if !soft.IsActive() {
		t.Error("softClose released a routing agent; if that is now intended, terminate's nil-cmd branch can be simplified")
	}
}
