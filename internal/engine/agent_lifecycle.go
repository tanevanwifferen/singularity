package engine

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// start launches the coding-agent subprocess via the configured Backend.
func (a *Agent) start() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.State != AgentIdle && a.State != AgentRouting {
		return fmt.Errorf("agent %s is in state %s, expected idle or routing", a.ID, a.State)
	}

	a.State = AgentStarting

	binary, args := a.backend.Binary(), a.backend.Args(a.model, a.effort, a.maxTurns, a.allowedTools)
	a.cmd = exec.Command(binary, args...)
	a.cmd.Dir = a.WorkDir
	a.cmd.Env = a.backend.Env()

	var err error
	a.stdin, err = a.cmd.StdinPipe()
	if err != nil {
		a.setState(AgentError)
		a.Error = fmt.Sprintf("stdin pipe: %v", err)
		a.closeDone()
		return fmt.Errorf("agent %s stdin pipe: %w", a.ID, err)
	}
	a.stdout, err = a.cmd.StdoutPipe()
	if err != nil {
		a.setState(AgentError)
		a.Error = fmt.Sprintf("stdout pipe: %v", err)
		a.closeDone()
		return fmt.Errorf("agent %s stdout pipe: %w", a.ID, err)
	}
	a.stderr, err = a.cmd.StderrPipe()
	if err != nil {
		a.setState(AgentError)
		a.Error = fmt.Sprintf("stderr pipe: %v", err)
		a.closeDone()
		return fmt.Errorf("agent %s stderr pipe: %w", a.ID, err)
	}

	if err := a.cmd.Start(); err != nil {
		a.setState(AgentError)
		a.Error = fmt.Sprintf("start: %v", err)
		a.closeDone()
		return fmt.Errorf("agent %s start: %w", a.ID, err)
	}

	now := time.Now()
	a.StartedAt = &now
	a.State = AgentRunning

	if a.RouteResult != nil {
		// Report what is actually in effect, not just what the classifier
		// suggested: an explicit --model / --effort overrides its choice per
		// field, so the two can differ.
		a.appendOutputLocked("system", fmt.Sprintf("Routed → model=%s effort=%s (%s: %s)",
			routedField(a.model, a.RouteResult.Model), routedField(a.effort, a.RouteResult.Effort),
			a.RouteResult.Category, a.RouteResult.Reason))
	}
	a.appendOutputLocked("system", fmt.Sprintf("Agent %s started [%s]", a.ID, a.backend.Name()))

	go a.streamOutput(a.stdout)
	go a.streamStderr(a.stderr)
	go a.waitForExit()

	// Send post-start configuration commands (e.g. thinking level for pi).
	for _, cmd := range a.backend.PostStartCommands(a.effort) {
		a.stdinMu.Lock()
		_, _ = a.stdin.Write(cmd)
		a.stdinMu.Unlock()
	}

	// Send the initial task, logging what was actually sent so the output
	// stream shows the conversation from the beginning.
	go func() {
		task := a.buildTask()
		logText := a.promptLogNote
		if logText == "" {
			logText = task
		}
		a.appendOutput("user_input", truncateForLog(logText, promptLogLimit))
		if err := a.sendInitialInput(task); err != nil {
			a.appendOutput("error", fmt.Sprintf("Failed to send initial task: %v", err))
		}
	}()

	return nil
}

// buildTask prepends any context file contents to the agent's task string.
func (a *Agent) buildTask() string {
	if len(a.contextFiles) == 0 {
		return a.Task
	}

	var context strings.Builder
	for _, path := range a.contextFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			a.appendOutput("system", fmt.Sprintf("Warning: could not read context file %s: %v", path, err))
			continue
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		context.WriteString(fmt.Sprintf("<context file=%q>\n%s\n</context>\n\n", path, content))
	}

	if context.Len() == 0 {
		return a.Task
	}
	return context.String() + a.Task
}

// sendInitialInput sends the first task over stdin using backend.InitialInput.
func (a *Agent) sendInitialInput(task string) error {
	a.mu.Lock()
	sid := a.sessionID
	a.mu.Unlock()

	data, err := a.backend.InitialInput(task, sid)
	if err != nil {
		return fmt.Errorf("backend InitialInput: %w", err)
	}
	a.stdinMu.Lock()
	defer a.stdinMu.Unlock()
	if a.stdin == nil {
		return fmt.Errorf("stdin closed before initial task could be sent")
	}
	_, err = a.stdin.Write(data)
	return err
}

