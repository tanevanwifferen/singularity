package engine

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/config"
)

// piGetStateLine is a real line captured from `pi --mode rpc --no-session
// --model anthropic/claude-sonnet-4-5`, in reply to the get_state command
// PostStartCommands sends at start-up.
const piGetStateLine = `{"id":"singularity-session-init","type":"response","command":"get_state","success":true,"data":{"model":{"id":"claude-sonnet-4-5","name":"Claude Sonnet 4.5 (latest)","api":"anthropic-messages","provider":"anthropic","baseUrl":"https://api.anthropic.com","reasoning":true,"input":["text","image"],"cost":{"input":3,"output":15,"cacheRead":0.3,"cacheWrite":3.75},"contextWindow":1000000,"maxTokens":64000,"compat":{"supportsStrictTools":true}},"thinkingLevel":"high","isStreaming":false,"isCompacting":false,"steeringMode":"one-at-a-time","followUpMode":"one-at-a-time","sessionId":"01a0580e-85f0-7258-8aa7-61e7b12f3142","autoCompactionEnabled":true,"messageCount":0,"pendingMessageCount":0}}`

// newPiTestBackend returns a piBackend with the compiled-in model table
// installed, so tests never touch the user's models.json.
func newPiTestBackend(t *testing.T) *piBackend {
	t.Helper()
	SetModels(config.DefaultModelsConfig())
	t.Cleanup(func() { SetModels(nil) })
	return &piBackend{}
}

