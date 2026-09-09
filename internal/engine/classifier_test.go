package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/oneshot"
)

func TestParseClassification(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantCat     PromptCategory
		wantModel   string
		wantEffort  string
		wantSummary string
		wantErr     bool
	}{
		{
			name:      "clean planning response",
			input:     `{"category": "planning", "reason": "user wants to think through architecture"}`,
			wantCat:   CategoryPlanning,
			wantModel: "opus",
		},
		{
			name:      "clean implementation response",
			input:     `{"category": "implementation", "reason": "user wants code changes"}`,
			wantCat:   CategoryImplementation,
			wantModel: "sonnet",
		},
		{
			name:      "json with surrounding text",
			input:     "Here is my classification:\n{\"category\": \"planning\", \"reason\": \"design question\"}\n",
			wantCat:   CategoryPlanning,
			wantModel: "opus",
		},
		{
			name:      "uppercase category",
			input:     `{"category": "PLANNING", "reason": "architecture discussion"}`,
			wantCat:   CategoryPlanning,
			wantModel: "opus",
		},
		{
			name:      "unknown category defaults to sonnet",
			input:     `{"category": "other", "reason": "unclear"}`,
			wantCat:   CategoryImplementation,
			wantModel: "sonnet",
		},
		{
			name:    "no json",
			input:   "this is just text with no json",
			wantErr: true,
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
		{
			name:    "malformed json content",
			input:   "{not: valid}",
			wantErr: true,
		},
		{
			name:       "effort field high",
			input:      `{"category": "implementation", "effort": "high", "reason": "complex task", "summary": "refactor auth"}`,
			wantCat:    CategoryImplementation,
			wantModel:  "sonnet",
			wantEffort: "high",
		},
		{
			name:       "invalid effort defaults to medium",
			input:      `{"category": "planning", "effort": "extreme", "reason": "design", "summary": "plan infra"}`,
			wantCat:    CategoryPlanning,
			wantModel:  "opus",
			wantEffort: "medium",
		},
		{
			name:        "summary field populated",
			input:       `{"category": "implementation", "effort": "low", "reason": "tiny fix", "summary": "fix typo in README"}`,
			wantCat:     CategoryImplementation,
			wantModel:   "sonnet",
			wantEffort:  "low",
			wantSummary: "fix typo in README",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseClassification(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Category != tt.wantCat {
				t.Errorf("category: got %q, want %q", result.Category, tt.wantCat)
			}
			if result.Model != tt.wantModel {
				t.Errorf("model: got %q, want %q", result.Model, tt.wantModel)
			}
			if tt.wantEffort != "" && result.Effort != tt.wantEffort {
				t.Errorf("effort: got %q, want %q", result.Effort, tt.wantEffort)
			}
			if tt.wantSummary != "" && result.Summary != tt.wantSummary {
				t.Errorf("summary: got %q, want %q", result.Summary, tt.wantSummary)
			}
		})
	}
}

// stderrBackend is a Backend whose one-shot argv fails the way pi does on a
// 429: a JSON error on stderr and exit status 1, after an optional delay.
type stderrBackend struct {
	stubBackend
	delay string
}

func (b stderrBackend) OneShotCommand(string) (string, []string) {
	script := `echo '429 {"type":"error","error":{"type":"rate_limit_error","message":"monthly spend limit"}}' >&2; exit 1`
	if b.delay != "" {
		// exec so the delay is the process itself: a forked sleep would
		// outlive a SIGKILLed sh and keep the stderr pipe open.
		script = "exec sleep " + b.delay
	}
	return "sh", []string{"-c", script}
}

// TestClassifyPromptReportsCLIStderr is the regression test for the herdr
// report "classifier failed (signal: killed)": the pi one-shot was rate
// limited, but the classifier used to exec it directly with a deadline
// shorter than pi's own retry budget and discard stderr, so the only thing
// the user saw was the SIGKILL.
func TestClassifyPromptReportsCLIStderr(t *testing.T) {
	_, err := ClassifyPrompt(context.Background(), "fix the bug", stderrBackend{})
	if err == nil {
		t.Fatal("ClassifyPrompt: want error, got nil")
	}
	if !strings.Contains(err.Error(), "monthly spend limit") {
		t.Errorf("error = %q, want the CLI's stderr in it", err)
	}
	if strings.Contains(err.Error(), "signal: killed") {
		t.Errorf("error = %q, must not be a bare SIGKILL", err)
	}
}

func TestClassifyPromptNamesTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := ClassifyPrompt(ctx, "fix the bug", stderrBackend{delay: "5"})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("ClassifyPrompt: want error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out after 100ms") {
		t.Errorf("error = %q, want it to name the ctx deadline that fired", err)
	}
	if strings.Contains(err.Error(), "after 0s") {
		t.Errorf("error = %q, must not report a zero duration", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("ClassifyPrompt returned after %v, want promptly after the 100ms deadline", elapsed)
	}
}

// piArgvBackend is a Backend whose one-shot argv is pi's, so the classifier
// treats it as pi without needing the binary on PATH.
type piArgvBackend struct{ stubBackend }

func (piArgvBackend) OneShotCommand(prompt string) (string, []string) {
	return "pi", []string{"--print", "--no-session", "--model", "m", prompt}
}

// TestClassifyPromptDisablesPiRetries pins the actual fix for the herdr
// report: pi must not retry a 429 inside the classifier, or the deadline
// kills it mid-backoff and the reason never reaches stderr. The classifier
// runs pi in a directory whose project-local settings switch off both the
// session-level and provider-level retry, and passes --approve so pi trusts
// that file.
func TestClassifyPromptDisablesPiRetries(t *testing.T) {
	var (
		gotBinary string
		gotArgs   []string
		gotReq    oneshot.Request
	)
	orig := classifyOneShot
	classifyOneShot = func(_ context.Context, c oneshot.Commander, req oneshot.Request) (string, error) {
		gotBinary, gotArgs = c.OneShotCommand(req.Prompt)
		gotReq = req
		return `{"category":"implementation","effort":"low"}`, nil
	}
	defer func() { classifyOneShot = orig }()

	if _, err := ClassifyPrompt(context.Background(), "fix the bug", piArgvBackend{}); err != nil {
		t.Fatalf("ClassifyPrompt: %v", err)
	}
	if gotBinary != "pi" {
		t.Fatalf("binary = %q, want pi", gotBinary)
	}
	argv := strings.Join(gotArgs, " ")
	if !strings.Contains(argv, "--approve") {
		t.Errorf("argv = %q, want --approve so pi honours the project-local settings", argv)
	}
	if !strings.HasSuffix(argv, "fix the bug") {
		t.Errorf("argv = %q, want the prompt to stay the last positional arg", argv)
	}
	if gotReq.Dir == "" {
		t.Fatal("Request.Dir is empty, want the no-retry settings directory")
	}
	data, err := os.ReadFile(filepath.Join(gotReq.Dir, ".pi", "settings.json"))
	if err != nil {
		t.Fatalf("read settings in %s: %v", gotReq.Dir, err)
	}
	for _, want := range []string{`"enabled": false`, `"maxRetries": 0`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("settings.json = %s, want it to contain %s", data, want)
		}
	}
}

// Non-pi backends are left alone: no --approve, no working-directory change.
func TestClassifyPromptLeavesOtherBackendsAlone(t *testing.T) {
	var gotArgs []string
	var gotReq oneshot.Request
	orig := classifyOneShot
	classifyOneShot = func(_ context.Context, c oneshot.Commander, req oneshot.Request) (string, error) {
		_, gotArgs = c.OneShotCommand(req.Prompt)
		gotReq = req
		return `{"category":"planning"}`, nil
	}
	defer func() { classifyOneShot = orig }()

	if _, err := ClassifyPrompt(context.Background(), "plan it", stubBackend{}); err != nil {
		t.Fatalf("ClassifyPrompt: %v", err)
	}
	if strings.Contains(strings.Join(gotArgs, " "), "--approve") {
		t.Errorf("argv = %q, --approve is a pi flag and must not leak to other backends", gotArgs)
	}
	if gotReq.Dir != "" {
		t.Errorf("Request.Dir = %q, want empty for non-pi backends", gotReq.Dir)
	}
}
