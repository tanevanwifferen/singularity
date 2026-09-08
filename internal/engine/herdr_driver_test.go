package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/config"
)

// fakeHerdr puts a stub `herdr` on PATH for the duration of the test and
// returns the directory holding its log. The driver shells out to herdr for
// everything, so this is what makes its whole loop testable without a herdr
// server: the stub records every invocation and answers with the JSON shapes
// herdr documents.
func fakeHerdr(t *testing.T, paneText string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the herdr stub is a shell script")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	panePath := filepath.Join(dir, "pane.txt")
	if err := os.WriteFile(panePath, []byte(paneText), 0o600); err != nil {
		t.Fatalf("write pane fixture: %v", err)
	}
	script := `#!/bin/sh
echo "$*" >> ` + logPath + `
case "$1 $2" in
  "workspace create") echo '{"result":{"root_pane":{"pane_id":"w1:p1"},"tab":{"tab_id":"w1:t1"},"workspace":{"workspace_id":"w1"}}}' ;;
  "agent start")      echo '{"result":{"type":"ok"}}' ;;
  "agent prompt")     echo '{"result":{"agent":{"agent_status":"done"}}}' ;;
  "agent read")       cat ` + panePath + ` ;;
  "workspace close")  echo '{"result":{"type":"ok"}}' ;;
  *) echo "{\"error\":{\"code\":\"unexpected\",\"message\":\"$*\"}}" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatalf("write herdr stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func newTestDriver(t *testing.T, out *bytes.Buffer) *herdrDriver {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &herdrDriver{
		name:          "singltest",
		kind:          "claude",
		promptTimeout: 1000,
		// Long enough that the poll loop never fires: these tests are about
		// the turn's own emissions, in order.
		pollInterval: time.Hour,
		// Same for watchAgent's liveness check, unless a test overrides it.
		watchInterval: time.Hour,
		ctx:           ctx,
		cancel:        cancel,
		out:           bufio.NewWriter(out),
	}
}

// markerOrder returns the markers emitted, in order, with their payloads.
func markerOrder(t *testing.T, out string) ([]string, []string) {
	t.Helper()
	var markers, payloads []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		for _, m := range []string{herdrMarkerInit, herdrMarkerOut, herdrMarkerState, herdrMarkerErr} {
			if strings.HasPrefix(line, m) {
				decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, m))
				if err != nil {
					t.Fatalf("payload of %s is not base64: %v", m, err)
				}
				markers = append(markers, m)
				payloads = append(payloads, string(decoded))
				break
			}
		}
	}
	return markers, payloads
}

func TestHerdrDriverEmitsOutputBeforeState(t *testing.T) {
	dir := fakeHerdr(t, paneSnapshot(t, 1))
	var out bytes.Buffer
	d := newTestDriver(t, &out)

	if code := d.run(strings.NewReader("T:" + base64.StdEncoding.EncodeToString([]byte("hello")) + "\n")); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}

	markers, payloads := markerOrder(t, out.String())
	want := []string{herdrMarkerInit, herdrMarkerOut, herdrMarkerState}
	if strings.Join(markers, ",") != strings.Join(want, ",") {
		t.Fatalf("markers = %v, want %v (the turn's text must precede the state line, which is what\n"+
			"ParseEvent turns into the BackendResult that marks the turn complete)", markers, want)
	}
	if !strings.Contains(payloads[1], "  120") {
		t.Errorf("output payload = %q, want the pane snapshot", payloads[1])
	}
	if !strings.Contains(payloads[2], `"agent_status":"done"`) {
		t.Errorf("state payload = %q, want herdr's prompt response", payloads[2])
	}

	calls, err := os.ReadFile(filepath.Join(dir, "calls.log"))
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	for _, want := range []string{
		// The pane ID must come from .result.root_pane.pane_id, not from
		// whichever pane_id a grep happens to hit first.
		"agent start singltest --kind claude --pane w1:p1",
		"agent prompt singltest hello --wait",
		"agent read singltest --source visible",
		// Closing the workspace on stdin EOF is what stops the claude
		// session inside the pane.
		"workspace close w1",
	} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("herdr was never called with %q\ncalls:\n%s", want, calls)
		}
	}
}

func TestHerdrDriverSurvivesFailedRead(t *testing.T) {
	dir := fakeHerdr(t, "")
	// Make only `agent read` fail, the way a closed pane or a reloading
	// server does: exit 1 with JSON on stderr. Under the `set -e` shell
	// script this replaces, that killed the driver mid-loop.
	script := `#!/bin/sh
echo "$*" >> ` + filepath.Join(dir, "calls.log") + `
case "$1 $2" in
  "workspace create") echo '{"result":{"root_pane":{"pane_id":"w1:p1"},"workspace":{"workspace_id":"w1"}}}' ;;
  "agent start")      echo '{"result":{"type":"ok"}}' ;;
  "agent prompt")     echo '{"result":{"agent":{"agent_status":"done"}}}' ;;
  "agent read")       echo '{"error":{"code":"pane_not_found","message":"pane is gone"}}' >&2; exit 1 ;;
  "workspace close")  echo '{"result":{"type":"ok"}}' ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatalf("rewrite herdr stub: %v", err)
	}

	var out bytes.Buffer
	d := newTestDriver(t, &out)
	stdin := "T:" + base64.StdEncoding.EncodeToString([]byte("first")) + "\n" +
		"T:" + base64.StdEncoding.EncodeToString([]byte("second")) + "\n"
	if code := d.run(strings.NewReader(stdin)); code != 0 {
		t.Fatalf("run() = %d, want 0 — a failed read must not end the driver", code)
	}

	markers, payloads := markerOrder(t, out.String())
	// Each turn reads the pane twice once it settles — the viewport
	// (emitLiveSnapshot) and the scrollback-inclusive transcript
	// (emitFullSnapshot) — and the stub fails both, so a turn reports two
	// read errors and then, regardless, its state.
	want := []string{
		herdrMarkerInit,
		herdrMarkerErr, herdrMarkerErr, herdrMarkerState,
		herdrMarkerErr, herdrMarkerErr, herdrMarkerState,
	}
	if strings.Join(markers, ",") != strings.Join(want, ",") {
		t.Fatalf("markers = %v, want %v (both turns still report a result)", markers, want)
	}
	for _, i := range []int{1, 2} {
		if !strings.Contains(payloads[i], "pane_not_found") {
			t.Errorf("error payload %d = %q, want the herdr error reported as an event", i, payloads[i])
		}
	}
	calls, _ := os.ReadFile(filepath.Join(dir, "calls.log"))
	if !strings.Contains(string(calls), "agent prompt singltest second") {
		t.Errorf("the second prompt was never sent\ncalls:\n%s", calls)
	}
}