// waitForExit waits for the subprocess to finish
func (a *Agent) waitForExit() {
	err := a.cmd.Wait()

	a.mu.Lock()
	if a.EndedAt == nil {
		now := time.Now()
		a.EndedAt = &now
	}

	var exitErrMsg string
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			a.ExitCode = exitErr.ExitCode()
		}
		if a.State == AgentRunning || a.State == AgentStarting {
			a.State = AgentError
			a.Error = err.Error()
			exitErrMsg = err.Error()
		} else if a.State == AgentError && a.Error == "" {
			a.Error = err.Error()
			exitErrMsg = err.Error()
		}
	} else {
		if a.State == AgentRunning {
			a.State = AgentComplete
		}
	}
	a.mu.Unlock()

	if exitErrMsg != "" {
		a.appendOutput("error", fmt.Sprintf("Process exit: %s", exitErrMsg))
	} else if a.notify != nil {
		a.notify()
	}

	a.closeDone()
}

// closeDone closes the done channel exactly once. start() calls this itself
// on every failure that leaves cmd.Process nil (a pipe error or cmd.Start
// failing): waitForExit will never run to close it, and without this call
// processExited() would report such an agent as still occupying its
// directory for the engine's lifetime. kill() also calls this, for the
// killed-while-never-started case — see kill's nil-cmd branch — so all three
// share one guard rather than each risk closing an already-closed channel.
func (a *Agent) closeDone() {
	a.doneOnce.Do(func() { close(a.done) })
}

// routedField renders one routed field as "value" when the classifier's
// suggestion was applied, or "value (pinned, classifier: suggested)" when the
// caller overrode it.
func routedField(applied, suggested string) string {
	if applied == suggested || suggested == "" {
		return applied
	}
	return fmt.Sprintf("%s (pinned, classifier: %s)", applied, suggested)
}

// sendInput sends a follow-up message to the agent's stdin.
// Accepts messages to running, completed, or soft-closed agents (process stays
// alive until explicitly removed via RemoveAgent).
func (a *Agent) sendInput(message string) error {
	a.mu.Lock()
	if a.State != AgentRunning && a.State != AgentComplete && a.State != AgentKilled {
		a.mu.Unlock()
		return fmt.Errorf("agent %s is in state %s, cannot send input", a.ID, a.State)
	}
	if a.cmd == nil || a.cmd.Process == nil {
		a.mu.Unlock()
		return fmt.Errorf("agent %s process is no longer running", a.ID)
	}

	prevState := a.State
	prevEndedAt := a.EndedAt

	// Resume agent back to running when sending a follow-up
	resumed := false
	if a.State == AgentComplete || a.State == AgentKilled {
		a.State = AgentRunning
		a.EndedAt = nil
		resumed = true
	}

	// isStreaming: was the agent already in a running (mid-response) state
	// before this call? Used by some backends to choose the input message type.
	isStreaming := prevState == AgentRunning

	sid := a.sessionID
	a.mu.Unlock()

	data, err := a.backend.FollowUpInput(message, sid, isStreaming)
	if err != nil {
		a.restoreState(prevState, prevEndedAt, resumed)
		return fmt.Errorf("backend FollowUpInput: %w", err)
	}

	a.stdinMu.Lock()
	defer a.stdinMu.Unlock()

	if a.stdin == nil {
		a.restoreState(prevState, prevEndedAt, resumed)
		return fmt.Errorf("agent %s stdin not available (process exited)", a.ID)
	}

	_, err = a.stdin.Write(data)
	if err != nil {
		a.restoreState(prevState, prevEndedAt, resumed)
		return fmt.Errorf("write to stdin: %w (process may have exited)", err)
	}

	// Announce the terminal → running transition on its own, before the input
	// echo. appendOutput below also notifies, but it drops the notification
	// (and the entry) when the agent has been soft-closed in the meantime, so
	// leaning on it would let the resume go unobserved — and observers such as
	// the daemon's WS broadcast read the state from the snapshot a
	// notification hands them, not from the entry.
	if resumed && a.notify != nil {
		a.notify()
	}

	a.appendOutput("user_input", truncateForLog(message, promptLogLimit))
	return nil
}

