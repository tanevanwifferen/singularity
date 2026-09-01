package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

// piSessionInitID correlates the get_state command issued at start-up with its
// response, which is where pi reports the resolved model and session id.
const piSessionInitID = "singularity-session-init"

// piToolAliases maps a tool name from an agent's allowed-tools list to pi's
// built-in tool name. pi's built-ins are read, bash, powershell, edit, write,
// grep, find and ls (pi docs: usage.md, settings.md); the Claude-style names
// callers use (Read, Glob, ...) have to be translated or the --tools allowlist
// would match nothing and disable every tool. pi's own names pass through.
var piToolAliases = map[string]string{
	"read":       "read",
	"write":      "write",
	"edit":       "edit",
	"multiedit":  "edit",
	"bash":       "bash",
	"powershell": "powershell",
	"grep":       "grep",
	"glob":       "find",
	"find":       "find",
	"ls":         "ls",
}

// piEffortLevels maps AgentOptions effort strings to pi thinking levels.
var piEffortLevels = map[string]string{
	"low":    "low",
	"medium": "medium",
	"high":   "high",
}

// piBackend drives the pi CLI using --mode rpc.
// It keeps per-agent state for streaming text accumulation and in-flight tool calls.
type piBackend struct {
	oneShotModel string

	// option warnings queued by Args, flushed into the event stream by ParseEvent
	warnMu   sync.Mutex
	warnings []string

	// text accumulation across streaming deltas
	textBuf strings.Builder

	// in-flight tool call state (captured from tool_execution_start)
	pendingToolID   string
	pendingToolName string
	pendingToolArgs map[string]interface{}
}

func (b *piBackend) Name() string   { return "pi" }
func (b *piBackend) Binary() string { return "pi" }

func (b *piBackend) Args(model, effort string, maxTurns int, allowedTools []string) []string {
	b.clearWarnings()

	args := []string{"--mode", "rpc", "--no-session"}

	if model != "" {
		args = append(args, "--model", b.resolveModel(model))
	}

	// effort → thinking level is handled via PostStartCommands, not a launch flag.
	_ = effort

	// pi has no turn limit: no CLI flag, no RPC command and no setting exposes
	// one. Say so rather than dropping the option silently.
	if maxTurns > 0 {
		b.warn(fmt.Sprintf("max_turns=%d is not supported by the pi backend (pi has no turn limit); "+
			"the agent will run until it finishes — use a timeout to bound it", maxTurns))
	}

	if len(allowedTools) > 0 {
		tools, dropped := piResolveTools(allowedTools)
		if len(dropped) > 0 {
			b.warn(fmt.Sprintf("allowed_tools entries with no pi equivalent were dropped: %s "+
				"(pi built-ins: read, bash, powershell, edit, write, grep, find, ls)",
				strings.Join(dropped, ", ")))
		}
		if len(tools) == 0 {
			b.warn("allowed_tools resolved to no pi tool; the allowlist was not applied and " +
				"the agent runs with pi's default tools")
		} else {
			args = append(args, "--tools", strings.Join(tools, ","))
		}
	}

	return args
}

