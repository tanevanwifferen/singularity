package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// processAlive reports whether pid is still a live process. Signal 0 is the
// portable existence probe: it validates the target without delivering
// anything.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func startStubAgent(t *testing.T, e *Engine) (string, int) {
	t.Helper()
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not available")
	}
	id, err := e.StartAgent(t.TempDir(), "task", AgentOptions{Backend: stubBackend{}})
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	a := e.GetAgent(id)
	if a == nil {
		t.Fatalf("agent %s not registered", id)
	}
	a.mu.Lock()
	pid := 0
	if a.cmd != nil && a.cmd.Process != nil {
		pid = a.cmd.Process.Pid
	}
	a.mu.Unlock()
	if pid == 0 {
		t.Fatal("agent has no subprocess")
	}
	return id, pid
}

// TestKillAgentLeavesTheProcessAlive pins the soft-close contract the TUI
// depends on, so the difference from TerminateAgent is documented by a test
// rather than only by a comment.
func TestKillAgentLeavesTheProcessAlive(t *testing.T) {
	e := New(2)
	id, pid := startStubAgent(t, e)
	t.Cleanup(func() { _ = e.RemoveAgent(id) })

	if err := e.KillAgent(id); err != nil {
		t.Fatalf("KillAgent: %v", err)
	}
	// No wait needed: softClose does not touch the process, so if it were
	// going to die it would already be dying.
	time.Sleep(50 * time.Millisecond)
	if !processAlive(pid) {
		t.Fatalf("pid %d is gone: KillAgent must soft-close, leaving the process addressable", pid)
	}
	if got := e.GetAgent(id).Snapshot().State; got != AgentKilled {
		t.Errorf("state after KillAgent = %s, want killed", got)
	}
}