func TestHerdrDriverRechecksStalledPrompt(t *testing.T) {
	dir := fakeHerdr(t, "pane text\n")
	// herdr returns agent_prompt_stalled when it sees no lifecycle change
	// within five seconds of a prompt. The prompt did go in, so the driver
	// must re-check the settled state instead of failing the turn.
	script := `#!/bin/sh
echo "$*" >> ` + filepath.Join(dir, "calls.log") + `
case "$1 $2" in
  "workspace create") echo '{"result":{"root_pane":{"pane_id":"w1:p1"},"workspace":{"workspace_id":"w1"}}}' ;;
  "agent start")      echo '{"result":{"type":"ok"}}' ;;
  "agent prompt")     echo '{"error":{"code":"agent_prompt_stalled","message":"no lifecycle change"}}' >&2; exit 1 ;;
  "agent wait")       echo '{"result":{"agent":{"agent_status":"done"}}}' ;;
  "agent read")       echo "pane text" ;;
  "workspace close")  echo '{"result":{"type":"ok"}}' ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatalf("rewrite herdr stub: %v", err)
	}

	var out bytes.Buffer
	d := newTestDriver(t, &out)
	d.run(strings.NewReader("T:" + base64.StdEncoding.EncodeToString([]byte("go")) + "\n"))

	markers, payloads := markerOrder(t, out.String())
	last := len(markers) - 1
	if markers[last] != herdrMarkerState {
		t.Fatalf("markers = %v, want a state line last", markers)
	}
	b := newHerdrTestBackend(t)
	if ev := b.resultEventFromState(payloads[last]); ev.IsResultError {
		t.Errorf("result = %+v, want a successful turn: a stalled *observation* is not a failed turn", ev)
	}
	calls, _ := os.ReadFile(filepath.Join(dir, "calls.log"))
	if !strings.Contains(string(calls), "agent wait singltest") {
		t.Errorf("the stalled prompt was never re-checked\ncalls:\n%s", calls)
	}
}

func TestHerdrDriverPollsWhileTurnRuns(t *testing.T) {
	dir := fakeHerdr(t, "progress\n")
	// A prompt that takes a while, so the poll loop has to be the thing that
	// produces output before the turn ends.
	script := `#!/bin/sh
echo "$*" >> ` + filepath.Join(dir, "calls.log") + `
case "$1 $2" in
  "workspace create") echo '{"result":{"root_pane":{"pane_id":"w1:p1"},"workspace":{"workspace_id":"w1"}}}' ;;
  "agent start")      echo '{"result":{"type":"ok"}}' ;;
  "agent prompt")     sleep 1; echo '{"result":{"agent":{"agent_status":"done"}}}' ;;
  "agent read")       date +%s%N ;;
  "workspace close")  echo '{"result":{"type":"ok"}}' ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatalf("rewrite herdr stub: %v", err)
	}

	var out bytes.Buffer
	d := newTestDriver(t, &out)
	d.pollInterval = 100 * time.Millisecond
	d.run(strings.NewReader("T:" + base64.StdEncoding.EncodeToString([]byte("go")) + "\n"))

	markers, _ := markerOrder(t, out.String())
	outCount := 0
	for _, m := range markers {
		if m == herdrMarkerOut {
			outCount++
		}
	}
	if outCount < 2 {
		t.Errorf("got %d output markers (%v), want several: the pane must be read while the turn "+
			"runs, not only once it has finished", outCount, markers)
	}
}

