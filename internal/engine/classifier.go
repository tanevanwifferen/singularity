package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/oneshot"
)

// classifierTimeout is the backstop for the one-shot classification call.
//
// It is a backstop, not the budget pi needs: the classifier runs pi with its
// retries switched off (see piNoRetry), so a 429 reaches stderr on the first
// attempt instead of being retried for 2+4+8s at the session level and, when
// retry.provider.maxRetries is configured, honouring Retry-After for up to 60s
// per attempt underneath that. Without the override a deadline of any length
// could SIGKILL pi mid-retry and turn "monthly spend limit exceeded" into an
// opaque "signal: killed". What is left for the deadline to cover is pi's
// startup plus one model round trip, and the agent is already visible in the
// routing state while it runs, so a generous value costs a later start, not
// a hang.
const classifierTimeout = 30 * time.Second

// classifyOneShot is the classifier's own seam onto the one-shot runner. It
// is separate from oneShotPrompt so tests can stub the summariser while the
// classifier still executes the backend's real argv (and vice versa).
var classifyOneShot = func(ctx context.Context, c oneshot.Commander, req oneshot.Request) (string, error) {
	return oneshot.Run(ctx, c, req)
}

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

// ClassifyPrompt uses a lightweight model to classify a prompt as planning or implementation.
// The backend determines which binary and flags to use.
func ClassifyPrompt(ctx context.Context, prompt string, backend Backend) (*ClassificationResult, error) {
	classifyInput := fmt.Sprintf(classifierPrompt, prompt)

	if backend == nil {
		backend = ConfiguredBackend()
	}

	// The budget is whatever the caller's ctx carries (RoutePrompt sets
	// classifierTimeout; tests pass their own), so a timeout reports the
	// deadline that actually fired rather than the constant.
	budget := time.Duration(0)
	if deadline, ok := ctx.Deadline(); ok {
		budget = time.Until(deadline).Round(100 * time.Millisecond)
	}

	// Go through the shared one-shot runner rather than exec directly: it
	// folds the CLI's stderr into the error and names a timeout as such, so a
	// failed route says why (rate limit, missing binary, bad model id) instead
	// of only that the process died. piNoRetry makes sure that stderr carries
	// the first attempt's error instead of pi sitting in a retry loop until
	// the deadline kills it.
	commander, dir := piNoRetry(backend)
	output, err := classifyOneShot(ctx, commander, oneshot.Request{Prompt: classifyInput, Dir: dir})
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			if budget > 0 {
				return nil, fmt.Errorf("classifier timed out after %v: %w", budget, err)
			}
			return nil, fmt.Errorf("classifier timed out: %w", err)
		}
		return nil, fmt.Errorf("classifier failed: %w", err)
	}

	return parseClassification(output)
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

// RoutePrompt classifies a prompt using the given backend and returns the result.
// Bounded by classifierTimeout to avoid blocking indefinitely.
func RoutePrompt(prompt string, backend Backend) (*ClassificationResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), classifierTimeout)
	defer cancel()

	return ClassifyPrompt(ctx, prompt, backend)
}
