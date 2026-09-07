package engine

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"gitlab.com/tanevanwifferen1/singularity/internal/oneshot"
)

// summaryTimeout bounds the one-shot title call. It runs after the agent has
// already been handed to the caller, so a slow classifier costs nothing but a
// late title — but it must not leak a goroutine for the process's lifetime.
const summaryTimeout = 20 * time.Second

const summaryPromptTemplate = `Summarize what this task asks for in a single short title.

Rules:
- Maximum 60 characters.
- Imperative mood, no trailing period, no quotes, no markdown.
- Describe the actual work, not the surrounding boilerplate: prompts often open
  with environment/worktree preamble that says nothing about the task.
- Respond with the title only, nothing else.

Task:
%s`

// generateTaskSummary asks the backend's cheap one-shot model for a display
// title for a task prompt. It returns "" on any failure (no backend, timeout,
// non-zero exit, junk answer) so the caller keeps its deterministic fallback —
// a missing summariser must never affect a spawn.
func generateTaskSummary(backend Backend, task string) string {
	// Long prompts are the norm here and the tail rarely changes the title,
	// so cap what we pay to classify.
	prompt := fmt.Sprintf(summaryPromptTemplate, truncateForSummary(task, 4000))

	answer, err := oneShotPrompt(context.Background(), backend, oneshot.Request{
		Prompt:  prompt,
		Timeout: summaryTimeout,
	})
	if err != nil {
		return ""
	}
	return sanitizeSummary(answer)
}

// sanitizeSummary reduces a model answer to one presentable line, or "" if
// nothing usable survives. Models occasionally wrap the title in quotes, prefix
// it with a markdown bullet/header, or add a second line of commentary.
func sanitizeSummary(answer string) string {
	line := ""
	for _, l := range strings.Split(answer, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			line = l
			break
		}
	}
	line = strings.TrimLeft(line, "#-*> \t")
	line = strings.TrimSpace(line)
	line = strings.Trim(line, `"'`)
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	if len(line) > 80 {
		return truncateForSummary(line, 77) + "..."
	}
	return line
}

// truncateForSummary shortens a prompt to at most n bytes, backing up to a
// rune boundary so the model never receives a split multi-byte character.
func truncateForSummary(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Summarizer produces a display title for a task prompt, or "" to keep the
// caller's deterministic fallback.
type Summarizer func(backend Backend, task string) string

// defaultSummarizer is the Summarizer installed by New. It is a variable so
// the package's own tests can neuter it once, in TestMain: an engine built
// with default AgentOptions would otherwise make a real cheap-model call.
var defaultSummarizer Summarizer = generateTaskSummary

// summarizeAsync fills in an AI-generated display title without delaying the
// spawn: the agent is already running (or starting) by the time this lands.
// Guarded by a sync.Once per agent so one agent can never cost two calls, no
// matter which of the routed / non-routed paths reaches it.
func (a *Agent) summarizeAsync(summarize Summarizer, backend Backend) {
	if summarize == nil {
		return
	}
	a.summaryOnce.Do(func() {
		go func() {
			// Sanitised again here so an injected Summarizer cannot put a
			// multi-line or overlong title into the list.
			summary := sanitizeSummary(summarize(backend, a.Task))
			if summary == "" {
				return // keep the extractSummary fallback
			}
			a.mu.Lock()
			a.Summary = summary
			a.mu.Unlock()
			a.notifySummary()
		}()
	})
}

// notifySummary signals the engine's observer that the display title changed.
// Observers read the new title by polling Snapshot()/List (the WS protocol has
// no summary-changed frame), but routing the change through notify keeps it on
// the same signalling path as every other agent mutation.
func (a *Agent) notifySummary() {
	if a.notify != nil {
		a.notify()
	}
}
