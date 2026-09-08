package engine

import (
	"fmt"
	"time"
)

// handleBackendEvent dispatches a normalised BackendEvent to the output buffer
// and triggers state transitions.
func (a *Agent) handleBackendEvent(ev *BackendEvent) {
	switch ev.Kind {
	case BackendText:
		if ev.Content != "" {
			a.appendOutput("text", ev.Content)
		}

	case BackendToolUse:
		summary := formatToolUseSummary(ev.ToolName, ev.ToolInput)
		entry := OutputEntry{
			Timestamp: time.Now(),
			Source:    "tool_use",
			Content:   summary,
			ToolName:  ev.ToolName,
			ToolID:    ev.ToolID,
		}
		a.outputMu.Lock()
		a.output = append(a.output, entry)
		a.outputMu.Unlock()
		if a.notify != nil {
			a.notify()
		}

	case BackendToolResult:
		entry := OutputEntry{
			Timestamp: time.Now(),
			Source:    "tool_result",
			Content:   ev.Content,
			ToolID:    ev.ToolID,
			IsError:   ev.IsError,
		}
		a.outputMu.Lock()
		a.output = append(a.output, entry)
		a.outputMu.Unlock()
		if a.notify != nil {
			a.notify()
		}

	case BackendSessionInit:
		if ev.Model != "" {
			a.appendOutput("system", fmt.Sprintf("Model: %s", ev.Model))
		}
		if ev.SessionID != "" {
			a.mu.Lock()
			a.sessionID = ev.SessionID
			a.mu.Unlock()
		}
		if ev.PaneID != "" {
			a.appendOutput("system", fmt.Sprintf("herdr pane: %s", ev.PaneID))
		}

	case BackendResult:
		a.handleResult(ev)

	case BackendError:
		if ev.Content != "" {
			a.appendOutput("error", fmt.Sprintf("Error: %s", ev.Content))
		}

	case BackendIgnore:
		// nothing to do
	}
}

// handleResult processes a BackendResult event: updates cost, transitions state,
// and triggers worktree merge-back on successful completion.
func (a *Agent) handleResult(ev *BackendEvent) {
	a.mu.Lock()
	if ev.CostUSD > 0 {
		a.TotalCostUSD = ev.CostUSD
	}
	if a.State == AgentRunning || a.State == AgentStarting {
		if ev.IsResultError {
			now := time.Now()
			a.EndedAt = &now
			a.State = AgentError
			a.Error = ev.Content
		} else if !a.useWorktree {
			now := time.Now()
			a.EndedAt = &now
			a.State = AgentComplete
		}
		// useWorktree && !isError: stay AgentRunning until merge finishes below
	}
	a.mu.Unlock()

	playSound(a.soundCfg)

	if ev.IsResultError {
		errMsg := ev.Content
		if errMsg == "" {
			errMsg = "agent exited with error (no message provided)"
		}
		a.appendOutput("error", fmt.Sprintf("Error: %s", errMsg))
	} else {
		status := "completed"
		if ev.Subtype != "" && ev.Subtype != "success" {
			status = ev.Subtype
		}
		costStr := ""
		if ev.CostUSD > 0 {
			costStr = fmt.Sprintf(" ($%.4f)", ev.CostUSD)
		}
		a.appendOutput("result", fmt.Sprintf("Agent %s%s", status, costStr))
	}

	if a.useWorktree && !ev.IsResultError {
		mergeResult := a.mergeWorktreeBack()
		a.mu.Lock()
		a.MergeResult = mergeResult
		now := time.Now()
		a.EndedAt = &now
		a.State = AgentComplete
		a.mu.Unlock()
	} else if a.useWorktree && ev.IsResultError {
		a.appendOutput("system", "Worktree preserved (agent errored) — merge manually or clean up later")
	}
}

// formatToolUseSummary creates a concise summary of a tool use event.
func formatToolUseSummary(name string, input map[string]interface{}) string {
	switch name {
	case "Read", "read":
		path, _ := input["file_path"].(string)
		return fmt.Sprintf("Read %s", path)
	case "Edit", "edit":
		path, _ := input["file_path"].(string)
		return fmt.Sprintf("Edit %s", path)
	case "Write", "write":
		path, _ := input["file_path"].(string)
		return fmt.Sprintf("Write %s", path)
	case "Bash", "bash":
		cmd, _ := input["command"].(string)
		return fmt.Sprintf("Bash: %s", truncate(cmd, 120))
	case "Grep", "grep":
		pattern, _ := input["pattern"].(string)
		return fmt.Sprintf("Grep: %s", truncate(pattern, 80))
	case "Glob", "glob":
		pattern, _ := input["pattern"].(string)
		return fmt.Sprintf("Glob: %s", pattern)
	case "WebSearch":
		query, _ := input["query"].(string)
		return fmt.Sprintf("WebSearch: %s", truncate(query, 80))
	case "WebFetch":
		url, _ := input["url"].(string)
		return fmt.Sprintf("WebFetch: %s", truncate(url, 80))
	default:
		return name
	}
}