// TestTerminateAgentKillsTheProcess is the queue's requirement: after the
// call the OS process is really gone, so the agent no longer occupies its
// working directory, while the record survives for its transcript.
func TestTerminateAgentKillsTheProcess(t *testing.T) {
	e := New(2)
	id, pid := startStubAgent(t, e)

	if err := e.TerminateAgent(id); err != nil {
		t.Fatalf("TerminateAgent: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("pid %d still alive 2s after TerminateAgent", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}

	a := e.GetAgent(id)
	if a == nil {
		t.Fatal("TerminateAgent dropped the agent record; the transcript has to stay reachable")
	}
	if got := a.Snapshot().State; got != AgentKilled {
		t.Errorf("state after TerminateAgent = %s, want killed", got)
	}
	if a.IsActive() {
		t.Error("a terminated agent must not report as active")
	}
	if len(e.ActiveAgents()) != 0 {
		t.Errorf("ActiveAgents = %d, want 0", len(e.ActiveAgents()))
	}
	if _, err := os.Stat(a.Snapshot().WorkDir); err != nil {
		t.Errorf("work dir should still exist: %v", err)
	}
}

// TestTerminateAgentReleasesARoutingAgent covers the case kill() alone
// cannot: an agent cancelled while the classifier is still choosing its
// model has no subprocess yet, and leaving its state untouched would keep
// IsActive true forever — an engine slot and a working directory held by an
// agent that will never run.
func TestTerminateAgentReleasesARoutingAgent(t *testing.T) {
	routing := newAgent("a-routing", t.TempDir(), "task", AgentOptions{}, stubBackend{})
	routing.setState(AgentRouting)
	if !routing.IsActive() {
		t.Fatal("a routing agent is expected to count as active")
	}
	if err := routing.terminate(); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if routing.IsActive() {
		t.Error("terminate left a routing agent active")
	}
	if got := routing.Snapshot().State; got != AgentKilled {
		t.Errorf("state after terminate = %s, want killed", got)
	}
	// The routing goroutine calls start() when the classifier returns;
	// having been terminated, the agent must refuse to launch.
	if err := routing.start(); err == nil {
		t.Error("start() succeeded on a terminated agent")
	}

	// softClose, by contrast, cannot move a routing agent out of active.
	// That asymmetry is why the queue needs its own call rather than
	// reusing the TUI's.
	soft := newAgent("a-soft", t.TempDir(), "task", AgentOptions{}, stubBackend{})
	soft.setState(AgentRouting)
	soft.softClose()
	if !soft.IsActive() {
		t.Error("softClose released a routing agent; if that is now intended, terminate's nil-cmd branch can be simplified")
	}
}

// TestWorkDirOccupiedFreesADirectoryTerminatedWhileRouting is a corollary of
// review cycle 7 finding 1's fix: WorkDirOccupied has to ask about the
// process, but an agent terminated before it ever started one (still
// routing, cmd nil) has no waitForExit goroutine to close done for it —
// kill()'s nil-cmd branch has to close it directly. Getting that wrong
// either way is a real failure: closing it unconditionally would let a kill
// on a not-yet-terminal agent lie about a process that has not launched
// yet; never closing it would hold the directory busy forever once the
// agent is genuinely terminated, since nothing else will ever end it.
func TestWorkDirOccupiedFreesADirectoryTerminatedWhileRouting(t *testing.T) {
	e := New(2)
	dir := t.TempDir()
	a := newAgent("a-routing", dir, "task", AgentOptions{}, stubBackend{})
	a.setState(AgentRouting)
	e.mu.Lock()
	e.agents[a.ID] = a
	e.mu.Unlock()

	if !e.WorkDirOccupied(dir) {
		t.Fatal("a routing agent's directory must be reported occupied — a real subprocess may be about to start there")
	}
	if err := a.terminate(); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if e.WorkDirOccupied(dir) {
		t.Error("WorkDirOccupied = true after a routing agent was terminated with no process ever started: this directory could never be dispatched into again")
	}
	// terminate's own guarantee, restated: a late start() must still refuse,
	// or the directory this just reported free could get a real process
	// after all.
	if err := a.start(); err == nil {
		t.Error("start() succeeded on a terminated agent — it could have resurrected a process into a directory already reported free")
	}
}

// TestWorkDirOccupiedStaysBusyForARoutingAgentKilledDirectly is
// TestWorkDirOccupiedFreesADirectoryTerminatedWhileRouting's other half: the
// per-agent timeout goroutine (StartAgent) calls kill(false) directly, not
// terminate — so a slow classifier that outlives its timeout can hit kill's
// nil-cmd branch while the agent is still AgentRouting, not yet terminal.
// closeDone must not run in that case: the classifier can still return and
// call start() for real, and a directory this reported free the moment
// before a real process starts in it is exactly review cycle 7 finding 1's
// bug, self-inflicted this time by kill() itself rather than by a foreign
// agent's label.
func TestWorkDirOccupiedStaysBusyForARoutingAgentKilledDirectly(t *testing.T) {
	e := New(2)
	dir := t.TempDir()
	a := newAgent("a-routing", dir, "task", AgentOptions{}, stubBackend{})
	a.setState(AgentRouting)
	e.mu.Lock()
	e.agents[a.ID] = a
	e.mu.Unlock()

	if err := a.kill(false); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if got := a.Snapshot().State; got != AgentRouting {
		t.Fatalf("state after a direct kill = %s, want unchanged routing (kill's nil-cmd branch does not force it)", got)
	}
	if !e.WorkDirOccupied(dir) {
		t.Error("WorkDirOccupied = false after kill(false) on a still-routing agent: the classifier can still return and start a real process in this directory")
	}
}

// TestTerminateIsANoOpOnAnAlreadyTerminalAgent covers the race a cancel can
// win against reconcile: handleResult moves a worktree agent to complete (or
// error) and deliberately leaves the merged worktree in place — cleanup is
// documented as deferred to kill() so follow-up messages can still reach it
// (worktree.go) — before the queue's next tick ever reads the new state. A
// cancel landing in that window used to force through kill() regardless,
// overwriting a genuine complete/error outcome with killed and running the
// unconditional worktree cleanup kill() performs. terminate must leave an
// agent that already stopped exactly as it stopped.
//
// These agents never started (cmd is nil), so this exercises the "process
// never existed" half of terminate's gate. TestTerminateEndsAnAliveAgentEvenWhenAlreadyLabelledTerminal
// below exercises the other half: the same labels with a process that is
// very much still alive.
func TestTerminateIsANoOpOnAnAlreadyTerminalAgent(t *testing.T) {
	for _, terminal := range []AgentState{AgentComplete, AgentError} {
		a := newAgent("a-"+terminal.String(), t.TempDir(), "task", AgentOptions{}, stubBackend{})
		a.setState(terminal)

		if err := a.terminate(); err != nil {
			t.Fatalf("terminate on a %s agent: %v", terminal, err)
		}
		if got := a.Snapshot().State; got != terminal {
			t.Errorf("state after terminate = %s, want unchanged %s", got, terminal)
		}
		if a.IsActive() {
			t.Errorf("a %s agent must not report as active", terminal)
		}
	}
}

// TestTerminateEndsAnAliveAgentEvenWhenAlreadyLabelledTerminal pins review
// cycle 5's finding 2: a terminal state label is not a statement about the
// OS process. softClose sets State=killed and deliberately leaves the
// process running; the pi backend's session process stays resident past a
// BackendResult event, so State can reach complete or error with the process
// still alive too. Before this fix, terminate()'s early return matched on
// a.State.Terminal() alone, so calling it on any of these left the process
// running forever — silently disabling the one call queue cleanup paths rely
// on to actually end a soft-closed or resident agent.
func TestTerminateEndsAnAliveAgentEvenWhenAlreadyLabelledTerminal(t *testing.T) {
	for _, terminal := range []AgentState{AgentKilled, AgentComplete, AgentError} {
		t.Run(terminal.String(), func(t *testing.T) {
			e := New(2)
			id, pid := startStubAgent(t, e)
			a := e.GetAgent(id)
			// Bypass the normal transitions to get a terminal label onto an
			// agent whose process the test controls directly — the same
			// shape softClose and a resident pi session produce in
			// production, without needing either backend here.
			a.mu.Lock()
			a.State = terminal
			a.mu.Unlock()

			if err := e.TerminateAgent(id); err != nil {
				t.Fatalf("TerminateAgent: %v", err)
			}

			deadline := time.Now().Add(2 * time.Second)
			for processAlive(pid) {
				if time.Now().After(deadline) {
					t.Fatalf("pid %d still alive after TerminateAgent on a %s-labelled agent: the label must not stop the process from being ended", pid, terminal)
				}
				time.Sleep(10 * time.Millisecond)
			}

			if got := a.Snapshot().State; got != terminal {
				t.Errorf("state after terminate = %s, want unchanged %s — a terminal label's outcome must survive", got, terminal)
			}
		})
	}
}

// failBinaryBackend names a binary that does not exist, so cmd.Start() fails
// inside Agent.start() with cmd.Process left nil — a spawn failure after the
// agent record already exists, with no subprocess ever created.
type failBinaryBackend struct{}

func (failBinaryBackend) Name() string   { return "fail-binary-stub" }
func (failBinaryBackend) Binary() string { return "/nonexistent/singularity-test-binary-xyz" }
func (failBinaryBackend) Args(string, string, int, []string) []string {
	return nil
}
func (failBinaryBackend) Env() []string                               { return nil }
func (failBinaryBackend) InitialInput(task, _ string) ([]byte, error) { return []byte(task), nil }
func (failBinaryBackend) PostStartCommands(string) [][]byte           { return nil }
func (failBinaryBackend) ParseEvent(line []byte) ([]*BackendEvent, error) {
	return []*BackendEvent{}, nil
}
func (failBinaryBackend) OneShotCommand(prompt string) (string, []string) { return "true", nil }
func (failBinaryBackend) UnattendedSessionCommand(prompt string) (string, []string, error) {
	return "true", nil, nil
}
func (failBinaryBackend) FollowUpInput(message, _ string, _ bool) ([]byte, error) {
	return []byte(message), nil
}

// TestWorkDirOccupiedIsFreeAfterAFailedSpawn is review cycle 8 finding 1: the
// mirror image of cycles 3-7's bug. processExited() answers a two-state
// question (has the process ended?) about a three-state world (never
// started / running / exited); collapsing "never started" into "still
// running" is safe for a live agent and catastrophic for one whose spawn
// failed. Before this fix neither of these paths ever closed done — nothing
// runs waitForExit for an agent with no subprocess — so WorkDirOccupied
// reported the directory busy for the engine's lifetime.
func TestWorkDirOccupiedIsFreeAfterAFailedSpawn(t *testing.T) {
	t.Run("cmd.Start failure", func(t *testing.T) {
		e := New(2)
		dir := t.TempDir()
		_, err := e.StartAgent(dir, "task", AgentOptions{Backend: failBinaryBackend{}})
		if err == nil {
			t.Fatal("StartAgent with a missing binary succeeded, want an error")
		}
		if e.WorkDirOccupied(dir) {
			t.Error("WorkDirOccupied = true after a spawn that never created a subprocess")
		}
	})

	t.Run("setupWorktree failure", func(t *testing.T) {
		e := New(2)
		dir := t.TempDir() // not a git repo: setupWorktree's gitCurrentBranch fails
		id, err := e.StartAgent(dir, "task", AgentOptions{Backend: stubBackend{}, UseWorktree: true})
		if err == nil {
			t.Fatal("StartAgent with UseWorktree against a non-repo dir succeeded, want an error")
		}
		if id == "" {
			t.Fatal("StartAgent returned no agent id alongside the worktree-setup error")
		}
		if e.WorkDirOccupied(dir) {
			t.Error("WorkDirOccupied = true after a worktree-setup failure that never created a subprocess")
		}
	})
}

// TestRemoveAgentWhileRoutingStartsNoProcess is review cycle 8 finding 2:
// RemoveAgent used to call a bare kill(false), whose nil-cmd branch neither
// forces the state terminal nor closes done when the agent has not started
// yet. Smart routing is the default for a queued task, so an operator (or
// the queue) removing an agent in its first seconds used to leave the
// pending classifier free to call start() for real after the record was
// already gone — a live process WorkDirOccupied could no longer see.
func TestRemoveAgentWhileRoutingStartsNoProcess(t *testing.T) {
	e := New(2)
	dir := t.TempDir()
	a := newAgent("a-routing", dir, "task", AgentOptions{}, stubBackend{})
	a.setState(AgentRouting)
	e.mu.Lock()
	e.agents[a.ID] = a
	e.mu.Unlock()

	if err := e.RemoveAgent(a.ID); err != nil {
		t.Fatalf("RemoveAgent: %v", err)
	}
	// The classifier returning after removal must not be able to start a
	// real process: terminate() forces the state terminal precisely so this
	// refuses.
	if err := a.start(); err == nil {
		t.Error("start() succeeded on an agent removed while routing — a process could still appear in a directory nothing tracks any more")
	}
	if e.WorkDirOccupied(dir) {
		t.Error("WorkDirOccupied = true for a directory whose only agent was removed while routing and can never start a process now")
	}
}

// slowRouteBackend's classifier one-shot sleeps past the timeout under test,
// so the agent is still AgentRouting when the timeout fires; if start() ever
// runs anyway, Binary/Args writes its pid to pidFile and blocks on stdin
// (like pidBackend in the queue package), making a wrongly-spawned process
// detectable rather than merely "no error observed".
type slowRouteBackend struct{ pidFile string }

func (b slowRouteBackend) Name() string   { return "slow-route-stub" }
func (b slowRouteBackend) Binary() string { return "sh" }
func (b slowRouteBackend) Args(string, string, int, []string) []string {
	return []string{"-c", "echo $$ > " + b.pidFile + "; exec cat"}
}
func (slowRouteBackend) Env() []string                               { return nil }
func (slowRouteBackend) InitialInput(task, _ string) ([]byte, error) { return []byte(task), nil }
func (slowRouteBackend) PostStartCommands(string) [][]byte           { return nil }
func (slowRouteBackend) ParseEvent(line []byte) ([]*BackendEvent, error) {
	return []*BackendEvent{}, nil
}
func (slowRouteBackend) OneShotCommand(prompt string) (string, []string) {
	return "sh", []string{"-c", `sleep 0.5; printf '%s' '{"category":"implementation","effort":"low","reason":"r","summary":"s"}'`}
}
func (slowRouteBackend) UnattendedSessionCommand(prompt string) (string, []string, error) {
	return "true", nil, nil
}
func (slowRouteBackend) FollowUpInput(message, _ string, _ bool) ([]byte, error) {
	return []byte(message), nil
}

// TestTimeoutDuringRoutingStopsTheAgent is review cycle 8 finding 3: the
// per-agent timeout goroutine called a bare kill(false), which does nothing
// on a not-yet-terminal, not-yet-started agent (cmd is nil, state is
// AgentRouting) — so a timeout firing while the classifier is still choosing
// a model was silently voided, and the agent went on to run unbounded once
// the classifier returned. This drives the real StartAgent timeout path
// (opts.Timeout) with a classifier slow enough to still be running when the
// timeout fires, exactly like the finding's failure scenario.
func TestTimeoutDuringRoutingStopsTheAgent(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	e := New(2)

	id, err := e.StartAgent(dir, "task", AgentOptions{
		Backend:    slowRouteBackend{pidFile: pidFile},
		SmartRoute: true,
		Timeout:    150 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	a := e.GetAgent(id)

	// The classifier (500ms) outlives the timeout (150ms): the agent must
	// still be routing when the timeout fires.
	time.Sleep(250 * time.Millisecond)
	if got := a.Snapshot().State; got != AgentKilled {
		t.Fatalf("state 250ms after a 150ms timeout fired mid-routing = %s, want killed", got)
	}

	// Give the classifier time to return and (if the bug is present) call
	// start() for real.
	time.Sleep(500 * time.Millisecond)
	if got := a.Snapshot().State; got != AgentKilled {
		t.Errorf("state after the classifier returned = %s, want unchanged killed — start() must have refused", got)
	}
	if data, err := os.ReadFile(pidFile); err == nil {
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		if pid > 0 && processAlive(pid) {
			t.Fatalf("pid %d is alive: the timeout was voided and the classifier started a real, unbounded process", pid)
		}
	}
	if e.WorkDirOccupied(dir) {
		t.Error("WorkDirOccupied = true after a routing timeout with no process ever started")
	}
}

// TestTerminatePreservesTheWorktreeOnlyForCompleteOrError pins the other half
// of b03f146's preserveWorktree parameter — the reason it was added in the
// first place — which TestTerminateIsANoOpOnAnAlreadyTerminalAgent and
// TestTerminateEndsAnAliveAgentEvenWhenAlreadyLabelledTerminal both leave
// unpinned: they only assert Snapshot().State (preserved by the separate
// `if !preserveWorktree { a.State = AgentKilled }` in terminate()) and never
// touch worktreePath or the cleanup branch. Deleting `&& !preserveWorktree`
// from kill()'s two guards (agent.go:654 and agent.go:675) leaves the whole
// suite green without this test: a complete or errored agent's merged
// worktree must survive termination, exactly like the handleResult comment
// in worktree.go promises, while a killed agent's worktree must still be
// force-cleaned as before.
//
// cleanupWorktreeFn is swapped for a recorder rather than driving a real git
// repository: what is under test is kill()'s decision to call it at all, not
// cleanupWorktree's own git commands (which have no preserveWorktree
// branching to protect). Exercised against both of kill()'s guards: the
// nil-cmd path (agent.go:654, a never-started agent) and the live-process
// path (agent.go:675, startStubAgent's real subprocess).
func TestTerminatePreservesTheWorktreeOnlyForCompleteOrError(t *testing.T) {
	orig := cleanupWorktreeFn
	t.Cleanup(func() { cleanupWorktreeFn = orig })

	cases := []struct {
		state       AgentState
		wantCleanup bool
	}{
		{AgentComplete, false},
		{AgentError, false},
		{AgentKilled, true},
	}

	for _, tc := range cases {
		t.Run("live/"+tc.state.String(), func(t *testing.T) {
			e := New(2)
			id, pid := startStubAgent(t, e)
			a := e.GetAgent(id)

			a.mu.Lock()
			a.State = tc.state
			a.worktreePath = "/fake/worktree/path"
			a.worktreeBranch = "agent/fake/branch"
			a.sourceRepoPath = "/fake/repo"
			a.mu.Unlock()

			called := false
			cleanupWorktreeFn = func(string, string, string) { called = true }

			if err := e.TerminateAgent(id); err != nil {
				t.Fatalf("TerminateAgent: %v", err)
			}

			deadline := time.Now().Add(2 * time.Second)
			for processAlive(pid) {
				if time.Now().After(deadline) {
					t.Fatalf("pid %d still alive after TerminateAgent", pid)
				}
				time.Sleep(10 * time.Millisecond)
			}

			if called != tc.wantCleanup {
				t.Errorf("cleanupWorktree called = %v, want %v for a %s agent whose process was alive", called, tc.wantCleanup, tc.state)
			}
		})
	}

	for _, tc := range cases {
		t.Run("never-started/"+tc.state.String(), func(t *testing.T) {
			a := newAgent("a-"+tc.state.String(), t.TempDir(), "task", AgentOptions{}, stubBackend{})
			a.mu.Lock()
			a.State = tc.state
			a.worktreePath = "/fake/worktree/path"
			a.worktreeBranch = "agent/fake/branch"
			a.sourceRepoPath = "/fake/repo"
			a.mu.Unlock()

			called := false
			cleanupWorktreeFn = func(string, string, string) { called = true }

			if err := a.terminate(); err != nil {
				t.Fatalf("terminate: %v", err)
			}

			if called != tc.wantCleanup {
				t.Errorf("cleanupWorktree called = %v, want %v for a never-started %s agent", called, tc.wantCleanup, tc.state)
			}
		})
	}
}
