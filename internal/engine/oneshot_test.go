package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/oneshot"
)

// TestBackendOneShotCommandArgv pins the exact argv each backend builds for a
// one-shot prompt, exercised through the oneshot runner seam (no subprocess).
func TestBackendOneShotCommandArgv(t *testing.T) {
	tests := []struct {
		name       string
		backend    Backend
		wantBinary string
		wantArgs   []string
	}{
		{
			name:       "claude",
			backend:    NewClaudeBackend(),
			wantBinary: "claude",
			wantArgs:   []string{"--print", "--model", "haiku", "--output-format", "text", "-p", "why is the sky blue"},
		},
		{
			name:       "pi default model",
			backend:    NewPiBackend(""),
			wantBinary: "pi",
			wantArgs:   []string{"--print", "--no-session", "--model", "anthropic/claude-haiku-4-5", "why is the sky blue"},
		},
		{
			name:       "pi explicit model",
			backend:    NewPiBackend("openai/gpt-5-mini"),
			wantBinary: "pi",
			wantArgs:   []string{"--print", "--no-session", "--model", "openai/gpt-5-mini", "why is the sky blue"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotBinary string
			var gotArgs []string
			runner := func(_ context.Context, _, binary string, args, _ []string) ([]byte, error) {
				gotBinary, gotArgs = binary, args
				return []byte("answer"), nil
			}

			answer, err := oneshot.Run(context.Background(), tt.backend, oneshot.Request{
				Prompt: "why is the sky blue",
				Runner: runner,
			})
			if err != nil {
				t.Fatalf("oneshot.Run: %v", err)
			}
			if answer != "answer" {
				t.Errorf("answer = %q, want %q", answer, "answer")
			}
			if gotBinary != tt.wantBinary {
				t.Errorf("binary = %q, want %q", gotBinary, tt.wantBinary)
			}
			if strings.Join(gotArgs, "\x00") != strings.Join(tt.wantArgs, "\x00") {
				t.Errorf("args =\n  %q\nwant\n  %q", gotArgs, tt.wantArgs)
			}
		})
	}
}

// stubOneShot replaces the package seam for the duration of the test.
func stubOneShot(t *testing.T, fn func(prompt string) (string, error)) *string {
	t.Helper()
	prev := oneShotPrompt
	t.Cleanup(func() { oneShotPrompt = prev })

	var seen string
	oneShotPrompt = func(_ context.Context, _ Backend, req oneshot.Request) (string, error) {
		seen = req.Prompt
		return fn(req.Prompt)
	}
	return &seen
}

func TestGenerateCommitMessage(t *testing.T) {
	tests := []struct {
		name   string
		answer string
		err    error
		want   string
	}{
		{name: "plain answer", answer: "Add retry logic to the poller", want: "Add retry logic to the poller"},
		{name: "quoted answer", answer: `"Add retry logic"`, want: "Add retry logic"},
		{name: "markdown header stripped", answer: "## Add retry logic", want: "Add retry logic"},
		{name: "backend failure falls back", err: errors.New("pi not installed"), want: "Agent work from agent-7"},
		{name: "empty answer falls back", answer: "   ", want: "Agent work from agent-7"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := stubOneShot(t, func(string) (string, error) { return tt.answer, tt.err })

			got := generateCommitMessage(NewPiBackend(""), "1 file changed", "fix the poller", "agent-7")
			if got != tt.want {
				t.Errorf("generateCommitMessage = %q, want %q", got, tt.want)
			}
			if !strings.Contains(*prompt, "fix the poller") || !strings.Contains(*prompt, "1 file changed") {
				t.Errorf("prompt did not carry task and diff stat: %q", *prompt)
			}
		})
	}
}

func TestGenerateMergeMessage(t *testing.T) {
	tests := []struct {
		name   string
		answer string
		err    error
		want   string
	}{
		{name: "plain answer", answer: "Merge poller retry work", want: "Merge poller retry work"},
		{name: "quoted answer", answer: `"Merge poller retry work"`, want: "Merge poller retry work"},
		{name: "backend failure falls back", err: errors.New("exit status 1"), want: "Merge agent work from agent-7"},
		{name: "empty answer falls back", answer: "\n", want: "Merge agent work from agent-7"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := stubOneShot(t, func(string) (string, error) { return tt.answer, tt.err })

			got := generateMergeMessage(NewPiBackend(""), "abc1234 do the thing", "fix the poller", "agent-7")
			if got != tt.want {
				t.Errorf("generateMergeMessage = %q, want %q", got, tt.want)
			}
			if !strings.Contains(*prompt, "abc1234 do the thing") {
				t.Errorf("prompt did not carry the commit log: %q", *prompt)
			}
		})
	}
}

// TestGenerateCommitMessageWithoutBackend covers the real (unstubbed) path with
// no backend configured: it must degrade to the static fallback, not panic or
// shell out to anything.
func TestGenerateCommitMessageWithoutBackend(t *testing.T) {
	if oneshot.Default() != nil {
		t.Skip("a process-wide one-shot backend is installed")
	}
	if got := generateCommitMessage(nil, "1 file changed", "task", "agent-9"); got != "Agent work from agent-9" {
		t.Errorf("generateCommitMessage = %q, want the static fallback", got)
	}
}
