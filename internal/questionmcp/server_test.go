package questionmcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAsker is the daemon as the server sees it: questions are answered by
// the test calling answer.
type fakeAsker struct {
	mu        sync.Mutex
	asked     []string
	agentID   string
	answers   []QA
	done      bool
	gone      bool
	cancelled []string
}

func (f *fakeAsker) Ask(_ context.Context, agentID string, questions []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(questions) == 0 {
		return "", fmt.Errorf("at least one question is required")
	}
	f.agentID, f.asked = agentID, questions
	return "b1", nil
}

func (f *fakeAsker) Poll(context.Context, string) ([]QA, bool, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.answers, f.done, f.gone, nil
}

func (f *fakeAsker) Cancel(_ context.Context, batchID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, batchID)
	return nil
}

func (f *fakeAsker) answer(answers ...QA) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers, f.done = answers, true
}

// session runs a Server over pipes and speaks the client side of it.
type session struct {
	t      *testing.T
	in     *io.PipeWriter
	out    *bufio.Reader
	served chan error
}

func startSession(t *testing.T, asker Asker) *session {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &session{t: t, in: inW, out: bufio.NewReader(outR), served: make(chan error, 1)}
	srv := &Server{AgentID: "agent-7", Asker: asker, PollInterval: 5 * time.Millisecond}
	go func() {
		s.served <- srv.Serve(context.Background(), inR, outW)
		outW.Close()
	}()
	return s
}

func (s *session) send(msg string) {
	s.t.Helper()
	if _, err := io.WriteString(s.in, msg+"\n"); err != nil {
		s.t.Fatal(err)
	}
}

// recv reads one response, failing the test if none arrives in time.
func (s *session) recv() map[string]any {
	s.t.Helper()
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := s.out.ReadString('\n')
		ch <- result{line, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			s.t.Fatalf("read response: %v", r.err)
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(r.line), &msg); err != nil {
			s.t.Fatalf("response is not JSON: %q", r.line)
		}
		return msg
	case <-time.After(3 * time.Second):
		s.t.Fatal("no response within 3s")
		return nil
	}
}

func (s *session) close() {
	s.t.Helper()
	s.in.Close()
	select {
	case err := <-s.served:
		if err != nil {
			s.t.Fatalf("Serve: %v", err)
		}
	case <-time.After(3 * time.Second):
		s.t.Fatal("Serve did not return after stdin closed")
	}
}

func resultText(t *testing.T, msg map[string]any) (string, bool) {
	t.Helper()
	res, ok := msg["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", msg)
	}
	content := res["content"].([]any)[0].(map[string]any)
	isErr, _ := res["isError"].(bool)
	return content["text"].(string), isErr
}

func TestHandshakeAndToolList(t *testing.T) {
	s := startSession(t, &fakeAsker{})
	defer s.close()

	s.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{}}}`)
	init := s.recv()["result"].(map[string]any)
	if init["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v, want the client's echoed back", init["protocolVersion"])
	}
	if _, ok := init["capabilities"].(map[string]any)["tools"]; !ok {
		t.Errorf("capabilities = %v, want tools", init["capabilities"])
	}

	// A notification gets no reply; the next response must be the list's.
	s.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	s.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	list := s.recv()
	if list["id"].(float64) != 2 {
		t.Fatalf("got a reply to id %v, want 2 (a notification was answered?)", list["id"])
	}
	tools := list["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "ask_questions" {
		t.Fatalf("tools = %v, want exactly ask_questions", tools)
	}

	s.send(`{"jsonrpc":"2.0","id":3,"method":"resources/list"}`)
	if s.recv()["error"] == nil {
		t.Error("an unknown method did not get an error")
	}
}

func TestAskBlocksUntilAnswered(t *testing.T) {
	asker := &fakeAsker{}
	s := startSession(t, asker)
	defer s.close()

	s.send(`{"jsonrpc":"2.0","id":"call-1","method":"tools/call","params":{"name":"ask_questions","arguments":{"questions":["which db?","keep v1?"]}}}`)

	// Nothing comes back while the questions are open: ping still answers,
	// and its reply is the first thing on the wire.
	time.Sleep(50 * time.Millisecond)
	s.send(`{"jsonrpc":"2.0","id":9,"method":"ping"}`)
	if got := s.recv()["id"]; got != float64(9) {
		t.Fatalf("first response has id %v, want the ping's — the tool call returned before any answer", got)
	}
	asker.mu.Lock()
	agentID, asked := asker.agentID, asker.asked
	asker.mu.Unlock()
	if agentID != "agent-7" || len(asked) != 2 {
		t.Fatalf("asked %v for %q, want both questions for agent-7", asked, agentID)
	}

	asker.answer(QA{"which db?", "postgres"}, QA{"keep v1?", "no"})
	msg := s.recv()
	if msg["id"] != "call-1" {
		t.Fatalf("response id = %v, want call-1", msg["id"])
	}
	text, isErr := resultText(t, msg)
	if isErr {
		t.Fatalf("tool call failed: %s", text)
	}
	for _, want := range []string{"Q1: which db?", "A1: postgres", "Q2: keep v1?", "A2: no"} {
		if !strings.Contains(text, want) {
			t.Errorf("result %q is missing %q", text, want)
		}
	}
}

func TestCancelledCallWithdrawsItsQuestions(t *testing.T) {
	asker := &fakeAsker{}
	s := startSession(t, asker)

	s.send(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"ask_questions","arguments":{"questions":["still there?"]}}}`)
	time.Sleep(30 * time.Millisecond)
	s.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":5}}`)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		asker.mu.Lock()
		n := len(asker.cancelled)
		asker.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	asker.mu.Lock()
	cancelled := append([]string(nil), asker.cancelled...)
	asker.mu.Unlock()
	if len(cancelled) != 1 || cancelled[0] != "b1" {
		t.Fatalf("cancelled batches = %v, want [b1]", cancelled)
	}

	// A cancelled request gets no response: the next one on the wire is the ping's.
	s.send(`{"jsonrpc":"2.0","id":6,"method":"ping"}`)
	if got := s.recv()["id"]; got != float64(6) {
		t.Errorf("a cancelled call was answered (next response id %v)", got)
	}
	s.close()
}

func TestStdinClosingWithdrawsOpenQuestions(t *testing.T) {
	asker := &fakeAsker{}
	s := startSession(t, asker)
	s.send(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"ask_questions","arguments":{"questions":["?"]}}}`)
	time.Sleep(30 * time.Millisecond)
	s.close()
	asker.mu.Lock()
	defer asker.mu.Unlock()
	if len(asker.cancelled) != 1 {
		t.Fatalf("cancelled batches = %v, want the open one withdrawn on shutdown", asker.cancelled)
	}
}

func TestToolErrorsAreReportedToTheAgent(t *testing.T) {
	asker := &fakeAsker{gone: true}
	s := startSession(t, asker)
	defer s.close()

	s.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ask_questions","arguments":{"questions":["?"]}}}`)
	if text, isErr := resultText(t, s.recv()); !isErr || !strings.Contains(text, "withdrawn") {
		t.Errorf("withdrawn batch → %q (isError=%v), want an error saying so", text, isErr)
	}

	s.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ask_questions","arguments":{"questions":[]}}}`)
	if text, isErr := resultText(t, s.recv()); !isErr {
		t.Errorf("empty question list → %q, want an error result", text)
	}

	s.send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"other","arguments":{}}}`)
	if s.recv()["error"] == nil {
		t.Error("an unknown tool did not get a protocol error")
	}
}