func TestPiResolveModel(t *testing.T) {
	b := newPiTestBackend(t)

	tests := []struct {
		name  string
		model string
		want  string
	}{
		{"sonnet alias", "sonnet", "anthropic/claude-sonnet-4-5"},
		{"opus alias", "opus", "anthropic/claude-opus-4-5"},
		{"haiku alias", "haiku", "anthropic/claude-haiku-4-5"},
		{"alias is case-insensitive", "Sonnet", "anthropic/claude-sonnet-4-5"},
		{"qualified id passes through", "anthropic/claude-opus-4-8", "anthropic/claude-opus-4-8"},
		{"other provider passes through", "openai/gpt-5", "openai/gpt-5"},
		{"unknown short name passes through", "gpt-nonsense", "gpt-nonsense"},
		{"empty stays empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := b.resolveModel(tt.model); got != tt.want {
				t.Errorf("resolveModel(%q) = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}

func TestPiResolveModelUsesConfiguredTable(t *testing.T) {
	SetModels(&config.ModelsConfig{
		Version: 1,
		Backends: map[string]config.BackendModels{
			"pi": {Aliases: map[string]string{"sonnet": "openai/gpt-5-codex"}},
		},
	})
	t.Cleanup(func() { SetModels(nil) })

	b := &piBackend{}
	if got := b.resolveModel("sonnet"); got != "openai/gpt-5-codex" {
		t.Errorf("resolveModel(sonnet) = %q, want the configured override", got)
	}
	// Aliases the file omits still fall back to the compiled-in table.
	if got := b.resolveModel("opus"); got != "anthropic/claude-opus-4-5" {
		t.Errorf("resolveModel(opus) = %q, want the default fallback", got)
	}
}

func TestPiResolveTools(t *testing.T) {
	tests := []struct {
		name        string
		in          []string
		wantTools   []string
		wantDropped []string
	}{
		{"claude names translate", []string{"Read", "Grep", "Glob", "Bash"},
			[]string{"read", "grep", "find", "bash"}, nil},
		{"pi names pass through", []string{"read", "edit", "ls"},
			[]string{"read", "edit", "ls"}, nil},
		{"duplicates collapse", []string{"Edit", "MultiEdit", "edit"},
			[]string{"edit"}, nil},
		{"unknown names are reported", []string{"Read", "WebFetch", "Task"},
			[]string{"read"}, []string{"WebFetch", "Task"}},
		{"all unknown yields no tools", []string{"WebSearch"},
			nil, []string{"WebSearch"}},
		{"blank entries are skipped", []string{" ", "Bash"},
			[]string{"bash"}, nil},
		{"empty input", nil, nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools, dropped := piResolveTools(tt.in)
			if !reflect.DeepEqual(tools, tt.wantTools) {
				t.Errorf("tools = %v, want %v", tools, tt.wantTools)
			}
			if !reflect.DeepEqual(dropped, tt.wantDropped) {
				t.Errorf("dropped = %v, want %v", dropped, tt.wantDropped)
			}
		})
	}
}

func TestPiArgs(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		effort       string
		maxTurns     int
		allowedTools []string
		wantArgs     []string
		wantWarnings []string // substrings, one per expected warning
	}{
		{
			name:     "defaults",
			wantArgs: []string{"--mode", "rpc", "--no-session"},
		},
		{
			name:     "model alias is resolved",
			model:    "opus",
			wantArgs: []string{"--mode", "rpc", "--no-session", "--model", "anthropic/claude-opus-4-5"},
		},
		{
			name:     "effort never becomes a launch flag",
			effort:   "high",
			wantArgs: []string{"--mode", "rpc", "--no-session"},
		},
		{
			name:         "allowed tools become a --tools allowlist",
			allowedTools: []string{"Read", "Grep", "Glob", "Bash"},
			wantArgs:     []string{"--mode", "rpc", "--no-session", "--tools", "read,grep,find,bash"},
		},
		{
			name:         "max turns is reported, not dropped",
			maxTurns:     15,
			wantArgs:     []string{"--mode", "rpc", "--no-session"},
			wantWarnings: []string{"max_turns=15"},
		},
		{
			name:         "untranslatable tools are reported",
			allowedTools: []string{"Read", "WebFetch"},
			wantArgs:     []string{"--mode", "rpc", "--no-session", "--tools", "read"},
			wantWarnings: []string{"WebFetch"},
		},
		{
			name:         "an allowlist matching nothing is not applied",
			allowedTools: []string{"WebFetch"},
			wantArgs:     []string{"--mode", "rpc", "--no-session"},
			wantWarnings: []string{"WebFetch", "no pi tool"},
		},
		{
			name:         "both gaps at once",
			model:        "sonnet",
			maxTurns:     20,
			allowedTools: []string{"Read", "Task"},
			wantArgs: []string{"--mode", "rpc", "--no-session",
				"--model", "anthropic/claude-sonnet-4-5", "--tools", "read"},
			wantWarnings: []string{"max_turns=20", "Task"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newPiTestBackend(t)

			got := b.Args(tt.model, tt.effort, tt.maxTurns, tt.allowedTools)
			if !reflect.DeepEqual(got, tt.wantArgs) {
				t.Errorf("Args() = %v, want %v", got, tt.wantArgs)
			}

			warnings := b.takeWarnings()
			if len(warnings) != len(tt.wantWarnings) {
				t.Fatalf("got %d warnings %v, want %d", len(warnings), warnings, len(tt.wantWarnings))
			}
			for i, want := range tt.wantWarnings {
				if !strings.Contains(warnings[i], want) {
					t.Errorf("warning %d = %q, want it to mention %q", i, warnings[i], want)
				}
			}
		})
	}
}

func TestPiArgsWarningsReachTheEventStream(t *testing.T) {
	b := newPiTestBackend(t)
	b.Args("sonnet", "", 15, []string{"WebFetch"})

	events, err := b.ParseEvent([]byte(piGetStateLine))
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("got %d events, want 3 warnings + 1 session_init: %+v", len(events), events)
	}
	for i, want := range []string{"max_turns=15", "WebFetch", "no pi tool"} {
		if events[i].Kind != BackendError {
			t.Errorf("event %d kind = %s, want %s", i, events[i].Kind, BackendError)
		}
		if !strings.Contains(events[i].Content, want) {
			t.Errorf("event %d content = %q, want it to mention %q", i, events[i].Content, want)
		}
	}
	if events[3].Kind != BackendSessionInit {
		t.Errorf("last event kind = %s, want %s", events[3].Kind, BackendSessionInit)
	}

	// Warnings are emitted once, not on every line.
	events, err = b.ParseEvent([]byte(piGetStateLine))
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("warnings repeated: got %d events, want 1", len(events))
	}

	// A relaunch without the unsupported options warns about nothing.
	b.Args("sonnet", "", 0, nil)
	if warnings := b.takeWarnings(); len(warnings) != 0 {
		t.Errorf("stale warnings after relaunch: %v", warnings)
	}
}

