package engine

import (
	"encoding/base64"
	"strings"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/config"
)

func newHerdrTestBackend(t *testing.T) *herdrBackend {
	t.Helper()
	SetModels(config.DefaultModelsConfig())
	t.Cleanup(func() { SetModels(nil) })
	return &herdrBackend{name: "singltest"}
}

func TestHerdrArgsBuildsDriverInvocation(t *testing.T) {
	b := newHerdrTestBackend(t)
	args := b.Args("sonnet", "high", 0, []string{"Read", "Edit"})

	joined := strings.Join(args, " ")
	if args[0] != "herdr-driver" {
		t.Fatalf("Args()[0] = %q, want herdr-driver", args[0])
	}
	for _, want := range []string{
		"--name singltest",
		"--kind claude",
		// Everything after `--` is claude's own flags, passed through
		// herdr's own `--` to the agent.
		"-- --permission-mode bypassPermissions",
		"--model sonnet",
		"--effort high",
		"--allowedTools Read --allowedTools Edit",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q\nfull args: %v", want, args)
		}
	}
	// No shell is involved on this path any more, so nothing is quoted.
	if strings.Contains(joined, "'") {
		t.Errorf("args contain shell quoting, which nothing on this path needs: %v", args)
	}
}

func TestHerdrBinaryIsTheSingularityBinary(t *testing.T) {
	b := newHerdrTestBackend(t)
	// The driver is this binary re-invoked (see herdr_driver.go). Which path
	// resolves depends on how the test binary was built, so the assertion is
	// only that it names singularity rather than a shell.
	if got := b.Binary(); !strings.HasSuffix(got, "singularity") {
		t.Errorf("Binary() = %q, want the singularity binary", got)
	}
}

func TestHerdrArgsWarnsOnMaxTurns(t *testing.T) {
	b := newHerdrTestBackend(t)
	b.Args("", "", 5, nil)

	events, err := b.ParseEvent([]byte("__SINGL_INIT__"))
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (warning + init)", len(events))
	}
	if events[0].Kind != BackendError || !strings.Contains(events[0].Content, "max_turns") {
		t.Errorf("events[0] = %+v, want a max_turns warning", events[0])
	}
	if events[1].Kind != BackendSessionInit {
		t.Errorf("events[1] = %+v, want BackendSessionInit", events[1])
	}
}

