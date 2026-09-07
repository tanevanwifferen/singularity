package queue

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
)

// pidBackend spawns `sh -c 'echo $$ >pidfile; exec cat'` instead of a coding
// agent: no backend binary, no API key, no spend. `exec cat` replaces the
// shell so the recorded pid is the process the engine actually holds, and
// cat blocks on the stdin pipe the engine keeps open, so the agent stays
// alive until something kills it — which is the whole subject of these
// tests.
type pidBackend struct{ pidFile string }

func (b pidBackend) Name() string   { return "pid-stub" }
func (b pidBackend) Binary() string { return "sh" }
func (b pidBackend) Args(string, string, int, []string) []string {
	return []string{"-c", "echo $$ > " + b.pidFile + "; exec cat"}
}
func (pidBackend) Env() []string { return nil }
func (pidBackend) InitialInput(task, _ string) ([]byte, error) {
	return []byte(task + "\n"), nil
}
func (pidBackend) FollowUpInput(message, _ string, _ bool) ([]byte, error) {
	return []byte(message + "\n"), nil
}
func (pidBackend) PostStartCommands(string) [][]byte { return nil }
func (pidBackend) ParseEvent([]byte) ([]*engine.BackendEvent, error) {
	return []*engine.BackendEvent{}, nil
}

// OneShotCommand returns a command that succeeds with no output, so the
// smart-route classifier fails cleanly and falls back to backend defaults
// rather than reaching a real model.
func (pidBackend) OneShotCommand(string) (string, []string) { return "true", nil }
func (pidBackend) UnattendedSessionCommand(string) (string, []string, error) {
	return "true", nil, nil
}

// resultThenCatBackend is pidBackend plus one twist: before settling into
// `cat` (blocked on stdin, alive until killed), it emits a line ParseEvent
// turns into a BackendResult. That is the shape both real backends have —
// claude runs --print --input-format stream-json and pi runs --mode rpc, and
// agent.go's own comment says "the pi backend's session process stays
// resident past a BackendResult event" — and handleResult acts on it by
// setting AgentComplete immediately, before waitForExit ever gets a chance
// to confirm the process actually exited. So an agent reaches `complete`
// with its subprocess still alive on stdin, which is exactly the case
// reconcile's agentStateComplete branch has to terminate itself rather than
// infer the engine already did.
type resultThenCatBackend struct{ pidFile string }

func (b resultThenCatBackend) Name() string   { return "result-then-cat-stub" }
func (b resultThenCatBackend) Binary() string { return "sh" }
func (b resultThenCatBackend) Args(string, string, int, []string) []string {
	return []string{"-c", "echo $$ > " + b.pidFile + "; echo RESULT; exec cat"}
}
func (resultThenCatBackend) Env() []string { return nil }
func (resultThenCatBackend) InitialInput(task, _ string) ([]byte, error) {
	return []byte(task + "\n"), nil
}
func (resultThenCatBackend) FollowUpInput(message, _ string, _ bool) ([]byte, error) {
	return []byte(message + "\n"), nil
}
func (resultThenCatBackend) PostStartCommands(string) [][]byte { return nil }
func (resultThenCatBackend) ParseEvent(line []byte) ([]*engine.BackendEvent, error) {
	if strings.TrimSpace(string(line)) == "RESULT" {
		return []*engine.BackendEvent{{Kind: engine.BackendResult, Subtype: "success"}}, nil
	}
	return []*engine.BackendEvent{{Kind: engine.BackendIgnore}}, nil
}
func (resultThenCatBackend) OneShotCommand(string) (string, []string) { return "true", nil }
func (resultThenCatBackend) UnattendedSessionCommand(string) (string, []string, error) {
	return "true", nil, nil
}

// missingBinaryBackend names a binary that does not exist, so cmd.Start()
// fails inside Agent.start() — the shape review cycle 8 finding 1 needs:
// a spawn that fails synchronously, after the engine has already inserted
// the agent record, with no subprocess ever created.
type missingBinaryBackend struct{}

func (missingBinaryBackend) Name() string   { return "missing-binary-stub" }
func (missingBinaryBackend) Binary() string { return "/nonexistent/singularity-test-binary-xyz" }
func (missingBinaryBackend) Args(string, string, int, []string) []string {
	return nil
}
func (missingBinaryBackend) Env() []string { return nil }
func (missingBinaryBackend) InitialInput(task, _ string) ([]byte, error) {
	return []byte(task + "\n"), nil
}
func (missingBinaryBackend) FollowUpInput(message, _ string, _ bool) ([]byte, error) {
	return []byte(message + "\n"), nil
}
func (missingBinaryBackend) PostStartCommands(string) [][]byte { return nil }
func (missingBinaryBackend) ParseEvent([]byte) ([]*engine.BackendEvent, error) {
	return []*engine.BackendEvent{}, nil
}
func (missingBinaryBackend) OneShotCommand(string) (string, []string) { return "true", nil }
func (missingBinaryBackend) UnattendedSessionCommand(string) (string, []string, error) {
	return "true", nil, nil
}

