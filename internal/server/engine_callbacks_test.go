package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
)

// fakeClaudeOnPath installs a stand-in for the claude CLI that speaks just
// enough stream-json for the engine: it announces a session, answers the
// initial task with one result event, then holds its stdin open without
// answering again. That leaves the agent parked in AgentComplete with a live
// process — the state a follow-up message has to resume from — and keeps the
// second turn from racing the assertions.
func fakeClaudeOnPath(t *testing.T) {
	t.Helper()

	bin := t.TempDir()
	script := `#!/bin/sh
echo '{"type":"system","subtype":"init","session_id":"fake","model":"fake"}'
IFS= read -r _line
echo '{"type":"result","subtype":"success","is_error":false}'
while IFS= read -r _line; do :; done
`
	path := filepath.Join(bin, "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// wsFrames dials the server's /ws endpoint and returns a channel of decoded
// frames plus a close func.
func wsFrames(t *testing.T, srv *httptest.Server) <-chan api.WSMessage {
	t.Helper()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	out := make(chan api.WSMessage, 64)
	go func() {
		defer close(out)
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg api.WSMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			out <- msg
		}
	}()
	return out
}

// awaitFrame waits for a frame of the given type, failing the test on timeout.
func awaitFrame(t *testing.T, frames <-chan api.WSMessage, typ string) api.WSMessage {
	t.Helper()

	deadline := time.After(15 * time.Second)
	for {
		select {
		case msg, ok := <-frames:
			if !ok {
				t.Fatalf("ws closed while waiting for %q", typ)
			}
			if msg.Type == typ {
				return msg
			}
		case <-deadline:
			t.Fatalf("timed out waiting for a %q frame", typ)
		}
	}
}

func awaitState(t *testing.T, e *engine.Engine, id string, want engine.AgentState) {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if a := e.GetAgent(id); a != nil && a.Snapshot().State == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("agent %s never reached state %v", id, want)
}

// A follow-up to a finished agent must reach WS subscribers as its own event.
// agent_output frames carry no state, so a client that has already seen
// agent_complete would otherwise have to re-poll to notice the agent resumed.
func TestBroadcastAgentUpdateEmitsResumedAfterFollowUp(t *testing.T) {
	fakeClaudeOnPath(t)

	workDir := t.TempDir()
	s := New("", workDir)
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	frames := wsFrames(t, srv)

	// Pin the backend: the engine's default is pi, and this test must talk to
	// the fake claude on PATH rather than a real agent CLI.
	id, err := s.engine.StartAgent(workDir, "initial task", engine.AgentOptions{
		BackendName: "claude",
	})
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	t.Cleanup(func() { _ = s.engine.RemoveAgent(id) })

	complete := awaitFrame(t, frames, api.WSEventAgentComplete)
	if got := payloadField(t, complete, "agent_id"); got != id {
		t.Fatalf("agent_complete for %q, want %q", got, id)
	}

	if err := s.engine.SendInput(id, "keep going"); err != nil {
		t.Fatalf("SendInput: %v", err)
	}

	resumed := awaitFrame(t, frames, api.WSEventAgentResumed)
	if got := payloadField(t, resumed, "agent_id"); got != id {
		t.Errorf("agent_resumed for %q, want %q", got, id)
	}
	if got := payloadField(t, resumed, "state"); got != engine.AgentRunning.String() {
		t.Errorf("agent_resumed state = %q, want %q", got, engine.AgentRunning.String())
	}
}

// The resume event is claimed under the same lock as the terminal re-arm, so
// repeated callbacks for an agent that is simply still running must not
// produce a second one.
func TestBroadcastAgentUpdateResumedEmittedOnce(t *testing.T) {
	fakeClaudeOnPath(t)

	workDir := t.TempDir()
	s := New("", workDir)
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	frames := wsFrames(t, srv)

	// Pin the backend: the engine's default is pi, and this test must talk to
	// the fake claude on PATH rather than a real agent CLI.
	id, err := s.engine.StartAgent(workDir, "initial task", engine.AgentOptions{
		BackendName: "claude",
	})
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	t.Cleanup(func() { _ = s.engine.RemoveAgent(id) })

	awaitFrame(t, frames, api.WSEventAgentComplete)

	if err := s.engine.SendInput(id, "keep going"); err != nil {
		t.Fatalf("SendInput: %v", err)
	}
	awaitState(t, s.engine, id, engine.AgentRunning)
	awaitFrame(t, frames, api.WSEventAgentResumed)

	// Further callbacks while the agent stays running are not resumes.
	for i := 0; i < 3; i++ {
		s.broadcastAgentUpdate(id)
	}
	for {
		select {
		case msg, ok := <-frames:
			if !ok {
				return
			}
			if msg.Type == api.WSEventAgentResumed {
				t.Fatal("agent_resumed emitted twice for one resume")
			}
		case <-time.After(300 * time.Millisecond):
			return
		}
	}
}

// payloadField reads one string field out of a decoded frame payload.
func payloadField(t *testing.T, msg api.WSMessage, field string) string {
	t.Helper()

	m, ok := msg.Payload.(map[string]interface{})
	if !ok {
		t.Fatalf("payload of %q is %T, want object", msg.Type, msg.Payload)
	}
	v, _ := m[field].(string)
	return v
}