func TestHerdrDriverStartFailsHardOnUnrecoverableError(t *testing.T) {
	dir := fakeHerdr(t, "")
	// agent_not_ready is the one documented recoverable start failure; any
	// other code (here standing in for an unsupported --kind or a pane not
	// at a shell prompt) must fail immediately instead of waiting for an
	// agent that will never come up.
	script := `#!/bin/sh
echo "$*" >> ` + filepath.Join(dir, "calls.log") + `
case "$1 $2" in
  "workspace create") echo '{"result":{"root_pane":{"pane_id":"w1:p1"},"workspace":{"workspace_id":"w1"}}}' ;;
  "agent start")      echo '{"error":{"code":"kind_unsupported","message":"no such agent kind"}}' >&2; exit 1 ;;
  "workspace close")  echo '{"result":{"type":"ok"}}' ;;
  *) echo "{\"error\":{\"code\":\"unexpected\",\"message\":\"$*\"}}" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatalf("rewrite herdr stub: %v", err)
	}

	var out bytes.Buffer
	d := newTestDriver(t, &out)
	if code := d.run(strings.NewReader("")); code != 1 {
		t.Fatalf("run() = %d, want 1: a hard agent-start failure must not be swallowed", code)
	}
	calls, _ := os.ReadFile(filepath.Join(dir, "calls.log"))
	if strings.Contains(string(calls), "agent wait") {
		t.Errorf("driver fell back to `agent wait` for a non-agent_not_ready failure\ncalls:\n%s", calls)
	}
}

func TestHerdrDriverStartRecoversFromAgentNotReady(t *testing.T) {
	dir := fakeHerdr(t, "")
	script := `#!/bin/sh
echo "$*" >> ` + filepath.Join(dir, "calls.log") + `
case "$1 $2" in
  "workspace create") echo '{"result":{"root_pane":{"pane_id":"w1:p1"},"workspace":{"workspace_id":"w1"}}}' ;;
  "agent start")      echo '{"error":{"code":"agent_not_ready","message":"still booting"}}' >&2; exit 1 ;;
  "agent wait")       echo '{"result":{"agent":{"agent_status":"idle"}}}' ;;
  "workspace close")  echo '{"result":{"type":"ok"}}' ;;
  *) echo "{\"error\":{\"code\":\"unexpected\",\"message\":\"$*\"}}" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatalf("rewrite herdr stub: %v", err)
	}

	var out bytes.Buffer
	d := newTestDriver(t, &out)
	if code := d.run(strings.NewReader("")); code != 0 {
		t.Fatalf("run() = %d, want 0: agent_not_ready is the documented recoverable case", code)
	}
	calls, _ := os.ReadFile(filepath.Join(dir, "calls.log"))
	// Not agent wait's plain default, which also accepts "blocked" as
	// settled — a blocked agent is not actually ready for the first prompt.
	if !strings.Contains(string(calls), "agent wait singltest --until idle --until done") {
		t.Errorf("recovery wait did not restrict itself to idle/done\ncalls:\n%s", calls)
	}
}

