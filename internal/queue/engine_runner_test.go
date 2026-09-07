package queue

import (
	"context"
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

// TestEngineRunnerSoftCloseIsNotEnough pins the trap the runner has to
// avoid, so a future change back to engine.KillAgent is caught here rather
// than in production: a soft-closed agent is invisible to WorkDirBusy while
// its process is still alive.
func TestEngineRunnerSoftCloseIsNotEnough(t *testing.T) {
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
	// The divergence itself, pinned: the agent record now says killed, so
	// ActiveAgents drops it and WorkDirBusy reports the directory free,
	// while the pid above is demonstrably still in it editing files.
	// Nothing in the scheduler can tell the difference from WorkDirBusy
	// alone, which is why the queue has to terminate a killed agent's
	// process itself rather than infer the engine already ended it
	// (reconcile, scheduler.go). A previous cycle left this unasserted on
	// the grounds that it "follows from the engine's active-agent
	// definition" — true, but that is precisely the fact review cycle 5
	// found nothing was pinning, so a future change could silently make
	// WorkDirBusy honest (or dishonest in a new way) with nothing to fail.
	if r.WorkDirBusy(dir) {
		t.Fatal("WorkDirBusy = true after KillAgent while the pid is still alive: the directory-freed-early divergence this test exists to pin has disappeared or been masked")
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
	}
	for _, c := range cases {
		if got := c.opts.RouteEnabled(); got != c.want {
			t.Errorf("%s: RouteEnabled = %v, want %v", c.name, got, c.want)
		}
	}
}
