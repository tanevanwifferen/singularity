package engine

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/oneshot"
)

// TestMain neuters the package's default summariser. The engine's default
// backend is pi, so any test that spawns with default AgentOptions would
// otherwise make a real cheap-model call — money, and a live subprocess.
// Tests that want the real path install it on their own engine explicitly.
func TestMain(m *testing.M) {
	defaultSummarizer = func(Backend, string) string { return "" }
	os.Exit(m.Run())
}

// preamblePrompt is the shape that produced the bug: several agents whose real
// task differs but whose first line is identical worktree boilerplate.
const preamblePrompt = "You are working in the git worktree /home/owner/.worktrees/singularity/fix-agent-summary-without-routing/singularity\n" +
	"on branch fix/agent-summary-without-routing, cut from master.\n\n" +
	"Decouple task summarisation from the smart-routing decision.\n"

// oneShotSpy replaces the package's one-shot seam with a thread-safe stub.
// The summariser runs on its own goroutine, so a plain captured variable
// (as in oneshot_test.go's stubOneShot) would race under -race.
type oneShotSpy struct {
	mu      sync.Mutex
	calls   int
	prompts []string
	answer  string
	err     error
}

func (s *oneShotSpy) install(t *testing.T) {
	t.Helper()
	prev := oneShotPrompt
	t.Cleanup(func() { oneShotPrompt = prev })
	oneShotPrompt = func(_ context.Context, _ Backend, req oneshot.Request) (string, error) {
		s.mu.Lock()
		s.calls++
		s.prompts = append(s.prompts, req.Prompt)
		s.mu.Unlock()
		return s.answer, s.err
	}
}

func (s *oneShotSpy) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *oneShotSpy) firstPrompt() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.prompts) == 0 {
		return ""
	}
	return s.prompts[0]
}

// waitForSummary polls the agent snapshot until Summary equals want.
func waitForSummary(t *testing.T, e *Engine, id, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		got = e.GetAgent(id).Snapshot().Summary
		if got == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("summary = %q after 3s, want %q", got, want)
}

// TestSummaryWithModelPinnedAndRoutingOff is the regression test for the
// reported bug: --model disables smart routing, which used to be the only
// producer of an AI summary, leaving the prompt's first line as the title.
func TestSummaryWithModelPinnedAndRoutingOff(t *testing.T) {
	spy := &oneShotSpy{answer: "Decouple summarisation from routing"}
	spy.install(t)

	e := New(5)
	e.summarize = generateTaskSummary
	t.Cleanup(e.Shutdown)
	id, err := e.StartAgent(t.TempDir(), preamblePrompt, AgentOptions{
		Model:   "sonnet",
		Backend: stubBackend{},
	})
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}

	waitForSummary(t, e, id, "Decouple summarisation from routing")

	if !strings.Contains(spy.firstPrompt(), "Decouple task summarisation") {
		t.Errorf("summariser prompt did not carry the task: %q", spy.firstPrompt())
	}
	if n := spy.callCount(); n != 1 {
		t.Errorf("one-shot calls = %d, want exactly 1", n)
	}
}

// TestSummaryFallsBackToPromptLine pins the deterministic fallback: a failing
// summariser must leave extractSummary's result in place and never block or
// fail the spawn.
func TestSummaryFallsBackToPromptLine(t *testing.T) {
	spy := &oneShotSpy{err: errors.New("pi not installed")}
	spy.install(t)

	e := New(5)
	e.summarize = generateTaskSummary
	t.Cleanup(e.Shutdown)
	id, err := e.StartAgent(t.TempDir(), preamblePrompt, AgentOptions{
		Model:   "sonnet",
		Backend: stubBackend{},
	})
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}

	want := extractSummary(preamblePrompt)
	// Give the failing call time to land before asserting nothing changed.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := e.GetAgent(id).Snapshot().Summary; got != want {
			t.Fatalf("summary = %q, want the fallback %q", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if spy.callCount() == 0 {
		t.Error("summariser was never called")
	}
}