func newMissingBinaryStubEngine() *engine.Engine {
	eng := engine.New(4)
	eng.SetDefaultBackend(missingBinaryBackend{})
	return eng
}

func newResultThenCatStubEngine(t *testing.T, pidFile string) *engine.Engine {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	eng := engine.New(4)
	eng.SetDefaultBackend(resultThenCatBackend{pidFile: pidFile})
	return eng
}

func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// startStubAgent dispatches t through the real EngineRunner and returns the
// agent ID and the pid of the subprocess behind it.
func startStubAgent(tb *testing.T, r *EngineRunner, task Task, pidFile string) (string, int) {
	tb.Helper()
	id, err := r.StartTask(context.Background(), task)
	if err != nil {
		tb.Fatalf("StartTask: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 0 {
				return id, pid
			}
		}
		if time.Now().After(deadline) {
			tb.Fatalf("stub agent never wrote its pid to %s", pidFile)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newStubEngine(t *testing.T, pidFile string) *engine.Engine {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	eng := engine.New(4)
	eng.SetDefaultBackend(pidBackend{pidFile: pidFile})
	return eng
}

// TestEngineRunnerTerminateEndsTheProcess is the end-to-end version of the
// invariant the queue exists to make structural. It fails against a
// TerminateAgent wired to engine.KillAgent: that soft-closes, so the agent
// stops counting as active — WorkDirBusy goes false — while its process
// carries on editing the directory the scheduler has just declared free.
func TestEngineRunnerTerminateEndsTheProcess(t *testing.T) {
	dir := t.TempDir()
	eng := newStubEngine(t, filepath.Join(dir, "pid"))
	r := NewEngineRunner(eng)

	task := Task{ID: "t1", Prompt: "p", WorkDir: dir}
	id, pid := startStubAgent(t, r, task, filepath.Join(dir, "pid"))

	if !r.WorkDirBusy(dir) {
		t.Fatal("WorkDirBusy = false while an agent is running in the directory")
	}
	if err := r.TerminateAgent(id); err != nil {
		t.Fatalf("TerminateAgent: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("pid %d is still alive after TerminateAgent: the directory is reported free while an agent is still editing it", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r.WorkDirBusy(dir) {
		t.Error("WorkDirBusy = true after the agent was terminated")
	}
	if active, _ := r.Capacity(); active != 0 {
		t.Errorf("active agents = %d after termination, want 0", active)
	}
	// The record — and with it the cancelled task's transcript — survives.
	if _, _, ok := r.AgentState(id); !ok {
		t.Error("AgentState reports the agent gone; TerminateAgent must keep the record")
	}
}

// TestEngineRunnerSoftCloseStillHoldsTheDirectory pins review cycle 7 finding
// 1's fix: WorkDirBusy now asks Engine.WorkDirOccupied, a question about the
// process, not the state label, so a soft-closed agent — including one the
// queue never started, such as a bare `agents spawn` an operator killed from
// the TUI — must still report its directory busy for as long as the process
// lives. ActiveAgents/Capacity are a different question (does this agent
// hold an engine slot?) and correctly keep excluding it immediately once its
// label goes terminal; TestEngineRunnerCapacityCountsRoutingAgents pins that
// side. Before the fix this test asserted the opposite — WorkDirBusy went
// false the moment KillAgent ran — which was the defect: a directory freed
// early while a foreign agent's process was still in it.
func TestEngineRunnerSoftCloseStillHoldsTheDirectory(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	eng := newStubEngine(t, pidFile)
	r := NewEngineRunner(eng)

	id, pid := startStubAgent(t, r, Task{ID: "t1", Prompt: "p", WorkDir: dir}, pidFile)
	t.Cleanup(func() { _ = r.TerminateAgent(id) })

	if err := eng.KillAgent(id); err != nil {
		t.Fatalf("KillAgent: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if !processAlive(pid) {
		t.Fatalf("pid %d died: engine.KillAgent is documented to soft-close, so the TUI can keep talking to the agent", pid)
	}
	if got := eng.GetAgent(id).Snapshot().State; got != engine.AgentKilled {
		t.Errorf("state after KillAgent = %s, want killed", got)
	}
	// The label goes terminal, but the process above is still editing dir:
	// the directory must stay busy.
	if !r.WorkDirBusy(dir) {
		t.Fatal("WorkDirBusy = false after KillAgent while the pid is still alive: a soft-closed agent's directory must stay occupied, whoever started it")
	}
	// Capacity, by contrast, is bookkeeping on the label, not the process: a
	// killed agent stops holding an engine slot immediately. That is correct
	// — the next StartAgent call may use the slot — it just must not be able
	// to use this directory until the process above is gone.
	if active, _ := r.Capacity(); active != 0 {
		t.Errorf("active = %d after KillAgent, want 0 — a soft-closed agent must free its slot even though its directory stays busy", active)
	}
}

// TestEngineRunnerCompleteStillHoldsTheDirectory is
// TestEngineRunnerSoftCloseStillHoldsTheDirectory's counterpart for
// `complete`: a state label with no guarantee the process behind it is gone.
// resultThenCatBackend's agent finishes its turn (handleResult sets
// AgentComplete) while its subprocess keeps running on stdin, which is
// exactly the shape a real backend has when it is left open for a follow-up.
func TestEngineRunnerCompleteStillHoldsTheDirectory(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	eng := newResultThenCatStubEngine(t, pidFile)
	r := NewEngineRunner(eng)

	id, pid := startStubAgent(t, r, Task{ID: "t1", Prompt: "p", WorkDir: dir}, pidFile)
	t.Cleanup(func() { _ = r.TerminateAgent(id) })

	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := eng.GetAgent(id).Snapshot().State; got == engine.AgentComplete {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent %s never reached complete", id)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !processAlive(pid) {
		t.Fatalf("pid %d died once the agent reported complete; sendInput's doc says a completed agent's process stays alive until explicitly removed", pid)
	}
	// Same fixed contract as soft-close: complete is a label, not a proof of
	// exit, so the directory must stay busy while the pid above is alive.
	if !r.WorkDirBusy(dir) {
		t.Fatal("WorkDirBusy = false while the agent is complete but the pid is still alive: a completed agent's directory must stay occupied")
	}
	if active, _ := r.Capacity(); active != 0 {
		t.Errorf("active = %d once complete, want 0 — a completed agent frees its slot even though its directory stays busy", active)
	}
}

// TestQueuedTaskIsSmartRoutedByDefault pins the default a queued task
// inherits: the same prompt handed to `agents spawn` is classified, and a
// task that says nothing about routing must be treated the same way.
// Routing is observable without a model call — the engine announces it on
// the agent's output stream before the classifier runs.
func TestQueuedTaskIsSmartRoutedByDefault(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	eng := newStubEngine(t, pidFile)
	r := NewEngineRunner(eng)

	routed, err := r.StartTask(context.Background(), Task{ID: "t1", Prompt: "p", WorkDir: dir})
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	t.Cleanup(func() { _ = r.TerminateAgent(routed) })
	if !waitForOutput(t, eng, routed, "Routing via Haiku") {
		t.Error("a task that did not mention routing was not smart-routed; queued work must be routed like spawned work")
	}

	off := false
	plain, err := r.StartTask(context.Background(), Task{
		ID: "t2", Prompt: "p", WorkDir: dir,
		Opts: TaskOptions{SmartRoute: &off},
	})
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	t.Cleanup(func() { _ = r.TerminateAgent(plain) })
	if waitForOutput(t, eng, plain, "Routing via Haiku") {
		t.Error("smart_route=false was ignored")
	}
}

// waitForOutput reports whether want appears in the agent's output stream
// within a short window.
func waitForOutput(t *testing.T, eng *engine.Engine, agentID, want string) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		entries, err := eng.GetOutputEntries(agentID, 0)
		if err != nil {
			t.Fatalf("GetOutputEntries: %v", err)
		}
		for _, e := range entries {
			if strings.Contains(e.Content, want) {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRouteEnabledDefaultsOn also covers review cycle 7 finding 4: the
// unset-SmartRoute default must mirror resolveSmartRoute's CLI rule (on
// unless --model or --effort was pinned), or a task submitted with only an
// effort via `queue add --file` routes when the same task submitted via
// `queue add --effort` would not — same declared intent, different model.
func TestRouteEnabledDefaultsOn(t *testing.T) {
	on, off := true, false
	cases := []struct {
		name string
		opts TaskOptions
		want bool
	}{
		{"unset routes", TaskOptions{}, true},
		{"explicit on", TaskOptions{SmartRoute: &on}, true},
		{"explicit off", TaskOptions{SmartRoute: &off}, false},
		// Unset SmartRoute but a pin present: must match `queue add --model`
		// / `--effort` (smartRouteFlags/resolveSmartRoute), which turns
		// routing off by default the moment either is given.
		{"effort pinned, no model, unset", TaskOptions{Effort: "medium"}, false},
		{"model pinned, no effort, unset", TaskOptions{Model: "sonnet"}, false},
		{"both pinned, unset", TaskOptions{Model: "sonnet", Effort: "medium"}, false},
		// An explicit SmartRoute always wins over a pin, same as
		// resolveSmartRoute's explicit --smart-route.
		{"effort pinned, explicit on", TaskOptions{Effort: "medium", SmartRoute: &on}, true},
	}
	for _, c := range cases {
		if got := c.opts.RouteEnabled(); got != c.want {
			t.Errorf("%s: RouteEnabled = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestEngineRunnerCapacityCountsRoutingAgents is review cycle 7 finding 3 as
// a test: nothing previously pinned EngineRunner.Capacity to ActiveCount
// rather than EngineStats.Active, so rewriting it to the latter (the exact
// regression review cycle 1 finding 1 was about) left the whole suite green.
// Parks two real agents in AgentRouting on a real engine — the queue-level
// analogue of engine.TestActiveCountIncludesRoutingAgents, which cannot
// inject state directly from outside the engine package — and asserts
// Capacity reports them active, i.e. that its active count is exactly the
// number StartAgent's own cap check gates on.
func TestEngineRunnerCapacityCountsRoutingAgents(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not available")
	}
	eng := engine.New(2)
	eng.SetDefaultBackend(slowRouteBackend{})
	r := NewEngineRunner(eng)
	dir := t.TempDir()

	id1, err := r.StartTask(context.Background(), Task{ID: "t1", Prompt: "p", WorkDir: dir})
	if err != nil {
		t.Fatalf("StartTask t1: %v", err)
	}
	t.Cleanup(func() { _ = r.TerminateAgent(id1) })
	id2, err := r.StartTask(context.Background(), Task{ID: "t2", Prompt: "p", WorkDir: dir})
	if err != nil {
		t.Fatalf("StartTask t2: %v", err)
	}
	t.Cleanup(func() { _ = r.TerminateAgent(id2) })

	for _, id := range []string{id1, id2} {
		deadline := time.Now().Add(2 * time.Second)
		for {
			state, _, ok := r.AgentState(id)
			if !ok {
				t.Fatalf("agent %s vanished", id)
			}
			if state == "routing" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("agent %s never reached routing (got %s)", id, state)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Both agents are confirmed routing, and the classifier stub sleeps for
	// a second before either resolves, so this read cannot race the
	// transition out of routing.
	active, max := r.Capacity()
	if active != 2 {
		t.Errorf("Capacity active = %d, want 2 — routing agents must count toward the cap Capacity reports", active)
	}
	if max != 2 {
		t.Errorf("Capacity max = %d, want 2", max)
	}

	// The cap check StartAgent itself performs must refuse a third spawn on
	// the same number: if Capacity ever disagreed with it (e.g. rewritten to
	// EngineStats.Active, which omits routing agents), the scheduler would
	// claim a slot the engine then refuses.
	if _, err := r.StartTask(context.Background(), Task{ID: "t3", Prompt: "p", WorkDir: dir}); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("StartTask t3 error = %v, want ErrNoCapacity", err)
	}
}

// slowRouteBackend is pidBackend plus a one-shot classifier command slow
// enough that two agents can be observed sitting in AgentRouting before it
// resolves. `sleep` is not a coding agent: no backend binary, no spend.
type slowRouteBackend struct{}

func (slowRouteBackend) Name() string                                { return "slow-route-stub" }
func (slowRouteBackend) Binary() string                              { return "sleep" }
func (slowRouteBackend) Args(string, string, int, []string) []string { return []string{"5"} }
func (slowRouteBackend) Env() []string                               { return nil }
func (slowRouteBackend) InitialInput(task, _ string) ([]byte, error) {
	return []byte(task + "\n"), nil
}
func (slowRouteBackend) FollowUpInput(message, _ string, _ bool) ([]byte, error) {
	return []byte(message + "\n"), nil
}
func (slowRouteBackend) PostStartCommands(string) [][]byte { return nil }
func (slowRouteBackend) ParseEvent([]byte) ([]*engine.BackendEvent, error) {
	return []*engine.BackendEvent{}, nil
}

// OneShotCommand is the classifier call StartAgent's routing goroutine
// blocks on; sleeping keeps both agents in AgentRouting long enough for the
// test to observe them there before either resolves.
func (slowRouteBackend) OneShotCommand(string) (string, []string) { return "sleep", []string{"1"} }
func (slowRouteBackend) UnattendedSessionCommand(string) (string, []string, error) {
	return "true", nil, nil
}
