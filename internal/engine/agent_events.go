package engine

import (
	"fmt"
	"sort"
	"strings"
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
		a.trackBackgroundWork(ev)
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

// backgroundWorkTools are the tools an agent uses to look at, wait for or
// stop background work it started. Calling one means the agent is dealing
// with that work itself, so it no longer counts as abandoned.
var backgroundWorkTools = map[string]bool{
	"TaskOutput": true, "BashOutput": true, "TaskStop": true, "KillShell": true, "KillBash": true,
}

// trackBackgroundWork records tool calls that leave work running after the
// call returns: a Monitor watch, or any tool started with run_in_background
// (a Bash command, a sub-agent). See handleResult for what that is used for.
func (a *Agent) trackBackgroundWork(ev *BackendEvent) {
	bg, _ := ev.ToolInput["run_in_background"].(bool)
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case backgroundWorkTools[ev.ToolName]:
		a.backgroundWork = nil
	case ev.ToolName == "Monitor" || bg:
		if a.backgroundWork == nil {
			a.backgroundWork = map[string]string{}
		}
		a.backgroundWork[ev.ToolID] = ev.ToolName
	}
}

// handleResult processes a BackendResult event: updates cost, transitions state,
// and triggers worktree merge-back on successful completion.
//
// A turn that ends while the agent still has background work it started and
// never looked at again (a Monitor watch, a run_in_background command) is not
// a completion: the agent said "waiting for the notification" and stopped,
// but the scheduler terminates the agent's process the moment the task is
// terminal, so the work is killed and nobody ever reads its outcome. Neither
// backend can observe that the work later finishes — herdr's `agent prompt
// --wait` returns at the first idle, claude's stream-json emits a result per
// turn — so it is reported as an error, not waited for, and a flow does not
// go on to review a tree whose tests were never awaited.
func (a *Agent) handleResult(ev *BackendEvent) {
	a.mu.Lock()
	if pending := a.backgroundWork; len(pending) > 0 {
		a.backgroundWork = nil
		if !ev.IsResultError {
			names := map[string]bool{}
			for _, n := range pending {
				names[n] = true
			}
			var list []string
			for n := range names {
				list = append(list, n)
			}
			sort.Strings(list)
			copied := *ev
			copied.IsResultError = true
			copied.Content = fmt.Sprintf("agent ended its turn with background work still running (%d started: %s) "+
				"that it never awaited; the work is killed when the task ends, so this is not a completion. "+
				"Run it in the foreground or wait for it before ending the turn", len(pending), strings.Join(list, ", "))
			ev = &copied
		}
	}
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