// TestSummaryNotGeneratedWhenSupplied covers the queue and Jira flows, which
// pass their own titles: no cheap-model call may be made for them.
func TestSummaryNotGeneratedWhenSupplied(t *testing.T) {
	spy := &oneShotSpy{answer: "should never be used"}
	spy.install(t)

	e := New(5)
	e.summarize = generateTaskSummary
	t.Cleanup(e.Shutdown)
	id, err := e.StartAgent(t.TempDir(), preamblePrompt, AgentOptions{
		Model:   "sonnet",
		Summary: "Refine: PROJ-123",
		Backend: stubBackend{},
	})
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	if got := e.GetAgent(id).Snapshot().Summary; got != "Refine: PROJ-123" {
		t.Errorf("summary = %q, want the caller-supplied title", got)
	}
	if n := spy.callCount(); n != 0 {
		t.Errorf("one-shot calls = %d, want 0 for a caller-supplied summary", n)
	}
}

// TestSummarizeAsyncOnlyFiresOnce pins the cost guard directly: repeated calls
// for one agent must collapse to a single cheap-model call.
func TestSummarizeAsyncOnlyFiresOnce(t *testing.T) {
	spy := &oneShotSpy{answer: "Do the thing"}
	spy.install(t)

	a := newAgent("agent-1", t.TempDir(), preamblePrompt, AgentOptions{}, stubBackend{})
	for i := 0; i < 5; i++ {
		a.summarizeAsync(generateTaskSummary, stubBackend{})
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && a.Snapshot().Summary != "Do the thing" {
		time.Sleep(5 * time.Millisecond)
	}
	if got := a.Snapshot().Summary; got != "Do the thing" {
		t.Fatalf("summary = %q, want %q", got, "Do the thing")
	}
	if n := spy.callCount(); n != 1 {
		t.Errorf("one-shot calls = %d, want exactly 1", n)
	}
}

// TestSummaryNotifiesObserver checks the summary change reaches the engine's
// observer, so the WS/refresh path is poked rather than waiting for the next
// unrelated event.
func TestSummaryNotifiesObserver(t *testing.T) {
	spy := &oneShotSpy{answer: "Decouple summarisation from routing"}
	spy.install(t)

	e := New(5)
	e.summarize = generateTaskSummary
	t.Cleanup(e.Shutdown)
	notified := make(chan string, 16)
	e.OnAgentUpdate(func(id string) {
		select {
		case notified <- id:
		default:
		}
	})

	id, err := e.StartAgent(t.TempDir(), preamblePrompt, AgentOptions{
		Model:   "sonnet",
		Backend: stubBackend{},
	})
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	waitForSummary(t, e, id, "Decouple summarisation from routing")

	select {
	case got := <-notified:
		if got != id {
			t.Errorf("observer notified for %q, want %q", got, id)
		}
	case <-time.After(2 * time.Second):
		t.Error("observer was never notified")
	}
}

func TestSanitizeSummary(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain", input: "Fix the poller retry logic", want: "Fix the poller retry logic"},
		{name: "quoted", input: `"Fix the poller"`, want: "Fix the poller"},
		{name: "markdown header", input: "## Fix the poller", want: "Fix the poller"},
		{name: "bullet", input: "- Fix the poller", want: "Fix the poller"},
		{name: "extra commentary dropped", input: "Fix the poller\nThis is a bug fix task.", want: "Fix the poller"},
		{name: "leading blank lines", input: "\n\n  Fix the poller  ", want: "Fix the poller"},
		{name: "empty", input: "   \n  ", want: ""},
		{
			name:  "overlong truncated",
			input: strings.Repeat("a", 100),
			want:  strings.Repeat("a", 77) + "...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeSummary(tt.input); got != tt.want {
				t.Errorf("sanitizeSummary(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTruncateForSummaryKeepsRuneBoundary(t *testing.T) {
	// "é" is two bytes: a 2-byte cut lands mid-rune and must back up to 1.
	if got := truncateForSummary("aé", 2); got != "a" {
		t.Errorf("truncateForSummary = %q, want %q", got, "a")
	}
	if got := truncateForSummary("abc", 10); got != "abc" {
		t.Errorf("truncateForSummary = %q, want %q", got, "abc")
	}
}

// routingBackend makes the (unseamed) classifier hermetic: its one-shot argv is
// an `echo` of a canned classification, so RoutePrompt succeeds without a
// model call.
type routingBackend struct{ stubBackend }

func (routingBackend) OneShotCommand(string) (string, []string) {
	return "echo", []string{`{"category":"implementation","effort":"low","reason":"r","summary":"Routed title"}`}
}

// TestSummaryFromRoutingIsNotDuplicated pins the no-double-spend rule: when the
// classifier already returned a title, the one-shot summariser must not run.
func TestSummaryFromRoutingIsNotDuplicated(t *testing.T) {
	spy := &oneShotSpy{answer: "should never be used"}
	spy.install(t)

	e := New(5)
	e.summarize = generateTaskSummary
	t.Cleanup(e.Shutdown)
	id, err := e.StartAgent(t.TempDir(), preamblePrompt, AgentOptions{
		SmartRoute: true,
		Backend:    routingBackend{},
	})
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}

	waitForSummary(t, e, id, "Routed title")
	if n := spy.callCount(); n != 0 {
		t.Errorf("one-shot calls = %d, want 0 when routing supplied the title", n)
	}
}

// TestSummaryAfterRoutingFailure covers the other half of the decoupling: with
// routing on but failing, the title must still come from the summariser rather
// than from the prompt's first line.
func TestSummaryAfterRoutingFailure(t *testing.T) {
	spy := &oneShotSpy{answer: "Decouple summarisation from routing"}
	spy.install(t)

	e := New(5)
	e.summarize = generateTaskSummary
	t.Cleanup(e.Shutdown)
	// stubBackend's one-shot argv is `true`: no output, so classification fails.
	id, err := e.StartAgent(t.TempDir(), preamblePrompt, AgentOptions{
		SmartRoute: true,
		Backend:    stubBackend{},
	})
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}

	waitForSummary(t, e, id, "Decouple summarisation from routing")
	if n := spy.callCount(); n != 1 {
		t.Errorf("one-shot calls = %d, want exactly 1", n)
	}
}

// TestRoutingHonoursPinnedModelButTakesEffortAndSummary pins the decoupled
// routing semantics: --model overrides only the classifier's model choice, so
// its effort and summary still apply.
func TestRoutingHonoursPinnedModelButTakesEffortAndSummary(t *testing.T) {
	spy := &oneShotSpy{answer: "should never be used"}
	spy.install(t)

	e := New(5)
	e.summarize = generateTaskSummary
	t.Cleanup(e.Shutdown)
	id, err := e.StartAgent(t.TempDir(), preamblePrompt, AgentOptions{
		Model:      "sonnet",
		SmartRoute: true,
		Backend:    routingBackend{},
	})
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}

	waitForSummary(t, e, id, "Routed title")

	a := e.GetAgent(id)
	a.mu.Lock()
	model, effort := a.model, a.effort
	a.mu.Unlock()
	if model != "sonnet" {
		t.Errorf("model = %q, want the pinned %q", model, "sonnet")
	}
	// routingBackend's canned classification says effort "low".
	if effort != "low" {
		t.Errorf("effort = %q, want the classifier's %q", effort, "low")
	}
	if n := spy.callCount(); n != 0 {
		t.Errorf("one-shot calls = %d, want 0 when routing supplied the title", n)
	}
}

