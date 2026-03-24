package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// PromptCategory represents the type of task a prompt is requesting
type PromptCategory string

const (
	CategoryPlanning       PromptCategory = "planning"
	CategoryImplementation PromptCategory = "implementation"
)

// ClassificationResult holds the classification result and selected model
type ClassificationResult struct {
	Category PromptCategory `json:"category"`
	Model    string         `json:"model"`
	Effort   string         `json:"effort"` // Effort level: "low", "medium", "high"
	Reason   string         `json:"reason"`
	Summary  string         `json:"summary"` // One-line summary of the task
}

const classifierPrompt = `Classify the following user prompt into exactly one category, and pick the appropriate effort level.

Categories:
- "planning": The user wants to think through architecture, design, strategy, tradeoffs, debugging approach, or investigation. They want analysis, not code changes. Examples: "how should we structure X", "what's the best approach for Y", "investigate why Z is broken", "design a system for W", "what are the tradeoffs of X vs Y".
- "implementation": The user wants concrete code changes, file edits, bug fixes, feature implementation, refactoring, or any hands-on coding work. Examples: "add a function that does X", "fix the bug in Y", "refactor Z to use W", "write tests for X", "implement feature Y".

Effort levels:
- "low": Simple, well-defined tasks with little ambiguity. Small edits, trivial fixes, or very narrow questions.
- "medium": Moderate complexity. Standard feature work, typical bug fixes, or focused investigation.
- "high": Complex, open-ended, or multi-step tasks. Deep architecture decisions, cross-cutting changes, tricky debugging, or tasks requiring broad reasoning.

Respond with ONLY a JSON object, no other text:
{"category": "planning" or "implementation", "effort": "low" or "medium" or "high", "reason": "one sentence why", "summary": "short one-line summary of what the task asks for (max 60 chars)"}

User prompt:
%s`

// ClassifyPrompt uses Claude Haiku to classify a prompt as planning or implementation.
// Returns the category and suggested model.
func ClassifyPrompt(ctx context.Context, prompt string) (*ClassificationResult, error) {
	classifyInput := fmt.Sprintf(classifierPrompt, prompt)

	args := []string{
		"--print",
		"--model", "haiku",
		"--output-format", "text",
		"--max-turns", "1",
		classifyInput,
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Env = append(os.Environ(), "CLAUDE_NO_ANALYTICS=true")

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("classifier failed: %w", err)
	}

	return parseClassification(strings.TrimSpace(string(output)))
}

// parseClassification extracts the category from the classifier's JSON response
func parseClassification(response string) (*ClassificationResult, error) {
	// The response should be a JSON object, but it might have extra text around it.
	// Find the JSON object boundaries.
	start := strings.Index(response, "{")
	end := strings.LastIndex(response, "}")
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("no JSON found in classifier response: %q", response)
	}

	jsonStr := response[start : end+1]

	var parsed struct {
		Category string `json:"category"`
		Effort   string `json:"effort"`
		Reason   string `json:"reason"`
		Summary  string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse classifier response: %w", err)
	}

	effort := strings.ToLower(parsed.Effort)
	if effort != "low" && effort != "medium" && effort != "high" {
		effort = "medium" // default
	}

	result := &ClassificationResult{
		Effort:  effort,
		Reason:  parsed.Reason,
		Summary: parsed.Summary,
	}

	switch strings.ToLower(parsed.Category) {
	case "planning":
		result.Category = CategoryPlanning
		result.Model = "opus"
	case "implementation":
		result.Category = CategoryImplementation
		result.Model = "sonnet"
	default:
		// Default to sonnet for unknown categories
		result.Category = CategoryImplementation
		result.Model = "sonnet"
	}

	return result, nil
}

// RoutePrompt classifies a prompt and returns the classification result.
// Uses a 15-second timeout to avoid blocking indefinitely.
func RoutePrompt(prompt string) (*ClassificationResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	return ClassifyPrompt(ctx, prompt)
}
