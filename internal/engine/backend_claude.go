package engine

import (
	"encoding/json"
	"fmt"
	"os"
)

// claudeBackend drives the claude CLI using --output-format stream-json /
// --input-format stream-json.
type claudeBackend struct{}

func (b *claudeBackend) Name() string   { return "claude" }
func (b *claudeBackend) Binary() string { return "claude" }

func (b *claudeBackend) Args(model, effort string, maxTurns int, allowedTools []string) []string {
	args := []string{
		"--print",
		"--verbose",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--permission-mode", "bypassPermissions",
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	if effort != "" {
		args = append(args, "--effort", effort)
	}
	if maxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", maxTurns))
	}
	for _, tool := range allowedTools {
		args = append(args, "--allowedTools", tool)
	}
	return args
}

func (b *claudeBackend) Env() []string {
	return append(os.Environ(), "CLAUDE_NO_ANALYTICS=true")
}

// PostStartCommands is a no-op for claude; effort is handled via launch flags.
func (b *claudeBackend) PostStartCommands(_ string) [][]byte { return nil }

func (b *claudeBackend) InitialInput(task, sessionID string) ([]byte, error) {
	return b.formatEnvelope(task, sessionID)
}

func (b *claudeBackend) FollowUpInput(message, sessionID string, _ bool) ([]byte, error) {
	return b.formatEnvelope(message, sessionID)
}

// formatEnvelope builds the claude stream-json input envelope.
func (b *claudeBackend) formatEnvelope(message, sessionID string) ([]byte, error) {
	if sessionID == "" {
		sessionID = "default"
	}
	msg := map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{
			"role":    "user",
			"content": message,
		},
		"session_id":         sessionID,
		"parent_tool_use_id": nil,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal claude input: %w", err)
	}
	return append(data, '\n'), nil
}

// ClassifyCommand returns args for a cheap one-shot Haiku classification call.
func (b *claudeBackend) ClassifyCommand(prompt string) (string, []string) {
	return "claude", []string{
		"--print",
		"--model", "haiku",
		"--output-format", "text",
		"--max-turns", "1",
		prompt,
	}
}

// ParseEvent parses one JSONL line from claude's stream-json output.
func (b *claudeBackend) ParseEvent(line []byte) ([]*BackendEvent, error) {
	var event map[string]interface{}
	if err := json.Unmarshal(line, &event); err != nil {
		return nil, fmt.Errorf("JSON parse: %w", err)
	}

	eventType, _ := event["type"].(string)
	switch eventType {
	case "assistant":
		return b.parseAssistantEvent(event)
	case "system":
		return b.parseSystemEvent(event)
	case "result":
		ev := b.parseResultEvent(event)
		return []*BackendEvent{ev}, nil
	default:
		return []*BackendEvent{{Kind: BackendIgnore}}, nil
	}
}

func (b *claudeBackend) parseAssistantEvent(event map[string]interface{}) ([]*BackendEvent, error) {
	msg, ok := event["message"].(map[string]interface{})
	if !ok {
		return []*BackendEvent{{Kind: BackendIgnore}}, nil
	}
	content, ok := msg["content"].([]interface{})
	if !ok {
		return []*BackendEvent{{Kind: BackendIgnore}}, nil
	}

	var events []*BackendEvent
	for _, block := range content {
		blockMap, ok := block.(map[string]interface{})
		if !ok {
			continue
		}
		blockType, _ := blockMap["type"].(string)
		switch blockType {
		case "text":
			text, _ := blockMap["text"].(string)
			if text != "" {
				events = append(events, &BackendEvent{Kind: BackendText, Content: text})
			}

		case "tool_use":
			name, _ := blockMap["name"].(string)
			id, _ := blockMap["id"].(string)
			input, _ := blockMap["input"].(map[string]interface{})
			events = append(events, &BackendEvent{
				Kind:      BackendToolUse,
				ToolName:  name,
				ToolID:    id,
				ToolInput: input,
			})

		case "tool_result":
			resultContent, _ := blockMap["content"].(string)
			toolID, _ := blockMap["tool_use_id"].(string)
			isError, _ := blockMap["is_error"].(bool)
			events = append(events, &BackendEvent{
				Kind:    BackendToolResult,
				Content: resultContent,
				ToolID:  toolID,
				IsError: isError,
			})
		}
	}

	if len(events) == 0 {
		return []*BackendEvent{{Kind: BackendIgnore}}, nil
	}
	return events, nil
}

func (b *claudeBackend) parseSystemEvent(event map[string]interface{}) ([]*BackendEvent, error) {
	subtype, _ := event["subtype"].(string)
	if subtype != "init" {
		return []*BackendEvent{{Kind: BackendIgnore}}, nil
	}
	model, _ := event["model"].(string)
	sessionID, _ := event["session_id"].(string)
	return []*BackendEvent{{
		Kind:      BackendSessionInit,
		Model:     model,
		SessionID: sessionID,
	}}, nil
}

func (b *claudeBackend) parseResultEvent(event map[string]interface{}) *BackendEvent {
	isError, _ := event["is_error"].(bool)
	result, _ := event["result"].(string)
	costUSD, _ := event["total_cost_usd"].(float64)
	subtype, _ := event["subtype"].(string)
	return &BackendEvent{
		Kind:          BackendResult,
		Content:       result,
		CostUSD:       costUSD,
		Subtype:       subtype,
		IsResultError: isError,
	}
}
