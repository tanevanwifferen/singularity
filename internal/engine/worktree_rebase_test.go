package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// recordedCommand is one invocation captured by the fake command runner.
type recordedCommand struct {
	dir    string
	env    []string
	binary string
	args   []string
}

// fakeRunner installs a command runner that records invocations instead of
// executing them, and restores the production runner when the test ends.
// handler runs for each recorded command and supplies its result.
func fakeRunner(t *testing.T, handler func(ctx context.Context, c recordedCommand) ([]byte, error)) *[]recordedCommand {
	t.Helper()
	var calls []recordedCommand
	prev := runCommand
	t.Cleanup(func() { runCommand = prev })
	runCommand = func(ctx context.Context, dir string, env []string, binary string, args ...string) ([]byte, error) {
		c := recordedCommand{dir: dir, env: env, binary: binary, args: args}
		calls = append(calls, c)
		if handler == nil {
			return nil, nil
		}
		return handler(ctx, c)
	}
	return &calls
}

func joinArgs(args []string) string { return strings.Join(args, "\x00") }

func TestRebaseWithAgentClaudeArgv(t *testing.T) {
	calls := fakeRunner(t, nil)

	err := rebaseWithAgent(context.Background(), NewClaudeBackend(), "/wt", "main", "do the thing")
	if err != nil {
		t.Fatalf("rebaseWithAgent: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected exactly 1 command, got %d: %+v", len(*calls), *calls)
	}

	got := (*calls)[0]
	if got.binary != "claude" {
		t.Errorf("binary = %q, want claude", got.binary)
	}
	if got.dir != "/wt" {
		t.Errorf("dir = %q, want /wt", got.dir)
	}

	// Byte-identical to the invocation worktree.go used before the abstraction:
	// claude --print --permission-mode bypassPermissions -p <prompt>
	wantPrefix := []string{"--print", "--permission-mode", "bypassPermissions", "-p"}
	if len(got.args) != len(wantPrefix)+1 {
		t.Fatalf("args = %q, want %d elements", got.args, len(wantPrefix)+1)
	}
	if joinArgs(got.args[:len(wantPrefix)]) != joinArgs(wantPrefix) {
		t.Errorf("args prefix = %q, want %q", got.args[:len(wantPrefix)], wantPrefix)
	}
	assertRebasePrompt(t, got.args[len(got.args)-1], "main", "do the thing")

	if !hasEnv(got.env, "CLAUDE_NO_ANALYTICS=true") {
		t.Errorf("claude session env missing CLAUDE_NO_ANALYTICS=true")
	}
}

func TestRebaseWithAgentPiArgv(t *testing.T) {
	calls := fakeRunner(t, nil)

	err := rebaseWithAgent(context.Background(), NewPiBackend(""), "/wt", "develop", "do the thing")
	if err != nil {
		t.Fatalf("rebaseWithAgent: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected exactly 1 command, got %d: %+v", len(*calls), *calls)
	}

	got := (*calls)[0]
	if got.binary != "pi" {
		t.Errorf("binary = %q, want pi", got.binary)
	}
	if got.dir != "/wt" {
		t.Errorf("dir = %q, want /wt", got.dir)
	}

	// pi has no permission prompts to bypass; the unattended guarantees come from
	// print mode plus --no-approve (no project-local extensions, no trust prompt).
	wantPrefix := []string{"--print", "--no-session", "--no-approve", "--"}
	if len(got.args) != len(wantPrefix)+1 {
		t.Fatalf("args = %q, want %d elements", got.args, len(wantPrefix)+1)
	}
	if joinArgs(got.args[:len(wantPrefix)]) != joinArgs(wantPrefix) {
		t.Errorf("args prefix = %q, want %q", got.args[:len(wantPrefix)], wantPrefix)
	}
	assertRebasePrompt(t, got.args[len(got.args)-1], "develop", "do the thing")

	// The prompt must come after "--" so a prompt starting with a dash is not
	// parsed as a flag.
	if got.args[len(got.args)-2] != "--" {
		t.Errorf("prompt is not preceded by %q: %q", "--", got.args)
	}
}

