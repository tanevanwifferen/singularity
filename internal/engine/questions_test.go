package engine

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// startQuestionAgent starts an agent on the cat-backed stub and waits for it to
// be running.
func startQuestionAgent(t *testing.T, e *Engine, opts AgentOptions) string {
	t.Helper()
	if opts.Backend == nil {
		opts.Backend = stubBackend{}
	}
	id, err := e.StartAgent(t.TempDir(), "task", opts)
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, e, id, AgentRunning)
	return id
}

func waitForState(t *testing.T, e *Engine, id string, want AgentState) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := e.GetStatus(id); got == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	got, _ := e.GetStatus(id)
	t.Fatalf("agent %s state = %s, want %s", id, got, want)
}

// TestAgentBlockedUntilEveryQuestionAnswered is the feature in one test: an
// agent that asked two questions is blocked until both have an answer, and
// the batch reports done only then.
func TestAgentBlockedUntilEveryQuestionAnswered(t *testing.T) {
	e := New(5)
	defer e.Shutdown()
	id := startQuestionAgent(t, e, AgentOptions{})

	batch, err := e.AskQuestions(id, []string{"which database?", "  ", "keep the old API?"})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Questions) != 2 {
		t.Fatalf("batch has %d questions, want 2 (blank ones dropped)", len(batch.Questions))
	}
	if got, _ := e.GetStatus(id); got != AgentBlocked {
		t.Fatalf("state after asking = %s, want blocked", got)
	}
	if snap := e.GetAgent(id).Snapshot(); snap.State != AgentBlocked || !snap.State.Active() {
		t.Fatalf("snapshot state = %s (active=%v), want an active blocked agent", snap.State, snap.State.Active())
	}
	if e.Stats().Active != 1 {
		t.Errorf("a blocked agent must keep its slot; stats = %+v", e.Stats())
	}

	q1, q2 := batch.Questions[0], batch.Questions[1]
	if _, err := e.AnswerQuestion(q1.ID, "postgres"); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.GetStatus(id); got != AgentBlocked {
		t.Fatalf("state with one of two answered = %s, want still blocked", got)
	}
	if polled, _ := e.QuestionBatch(batch.ID); polled.Done {
		t.Fatal("batch reports done with a question still open")
	}
	if listed := e.ListQuestions(); len(listed) != 2 || !listed[0].Answered || listed[1].Answered {
		t.Fatalf("list = %+v, want q1 answered and q2 open", listed)
	}

	if _, err := e.AnswerQuestion(q1.ID, "again"); !errors.Is(err, ErrQuestionAnswered) {
		t.Errorf("answering twice = %v, want ErrQuestionAnswered", err)
	}
	if _, err := e.AnswerQuestion(q2.ID, "   "); !errors.Is(err, ErrQuestionInvalid) {
		t.Errorf("empty answer = %v, want ErrQuestionInvalid", err)
	}
	if _, err := e.AnswerQuestion("q999", "x"); !errors.Is(err, ErrQuestionNotFound) {
		t.Errorf("unknown question = %v, want ErrQuestionNotFound", err)
	}

	if _, err := e.AnswerQuestion(q2.ID, "yes"); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.GetStatus(id); got != AgentRunning {
		t.Fatalf("state with everything answered = %s, want running", got)
	}
	polled, err := e.QuestionBatch(batch.ID)
	if err != nil || !polled.Done {
		t.Fatalf("poll after the last answer = %+v, %v; want done", polled, err)
	}
	if polled.Questions[0].Answer != "postgres" || polled.Questions[1].Answer != "yes" {
		t.Errorf("answers = %+v", polled.Questions)
	}
	// A done batch is handed out once.
	if _, err := e.QuestionBatch(batch.ID); !errors.Is(err, ErrQuestionNotFound) {
		t.Errorf("second poll of a delivered batch = %v, want ErrQuestionNotFound", err)
	}
	if left := e.ListQuestions(); len(left) != 0 {
		t.Errorf("questions left after delivery: %+v", left)
	}
}

