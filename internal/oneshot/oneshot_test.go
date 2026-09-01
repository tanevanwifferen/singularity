package oneshot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// stubCommander returns a fixed binary and echoes the prompt into the argv.
type stubCommander struct {
	binary string
	args   []string
}

func (s stubCommander) OneShotCommand(prompt string) (string, []string) {
	return s.binary, append(append([]string{}, s.args...), prompt)
}

// capture records what the runner was asked to execute.
type capture struct {
	dir    string
	binary string
	args   []string
	env    []string
}

func recordingRunner(c *capture, out string, err error) Runner {
	return func(_ context.Context, dir, binary string, args, env []string) ([]byte, error) {
		c.dir, c.binary, c.args, c.env = dir, binary, args, env
		if err != nil {
			return nil, err
		}
		return []byte(out), nil
	}
}

func TestRunPassesPromptAndDirToRunner(t *testing.T) {
	var got capture
	answer, err := Run(context.Background(), stubCommander{binary: "fake", args: []string{"--flag"}}, Request{
		Prompt: "hello world",
		Dir:    "/tmp/repo",
		Runner: recordingRunner(&got, "  the answer\n", nil),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "the answer" {
		t.Errorf("answer = %q, want %q (trimmed)", answer, "the answer")
	}
	if got.binary != "fake" {
		t.Errorf("binary = %q, want fake", got.binary)
	}
	if strings.Join(got.args, " ") != "--flag hello world" {
		t.Errorf("args = %q", got.args)
	}
	if got.dir != "/tmp/repo" {
		t.Errorf("dir = %q, want /tmp/repo", got.dir)
	}
	if len(got.env) != 1 || got.env[0] != "CLAUDE_NO_ANALYTICS=true" {
		t.Errorf("env = %q, want [CLAUDE_NO_ANALYTICS=true]", got.env)
	}
}

func TestRunEmptyAnswerIsAnError(t *testing.T) {
	var got capture
	_, err := Run(context.Background(), stubCommander{binary: "fake"}, Request{
		Prompt: "p",
		Runner: recordingRunner(&got, "   \n\t", nil),
	})
	if err == nil {
		t.Fatal("expected an error for an empty answer")
	}
	if !strings.Contains(err.Error(), "empty answer") {
		t.Errorf("error = %v, want it to mention an empty answer", err)
	}
}

func TestRunPropagatesRunnerError(t *testing.T) {
	var got capture
	sentinel := errors.New("exit status 1: boom")
	_, err := Run(context.Background(), stubCommander{binary: "fake"}, Request{
		Prompt: "p",
		Runner: recordingRunner(&got, "", sentinel),
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want it to wrap %v", err, sentinel)
	}
}

func TestRunTimeoutIsReported(t *testing.T) {
	_, err := Run(context.Background(), stubCommander{binary: "fake"}, Request{
		Prompt:  "p",
		Timeout: 20 * time.Millisecond,
		Runner: func(ctx context.Context, _, _ string, _, _ []string) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %v, want it to mention a timeout", err)
	}
}

func TestRunWithoutCommander(t *testing.T) {
	if Default() != nil {
		t.Fatal("precondition: no default Commander should be installed")
	}
	if _, err := Run(context.Background(), nil, Request{Prompt: "p"}); !errors.Is(err, ErrNoCommander) {
		t.Fatalf("error = %v, want ErrNoCommander", err)
	}
}

func TestRunFallsBackToDefaultCommander(t *testing.T) {
	SetDefault(stubCommander{binary: "defaulted"})
	defer SetDefault(nil)

	var got capture
	if _, err := Run(context.Background(), nil, Request{
		Prompt: "p",
		Runner: recordingRunner(&got, "ok", nil),
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.binary != "defaulted" {
		t.Errorf("binary = %q, want defaulted", got.binary)
	}
}

func TestRunRejectsBlankPrompt(t *testing.T) {
	if _, err := Run(context.Background(), stubCommander{binary: "fake"}, Request{Prompt: "  "}); err == nil {
		t.Fatal("expected an error for a blank prompt")
	}
}

func TestRunRejectsCommanderWithoutBinary(t *testing.T) {
	if _, err := Run(context.Background(), stubCommander{}, Request{Prompt: "p"}); err == nil {
		t.Fatal("expected an error when the backend returns no binary")
	}
}
