// Package questionmcp is the MCP server behind `singularity mcp-questions`:
// a stdio server, launched by an agent's own CLI, with a single tool,
// ask_questions. The tool hands the agent's questions to the daemon, which
// marks the agent blocked and shows them to the operator, and returns only
// once every one of them has been answered. There is no timeout on that wait.
//
// The wire format is MCP's stdio transport: one JSON-RPC 2.0 message per
// line, requests in on stdin, responses out on stdout.
package questionmcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Asker is the daemon as this server needs it. *client.Client satisfies it
// through the adapter in run.go; tests supply their own.
type Asker interface {
	// Ask registers the questions and returns the batch id to poll.
	Ask(ctx context.Context, agentID string, questions []string) (batchID string, err error)
	// Poll reports whether the batch is fully answered, and the answers.
	// gone is true when the daemon no longer knows the batch.
	Poll(ctx context.Context, batchID string) (answers []QA, done, gone bool, err error)
	// Cancel withdraws a batch the agent stopped waiting for.
	Cancel(ctx context.Context, batchID string) error
}

// QA is one answered question.
type QA struct {
	Question string
	Answer   string
}

const (
	toolName        = "ask_questions"
	protocolVersion = "2024-11-05"
	// maxPollFailures is how many polls in a row may fail before the tool
	// call gives up: at the default interval, a minute of unreachable daemon.
	maxPollFailures = 60
)

const toolDescription = "Ask the human operator one or more questions and wait for their answers. " +
	"Use this when you cannot proceed responsibly without a decision or information only the operator has: " +
	"an ambiguous requirement, a choice between approaches with real trade-offs, a missing credential or " +
	"value, or confirmation before something destructive. Ask everything you need in ONE call — pass " +
	"several questions at once rather than calling repeatedly. Each question is answered in free text, so " +
	"make each one self-contained and specific, and name the options if there are any. The call blocks " +
	"until every question is answered, which may take a long time; that is expected, do not work around it."

// Server serves one agent's MCP session.
type Server struct {
	AgentID string
	Asker   Asker
	// PollInterval is how often an open batch is checked; zero means 1s.
	PollInterval time.Duration

	outMu sync.Mutex
	out   *bufio.Writer

	// calls maps an in-flight tools/call request id to its cancel func, so
	// a notifications/cancelled can stop the wait. wg tracks those calls.
	callsMu sync.Mutex
	calls   map[string]context.CancelFunc
	wg      sync.WaitGroup
}

// waitCalls blocks until every in-flight tool call has returned.
func (s *Server) waitCalls() { s.wg.Wait() }

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve reads requests from in until EOF and writes responses to out. On the
// way out every question still waiting is withdrawn, so an agent that died
// mid-question does not leave it on the operator's list.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	s.out = bufio.NewWriter(out)
	s.calls = make(map[string]context.CancelFunc)
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		s.wg.Wait()
	}()

	r := bufio.NewReader(in)
	for {
		line, err := r.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var req rpcRequest
			if jerr := json.Unmarshal(line, &req); jerr != nil {
				s.reply(nil, nil, &rpcError{Code: -32700, Message: "parse error"})
			} else {
				s.dispatch(ctx, req)
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (s *Server) dispatch(ctx context.Context, req rpcRequest) {
	isNotification := len(req.ID) == 0
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := p.ProtocolVersion
		if version == "" {
			version = protocolVersion
		}
		s.reply(req.ID, map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "singularity", "version": "1"},
		}, nil)
	case "ping":
		s.reply(req.ID, map[string]any{}, nil)
	case "tools/list":
		s.reply(req.ID, map[string]any{"tools": []any{toolSpec()}}, nil)
	case "tools/call":
		callCtx, cancel := context.WithCancel(ctx)
		key := string(req.ID)
		s.callsMu.Lock()
		s.calls[key] = cancel
		s.callsMu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() {
				s.callsMu.Lock()
				delete(s.calls, key)
				s.callsMu.Unlock()
				cancel()
			}()
			s.handleCall(callCtx, req)
		}()
	case "notifications/cancelled":
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		_ = json.Unmarshal(req.Params, &p)
		s.callsMu.Lock()
		if cancel := s.calls[string(p.RequestID)]; cancel != nil {
			cancel()
		}
		s.callsMu.Unlock()
	default:
		if !isNotification {
			s.reply(req.ID, nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method})
		}
	}
}

func toolSpec() map[string]any {
	return map[string]any{
		"name":        toolName,
		"description": toolDescription,
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"questions": map[string]any{
					"type":        "array",
					"minItems":    1,
					"items":       map[string]any{"type": "string"},
					"description": "The questions to ask, one self-contained question per entry.",
				},
			},
			"required": []string{"questions"},
		},
	}
}

// handleCall runs one tools/call to completion and replies to it — unless
// the call was cancelled, in which case MCP wants no response at all.
func (s *Server) handleCall(ctx context.Context, req rpcRequest) {
	var p struct {
		Name      string `json:"name"`
		Arguments struct {
			Questions []string `json:"questions"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.reply(req.ID, nil, &rpcError{Code: -32602, Message: "invalid params: " + err.Error()})
		return
	}
	if p.Name != toolName {
		s.reply(req.ID, nil, &rpcError{Code: -32602, Message: "unknown tool: " + p.Name})
		return
	}

	text, err := s.ask(ctx, p.Arguments.Questions)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		s.reply(req.ID, toolResult(err.Error(), true), nil)
		return
	}
	s.reply(req.ID, toolResult(text, false), nil)
}

// ask registers the questions and waits, without limit, for the answers.
func (s *Server) ask(ctx context.Context, questions []string) (string, error) {
	batchID, err := s.Asker.Ask(ctx, s.AgentID, questions)
	if err != nil {
		return "", fmt.Errorf("could not ask the operator: %v", err)
	}

	interval := s.PollInterval
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			// The agent stopped waiting (interrupted, or shutting down):
			// take the questions off the operator's list. ctx is dead, so
			// the withdrawal gets a short one of its own.
			cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.Asker.Cancel(cctx, batchID)
			cancel()
			return "", ctx.Err()
		case <-ticker.C:
		}

		answers, done, gone, err := s.Asker.Poll(ctx, batchID)
		switch {
		case ctx.Err() != nil:
			continue // handled by the select above
		case gone:
			return "", fmt.Errorf("the questions were withdrawn before the operator answered them; " +
				"ask again if you still need the answers")
		case err != nil:
			if failures++; failures >= maxPollFailures {
				return "", fmt.Errorf("lost contact with the singularity daemon while waiting for answers: %v", err)
			}
			continue
		}
		failures = 0
		if done {
			return formatAnswers(answers), nil
		}
	}
}

func formatAnswers(answers []QA) string {
	var b strings.Builder
	b.WriteString("The operator answered:\n")
	for i, qa := range answers {
		fmt.Fprintf(&b, "\nQ%d: %s\nA%d: %s\n", i+1, qa.Question, i+1, qa.Answer)
	}
	return b.String()
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
		"isError": isError,
	}
}

func (s *Server) reply(id json.RawMessage, result any, rpcErr *rpcError) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: id, Error: rpcErr}
	if rpcErr == nil {
		resp.Result = result
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	s.out.Write(data)
	s.out.WriteByte('\n')
	s.out.Flush()
}