// assertRebasePrompt checks the prompt carries the branch and task context and
// tells the agent not to abort.
func assertRebasePrompt(t *testing.T, prompt, sourceBranch, task string) {
	t.Helper()
	if prompt != rebaseConflictPrompt(sourceBranch, task) {
		t.Fatalf("prompt = %q, want rebaseConflictPrompt output", prompt)
	}
	for _, want := range []string{"git rebase " + sourceBranch, task, "Do not abort the rebase"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func hasEnv(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

func TestRebaseWithAgentTimeoutAbortsRebase(t *testing.T) {
	prev := rebaseSessionTimeout
	rebaseSessionTimeout = 20 * time.Millisecond
	t.Cleanup(func() { rebaseSessionTimeout = prev })

	calls := fakeRunner(t, func(ctx context.Context, c recordedCommand) ([]byte, error) {
		if c.binary != "pi" {
			return nil, nil // the abort call
		}
		// Simulate a session that hangs until the context deadline kills it.
		<-ctx.Done()
		return []byte("partial session output"), errors.New("signal: killed")
	})

	err := rebaseWithAgent(context.Background(), NewPiBackend(""), "/wt", "main", "task")
	if err == nil {
		t.Fatal("expected an error when the session times out")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %v, want it to mention the timeout", err)
	}
	if !strings.Contains(err.Error(), "rebase aborted") {
		t.Errorf("error = %v, want it to report that the rebase was aborted", err)
	}

	if len(*calls) != 2 {
		t.Fatalf("expected session + abort, got %d calls: %+v", len(*calls), *calls)
	}
	abort := (*calls)[1]
	if abort.binary != "git" || joinArgs(abort.args) != joinArgs([]string{"-C", "/wt", "rebase", "--abort"}) {
		t.Errorf("cleanup call = %s %q, want git -C /wt rebase --abort", abort.binary, abort.args)
	}
}

func TestRebaseWithAgentSessionFailureAbortsRebase(t *testing.T) {
	calls := fakeRunner(t, func(_ context.Context, c recordedCommand) ([]byte, error) {
		if c.binary == "claude" {
			return []byte("boom"), errors.New("exit status 1")
		}
		return nil, nil
	})

	err := rebaseWithAgent(context.Background(), NewClaudeBackend(), "/wt", "main", "task")
	if err == nil {
		t.Fatal("expected an error when the session exits non-zero")
	}
	if !strings.Contains(err.Error(), "exit status 1") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want it to carry the exit status and session output", err)
	}
	if len(*calls) != 2 || (*calls)[1].binary != "git" {
		t.Fatalf("expected a git rebase --abort after failure, got %+v", *calls)
	}
}

// unattendedRefusingBackend is a backend that cannot guarantee a non-interactive
// session — the case the merge path must refuse rather than risk hanging on.
type unattendedRefusingBackend struct{ Backend }

func (unattendedRefusingBackend) Name() string { return "toybackend" }

func (unattendedRefusingBackend) UnattendedSessionCommand(string) (string, []string, error) {
	return "", nil, errors.New("no non-interactive mode")
}

func TestRebaseWithAgentUnsupportedBackendLaunchesNothing(t *testing.T) {
	calls := fakeRunner(t, func(_ context.Context, c recordedCommand) ([]byte, error) {
		t.Errorf("no command should be launched, got %s %q", c.binary, c.args)
		return nil, nil
	})

	err := rebaseWithAgent(context.Background(), unattendedRefusingBackend{}, "/wt", "main", "task")
	if err == nil {
		t.Fatal("expected an error for a backend without unattended session support")
	}
	if !strings.Contains(err.Error(), "toybackend") || !strings.Contains(err.Error(), "no non-interactive mode") {
		t.Errorf("error = %v, want it to name the backend and the reason", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("expected no commands, got %+v", *calls)
	}
}

func TestRebaseWithAgentNilBackend(t *testing.T) {
	calls := fakeRunner(t, func(_ context.Context, c recordedCommand) ([]byte, error) {
		t.Errorf("no command should be launched, got %s %q", c.binary, c.args)
		return nil, nil
	})

	err := rebaseWithAgent(context.Background(), nil, "/wt", "main", "task")
	if err == nil {
		t.Fatal("expected an error for a nil backend")
	}
	if len(*calls) != 0 {
		t.Fatalf("expected no commands, got %+v", *calls)
	}
}

func TestTailOutputTruncatesLongSessions(t *testing.T) {
	long := strings.Repeat("x", maxSessionOutput) + "TAIL"
	got := tailOutput([]byte(long))
	if len(got) > maxSessionOutput+len("...(truncated)...\n") {
		t.Errorf("tailOutput returned %d bytes, want it capped", len(got))
	}
	if !strings.HasSuffix(got, "TAIL") {
		t.Errorf("tailOutput dropped the tail of the output")
	}
	if !strings.HasPrefix(got, "...(truncated)...") {
		t.Errorf("tailOutput did not mark the output as truncated")
	}

	short := []byte("all of it")
	if tailOutput(short) != "all of it" {
		t.Errorf("tailOutput mangled short output: %q", tailOutput(short))
	}
}

func TestBackendLabel(t *testing.T) {
	if got := backendLabel(nil); got != "agent" {
		t.Errorf("backendLabel(nil) = %q, want agent", got)
	}
	if got := backendLabel(NewPiBackend("")); got != "pi" {
		t.Errorf("backendLabel(pi) = %q, want pi", got)
	}
	if got := backendLabel(NewClaudeBackend()); got != "claude" {
		t.Errorf("backendLabel(claude) = %q, want claude", got)
	}
}