func TestPiParseSessionInit(t *testing.T) {
	tests := []struct {
		name          string
		line          string
		wantKind      BackendEventKind
		wantModel     string
		wantSessionID string
	}{
		{
			name:          "real get_state response",
			line:          piGetStateLine,
			wantKind:      BackendSessionInit,
			wantModel:     "anthropic/claude-sonnet-4-5",
			wantSessionID: "01a0580e-85f0-7258-8aa7-61e7b12f3142",
		},
		{
			name:          "model without provider is not prefixed",
			line:          `{"type":"response","command":"get_state","success":true,"data":{"model":{"id":"local-model"},"sessionId":"abc"}}`,
			wantKind:      BackendSessionInit,
			wantModel:     "local-model",
			wantSessionID: "abc",
		},
		{
			name:          "session id without a model still counts",
			line:          `{"type":"response","command":"get_state","success":true,"data":{"model":null,"sessionId":"abc"}}`,
			wantKind:      BackendSessionInit,
			wantSessionID: "abc",
		},
		{
			name:     "other command acks are ignored",
			line:     `{"type":"response","command":"set_auto_retry","success":true}`,
			wantKind: BackendIgnore,
		},
		{
			name:     "failed get_state is ignored",
			line:     `{"type":"response","command":"get_state","success":false,"error":"nope"}`,
			wantKind: BackendIgnore,
		},
		{
			name:     "get_state without data is ignored",
			line:     `{"type":"response","command":"get_state","success":true}`,
			wantKind: BackendIgnore,
		},
		{
			name:     "empty state is ignored",
			line:     `{"type":"response","command":"get_state","success":true,"data":{}}`,
			wantKind: BackendIgnore,
		},
		{
			name:     "unrelated pi event is ignored",
			line:     `{"type":"thinking_level_changed","level":"high"}`,
			wantKind: BackendIgnore,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newPiTestBackend(t)

			events, err := b.ParseEvent([]byte(tt.line))
			if err != nil {
				t.Fatalf("ParseEvent: %v", err)
			}
			if len(events) != 1 {
				t.Fatalf("got %d events, want 1: %+v", len(events), events)
			}
			ev := events[0]
			if ev.Kind != tt.wantKind {
				t.Fatalf("kind = %s, want %s", ev.Kind, tt.wantKind)
			}
			if ev.Model != tt.wantModel {
				t.Errorf("model = %q, want %q", ev.Model, tt.wantModel)
			}
			if ev.SessionID != tt.wantSessionID {
				t.Errorf("session id = %q, want %q", ev.SessionID, tt.wantSessionID)
			}
		})
	}
}

func TestPiPostStartCommandsRequestState(t *testing.T) {
	b := newPiTestBackend(t)

	cmds := b.PostStartCommands("high")
	if len(cmds) == 0 {
		t.Fatal("no post-start commands")
	}

	last := cmds[len(cmds)-1]
	if !strings.HasSuffix(string(last), "\n") {
		t.Error("post-start command is not newline-terminated")
	}
	var cmd map[string]interface{}
	if err := json.Unmarshal(last, &cmd); err != nil {
		t.Fatalf("unmarshal last command: %v", err)
	}
	if cmd["type"] != "get_state" {
		t.Errorf("last command type = %v, want get_state (it must run after the setters)", cmd["type"])
	}
	if cmd["id"] != piSessionInitID {
		t.Errorf("get_state id = %v, want %q", cmd["id"], piSessionInitID)
	}
}

func TestPiOneShotCommandUsesModelTable(t *testing.T) {
	SetModels(config.DefaultModelsConfig())
	t.Cleanup(func() { SetModels(nil) })

	tests := []struct {
		name         string
		oneShotModel string
		want         string
	}{
		{"table default", "", "anthropic/claude-haiku-4-5"},
		{"explicit override", "openai/gpt-5-mini", "openai/gpt-5-mini"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &piBackend{oneShotModel: tt.oneShotModel}
			binary, args := b.OneShotCommand("hello")
			if binary != "pi" {
				t.Errorf("binary = %q, want pi", binary)
			}
			var got string
			for i, a := range args {
				if a == "--model" && i+1 < len(args) {
					got = args[i+1]
				}
			}
			if got != tt.want {
				t.Errorf("--model = %q, want %q", got, tt.want)
			}
		})
	}
}
