package engine

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// herdrOneshotTimeoutMS bounds the single turn a `herdr-oneshot` invocation
// runs. Short on purpose: a one-shot call is a classifier prompt or a
// commit-message/MR-title draft, never a real coding turn, so it should fail
// fast rather than sit on herdrPromptTimeoutMS's 30-minute default.
const herdrOneshotTimeoutMS = 2 * 60 * 1000

// RunHerdrOneshot implements the `singularity herdr-oneshot <prompt>`
// subcommand: herdrBackend.OneShotCommand's answer to "how does a backend
// whose only launch path is an interactive herdr pane serve the
// prompt-in/text-out calls internal/oneshot needs" (commit messages, MR
// titles, the smart-router classifier).
//
// It is a thin wrapper around `singularity herdr-driver`, not a second
// implementation of herdr's workspace/pane/transcript handling: it spawns
// the driver exactly as herdrBackend.Args does, feeds it the one task line
// the driver's relay loop expects, and decodes its marker-protocol stdout
// with the same parseLine the interactive backend uses — so a one-shot call
// gets the driver's tested transcript-tailing and pane-fallback logic for
// free instead of a second, unverified way to pull text out of a pane.
// Reading it as a subprocess rather than calling the driver in-process keeps
// this on the same footing as every other OneShotCommand: one exec, whole
// stdout captured, no herdr state leaked into the daemon's own process if
// something in the driver ever panics or hangs.
//
// It is spawned by oneshot.Run through herdrBackend.OneShotCommand, not by
// users.
func RunHerdrOneshot(args []string) int {
	fs := flag.NewFlagSet("herdr-oneshot", flag.ContinueOnError)
	timeoutMS := fs.Int("timeout-ms", herdrOneshotTimeoutMS, "bound on the single turn this runs")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	prompt := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(prompt) == "" {
		fmt.Fprintln(os.Stderr, "herdr-oneshot: a prompt is required")
		return 2
	}

	answer, err := runHerdrOneshot(prompt, time.Duration(*timeoutMS)*time.Millisecond)
	if err != nil {
		fmt.Fprintf(os.Stderr, "herdr-oneshot: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stdout, answer)
	return 0
}

func runHerdrOneshot(prompt string, timeout time.Duration) (string, error) {
	name := "singl-1s-" + randomHex(6)
	sessionID := newSessionUUID()
	model := Models().ClassifierModel("claude")

	driverArgs := []string{
		"herdr-driver",
		"--name", name,
		"--kind", "claude",
		"--prompt-timeout-ms", strconv.Itoa(int(timeout.Milliseconds())),
		"--poll-ms", strconv.Itoa(herdrDefaultPollMS),
		"--session-id", sessionID,
		"--",
		"--permission-mode", "bypassPermissions",
		"--session-id", sessionID,
	}
	if model != "" {
		driverArgs = append(driverArgs, "--model", model)
	}

	binary := (&herdrBackend{}).Binary()
	cmd := exec.Command(binary, driverArgs...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("driver stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("driver stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start driver: %w", err)
	}

	// One task, then EOF: relay's queue runs it and returns once stdin
	// closes and the turn is done (see herdr_driver.go's relay), which is
	// exactly "one prompt in, one answer out, then exit".
	if _, err := stdin.Write(herdrTaskLine(prompt)); err != nil {
		_ = cmd.Process.Kill()
		return "", fmt.Errorf("write prompt: %w", err)
	}
	if err := stdin.Close(); err != nil {
		_ = cmd.Process.Kill()
		return "", fmt.Errorf("close driver stdin: %w", err)
	}

	answer, resultErr := collectHerdrOneshotAnswer(stdout)

	waitErr := cmd.Wait()
	if resultErr != nil {
		return "", resultErr
	}
	if waitErr != nil {
		return "", fmt.Errorf("driver exited: %w", waitErr)
	}
	if strings.TrimSpace(answer) == "" {
		return "", fmt.Errorf("driver produced no text for this turn")
	}
	return answer, nil
}

// collectHerdrOneshotAnswer decodes the driver's marker-protocol stdout with
// the same parseLine herdrBackend.ParseEvent uses for real sessions,
// collecting BackendText content until the turn's BackendResult settles it.
// A zero-value herdrBackend is enough: parseLine's only receiver state is
// initOnce/sessionID/launchModel, all irrelevant to a one-shot caller that
// throws the session-init event away.
func collectHerdrOneshotAnswer(r io.Reader) (string, error) {
	b := &herdrBackend{}
	var lines []string
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		for _, ev := range b.parseLine(scanner.Text()) {
			switch ev.Kind {
			case BackendText:
				lines = append(lines, ev.Content)
			case BackendResult:
				if ev.IsResultError {
					return "", fmt.Errorf("%s", ev.Content)
				}
				// Success: keep draining stdout (the driver still has to
				// exit) but the answer is already complete.
			case BackendError:
				// Driver-level warnings are not fatal on their own — the
				// transcript/pane text collected so far still stands.
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read driver output: %w", err)
	}
	return strings.Join(lines, "\n"), nil
}