// TestNoRoutingWhenModelAndEffortPinned covers the other end of the truth
// table: the classifier would decide nothing, so only the one-shot summariser
// runs.
func TestNoRoutingWhenModelAndEffortPinned(t *testing.T) {
	spy := &oneShotSpy{answer: "Decouple summarisation from routing"}
	spy.install(t)

	e := New(5)
	e.summarize = generateTaskSummary
	t.Cleanup(e.Shutdown)
	id, err := e.StartAgent(t.TempDir(), preamblePrompt, AgentOptions{
		Model:      "sonnet",
		Effort:     "high",
		SmartRoute: true,
		Backend:    routingBackend{},
	})
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}

	waitForSummary(t, e, id, "Decouple summarisation from routing")
	if snap := e.GetAgent(id).Snapshot(); snap.RouteResult != nil {
		t.Errorf("RouteResult = %+v, want nil (classifier must not run)", snap.RouteResult)
	}
	if n := spy.callCount(); n != 1 {
		t.Errorf("one-shot calls = %d, want exactly 1", n)
	}
}

func TestRoutedField(t *testing.T) {
	if got := routedField("opus", "opus"); got != "opus" {
		t.Errorf("routedField = %q, want %q", got, "opus")
	}
	if got := routedField("haiku", "opus"); got != "haiku (pinned, classifier: opus)" {
		t.Errorf("routedField = %q, want the pinned form", got)
	}
	if got := routedField("haiku", ""); got != "haiku" {
		t.Errorf("routedField = %q, want %q", got, "haiku")
	}
}