func TestHerdrDriverPromptChunksOversizedText(t *testing.T) {
	dir := fakeHerdr(t, "done\n")
	script := `#!/bin/sh
echo "$*" >> ` + filepath.Join(dir, "calls.log") + `
case "$1 $2" in
  "workspace create") echo '{"result":{"root_pane":{"pane_id":"w1:p1"},"workspace":{"workspace_id":"w1"}}}' ;;
  "agent start")      echo '{"result":{"type":"ok"}}' ;;
  "agent get")        echo '{"result":{"agent":{"agent_status":"idle"}}}' ;;
  "pane send-text")   echo '{"result":{"type":"ok"}}' ;;
  "agent send-keys")  echo '{"result":{"type":"ok"}}' ;;
  "agent wait")       echo '{"result":{"agent":{"agent_status":"done"}}}' ;;
  "agent read")       echo "done" ;;
  "workspace close")  echo '{"result":{"type":"ok"}}' ;;
  *) echo "{\"error\":{\"code\":\"unexpected\",\"message\":\"$*\"}}" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatalf("rewrite herdr stub: %v", err)
	}

	big := strings.Repeat("x", herdrMaxPromptArgBytes+1)
	var out bytes.Buffer
	d := newTestDriver(t, &out)
	if code := d.run(strings.NewReader("T:" + base64.StdEncoding.EncodeToString([]byte(big)) + "\n")); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}

	markers, payloads := markerOrder(t, out.String())
	last := len(markers) - 1
	if markers[last] != herdrMarkerState || !strings.Contains(payloads[last], `"agent_status":"done"`) {
		t.Fatalf("markers = %v, want a successful state line last", markers)
	}
	calls, _ := os.ReadFile(filepath.Join(dir, "calls.log"))
	if strings.Contains(string(calls), "agent prompt") {
		t.Errorf("oversized prompt went through `agent prompt` (one argv entry) instead of being chunked\ncalls:\n%s", calls)
	}
	if !strings.Contains(string(calls), "pane send-text") {
		t.Errorf("oversized prompt was never typed into the pane\ncalls:\n%s", calls)
	}
	if !strings.Contains(string(calls), "agent send-keys singltest enter") {
		t.Errorf("oversized prompt was never submitted with Enter\ncalls:\n%s", calls)
	}
}

func TestHerdrDriverWatchAgentExitsWhenSessionIsGone(t *testing.T) {
	dir := fakeHerdr(t, "")
	script := `#!/bin/sh
echo "$*" >> ` + filepath.Join(dir, "calls.log") + `
case "$1 $2" in
  "workspace create") echo '{"result":{"root_pane":{"pane_id":"w1:p1"},"workspace":{"workspace_id":"w1"}}}' ;;
  "agent start")      echo '{"result":{"type":"ok"}}' ;;
  "agent get")        echo '{"error":{"code":"agent_not_found","message":"agent target singltest not found"}}' >&2; exit 1 ;;
  "workspace close")  echo '{"result":{"type":"ok"}}' ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatalf("rewrite herdr stub: %v", err)
	}

	var out bytes.Buffer
	d := newTestDriver(t, &out)
	d.watchInterval = 20 * time.Millisecond

	// relay would otherwise block forever on this pipe's read end: the point
	// is that watchAgent, not stdin EOF, is what ends the driver.
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })

	done := make(chan int, 1)
	go func() { done <- d.run(pr) }()

	select {
	case code := <-done:
		if code == 0 {
			t.Error("run() = 0, want a non-zero exit: the driver noticed its agent is gone")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("driver did not exit after its agent became agent_not_found")
	}

	markers, payloads := markerOrder(t, out.String())
	found := false
	for i, m := range markers {
		if m == herdrMarkerErr && strings.Contains(payloads[i], "agent_not_found") {
			found = true
		}
	}
	if !found {
		t.Errorf("markers = %v, want an error event reporting agent_not_found", markers)
	}
}