// restoreState rolls back the resume transition sendInput made optimistically
// when the follow-up could not be delivered. The rollback notifies observers
// only when there was a transition to undo: a concurrent notification may
// already have published the AgentRunning snapshot, and without a second one
// observers would keep reporting an agent that never actually restarted.
func (a *Agent) restoreState(state AgentState, endedAt *time.Time, notifyObservers bool) {
	a.mu.Lock()
	a.State = state
	a.EndedAt = endedAt
	a.mu.Unlock()

	if notifyObservers && a.notify != nil {
		a.notify()
	}
}

// softClose marks the agent as killed without terminating the subprocess.
// The process stays alive and can still receive messages until RemoveAgent.
func (a *Agent) softClose() {
	a.mu.Lock()
	changed := false
	if a.State == AgentRunning || a.State == AgentStarting || a.State == AgentComplete {
		a.State = AgentKilled
		now := time.Now()
		if a.EndedAt == nil {
			a.EndedAt = &now
		}
		changed = true
	}
	a.mu.Unlock()

	if changed && a.notify != nil {
		a.notify()
	}
}

// terminate ends the agent for good: the subprocess is killed, any worktree
// is cleaned up (unless preserved, see below), and the record is kept so the
// transcript stays readable.
//
// It differs from softClose in that the process does not survive, and from
// kill in that the state is set even when no subprocess was ever started.
// That last part matters for an agent killed while it is still routing: kill
// alone returns early on a nil cmd, leaving the record in AgentRouting, where
// IsActive keeps reporting it as live forever — holding an engine slot and
// its working directory against an agent that will never run. Setting the
// state also makes the pending start() refuse, so the routing goroutine
// cannot resurrect it after the caller believed it gone.
//
// The early return is gated on the process, not the state label: a state of
// complete/error/killed is not proof the subprocess is gone. softClose sets
// State to killed while deliberately leaving the process running, and the pi
// backend's session process stays resident past a BackendResult event, so an
// agent can sit in AgentComplete or AgentError with its process still alive.
// Calling terminate on either must still end that process — that is the
// whole reason a caller reaches for terminate over softClose. Only once the
// process has genuinely exited (waitForExit has closed a.done, or it was
// never started) is there nothing left to do.
//
// A terminal record whose process is still alive keeps its state and its
// worktree: handleResult deliberately leaves a completed worktree agent's
// merged worktree in place (worktree.go) and an errored one's preserved for
// manual merge, precisely so a cancel landing in that window does not
// overwrite a genuine outcome with killed or force-remove what the operator
// still needs. So the process is ended but cleanupWorktree is skipped, and
// the label is left as complete/error. Everything else — still running,
// starting, routing, or already (soft-)killed — gets the full kill(): state
// becomes killed and the worktree is cleaned up.
//
// This is every caller's policy, including RemoveAgent and Shutdown, and
// that is deliberate, not an oversight: this project treats worktree
// reclamation as an operator action (the TUI's Worktrees view, or `git
// worktree remove`/`prune` by hand), not something the engine does on an
// agent's behalf. Do not "fix" RemoveAgent/Shutdown to force cleanupWorktree
// regardless of state — that would delete a completed agent's merged
// worktree and, worse, an errored one's, which is kept precisely so a human
// can salvage it. See the use_worktree note in cmd/singl/prime.md.
func (a *Agent) terminate() error {
	if a.processExited() {
		return nil
	}

	a.mu.Lock()
	preserveWorktree := a.State == AgentComplete || a.State == AgentError
	if !preserveWorktree {
		a.State = AgentKilled
	}
	if a.EndedAt == nil {
		now := time.Now()
		a.EndedAt = &now
	}
	a.mu.Unlock()

	err := a.kill(preserveWorktree)
	if a.notify != nil {
		a.notify()
	}
	return err
}

// processExited reports whether the subprocess is confirmed gone: either it
// never started, or waitForExit has already run cmd.Wait() to completion and
// closed done. Safe to call without a.mu — done is set once at construction
// and only ever closed, never reassigned.
func (a *Agent) processExited() bool {
	select {
	case <-a.done:
		return true
	default:
		return false
	}
}

// terminationGrace is how long this agent's backend wants between SIGTERM and
// SIGKILL. Zero (the default for every backend that does not implement
// GracefulBackend) means SIGKILL straight away.
func (a *Agent) terminationGrace() time.Duration {
	if graceful, ok := a.backend.(GracefulBackend); ok {
		return graceful.TerminationGrace()
	}
	return 0
}

