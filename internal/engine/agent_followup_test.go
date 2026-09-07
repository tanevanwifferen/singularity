package engine

import (
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

// notifyLog records what an observer could see each time the agent notified
// it: the state and how much output existed at that moment. The daemon's WS
// broadcast works exactly this way (snapshot on notification), so this is the
// shape of the contract that matters.
type notifyLog struct {
	mu    sync.Mutex
	seen  []notifySample
	agent *Agent
}

type notifySample struct {
	state     AgentState
	outputLen int
}

func (n *notifyLog) hook() {
	n.mu.Lock()
	defer n.mu.Unlock()
	snap := n.agent.Snapshot()
	n.agent.outputMu.Lock()
	l := len(n.agent.output)
	n.agent.outputMu.Unlock()
	n.seen = append(n.seen, notifySample{state: snap.State, outputLen: l})
}

func (n *notifyLog) samples() []notifySample {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]notifySample(nil), n.seen...)
}

// completedAgent returns an agent parked in AgentComplete with a live process
// whose stdin is still open — the state the engine leaves a claude agent in
// after it reports a result but before RemoveAgent.
func completedAgent(t *testing.T) (*Agent, *notifyLog) {
	t.Helper()

	cmd := exec.Command("cat")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start cat: %v", err)
	}
	t.Cleanup(func() {
		stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	a := newAgent("test-followup", os.TempDir(), "task", AgentOptions{}, NewClaudeBackend())
	a.cmd = cmd
	a.stdin = stdin
	ended := time.Now()
	a.State = AgentComplete
	a.EndedAt = &ended

	log := &notifyLog{agent: a}
	a.notify = log.hook
	return a, log
}

func TestSendInputNotifiesResumeTransition(t *testing.T) {
	a, log := completedAgent(t)

	if err := a.sendInput("keep going"); err != nil {
		t.Fatalf("sendInput: %v", err)
	}

	if got := a.Snapshot().State; got != AgentRunning {
		t.Fatalf("state after follow-up = %v, want %v", got, AgentRunning)
	}

	// The resume must be notified as a state change of its own, not merely
	// implied by the user_input entry appended after it.
	var found bool
	for _, s := range log.samples() {
		if s.state == AgentRunning && s.outputLen == 0 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no notification observed the resume before the input echo; samples=%v", log.samples())
	}
}

func TestSendInputRollbackNotifiesRestoredState(t *testing.T) {
	a, log := completedAgent(t)

	// Break stdin so the write fails and sendInput has to undo the resume.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	r.Close()
	a.stdinMu.Lock()
	a.stdin = w
	a.stdinMu.Unlock()
	t.Cleanup(func() { w.Close() })

	if err := a.sendInput("keep going"); err == nil {
		t.Fatal("expected sendInput to fail on a broken stdin")
	}

	if got := a.Snapshot().State; got != AgentComplete {
		t.Fatalf("state after failed follow-up = %v, want %v", got, AgentComplete)
	}

	samples := log.samples()
	if len(samples) == 0 {
		t.Fatal("rollback was not notified: an observer that saw the optimistic AgentRunning would stay stuck on it")
	}
	last := samples[len(samples)-1]
	if last.state != AgentComplete {
		t.Errorf("last notification observed state %v, want %v", last.state, AgentComplete)
	}
}

// A follow-up to an agent that was already mid-response is not a resume, so
// there is nothing to roll back and nothing extra to announce.
func TestSendInputRunningAgentDoesNotNotifyRollback(t *testing.T) {
	a, log := completedAgent(t)
	a.mu.Lock()
	a.State = AgentRunning
	a.EndedAt = nil
	a.mu.Unlock()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	r.Close()
	a.stdinMu.Lock()
	a.stdin = w
	a.stdinMu.Unlock()
	t.Cleanup(func() { w.Close() })

	if err := a.sendInput("keep going"); err == nil {
		t.Fatal("expected sendInput to fail on a broken stdin")
	}
	if got := a.Snapshot().State; got != AgentRunning {
		t.Fatalf("state = %v, want %v", got, AgentRunning)
	}
	if got := len(log.samples()); got != 0 {
		t.Errorf("expected no notifications for a non-resume send, got %d: %v", got, log.samples())
	}
}
