package engine

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestActiveCountIncludesRoutingAgents pins the number anything gating on
// capacity has to use. Routing agents hold a pool slot, so ActiveCount must
// match the count StartAgent's own cap check performs — and, since both
// derive from the same AgentState.Active() predicate, Stats().Active must
// report the identical number. The two used to be computed independently,
// and a scheduler sizing its dispatch on the stats number over-dispatched
// for the whole classifier round trip and then had its spawn refused.
func TestActiveCountIncludesRoutingAgents(t *testing.T) {
	e := New(2)

	a1 := newAgent("routing-1", os.TempDir(), "task1", AgentOptions{}, NewClaudeBackend())
	a1.State = AgentRouting
	a2 := newAgent("routing-2", os.TempDir(), "task2", AgentOptions{}, NewClaudeBackend())
	a2.State = AgentRouting

	e.mu.Lock()
	e.agents[a1.ID] = a1
	e.agents[a2.ID] = a2
	e.mu.Unlock()

	if got := e.ActiveCount(); got != 2 {
		t.Errorf("ActiveCount = %d, want 2 — routing agents hold a slot", got)
	}
	if got := e.Stats().Active; got != 2 {
		t.Errorf("Stats().Active = %d, want 2 — it must report the same count as ActiveCount", got)
	}

	_, err := e.StartAgent(os.TempDir(), "task3", AgentOptions{})
	if !errors.Is(err, ErrAgentLimit) {
		t.Fatalf("StartAgent error = %v, want ErrAgentLimit — the cap gates on the ActiveCount number", err)
	}
	if !strings.Contains(err.Error(), "2/2") {
		t.Errorf("error %q should report the counts it refused on", err)
	}
}

// TestEveryObserverSeesTerminalTransition guards the observer fan-out: the
// primary slot (the daemon's WS broadcast) and every AddAgentObserver
// listener (the queue scheduler) must each see a terminal transition that
// arrives immediately after a burst of output. The debounce coalesces the
// burst, so the risk is the terminal notification being swallowed with it.
func TestEveryObserverSeesTerminalTransition(t *testing.T) {
	e := New(2)

	// Recorded per observer: whether it was notified at all, and whether
	// any of its notifications showed the agent already terminal. Exact
	// call counts are not the contract — the debounce deliberately
	// coalesces a burst — but seeing the terminal state is.
	var mu sync.Mutex
	seen := make(map[string]bool)
	sawTerminal := make(map[string]bool)
	record := func(name string) func(string) {
		return func(agentID string) {
			terminal := false
			if a := e.GetAgent(agentID); a != nil {
				switch a.Snapshot().State {
				case AgentComplete, AgentError, AgentKilled:
					terminal = true
				}
			}
			mu.Lock()
			seen[name+":"+agentID] = true
			if terminal {
				sawTerminal[name+":"+agentID] = true
			}
			mu.Unlock()
		}
	}

	e.OnAgentUpdate(record("primary"))
	removeA := e.AddAgentObserver(record("extra-a"))
	e.AddAgentObserver(record("extra-b"))

	a := newAgent("obs-1", os.TempDir(), "task", AgentOptions{}, NewClaudeBackend())
	a.notify = func() { e.notifyUpdate(a.ID) }
	e.mu.Lock()
	e.agents[a.ID] = a
	e.mu.Unlock()

	for i := 0; i < 20; i++ {
		a.appendOutput("assistant", "chatter")
	}
	a.mu.Lock()
	a.setState(AgentComplete)
	a.mu.Unlock()
	a.notify()

	names := []string{"primary", "extra-a", "extra-b"}
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		done := len(sawTerminal) == len(names)
		mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, name := range names {
		key := name + ":" + a.ID
		if !seen[key] {
			t.Errorf("observer %s never saw agent %s", name, a.ID)
			continue
		}
		if !sawTerminal[key] {
			t.Errorf("observer %s saw agent %s but never with its terminal state — the burst swallowed the transition", name, a.ID)
		}
	}

	// The remove closure is idempotent: calling it twice must not panic or
	// drop the observer registered after it.
	removeA()
	removeA()
	if got := len(e.observers()); got != 2 {
		t.Errorf("observers after two removes = %d, want 2 (primary + extra-b)", got)
	}
}

// TestObserverRegisteredDuringDebounceStillFires: notifyUpdate re-reads the
// observer set when the timer fires rather than capturing it up front, so a
// listener that registers inside an open debounce window still gets that
// notification. The queue scheduler registers at daemon start, while agents
// restored from a previous lifetime may already be chattering.
func TestObserverRegisteredDuringDebounceStillFires(t *testing.T) {
	e := New(2)
	e.OnAgentUpdate(func(string) {})

	a := newAgent("obs-2", os.TempDir(), "task", AgentOptions{}, NewClaudeBackend())
	a.notify = func() { e.notifyUpdate(a.ID) }
	e.mu.Lock()
	e.agents[a.ID] = a
	e.mu.Unlock()

	a.appendOutput("assistant", "first")

	late := make(chan string, 1)
	e.AddAgentObserver(func(agentID string) {
		select {
		case late <- agentID:
		default:
		}
	})

	select {
	case got := <-late:
		if got != a.ID {
			t.Errorf("late observer got %q, want %q", got, a.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("observer registered during the debounce window never fired")
	}
}