// piResolveTools translates an allowed-tools list into pi built-in tool names,
// preserving order and de-duplicating. Names with no pi equivalent (WebFetch,
// Task, ...) are returned separately so the caller can report them.
func piResolveTools(allowedTools []string) (tools, dropped []string) {
	seen := map[string]bool{}
	for _, tool := range allowedTools {
		name := strings.TrimSpace(tool)
		if name == "" {
			continue
		}
		resolved, ok := piToolAliases[strings.ToLower(name)]
		if !ok {
			dropped = append(dropped, name)
			continue
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		tools = append(tools, resolved)
	}
	return tools, dropped
}

// warn queues an option warning for the next ParseEvent call.
func (b *piBackend) warn(msg string) {
	b.warnMu.Lock()
	b.warnings = append(b.warnings, msg)
	b.warnMu.Unlock()
}

// clearWarnings drops queued warnings, so a relaunch does not repeat them.
func (b *piBackend) clearWarnings() {
	b.warnMu.Lock()
	b.warnings = nil
	b.warnMu.Unlock()
}

// takeWarnings returns and clears the queued warnings.
func (b *piBackend) takeWarnings() []string {
	b.warnMu.Lock()
	warnings := b.warnings
	b.warnings = nil
	b.warnMu.Unlock()
	return warnings
}

// resolveModel translates short model names to pi-compatible IDs using the
// configured model table. Already-qualified ids pass through untouched.
func (b *piBackend) resolveModel(model string) string {
	return Models().ResolveModel("pi", model)
}

func (b *piBackend) Env() []string {
	return os.Environ()
}

// PostStartCommands sends pi configuration commands before the initial task.
func (b *piBackend) PostStartCommands(effort string) [][]byte {
	var cmds [][]byte

	encode := func(v interface{}) []byte {
		data, _ := json.Marshal(v)
		return append(data, '\n')
	}

	// Enable auto-retry on transient errors (overloaded, rate-limit, 5xx).
	cmds = append(cmds, encode(map[string]interface{}{
		"type":    "set_auto_retry",
		"enabled": true,
	}))

	// Enable auto-compaction when the context window fills up.
	cmds = append(cmds, encode(map[string]interface{}{
		"type":    "set_auto_compaction",
		"enabled": true,
	}))

	// Map effort to thinking level when specified.
	if level, ok := piEffortLevels[strings.ToLower(effort)]; ok {
		cmds = append(cmds, encode(map[string]interface{}{
			"type":  "set_thinking_level",
			"level": level,
		}))
	}

	// pi emits no session-init event, so ask for the state once everything is
	// configured. The response carries the resolved model and the session id.
	cmds = append(cmds, encode(map[string]interface{}{
		"type": "get_state",
		"id":   piSessionInitID,
	}))

	return cmds
}

func (b *piBackend) InitialInput(task, _ string) ([]byte, error) {
	return b.encodePrompt(task)
}

// FollowUpInput uses "steer" when the agent is mid-response, "prompt" otherwise.
func (b *piBackend) FollowUpInput(message, _ string, isStreaming bool) ([]byte, error) {
	if isStreaming {
		data, err := json.Marshal(map[string]interface{}{
			"type":    "steer",
			"message": message,
		})
		if err != nil {
			return nil, fmt.Errorf("marshal pi steer: %w", err)
		}
		return append(data, '\n'), nil
	}
	return b.encodePrompt(message)
}

func (b *piBackend) encodePrompt(message string) ([]byte, error) {
	data, err := json.Marshal(map[string]interface{}{
		"type":    "prompt",
		"message": message,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal pi prompt: %w", err)
	}
	return append(data, '\n'), nil
}

// OneShotCommand runs a cheap one-shot prompt via pi --print. The model is the
// backend's configured one-shot model, falling back to the model table's entry
// for pi, so one-shot calls never inherit the agent's (expensive) model.
func (b *piBackend) OneShotCommand(prompt string) (string, []string) {
	model := b.oneShotModel
	if model == "" {
		model = Models().ClassifierModel("pi")
	}
	return "pi", []string{
		"--print",
		"--no-session",
		"--model", model,
		prompt,
	}
}

// ParseEvent parses one JSONL line from pi's RPC stdout. Option warnings queued
// by Args are flushed ahead of the first successfully parsed event so they reach
// the agent's output stream.
func (b *piBackend) ParseEvent(line []byte) ([]*BackendEvent, error) {
	events, err := b.parseEvent(line)
	if err != nil {
		return nil, err
	}
	warnings := b.takeWarnings()
	if len(warnings) == 0 {
		return events, nil
	}
	out := make([]*BackendEvent, 0, len(warnings)+len(events))
	for _, msg := range warnings {
		out = append(out, &BackendEvent{Kind: BackendError, Content: msg})
	}
	return append(out, events...), nil
}

func (b *piBackend) parseEvent(line []byte) ([]*BackendEvent, error) {
	var event map[string]interface{}
	if err := json.Unmarshal(line, &event); err != nil {
		return nil, fmt.Errorf("JSON parse: %w", err)
	}

	eventType, _ := event["type"].(string)

	switch eventType {
	case "message_update":
		return b.parseMessageUpdate(event)

	case "tool_execution_start":
		return b.parseToolStart(event)

	case "tool_execution_end":
		return b.parseToolEnd(event)

	case "agent_end":
		return b.parseAgentEnd(event)

	case "response":
		return b.parseResponse(event)

	case "auto_retry_end":
		success, _ := event["success"].(bool)
		if !success {
			finalErr, _ := event["finalError"].(string)
			if finalErr == "" {
				finalErr = "auto-retry exhausted"
			}
			return []*BackendEvent{{Kind: BackendError, Content: finalErr}}, nil
		}
		return []*BackendEvent{{Kind: BackendIgnore}}, nil

	case "extension_error":
		errMsg, _ := event["error"].(string)
		ext, _ := event["extensionPath"].(string)
		if ext != "" {
			errMsg = fmt.Sprintf("extension %s: %s", ext, errMsg)
		}
		return []*BackendEvent{{Kind: BackendError, Content: errMsg}}, nil

	default:
		// response acks, compaction events, queue_update, etc.
		return []*BackendEvent{{Kind: BackendIgnore}}, nil
	}
}

// parseResponse turns the get_state reply issued by PostStartCommands into a
// session-init event. pi has no session_init event of its own; the get_state
// response is the only message carrying both the resolved model and the
// session id. Other command responses are acks and carry nothing actionable.
func (b *piBackend) parseResponse(event map[string]interface{}) ([]*BackendEvent, error) {
	ignore := []*BackendEvent{{Kind: BackendIgnore}}

	if cmd, _ := event["command"].(string); cmd != "get_state" {
		return ignore, nil
	}
	if success, _ := event["success"].(bool); !success {
		return ignore, nil
	}
	data, ok := event["data"].(map[string]interface{})
	if !ok {
		return ignore, nil
	}

	sessionID, _ := data["sessionId"].(string)

	var model string
	if m, ok := data["model"].(map[string]interface{}); ok {
		model, _ = m["id"].(string)
		if provider, _ := m["provider"].(string); provider != "" && model != "" {
			model = provider + "/" + model
		}
	}

	if model == "" && sessionID == "" {
		return ignore, nil
	}
	return []*BackendEvent{{
		Kind:      BackendSessionInit,
		Model:     model,
		SessionID: sessionID,
	}}, nil
}

// parseMessageUpdate handles streaming text / thinking / toolcall deltas.
func (b *piBackend) parseMessageUpdate(event map[string]interface{}) ([]*BackendEvent, error) {
	ame, ok := event["assistantMessageEvent"].(map[string]interface{})
	if !ok {
		return []*BackendEvent{{Kind: BackendIgnore}}, nil
	}

	ameType, _ := ame["type"].(string)
	switch ameType {
	case "text_start":
		b.textBuf.Reset()

	case "text_delta":
		delta, _ := ame["delta"].(string)
		b.textBuf.WriteString(delta)

	case "text_end":
		text := b.textBuf.String()
		b.textBuf.Reset()
		if text != "" {
			return []*BackendEvent{{Kind: BackendText, Content: text}}, nil
		}

	case "error":
		// Message generation failed (aborted or error).
		reason, _ := ame["reason"].(string)
		if reason != "aborted" {
			return []*BackendEvent{{Kind: BackendError, Content: fmt.Sprintf("message error: %s", reason)}}, nil
		}
	}

	return []*BackendEvent{{Kind: BackendIgnore}}, nil
}

// parseToolStart captures tool call metadata for later emission.
func (b *piBackend) parseToolStart(event map[string]interface{}) ([]*BackendEvent, error) {
	b.pendingToolID, _ = event["toolCallId"].(string)
	b.pendingToolName, _ = event["toolName"].(string)
	b.pendingToolArgs, _ = event["args"].(map[string]interface{})
	return []*BackendEvent{{
		Kind:      BackendToolUse,
		ToolName:  b.pendingToolName,
		ToolID:    b.pendingToolID,
		ToolInput: b.pendingToolArgs,
	}}, nil
}

// parseToolEnd emits the tool result.
func (b *piBackend) parseToolEnd(event map[string]interface{}) ([]*BackendEvent, error) {
	toolID, _ := event["toolCallId"].(string)
	isError, _ := event["isError"].(bool)

	var resultText string
	if result, ok := event["result"].(map[string]interface{}); ok {
		if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
			if first, ok := content[0].(map[string]interface{}); ok {
				resultText, _ = first["text"].(string)
			}
		}
	}

	return []*BackendEvent{{
		Kind:    BackendToolResult,
		ToolID:  toolID,
		Content: resultText,
		IsError: isError,
	}}, nil
}

// parseAgentEnd handles the completion event and extracts cost.
func (b *piBackend) parseAgentEnd(event map[string]interface{}) ([]*BackendEvent, error) {
	messages, _ := event["messages"].([]interface{})
	cost := extractPiSessionCost(messages)
	return []*BackendEvent{{
		Kind:    BackendResult,
		CostUSD: cost,
		Subtype: "success",
	}}, nil
}

// extractPiSessionCost sums the cost.total field across all assistant messages.
func extractPiSessionCost(messages []interface{}) float64 {
	var total float64
	for _, m := range messages {
		msg, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		if role, _ := msg["role"].(string); role != "assistant" {
			continue
		}
		usage, ok := msg["usage"].(map[string]interface{})
		if !ok {
			continue
		}
		cost, ok := usage["cost"].(map[string]interface{})
		if !ok {
			continue
		}
		t, _ := cost["total"].(float64)
		total += t
	}
	return total
}
