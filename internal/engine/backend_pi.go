package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// piModelAliases maps Claude short-names to pi-compatible model IDs.
// Pass-through if already in "provider/model" form.
var piModelAliases = map[string]string{
	"haiku":  "anthropic/claude-haiku-4-5",
	"sonnet": "anthropic/claude-sonnet-4-20250514",
	"opus":   "anthropic/claude-opus-4-5",
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
	classifyModel string

	// text accumulation across streaming deltas
	textBuf strings.Builder

	// in-flight tool call state (captured from tool_execution_start)
	pendingToolID   string
	pendingToolName string
	pendingToolArgs map[string]interface{}
}

func (b *piBackend) Name() string   { return "pi" }
func (b *piBackend) Binary() string { return "pi" }

func (b *piBackend) Args(model, effort string, maxTurns int, _ []string) []string {
	args := []string{"--mode", "rpc", "--no-session"}

	if model != "" {
		args = append(args, "--model", b.resolveModel(model))
	}

	// effort → thinking level is handled via PostStartCommands, not a launch flag.
	// maxTurns and allowedTools have no pi CLI equivalent.
	_ = effort
	_ = maxTurns

	return args
}

// resolveModel translates short model names to pi-compatible IDs.
func (b *piBackend) resolveModel(model string) string {
	if strings.Contains(model, "/") {
		return model // already fully qualified
	}
	if resolved, ok := piModelAliases[strings.ToLower(model)]; ok {
		return resolved
	}
	return model
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

// ClassifyCommand runs a cheap one-shot prompt via pi --print.
func (b *piBackend) ClassifyCommand(prompt string) (string, []string) {
	return "pi", []string{
		"--print",
		"--no-session",
		"--model", b.classifyModel,
		prompt,
	}
}

// ParseEvent parses one JSONL line from pi's RPC stdout.
func (b *piBackend) ParseEvent(line []byte) ([]*BackendEvent, error) {
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
