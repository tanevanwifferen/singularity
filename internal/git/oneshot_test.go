package git

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/oneshot"
)

// stubOneShot replaces the package seam for the duration of the test and
// returns a pointer to the last prompt it saw. It also clears the global
// commit-message cache so a previous test's diff cannot short-circuit the call.
func stubOneShot(t *testing.T, fn func(prompt string) (string, error)) *string {
	t.Helper()
	GetGlobalCache().Clear()
	t.Cleanup(func() { GetGlobalCache().Clear() })

	prev := oneShotPrompt
	t.Cleanup(func() { oneShotPrompt = prev })

	var seen string
	oneShotPrompt = func(_ context.Context, req oneshot.Request) (string, error) {
		seen = req.Prompt
		return fn(req.Prompt)
	}
	return &seen
}

func TestGenerateCommitMessageUsesAgentAnswer(t *testing.T) {
	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	createFileTree(t, tmpDir, "newfeature.go", "package main\nfunc main() {}")
	runGitTree(t, tmpDir, "add", "newfeature.go")

	prompt := stubOneShot(t, func(string) (string, error) { return "feat: add the new feature", nil })

	msg, err := GenerateCommitMessage(tmpDir)
	if err != nil {
		t.Fatalf("GenerateCommitMessage: %v", err)
	}
	if msg.Type != "feat" {
		t.Errorf("Type = %q, want feat", msg.Type)
	}
	if msg.Subject != "add the new feature" {
		t.Errorf("Subject = %q, want %q", msg.Subject, "add the new feature")
	}
	if !strings.Contains(*prompt, "newfeature.go") {
		t.Errorf("prompt did not carry the diff: %q", *prompt)
	}
}

func TestGenerateCommitMessageFallsBackWhenAgentFails(t *testing.T) {
	cases := map[string]struct {
		answer string
		err    error
	}{
		"backend unavailable": {err: errors.New(`oneshot: "pi" not found on PATH`)},
		"non-zero exit":       {err: errors.New("oneshot: pi failed: exit status 1: boom")},
		"empty answer":        {err: errors.New("oneshot: pi returned an empty answer")},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tmpDir := setupTestRepo(t)
			defer os.RemoveAll(tmpDir)

			createFileTree(t, tmpDir, "handler_test.go", "package main")
			runGitTree(t, tmpDir, "add", "handler_test.go")

			stubOneShot(t, func(string) (string, error) { return tc.answer, tc.err })

			msg, err := GenerateCommitMessage(tmpDir)
			if err != nil {
				t.Fatalf("GenerateCommitMessage should degrade, not fail: %v", err)
			}
			if msg == nil || msg.Type == "" || msg.Subject == "" {
				t.Fatalf("expected a heuristic message, got %+v", msg)
			}
		})
	}
}

func TestCallAgentForMR(t *testing.T) {
	tests := []struct {
		name   string
		answer string
		err    error
		want   *MRContent
	}{
		{
			name:   "clean json",
			answer: `{"title":"Add retry logic","description":"## Summary\n\n- retries"}`,
			want:   &MRContent{Title: "Add retry logic", Description: "## Summary\n\n- retries"},
		},
		{
			name:   "json wrapped in prose and fences",
			answer: "Here you go:\n```json\n{\"title\":\"Add retry logic\",\"description\":\"d\"}\n```",
			want:   &MRContent{Title: "Add retry logic", Description: "d"},
		},
		{name: "backend failure", err: errors.New("oneshot: pi failed: exit status 1")},
		{name: "not json", answer: "I could not do that"},
		{name: "json without a title", answer: `{"description":"d"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := stubOneShot(t, func(string) (string, error) { return tt.answer, tt.err })

			got := callAgentForMR("/tmp/repo", "the prompt")
			switch {
			case tt.want == nil && got != nil:
				t.Fatalf("expected nil so the caller falls back, got %+v", got)
			case tt.want != nil && got == nil:
				t.Fatal("expected content, got nil")
			case tt.want != nil:
				if got.Title != tt.want.Title || got.Description != tt.want.Description {
					t.Errorf("got %+v, want %+v", got, tt.want)
				}
			}
			if *prompt != "the prompt" {
				t.Errorf("prompt = %q, want %q", *prompt, "the prompt")
			}
		})
	}
}

// TestGenerateCommitMessageWithoutBackend covers the real (unstubbed) path with
// no backend configured: heuristics, no subprocess.
func TestGenerateCommitMessageWithoutBackend(t *testing.T) {
	if oneshot.Default() != nil {
		t.Skip("a process-wide one-shot backend is installed")
	}
	GetGlobalCache().Clear()
	defer GetGlobalCache().Clear()

	tmpDir := setupTestRepo(t)
	defer os.RemoveAll(tmpDir)

	createFileTree(t, tmpDir, "docs.md", "# docs")
	runGitTree(t, tmpDir, "add", "docs.md")

	msg, err := GenerateCommitMessage(tmpDir)
	if err != nil {
		t.Fatalf("GenerateCommitMessage: %v", err)
	}
	if msg == nil || msg.Full == "" {
		t.Fatalf("expected a heuristic message, got %+v", msg)
	}
}