func TestAskQuestionsRejectsBadInput(t *testing.T) {
	e := New(5)
	defer e.Shutdown()
	id := startQuestionAgent(t, e, AgentOptions{})

	if _, err := e.AskQuestions("nope", []string{"q"}); err == nil {
		t.Error("asking for an unknown agent succeeded")
	}
	if _, err := e.AskQuestions(id, []string{"", " "}); !errors.Is(err, ErrQuestionInvalid) {
		t.Errorf("no questions = %v, want ErrQuestionInvalid", err)
	}
	if _, err := e.AskQuestions(id, make([]string, 0)); !errors.Is(err, ErrQuestionInvalid) {
		t.Errorf("empty list = %v, want ErrQuestionInvalid", err)
	}
	if got, _ := e.GetStatus(id); got != AgentRunning {
		t.Errorf("a refused ask left the agent %s", got)
	}
}

// TestQuestionsGoWithTheirAgent: a cancelled tool call and a dead agent both
// take their questions off the operator's list.
func TestQuestionsGoWithTheirAgent(t *testing.T) {
	e := New(5)
	defer e.Shutdown()
	id := startQuestionAgent(t, e, AgentOptions{})

	batch, err := e.AskQuestions(id, []string{"a?"})
	if err != nil {
		t.Fatal(err)
	}
	e.CancelQuestionBatch(batch.ID)
	e.CancelQuestionBatch(batch.ID) // idempotent
	if got, _ := e.GetStatus(id); got != AgentRunning {
		t.Fatalf("state after withdrawing the only question = %s, want running", got)
	}

	if _, err := e.AskQuestions(id, []string{"b?"}); err != nil {
		t.Fatal(err)
	}
	if err := e.TerminateAgent(id); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(e.ListQuestions()) > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if left := e.ListQuestions(); len(left) != 0 {
		t.Fatalf("a terminated agent's questions are still listed: %+v", left)
	}
	if _, err := e.AskQuestions(id, []string{"c?"}); !errors.Is(err, ErrQuestionInvalid) {
		t.Errorf("asking for a dead agent = %v, want ErrQuestionInvalid", err)
	}
}

// TestUnpolledQuestionsAreReaped: a batch whose MCP server stopped polling
// belongs to a tool call that is gone, and must not block the agent forever.
func TestUnpolledQuestionsAreReaped(t *testing.T) {
	oldStale, oldInterval := questionStaleAfter, questionReapInterval
	questionStaleAfter, questionReapInterval = 30*time.Millisecond, 10*time.Millisecond
	defer func() { questionStaleAfter, questionReapInterval = oldStale, oldInterval }()

	e := New(5)
	defer e.Shutdown()
	id := startQuestionAgent(t, e, AgentOptions{})
	if _, err := e.AskQuestions(id, []string{"anyone there?"}); err != nil {
		t.Fatal(err)
	}
	waitForState(t, e, id, AgentRunning)
	if left := e.ListQuestions(); len(left) != 0 {
		t.Errorf("reaped questions still listed: %+v", left)
	}
}

// TestTimeoutDoesNotRunWhileBlocked: the agent's timeout counts working
// time, not time spent waiting on the operator.
func TestTimeoutDoesNotRunWhileBlocked(t *testing.T) {
	e := New(5)
	defer e.Shutdown()
	id := startQuestionAgent(t, e, AgentOptions{Timeout: 300 * time.Millisecond})

	batch, err := e.AskQuestions(id, []string{"take your time?"})
	if err != nil {
		t.Fatal(err)
	}
	// Keep the batch polled, as the MCP server would, well past the timeout.
	time.Sleep(600 * time.Millisecond)
	if got, _ := e.GetStatus(id); got != AgentBlocked {
		t.Fatalf("state after outwaiting the timeout while blocked = %s, want blocked", got)
	}
	if _, err := e.AnswerQuestion(batch.Questions[0].ID, "done"); err != nil {
		t.Fatal(err)
	}
	// Unblocked, the clock runs again and the timeout still applies.
	waitForState(t, e, id, AgentKilled)
}