func TestTerminateProcessSignalsBeforeKilling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM handling is not portable to windows")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "cleaned")
	ready := filepath.Join(dir, "ready")
	// Stands in for the driver: it has work to finish on the way out (there,
	// closing the herdr workspace) and only gets to do it if it is signalled
	// rather than SIGKILLed.
	cmd := exec.Command("sh", "-c", `trap 'printf done > `+marker+`; exit 0' TERM
printf ready > `+ready+`
while :; do sleep 0.05; done`)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Signalling before the trap is installed would just terminate it, which
	// would prove nothing.
	for i := 0; ; i++ {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if i > 200 {
			t.Fatal("helper process never installed its signal handler")
		}
		time.Sleep(10 * time.Millisecond)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	start := time.Now()
	if err := terminateProcess(cmd.Process, done, 5*time.Second); err != nil {
		t.Fatalf("terminateProcess: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Errorf("took %s — it waited out the grace period instead of noticing the clean exit", elapsed)
	}
	if _, err := os.ReadFile(marker); err != nil {
		t.Errorf("the process never ran its cleanup handler: %v", err)
	}
}

func TestTerminateProcessKillsWithoutGrace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal handling is not portable to windows")
	}
	// Zero grace is what every backend that does not implement
	// GracefulBackend gets: straight to SIGKILL, unchanged behaviour.
	cmd := exec.Command("sh", "-c", `trap '' TERM; while :; do sleep 0.05; done`)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	if err := terminateProcess(cmd.Process, done, 0); err != nil {
		t.Fatalf("terminateProcess: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("process ignoring SIGTERM was not killed")
	}
}

// TestHerdrDriverLiveIntegration exercises the real driver against a real
// herdr server and a real interactive claude session. The stub-based tests
// above prove the protocol and the ordering; only a live run proves the
// process actually stays attached, that herdr accepts the commands as
// written, and that a real completion signal comes back. Skipped wherever
// herdr, claude or a running herdr server are absent (CI, most dev machines).
func TestHerdrDriverLiveIntegration(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not installed")
	}
	if err := herdrPreflight(); err != nil {
		t.Skipf("herdr not usable: %v", err)
	}

	// The driver is a subcommand of the singularity binary, so the test has
	// to build it (see herdrBackend.Binary).
	bin := filepath.Join(t.TempDir(), "singularity")
	build := exec.Command("go", "build", "-o", bin, "gitlab.com/tanevanwifferen1/singularity/cmd/singularity")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build singularity: %v: %s", err, out)
	}

	SetModels(config.DefaultModelsConfig())
	t.Cleanup(func() { SetModels(nil) })

	b := &herdrBackend{name: "singlit-" + randomHex(4)}
	cmd := exec.Command(bin, b.Args("", "", 0, nil)...)
	cmd.Dir = t.TempDir()
	cmd.Env = b.Env()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	lines := make(chan string, 256)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()

	// events collects what the backend makes of the driver's output, which
	// is the thing the engine actually consumes.
	var mu sync.Mutex
	var text []string
	var results []*BackendEvent
	go func() {
		for line := range lines {
			evs, _ := b.ParseEvent([]byte(line))
			mu.Lock()
			for _, ev := range evs {
				switch ev.Kind {
				case BackendText:
					text = append(text, ev.Content)
				case BackendResult:
					results = append(results, ev)
				}
			}
			mu.Unlock()
		}
	}()

	waitFor := func(what string, timeout time.Duration, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			mu.Lock()
			ok := cond()
			mu.Unlock()
			if ok {
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", what)
	}

	task, err := b.InitialInput("Reply with exactly the single word OK and nothing else.", "")
	if err != nil {
		t.Fatalf("InitialInput: %v", err)
	}
	// The driver only reads stdin once the agent is up, so writing straight
	// away is fine — the pipe buffers it.
	if _, err := stdin.Write(task); err != nil {
		t.Fatalf("write task: %v", err)
	}

	waitFor("the turn to settle", 4*time.Minute, func() bool { return len(results) > 0 })
	mu.Lock()
	result := results[0]
	transcript := strings.Join(text, "\n")
	mu.Unlock()

	if result.IsResultError {
		t.Fatalf("turn result = %+v, want a successful BackendResult", result)
	}
	if !strings.Contains(transcript, "OK") {
		t.Errorf("transcript = %q, want the agent's reply (OK) in the text events", transcript)
	}
	// The text must arrive before the result: handleResult treats the result
	// as "turn complete" and consumers read the transcript on that signal.
	if len(text) == 0 {
		t.Error("no BackendText was emitted before the turn's result")
	}

	if err := stdin.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("driver exit: %v, want a clean exit after stdin EOF", err)
		}
	case <-time.After(30 * time.Second):
		t.Error("driver did not exit within 30s of stdin EOF")
	}

	// The workspace must be gone: leaving it open leaves an unsupervised
	// claude running in it.
	if out, err := exec.Command("herdr", "agent", "list").Output(); err == nil {
		if strings.Contains(string(out), b.name) {
			t.Errorf("herdr agent %q is still live after the driver exited: %s", b.name, out)
		}
	}
}
