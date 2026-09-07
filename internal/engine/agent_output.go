package engine

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"time"
)

// streamOutput reads JSONL from the backend subprocess stdout, normalises each
// line via backend.ParseEvent, and dispatches the results.
func (a *Agent) streamOutput(r io.ReadCloser) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		events, err := a.backend.ParseEvent(line)
		if err != nil {
			a.appendOutput("error", fmt.Sprintf("parse error: %v", err))
			continue
		}
		for _, ev := range events {
			a.handleBackendEvent(ev)
		}
	}
}

// streamStderr reads stderr and appends as error entries
func (a *Agent) streamStderr(r io.ReadCloser) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			a.appendOutput("error", line)
		}
	}
}

// getOutput returns output entries from the given offset.
func (a *Agent) getOutput(offset int) []OutputEntry {
	a.outputMu.Lock()
	defer a.outputMu.Unlock()

	if offset >= len(a.output) {
		return nil
	}
	if offset < 0 {
		offset = 0
	}

	result := make([]OutputEntry, len(a.output)-offset)
	copy(result, a.output[offset:])
	return result
}

// getFullOutput returns all text content joined as a single string.
func (a *Agent) getFullOutput() string {
	a.outputMu.Lock()
	defer a.outputMu.Unlock()

	var parts []string
	for _, entry := range a.output {
		if entry.Source == "text" || entry.Source == "tool_use" || entry.Source == "tool_result" {
			parts = append(parts, entry.Content)
		}
	}
	return strings.Join(parts, "\n")
}

// appendOutput appends an output entry (thread-safe).
// Output is suppressed after the agent has been killed.
func (a *Agent) appendOutput(source, content string) {
	a.mu.Lock()
	killed := a.State == AgentKilled
	a.mu.Unlock()
	if killed {
		return
	}

	a.outputMu.Lock()
	a.output = append(a.output, OutputEntry{
		Timestamp: time.Now(),
		Source:    source,
		Content:   content,
	})
	a.outputMu.Unlock()

	if a.notify != nil {
		a.notify()
	}
}

// appendOutputLocked appends output when mu is already held (uses outputMu only).
func (a *Agent) appendOutputLocked(source, content string) {
	a.outputMu.Lock()
	a.output = append(a.output, OutputEntry{
		Timestamp: time.Now(),
		Source:    source,
		Content:   content,
	})
	a.outputMu.Unlock()

	if a.notify != nil {
		a.notify()
	}
}

// GetConversationHistory returns the agent's conversation formatted as a transcript
// suitable for injecting into a new agent's prompt to resume the conversation.
func (a *Agent) GetConversationHistory() string {
	a.outputMu.Lock()
	defer a.outputMu.Unlock()

	var parts []string
	for _, entry := range a.output {
		switch entry.Source {
		case "text":
			parts = append(parts, entry.Content)
		case "tool_use":
			parts = append(parts, fmt.Sprintf("[Tool: %s] %s", entry.ToolName, entry.Content))
		case "tool_result":
			if entry.Content != "" {
				prefix := "[Tool Result]"
				if entry.IsError {
					prefix = "[Tool Error]"
				}
				parts = append(parts, fmt.Sprintf("%s %s", prefix, entry.Content))
			}
		case "user_input":
			parts = append(parts, fmt.Sprintf("[User Message] %s", entry.Content))
		case "error":
			parts = append(parts, fmt.Sprintf("[Error] %s", entry.Content))
		}
	}
	return strings.Join(parts, "\n")
}
