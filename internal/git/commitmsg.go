package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/oneshot"
)

// oneShotPrompt is the seam through which this package reaches the configured
// coding-agent CLI. It resolves the backend lazily from the process-wide
// default that the daemon installs at startup (oneshot.SetDefault), so the git
// package never has to import internal/engine. Tests replace it to stay
// hermetic; when no backend is configured the callers fall back to heuristics.
var oneShotPrompt = func(ctx context.Context, req oneshot.Request) (string, error) {
	return oneshot.Run(ctx, nil, req)
}

// CommitMessage holds a generated commit message
type CommitMessage struct {
	Type    string   `json:"type"`    // feat, fix, docs, etc.
	Scope   string   `json:"scope"`   // optional scope
	Subject string   `json:"subject"` // short description
	Body    string   `json:"body"`    // optional detailed description
	Footers []string `json:"footers"` // optional footers (BREAKING CHANGE, etc.)
	Full    string   `json:"full"`    // complete commit message
}

// genConfig holds configuration for the one-shot generation calls.
var genConfig = struct {
	Timeout    time.Duration
	Enabled    bool
	MaxRetries int
}{
	Timeout:    30 * time.Second, // Enterprise API limit friendly
	Enabled:    true,
	MaxRetries: 1,
}

// GetStagedDiff returns the diff of staged changes
func GetStagedDiff(path string) (string, error) {
	cmd := exec.Command("git", "-C", path, "diff", "--cached")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	err := cmd.Run()
	if err != nil && !strings.Contains(output.String(), "diff") {
		return "", fmt.Errorf("failed to get staged diff: %w", err)
	}

	return output.String(), nil
}

// GetUnstagedDiff returns the diff of unstaged changes
func GetUnstagedDiff(path string) (string, error) {
	cmd := exec.Command("git", "-C", path, "diff")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	err := cmd.Run()
	if err != nil && !strings.Contains(output.String(), "diff") {
		return "", fmt.Errorf("failed to get unstaged diff: %w", err)
	}

	return output.String(), nil
}

// GenerateCommitMessage creates a commit message from the staged diff,
// falling back to the unstaged diff when nothing is staged yet (so a message
// can be suggested before the caller decides what to stage).
// Uses a cheap one-shot prompt on the configured backend (not a full
// interactive session). Results are cached to avoid redundant API calls
// for the same diff.
func GenerateCommitMessage(path string) (*CommitMessage, error) {
	diff, err := GetStagedDiff(path)
	if err != nil {
		return nil, err
	}

	if diff == "" {
		diff, err = GetUnstagedDiff(path)
		if err != nil {
			return nil, err
		}
	}

	if diff == "" {
		return nil, fmt.Errorf("no staged or unstaged changes to commit")
	}

	// Check cache first
	cache := GetGlobalCache()
	cacheKey := commitMsgCacheKey(diff)
	if cached, ok := cache.Get(cacheKey); ok {
		if msg, ok := cached.(*CommitMessage); ok && msg != nil {
			log.Printf("[commitmsg] cache hit for diff hash: %s", cacheKey[:16])
			return msg, nil
		}
	}

	// Try the configured coding-agent CLI first
	if msg := generateWithAgent(path, diff); msg != nil {
		log.Printf("[commitmsg] generated message via agent one-shot: %s: %s", msg.Type, msg.Subject)
		cache.Set(cacheKey, msg)
		return msg, nil
	}

	log.Printf("[commitmsg] agent unavailable, using heuristic fallback")

	// Fallback to heuristic-based generation
	msg := analyzeDiffAndGenerateMessage(diff)
	cache.Set(cacheKey, msg)
	return msg, nil
}

// commitMsgCacheKey generates a cache key from diff content hash
func commitMsgCacheKey(diff string) string {
	hash := sha256.Sum256([]byte(diff))
	return "commitmsg:" + hex.EncodeToString(hash[:16])
}

// generateWithAgent asks the configured coding-agent CLI for a commit message.
// Returns nil if the agent is unavailable or fails, so the caller can fall back
// to the heuristic generator.
func generateWithAgent(path, diff string) *CommitMessage {
	if !genConfig.Enabled {
		log.Printf("[commitmsg] agent generation disabled by config")
		return nil
	}

	prompt := fmt.Sprintf(`Analyze this git diff and generate a conventional commit message.
Respond with ONLY the commit message in this format:
  type: short description

Type must be one of: feat, fix, docs, style, refactor, test, chore, perf, ci, build
Do NOT include any explanation, markdown, or additional text.

Diff:
%s`, diff)

	var lastErr error
	for attempt := 0; attempt <= genConfig.MaxRetries; attempt++ {
		msg, err := attemptAgentGenerate(path, prompt)
		if err == nil && msg != nil {
			return msg
		}
		lastErr = err
		if err != nil {
			log.Printf("[commitmsg] agent attempt %d failed: %v", attempt+1, err)
		}
	}

	log.Printf("[commitmsg] agent generation failed after %d attempts: %v", genConfig.MaxRetries+1, lastErr)
	return nil
}

// attemptAgentGenerate makes a single attempt to generate a commit message via
// the configured backend's one-shot prompt call.
func attemptAgentGenerate(path, prompt string) (*CommitMessage, error) {
	result, err := oneShotPrompt(context.Background(), oneshot.Request{
		Prompt:  prompt,
		Dir:     path,
		Timeout: genConfig.Timeout,
	})
	if err != nil {
		return nil, err
	}

	// Parse the output into type and subject
	lines := strings.SplitN(result, ":", 2)
	if len(lines) < 2 {
		// Couldn't parse conventional format, use raw output
		return &CommitMessage{
			Type:    detectCommitType(prompt), // Use prompt to detect since we don't have diff here
			Subject: result,
			Full:    result,
		}, nil
	}

	msgType := strings.TrimSpace(lines[0])
	subject := strings.TrimSpace(lines[1])

	// Validate the type is conventional
	if !isValidCommitType(msgType) {
		log.Printf("[commitmsg] invalid commit type from agent: %q, using 'feat'", msgType)
		msgType = "feat"
	}

	return &CommitMessage{
		Type:    msgType,
		Subject: subject,
		Body:    "", // the one-shot answer is a single line
		Full:    result,
	}, nil
}