// mcpStubBackend is the stub with MCP support: `sh -c cat` ignores whatever
// is appended to its argv, so the agent still starts.
type mcpStubBackend struct{ stubBackend }

func (mcpStubBackend) Binary() string { return "sh" }
func (mcpStubBackend) Args(string, string, int, []string) []string {
	return []string{"-c", "cat"}
}
func (mcpStubBackend) MCPArgs(path string) []string { return []string{"--mcp-config", path} }

func TestMCPConfigWrittenForCapableBackends(t *testing.T) {
	e := New(5)
	defer e.Shutdown()
	e.SetQuestionBridge(QuestionBridge{Binary: "/opt/singularity", Server: "unix:///tmp/d.sock"})

	id := startQuestionAgent(t, e, AgentOptions{Backend: mcpStubBackend{}})
	agent := e.GetAgent(id)
	agent.mu.Lock()
	path, argv := agent.mcpConfigPath, agent.cmd.Args
	agent.mu.Unlock()
	if path == "" {
		t.Fatal("no MCP config written for an MCP-capable backend")
	}
	if n := len(argv); argv[n-2] != "--mcp-config" || argv[n-1] != path {
		t.Errorf("argv = %v, want it to end in --mcp-config %s", argv, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"singularity"`, `"/opt/singularity"`, `"mcp-questions"`, `"--agent","` + id + `"`, `"unix:///tmp/d.sock"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("config %s is missing %s", data, want)
		}
	}

	if err := e.TerminateAgent(id); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("config %s outlived its agent", filepath.Base(path))
	}

	// A backend without MCP support, or an engine without a bridge, starts
	// agents exactly as before.
	plain := startQuestionAgent(t, e, AgentOptions{})
	if p := e.GetAgent(plain).mcpConfigPath; p != "" {
		t.Errorf("config written for a backend that cannot load it: %s", p)
	}
	bare := New(5)
	defer bare.Shutdown()
	noBridge := startQuestionAgent(t, bare, AgentOptions{Backend: mcpStubBackend{}})
	if p := bare.GetAgent(noBridge).mcpConfigPath; p != "" {
		t.Errorf("config written without a bridge: %s", p)
	}
}

func TestClaudeAndHerdrLoadTheQuestionTool(t *testing.T) {
	for _, b := range []Backend{NewClaudeBackend(), NewHerdrBackend()} {
		mcp, ok := b.(MCPBackend)
		if !ok {
			t.Fatalf("%s backend does not implement MCPBackend", b.Name())
		}
		got := mcp.MCPArgs("/x.json")
		if n := len(got); n < 2 || got[n-2] != "--mcp-config" || got[n-1] != "/x.json" {
			t.Errorf("%s MCPArgs must end with --mcp-config <path>: %v", b.Name(), got)
		}
		// The built-in dialog would block the pane with nobody watching.
		if i := slices.Index(got, "--disallowedTools"); i < 0 || got[i+1] != builtinQuestionTool {
			t.Errorf("%s MCPArgs leaves claude's own question dialog on: %v", b.Name(), got)
		}
	}
	if _, ok := NewPiBackend("").(MCPBackend); ok {
		t.Error("pi has no MCP support and must not claim it")
	}
	if !hasEnv(NewClaudeBackend().Env(), "MCP_TOOL_TIMEOUT="+mcpToolTimeoutMS) {
		t.Error("claude env does not lift the MCP tool timeout")
	}
	aware, ok := NewHerdrBackend().(BlockAwareBackend)
	if !ok {
		t.Fatal("herdr backend does not implement BlockAwareBackend")
	}
	if string(aware.BlockedInput(true)) != "B:1\n" || string(aware.BlockedInput(false)) != "B:0\n" {
		t.Errorf("herdr blocked lines = %q / %q", aware.BlockedInput(true), aware.BlockedInput(false))
	}
}