func TestHerdrParseEventSessionInitOnce(t *testing.T) {
	b := newHerdrTestBackend(t)
	b.launchModel = "anthropic/claude-sonnet-5"

	events, err := b.ParseEvent(b64Line("__SINGL_INIT__", "w1:p1"))
	if err != nil || len(events) != 1 || events[0].Kind != BackendSessionInit {
		t.Fatalf("first init: events=%+v err=%v", events, err)
	}
	if events[0].SessionID != "singltest" || events[0].Model != "anthropic/claude-sonnet-5" || events[0].PaneID != "w1:p1" {
		t.Errorf("init event = %+v, want SessionID=singltest Model=anthropic/claude-sonnet-5 PaneID=w1:p1", events[0])
	}

	events, err = b.ParseEvent(b64Line("__SINGL_INIT__", "w1:p1"))
	if err != nil {
		t.Fatalf("second init: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("second __SINGL_INIT__ produced %+v, want no events (initOnce)", events)
	}
}

// TestHerdrNewForAgentIsolatesConcurrentAgents covers the reason
// PerAgentBackend exists: one instance is installed as the engine's default
// backend, so without a per-agent copy two concurrent agents would share a
// herdr agent name (which herdr requires to be unique among live agents) and
// share this instance's one-shot session-init guard.
func TestHerdrNewForAgentIsolatesConcurrentAgents(t *testing.T) {
	template := newHerdrTestBackend(t)

	first, ok := Backend(template).(PerAgentBackend)
	if !ok {
		t.Fatal("herdrBackend must implement PerAgentBackend or the shared engine default will collide")
	}
	a := first.NewForAgent("agent-1757338800-1").(*herdrBackend)
	b := first.NewForAgent("agent-1757338800-2").(*herdrBackend)

	if a.name == b.name {
		t.Errorf("both agents got the herdr agent name %q; herdr rejects a duplicate live name", a.name)
	}
	if template.name != "singltest" {
		t.Errorf("template name changed to %q; NewForAgent must not mutate the receiver", template.name)
	}

	// Each instance reports its own session init, which a shared initOnce
	// would have swallowed for the second agent.
	for _, backend := range []*herdrBackend{a, b} {
		events, err := backend.ParseEvent(b64Line("__SINGL_INIT__", "w1:p1"))
		if err != nil {
			t.Fatalf("ParseEvent: %v", err)
		}
		if len(events) != 1 || events[0].Kind != BackendSessionInit || events[0].SessionID != backend.name {
			t.Errorf("agent %q init events = %+v, want one BackendSessionInit naming its own session",
				backend.name, events)
		}
	}
}

func TestHerdrAgentNameIsValidForHerdr(t *testing.T) {
	// herdr requires [a-z][a-z0-9_-]{0,31}.
	cases := map[string]string{
		"agent-1757338800-3":   "singl-agent-1757338800-3",
		"Agent-With-CAPS":      "singl-agent-with-caps",
		"weird/id with spaces": "singl-weird-id-with-spaces",
		// Over herdr's 32-char limit, so the tail (which holds the ID's
		// unique sequence number) is what survives.
		"agent-99999999999999999999999999999999": "s9999999999999999999999999999999",
	}
	for id, want := range cases {
		got := herdrAgentName(id)
		if got != want {
			t.Errorf("herdrAgentName(%q) = %q, want %q", id, got, want)
		}
		if len(got) > 32 {
			t.Errorf("herdrAgentName(%q) = %q is %d chars, over herdr's 32-char limit", id, got, len(got))
		}
		for i, r := range got {
			valid := (r >= 'a' && r <= 'z') || (i > 0 && (r >= '0' && r <= '9' || r == '_' || r == '-'))
			if !valid {
				t.Errorf("herdrAgentName(%q) = %q has an invalid character %q at %d", id, got, r, i)
			}
		}
	}
}

func b64Line(marker, payload string) []byte {
	return []byte(marker + base64.StdEncoding.EncodeToString([]byte(payload)))
}

func TestHerdrParseEventStateDone(t *testing.T) {
	b := newHerdrTestBackend(t)
	line := b64Line("__SINGL_STATE__", `{"id":"cli:agent:prompt","result":{"agent":{"agent_status":"done"}}}`)

	events, err := b.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if len(events) != 1 || events[0].Kind != BackendResult || events[0].IsResultError || events[0].Subtype != "success" {
		t.Fatalf("events = %+v, want one successful BackendResult", events)
	}
}

func TestHerdrParseEventStateBlockedAndError(t *testing.T) {
	b := newHerdrTestBackend(t)

	blocked := b64Line("__SINGL_STATE__", `{"result":{"agent":{"agent_status":"blocked"}}}`)
	events, err := b.ParseEvent(blocked)
	if err != nil {
		t.Fatalf("ParseEvent(blocked): %v", err)
	}
	if len(events) != 1 || events[0].Kind != BackendResult || !events[0].IsResultError {
		t.Fatalf("blocked events = %+v, want IsResultError=true", events)
	}

	herdrErr := b64Line("__SINGL_STATE__", `{"error":{"code":"agent_not_found","message":"agent target x not found"}}`)
	events, err = b.ParseEvent(herdrErr)
	if err != nil {
		t.Fatalf("ParseEvent(error): %v", err)
	}
	if len(events) != 1 || !events[0].IsResultError || !strings.Contains(events[0].Content, "agent_not_found") {
		t.Fatalf("error events = %+v, want an IsResultError result mentioning agent_not_found", events)
	}
}

func TestHerdrParseEventOutputEmitsOneEventPerLine(t *testing.T) {
	b := newHerdrTestBackend(t)

	// The driver already diffed this chunk against the previous pane read
	// (herdrDiffSnapshot), so ParseEvent only splits it into lines and drops
	// the blank ones the TUI pads its frame with.
	events, err := b.ParseEvent(b64Line("__SINGL_OUT__", "line one\n\nline two\n"))
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	var texts []string
	for _, ev := range events {
		if ev.Kind == BackendText {
			texts = append(texts, ev.Content)
		}
	}
	if strings.Join(texts, "|") != "line one|line two" {
		t.Fatalf("text = %v, want [line one, line two]", texts)
	}
}

func TestHerdrParseEventErrorMarker(t *testing.T) {
	b := newHerdrTestBackend(t)
	events, err := b.ParseEvent(b64Line("__SINGL_ERR__", "herdr agent read: pane_not_found"))
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if len(events) != 1 || events[0].Kind != BackendError || !strings.Contains(events[0].Content, "pane_not_found") {
		t.Fatalf("events = %+v, want one BackendError carrying the driver's message", events)
	}
}

func TestHerdrParseEventUnknownLineIgnored(t *testing.T) {
	b := newHerdrTestBackend(t)
	events, err := b.ParseEvent([]byte(`{"id":"cli:agent:start","result":{}}`))
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if len(events) != 1 || events[0].Kind != BackendIgnore {
		t.Fatalf("events = %+v, want a single BackendIgnore", events)
	}
}

func TestHerdrUnattendedSessionCommandErrors(t *testing.T) {
	b := newHerdrTestBackend(t)
	if _, _, err := b.UnattendedSessionCommand("do something"); err == nil {
		t.Fatal("UnattendedSessionCommand: want error, got nil")
	}
}

func TestHerdrOneShotCommandIsClaudePrint(t *testing.T) {
	b := newHerdrTestBackend(t)
	binary, args := b.OneShotCommand("hello")
	// This is deliberately claude's own headless mode, not herdr: it works
	// under API/enterprise auth and fails only on the Max-plan auth this
	// backend exists for. cmd/singl/prime.md says so to operators.
	if binary != "claude" || strings.Join(args, " ") == "" || args[0] != "--print" {
		t.Fatalf("OneShotCommand = %q %v, want claude --print …", binary, args)
	}
}

func TestHerdrTerminationGraceIsPositive(t *testing.T) {
	b := newHerdrTestBackend(t)
	graceful, ok := Backend(b).(GracefulBackend)
	if !ok {
		t.Fatal("herdrBackend must implement GracefulBackend, or a killed agent leaks its pane and claude session")
	}
	if graceful.TerminationGrace() <= 0 {
		t.Errorf("TerminationGrace() = %v, want a positive grace period", graceful.TerminationGrace())
	}
}

func TestHerdrInitialInputEncodesBase64(t *testing.T) {
	b := newHerdrTestBackend(t)
	data, err := b.InitialInput("hello\nworld \"quoted\"", "")
	if err != nil {
		t.Fatalf("InitialInput: %v", err)
	}
	line := strings.TrimSuffix(string(data), "\n")
	if !strings.HasPrefix(line, "T:") {
		t.Fatalf("InitialInput = %q, want a T: line", line)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "T:"))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(decoded) != "hello\nworld \"quoted\"" {
		t.Errorf("decoded = %q, want original task text unchanged", decoded)
	}
}

func TestBackendByName(t *testing.T) {
	for name, wantName := range map[string]string{
		"claude": "claude",
		"pi":     "pi",
		"herdr":  "herdr",
	} {
		b := BackendByName(name)
		if b == nil {
			t.Errorf("BackendByName(%q) = nil, want a backend", name)
			continue
		}
		if b.Name() != wantName {
			t.Errorf("BackendByName(%q).Name() = %q, want %q", name, b.Name(), wantName)
		}
	}
	if b := BackendByName("nope"); b != nil {
		t.Errorf("BackendByName(\"nope\") = %v, want nil so callers fall back to the engine default", b)
	}
}