// isValidCommitType checks if a commit type is a valid conventional commit type
func isValidCommitType(t string) bool {
	validTypes := map[string]bool{
		"feat":     true,
		"fix":      true,
		"docs":     true,
		"style":    true,
		"refactor": true,
		"test":     true,
		"chore":    true,
		"perf":     true,
		"ci":       true,
		"build":    true,
	}
	return validTypes[t]
}

// analyzeDiffAndGenerateMessage creates a commit message from diff content
func analyzeDiffAndGenerateMessage(diff string) *CommitMessage {
	// Detect the type of change
	msgType := detectCommitType(diff)

	// Detect scope from file paths
	scope := detectScope(diff)

	// Generate subject from diff summary
	subject := generateSubject(diff, msgType)

	// Generate body from diff details
	body := generateBody(diff)

	// Build full message
	full := fmt.Sprintf("%s", subject)
	if body != "" {
		full += fmt.Sprintf("\n\n%s", body)
	}

	return &CommitMessage{
		Type:    msgType,
		Scope:   scope,
		Subject: subject,
		Body:    body,
		Full:    full,
	}
}

// detectCommitType determines the conventional commit type
func detectCommitType(diff string) string {
	// Check for test files
	if strings.Contains(diff, "_test.go") || strings.Contains(diff, "/test/") || strings.Contains(diff, "/tests/") {
		return "test"
	}

	// Check for documentation
	if strings.Contains(diff, ".md") || strings.Contains(diff, ".txt") || strings.Contains(diff, "/docs/") {
		return "docs"
	}

	// Check for config files
	if strings.Contains(diff, ".yml") || strings.Contains(diff, ".yaml") || strings.Contains(diff, ".json") || strings.Contains(diff, "config") {
		return "chore"
	}

	// Check for refactoring (many files changed, similar additions/deletions)
	if strings.Count(diff, "+++") > 5 && strings.Count(diff, "---") > 5 {
		return "refactor"
	}

	// Check for bug fix indicators
	if strings.Contains(diff, "fix") || strings.Contains(diff, "bug") || strings.Contains(diff, "error") {
		return "fix"
	}

	// Default to feat for new code
	return "feat"
}

// detectScope extracts a scope from changed file paths
func detectScope(diff string) string {
	// Look for common patterns in file paths
	if strings.Contains(diff, "internal/git/") {
		return "git"
	}
	if strings.Contains(diff, "internal/app/") {
		return "app"
	}
	if strings.Contains(diff, "cmd/") {
		return "cli"
	}
	if strings.Contains(diff, "ui/") || strings.Contains(diff, "tui/") {
		return "ui"
	}
	if strings.Contains(diff, "api/") {
		return "api"
	}
	return ""
}

// generateSubject creates a short subject line from the diff
func generateSubject(diff, msgType string) string {
	// Extract changed file names
	files := extractChangedFiles(diff)

	if len(files) == 1 {
		// Single file change
		file := files[0]
		baseName := strings.TrimPrefix(file, "internal/")
		baseName = strings.TrimSuffix(baseName, ".go")
		baseName = strings.ReplaceAll(baseName, "/", "-")
		return fmt.Sprintf("%s: add %s", msgType, baseName)
	} else if len(files) > 1 {
		// Multiple files
		if len(files) <= 3 {
			return fmt.Sprintf("%s: add %s", msgType, strings.Join(files[:3], ", "))
		}
		return fmt.Sprintf("%s: add %d components", msgType, len(files))
	}

	// Fallback
	return fmt.Sprintf("%s: implement new functionality", msgType)
}

// generateBody creates a detailed body from the diff
func generateBody(diff string) string {
	// Count additions and deletions
	additions := strings.Count(diff, "^M") + strings.Count(diff, "+++")
	deletions := strings.Count(diff, "---")

	if additions == 0 && deletions == 0 {
		return ""
	}

	body := fmt.Sprintf("Changed %d lines (+%d, -%d)", additions+deletions, additions, deletions)
	return body
}

// extractChangedFiles extracts the list of changed files from diff
func extractChangedFiles(diff string) []string {
	var files []string
	lines := strings.Split(diff, "\n")

	for _, line := range lines {
		if strings.HasPrefix(line, "+++ b/") {
			file := strings.TrimPrefix(line, "+++ b/")
			files = append(files, file)
		}
	}

	return files
}

// FormatConventionalCommit formats a commit message in conventional commits format
func FormatConventionalCommit(msg *CommitMessage) string {
	if msg == nil {
		return ""
	}

	var subject string
	if msg.Scope != "" {
		subject = fmt.Sprintf("%s(%s): %s", msg.Type, msg.Scope, msg.Subject)
	} else {
		subject = fmt.Sprintf("%s: %s", msg.Type, msg.Subject)
	}

	full := subject
	if msg.Body != "" {
		full += fmt.Sprintf("\n\n%s", msg.Body)
	}

	for _, footer := range msg.Footers {
		full += fmt.Sprintf("\n\n%s", footer)
	}

	return full
}

// SuggestCommitMessage is a helper that generates and formats a commit message
func SuggestCommitMessage(path string) (string, error) {
	msg, err := GenerateCommitMessage(path)
	if err != nil {
		return "", err
	}
	return FormatConventionalCommit(msg), nil
}
