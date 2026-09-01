// Package oneshot runs single-prompt, single-answer calls against whichever
// coding-agent CLI the daemon is configured to use.
//
// The four historic call sites (commit messages, MR title/description, worktree
// auto-commit and merge messages) all shelled out to `claude` directly. They now
// go through Run, which asks the configured Commander for the binary and argv,
// so switching ai.provider switches the one-shot calls too.
package oneshot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Commander produces the binary and argv for a one-shot prompt.
//
// engine.Backend satisfies this interface via its OneShotCommand method. The
// interface is redeclared here rather than imported so this package stays a
// leaf: internal/git can depend on it without pulling in (and risking an import
// cycle with) internal/engine.
type Commander interface {
	OneShotCommand(prompt string) (binary string, args []string)
}

// Runner executes a prepared command and returns its stdout.
// Tests replace it to keep them hermetic — no real subprocess, no network.
type Runner func(ctx context.Context, dir, binary string, args, env []string) ([]byte, error)

// Request describes one prompt call.
type Request struct {
	// Prompt is the full text sent to the model.
	Prompt string
	// Dir is the working directory for the subprocess; empty means inherit.
	Dir string
	// Timeout bounds the call; zero means no timeout.
	Timeout time.Duration
	// Runner overrides command execution. Nil uses the real exec runner.
	Runner Runner
}

// ErrNoCommander is returned when no Commander was passed and none was
// installed with SetDefault. Callers are expected to degrade gracefully.
var ErrNoCommander = errors.New("oneshot: no backend configured")

var (
	mu         sync.RWMutex
	defaultCmd Commander
)

// SetDefault installs the process-wide Commander used when Run is called with a
// nil Commander. The daemon calls this at startup with the backend resolved
// from ai.provider, which is how packages outside the engine (internal/git)
// learn which CLI to drive without importing the engine.
func SetDefault(c Commander) {
	mu.Lock()
	defaultCmd = c
	mu.Unlock()
}

// Default returns the process-wide Commander, or nil if none was installed.
func Default() Commander {
	mu.RLock()
	defer mu.RUnlock()
	return defaultCmd
}

// Run sends req.Prompt to c and returns the trimmed answer.
// A nil c falls back to the Commander installed by SetDefault.
//
// Errors are deliberately specific — binary missing, non-zero exit (with
// stderr), timeout, empty answer — because every caller logs them and then
// falls back to a heuristic result.
func Run(ctx context.Context, c Commander, req Request) (string, error) {
	if c == nil {
		c = Default()
	}
	if c == nil {
		return "", ErrNoCommander
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return "", errors.New("oneshot: empty prompt")
	}

	binary, args := c.OneShotCommand(req.Prompt)
	if binary == "" {
		return "", errors.New("oneshot: backend returned no binary")
	}

	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	run := req.Runner
	if run == nil {
		run = execRunner
	}

	out, err := run(ctx, req.Dir, binary, args, []string{"CLAUDE_NO_ANALYTICS=true"})
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("oneshot: %s timed out after %v", binary, req.Timeout)
		}
		return "", err
	}

	answer := strings.TrimSpace(string(out))
	if answer == "" {
		return "", fmt.Errorf("oneshot: %s returned an empty answer", binary)
	}
	return answer, nil
}

// execRunner is the production Runner: it runs the command and captures stdout,
// folding stderr into the error so failures stay diagnosable.
func execRunner(ctx context.Context, dir, binary string, args, env []string) ([]byte, error) {
	if _, err := exec.LookPath(binary); err != nil {
		return nil, fmt.Errorf("oneshot: %q not found on PATH — install it or change ai.provider: %w", binary, err)
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if detail := truncate(strings.TrimSpace(stderr.String()), 500); detail != "" {
			return nil, fmt.Errorf("oneshot: %s failed: %w: %s", binary, err, detail)
		}
		return nil, fmt.Errorf("oneshot: %s failed: %w", binary, err)
	}
	return stdout.Bytes(), nil
}

// truncate shortens s to at most n bytes, marking elision.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
