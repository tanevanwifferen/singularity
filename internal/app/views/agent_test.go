package views

import (
	"context"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
	"gitlab.com/tanevanwifferen1/singularity/internal/service/fake"
)

// stubAgentService serves one agent whose state follows the daemon's: a
// follow-up to a finished agent puts it back in AgentRunning.
type stubAgentService struct {
	service.AgentService

	mu    sync.Mutex
	snap  service.AgentSnapshot
	sent  []string
	lists int

	// listGate, when set, blocks every List until it is closed — lets a test
	// hold one refresh open while further requests pile up.
	listGate chan struct{}
}

func (s *stubAgentService) List(context.Context) ([]service.AgentSnapshot, error) {
	s.mu.Lock()
	gate := s.listGate
	s.lists++
	snap := s.snap
	s.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return []service.AgentSnapshot{snap}, nil
}

func (s *stubAgentService) listCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lists
}

func (s *stubAgentService) Output(context.Context, string, int) ([]service.OutputEntry, error) {
	return []service.OutputEntry{{Timestamp: time.Now(), Source: "text", Content: "done"}}, nil
}

func (s *stubAgentService) MaxAgents(context.Context) (int, error) { return 4, nil }

func (s *stubAgentService) SendInput(_ context.Context, agentID, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, message)
	if s.snap.ID == agentID {
		s.snap.State = engine.AgentRunning
		s.snap.EndedAt = nil
	}
	return nil
}

// completedAgentView returns a view showing one selected, completed agent.
func completedAgentView(t *testing.T) (*AgentView, *stubAgentService) {
	t.Helper()

	ended := time.Now()
	started := ended.Add(-time.Minute)
	svc := &stubAgentService{snap: service.AgentSnapshot{
		ID:        "agent-1",
		State:     engine.AgentComplete,
		Task:      "initial task",
		Summary:   "initial task",
		StartedAt: &started,
		EndedAt:   &ended,
	}}
	svcs := fake.New()
	svcs.Agent = svc

	v := NewAgentView(t.TempDir())
	v.SetServices(svcs)
	v.loadAgents()
	if v.selectedAgent == nil {
		t.Fatal("expected the loaded agent to be auto-selected")
	}
	if v.selectedAgent.State != engine.AgentComplete {
		t.Fatalf("selected agent state = %v, want %v", v.selectedAgent.State, engine.AgentComplete)
	}
	return v, svc
}

// runCmd drains a tea.Cmd and feeds the resulting message back into the view,
// the way the bubbletea event loop would.
func runCmd(v *AgentView, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	_, next := v.Update(msg)
	return next
}

// Sending a follow-up to a finished agent must refresh the view straight
// away. Waiting for the 2s refresh tick leaves the list and the output-pane
// header reporting "done" for an agent the user has just given more work.
func TestFollowUpMessageRefreshesAgentState(t *testing.T) {
	v, svc := completedAgentView(t)

	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if !v.showMessageInput {
		t.Fatal("'i' did not open the message input")
	}
	for _, r := range "keep going" {
		v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	_, cmd := v.Update(tea.KeyMsg{Type: tea.KeyEnter})
	// enter → SendInput → messageSendDoneMsg → reload.
	for cmd != nil {
		cmd = runCmd(v, cmd)
	}

	if len(svc.sent) != 1 || svc.sent[0] != "keep going" {
		t.Fatalf("messages sent = %v, want [\"keep going\"]", svc.sent)
	}
	if v.selectedAgent.State != engine.AgentRunning {
		t.Errorf("selected agent state = %v, want %v", v.selectedAgent.State, engine.AgentRunning)
	}
	if len(v.agents) != 1 || v.agents[0].State != engine.AgentRunning {
		t.Errorf("agent list states = %v, want one %v", v.agents, engine.AgentRunning)
	}
	if v.err != nil {
		t.Errorf("unexpected view error: %v", v.err)
	}
}

// A failed send must surface the error and leave the stale state alone rather
// than reporting the agent as resumed.
func TestFollowUpMessageErrorIsReported(t *testing.T) {
	v, _ := completedAgentView(t)

	boom := context.DeadlineExceeded
	_, cmd := v.Update(messageSendDoneMsg{err: boom})
	if cmd != nil {
		t.Error("expected no reload after a failed send")
	}
	if v.err == nil {
		t.Fatal("expected the send error to be recorded")
	}
	if v.selectedAgent.State != engine.AgentComplete {
		t.Errorf("selected agent state = %v, want %v", v.selectedAgent.State, engine.AgentComplete)
	}
}

// A burst of refresh requests (one per agent event) must not queue up: each
// pass costs daemon round-trips and a glamour re-render, and a deep queue is
// what left the view rendering seconds-old state.
func TestConcurrentLoadAgentsCoalesce(t *testing.T) {
	v, svc := completedAgentView(t)

	gate := make(chan struct{})
	svc.mu.Lock()
	svc.listGate = gate
	before := svc.lists
	svc.mu.Unlock()

	first := make(chan struct{})
	go func() {
		defer close(first)
		v.loadAgents()
	}()

	// Wait for the held refresh to be inside List, then pile requests on.
	deadline := time.Now().Add(2 * time.Second)
	for svc.listCount() == before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if svc.listCount() == before {
		t.Fatal("first refresh never reached List")
	}

	for i := 0; i < 5; i++ {
		v.loadAgents() // must return immediately, not queue behind the first
	}

	close(gate)
	svc.mu.Lock()
	svc.listGate = nil
	svc.mu.Unlock()
	<-first

	// One held pass plus exactly one follow-up covering all five requests.
	if got := svc.listCount() - before; got != 2 {
		t.Errorf("List calls for 1 held + 5 queued refreshes = %d, want 2", got)
	}
}