// terminateProcess ends proc, giving it grace to exit on its own first.
//
// With grace > 0 the process gets SIGTERM and up to grace to clean up after
// itself before SIGKILL follows; done is closed by waitForExit once the
// process is reaped, so a process that exits early costs no waiting. This is
// what lets a backend release resources it does not itself own — the herdr
// driver closes its herdr workspace here, and without it the pane and the
// claude session inside it survive the agent indefinitely (see
// GracefulBackend).
//
// The returned error is from the signal that was actually needed; a clean
// exit within the grace period returns nil.
func terminateProcess(proc *os.Process, done <-chan struct{}, grace time.Duration) error {
	if grace > 0 {
		// A platform that cannot deliver SIGTERM (Windows) errors here
		// immediately; there is nothing to wait for in that case, so fall
		// straight through to Kill rather than burning the grace period.
		if err := proc.Signal(syscall.SIGTERM); err == nil {
			select {
			case <-done:
				return nil
			case <-time.After(grace):
			}
		}
	}
	return proc.Kill()
}

// kill terminates the agent subprocess and, unless preserveWorktree is set,
// cleans up any worktree and forces the state to killed. preserveWorktree is
// for terminate's complete/error-but-still-alive case (see terminate); every
// other caller (RemoveAgent, Shutdown, the per-agent timeout) wants the
// unconditional behaviour and passes false.
//
// kill does not return until the process is actually gone: callers —
// Engine.TerminateAgent above all, which the queue's scheduler relies on to
// free a directory before the same tick's dispatch runs — need
// processExited to already be true the instant this returns, since
// Engine.WorkDirOccupied asks the process, not the state label. A signal
// alone is not enough: the process is not confirmed reaped until
// waitForExit's cmd.Wait returns and closes done.
//
// Backends that implement GracefulBackend get SIGTERM and a grace period
// before SIGKILL (see terminateProcess). That is also why cleanupWorktree
// below is safe to run right after: the subprocess has had its chance to stop
// whatever it started elsewhere — the herdr backend's pane, and the claude
// process inside it whose cwd is the worktree about to be removed.
func (a *Agent) kill(preserveWorktree bool) error {
	a.mu.Lock()

	wtPath := a.worktreePath
	sourceRepoPath := a.sourceRepoPath
	wtBranch := a.worktreeBranch

	if a.cmd == nil || a.cmd.Process == nil {
		// No process was ever created — the agent was killed before start()
		// ran (e.g. still routing). done is only ever closed here or by
		// waitForExit, and waitForExit cannot run without a started cmd, so
		// it is safe to close it ourselves — but only once nothing can
		// start a process later: start() refuses once the state is
		// terminal (see terminate's doc), which is exactly the condition
		// checked below. If the state is not yet terminal (a bare kill on
		// an otherwise-untouched agent, without terminate's state force),
		// leave done alone: the routing goroutine may still call start()
		// for real.
		terminalNow := a.State.Terminal()
		a.mu.Unlock()
		if terminalNow {
			a.closeDone()
		}
		if wtPath != "" && !preserveWorktree {
			cleanupWorktreeFn(sourceRepoPath, wtPath, wtBranch)
		}
		return nil
	}

	a.stdinMu.Lock()
	if a.stdin != nil {
		a.stdin.Close()
		a.stdin = nil
	}
	a.stdinMu.Unlock()

	if !preserveWorktree && a.State != AgentKilled {
		a.State = AgentKilled
	}
	a.appendOutputLocked("system", "Agent killed")

	proc := a.cmd.Process
	grace := a.terminationGrace()
	a.mu.Unlock()

	err := terminateProcess(proc, a.done, grace)

	// Wait for waitForExit's cmd.Wait to actually reap the process. SIGKILL
	// cannot be blocked, so this is bounded by how fast the kernel delivers
	// it — except for a process stuck in uninterruptible I/O (D state),
	// which SIGKILL cannot touch either; that is a pre-existing OS-level
	// risk this does not introduce, not a new one.
	<-a.done

	if wtPath != "" && !preserveWorktree {
		cleanupWorktreeFn(sourceRepoPath, wtPath, wtBranch)
	}

	if err != nil {
		return fmt.Errorf("kill agent %s: %w", a.ID, err)
	}
	return nil
}
