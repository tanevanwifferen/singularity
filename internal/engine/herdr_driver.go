package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// The driver is the subprocess herdrBackend spawns for one agent: the
// singularity binary re-invoked as `singularity herdr-driver …` (see
// cmd/singularity/main.go and herdrBackend.Binary). It owns one herdr
// workspace and one interactive `claude` inside it, and speaks the marker
// protocol on stdout that herdrBackend.ParseEvent reads.
//
// Why a Go subprocess rather than an `sh -c` script driving the same herdr
// commands: every job in here is one a shell does badly.
//   - herdr returns JSON, and the IDs the driver needs are nested
//     (.result.root_pane.pane_id). encoding/json reads them; a grep for
//     `"pane_id":"…"` only worked because herdr happens to serialise keys
//     alphabetically.
//   - `herdr agent read` can fail transiently (pane closed, agent name
//     released when claude exits, server reload; herdr reports server errors
//     as JSON on stderr with exit status 1). Here that is one error event on
//     the stream; under `set -e` it killed the driver mid-loop.
//   - the workspace must be closed when the agent is killed, not only on a
//     clean stdin EOF. A signal handler does that; a shell script cannot
//     catch the SIGKILL Agent.kill used to send (see the SIGTERM grace
//     period in agent_lifecycle.go, and herdrBackend.TerminationGrace).
//   - claude's session transcript has to be tailed *while* the turn runs for
//     the output stream to show live progress (see herdr_transcript.go),
//     which means writing to stdout from two places at once — safe behind a
//     mutex, a race in shell.
const (
	// herdrMarkerInit is printed once, after the workspace exists and herdr
	// has confirmed claude is ready for input. The payload is the pane ID.
	herdrMarkerInit = "__SINGL_INIT__"
	// herdrMarkerOut carries a base64 chunk of pane text — only the
	// end-of-turn fallback read, when the transcript produced nothing for
	// the turn (see runTurn). The transcript itself travels on
	// herdrMarkerJSONL (herdr_transcript.go).
	herdrMarkerOut = "__SINGL_OUT__"
	// herdrMarkerState carries the base64 JSON response of the
	// `herdr agent prompt --wait` (or follow-up `herdr agent wait`) that
	// settled the turn.
	herdrMarkerState = "__SINGL_STATE__"
	// herdrMarkerErr carries a base64 driver-level error message: something
	// went wrong for this turn, but the driver is still alive and the next
	// follow-up can still be sent.
	herdrMarkerErr = "__SINGL_ERR__"
)

// herdrTaskPrefix marks a stdin line carrying base64 prompt text (see
// herdrTaskLine). Base64 keeps multi-line task text on one line.
const herdrTaskPrefix = "T:"

// herdrOutChunkBytes bounds the raw text carried by a single __SINGL_OUT__
// line. streamOutput's scanner rejects tokens over 1MiB (agent_output.go) and
// base64 adds a third, so a snapshot larger than this is split across several
// marker lines instead of being emitted as one oversized line the reader
// would choke on. Splitting happens on line boundaries.
const herdrOutChunkBytes = 64 * 1024

// herdrFullReadSource and herdrFullReadLines are the pane read the driver
// falls back to once a turn has settled and claude's transcript produced no
// record for it (transcript saving off in the pane's claude, or the file
// never found). It is the only pane read the driver makes.
//
// It is made only after the turn settles because the larger sources are
// refused while a turn is in flight: verified against the installed herdr
// (0.8.2) that `agent read <name> --source recent --lines 600` fails with
// agent_not_idle for as long as the agent reports "working", and answers
// normally the moment it settles. Measured then in a daemon-created pane:
// after a turn printing 150 numbered lines, `--source recent-unwrapped
// --lines 400` returned the whole turn from claude's banner onward, where
// the 39-row viewport held only the tail. recent-unwrapped because herdr
// recommends it for transcripts (it joins soft-wrapped rows back into one
// line), and a line count far above any plausible single turn because the
// window slides: herdrDiffSnapshot's overlapAt anchors a later window
// against an earlier one, so asking for more rows than the turn produced
// costs a slightly larger diff and nothing else.
const (
	herdrFullReadSource = "recent-unwrapped"
	herdrFullReadLines  = 5000
)

// herdrStartTimeoutMS bounds `herdr agent start`, which returns only once
// herdr has confirmed claude is up and ready for input. claude's first boot
// (config load, MCP servers) is slower than herdr's own 30s default.
const herdrStartTimeoutMS = 120 * 1000

// herdrDefaultPollMS is how often claude's transcript is re-read while a
// turn is in flight. A turn can run for many minutes; without this nothing
// at all reaches `singl agents output` or the TUI until it ends.
const herdrDefaultPollMS = 500

// herdrPromptTimeoutMS bounds a single `herdr agent prompt --wait` —
// generous because it spans a whole claude turn, tool calls included. It is
// only the default: herdrBackend.SetTurnTimeout overrides it with the
// agent's own --timeout when one is configured, so a turn legitimately
// longer than this does not come back as herdr's own `timeout` error while
// claude keeps working unsupervised in the pane.
const herdrPromptTimeoutMS = 30 * 60 * 1000

// herdrMaxPromptArgBytes bounds the prompt text the driver will pass whole
// as a single `herdr agent prompt <name> <TEXT>` argument. Linux caps one
// argv entry at MAX_ARG_STRLEN (32 pages = 131072 bytes on typical kernels);
// verified against the installed herdr that 131000 bytes succeeds and
// 131100 fails execve with E2BIG. This is a per-string limit, not an
// overall argv budget, so splitting one big value across more arguments
// does not help — a prompt over this threshold is instead typed into the
// pane in pieces (see promptChunked). buildTask (agent_lifecycle.go) can
// inline whole --context-files, and Engine.ResumeWithHistory seeds an
// unbounded conversation history, so ordinary use reaches this size.
const herdrMaxPromptArgBytes = 100 * 1024

// herdrPromptChunkBytes bounds each `herdr pane send-text` call promptChunked
// makes, comfortably under herdrMaxPromptArgBytes.
const herdrPromptChunkBytes = 64 * 1024

// herdrWatchIntervalMS is how often the driver checks, independent of
// whether a turn is in flight, that the claude session it started is still
// there. Turns can be many minutes apart (or never happen again, if the
// operator walks away), so this cannot piggy-back on the per-turn poll
// loop, which only runs while runTurn is active.
const herdrWatchIntervalMS = 5 * 1000

// herdrDriver holds one agent's driver state.
type herdrDriver struct {
	name string
	kind string
	// agentArgs are passed to claude itself, after herdr's `--`.
	agentArgs     []string
	promptTimeout int
	pollInterval  time.Duration
	// watchInterval is how often watchAgent checks the claude session is
	// still alive; defaults to herdrWatchIntervalMS, overridable by tests.
	watchInterval time.Duration

	// ctx is cancelled by cleanup so an in-flight `herdr agent prompt
	// --wait` (which can be blocked for half an hour) dies with the driver
	// instead of outliving it.
	ctx    context.Context
	cancel context.CancelFunc

	// outMu serialises stdout writes: the poll loop and the turn loop both
	// emit marker lines.
	outMu sync.Mutex
	out   *bufio.Writer

	// paneID is the pane claude runs in, reported once on the init marker so
	// the agent record can point a human at the right pane.
	paneID string

	// workspaceID is the workspace to close on the way out. Empty until
	// creation succeeds, which is also what makes cleanup safe to call from
	// any failure path.
	workspaceID string

	// transcript tails the claude session's own transcript file, the
	// source of every text and tool event this driver reports.
	transcript *herdrTranscriptTailer

	// trustFile is claude's top-level config file, where start marks the
	// work dir trusted before launching (see herdr_trust.go). Empty
	// disables the step.
	trustFile string

	// snapMu guards fullSnapshot.
	snapMu sync.Mutex
	// fullSnapshot is the previous scrollback-inclusive pane read (see
	// herdrFullReadSource), the baseline the fallback read is diffed
	// against so it yields one turn's output and not the whole session's.
	fullSnapshot string

	cleanupOnce sync.Once
	// shuttingDown stops emit from reporting anything after cleanup has
	// started: cancelling the in-flight prompt makes it fail, and a failed
	// turn is not news when the agent is being killed.
	shuttingDown atomic.Bool
}

// RunHerdrDriver implements the `singularity herdr-driver` subcommand: it
// creates a herdr workspace, starts an interactive claude in it, and then
// relays prompts from stdin to that agent, reporting settled state and pane
// text back on stdout. It is spawned by herdrBackend, not by users.
func RunHerdrDriver(args []string) int {
	fs := flag.NewFlagSet("herdr-driver", flag.ContinueOnError)
	name := fs.String("name", "", "herdr agent name (also the workspace label)")
	kind := fs.String("kind", "claude", "herdr agent kind")
	promptTimeout := fs.Int("prompt-timeout-ms", herdrPromptTimeoutMS, "per-turn timeout for `herdr agent prompt --wait`")
	pollMS := fs.Int("poll-ms", herdrDefaultPollMS, "how often to re-read claude's transcript while a turn is running")
	sessionID := fs.String("session-id", "", "claude session id (also passed to claude as --session-id after `--`)")
	transcriptDir := fs.String("transcript-dir", herdrClaudeConfigDir(), "claude config dir holding projects/*/<session>.jsonl")
	trustFile := fs.String("claude-config", herdrClaudeConfigFile(), "claude config file to mark the work dir trusted in (empty: skip)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *name == "" {
		fmt.Fprintln(os.Stderr, "herdr-driver: --name is required")
		return 2
	}

	ctx, cancel := context.WithCancel(context.Background())
	d := &herdrDriver{
		name:          *name,
		kind:          *kind,
		agentArgs:     fs.Args(),
		promptTimeout: *promptTimeout,
		pollInterval:  time.Duration(*pollMS) * time.Millisecond,
		watchInterval: herdrWatchIntervalMS * time.Millisecond,
		ctx:           ctx,
		cancel:        cancel,
		out:           bufio.NewWriter(os.Stdout),
		transcript:    newHerdrTranscriptTailer(*transcriptDir, *sessionID),
		trustFile:     *trustFile,
	}
	return d.run(os.Stdin)
}

// run is the driver's whole lifecycle: set up, relay stdin, tear down.
func (d *herdrDriver) run(stdin io.Reader) int {
	// SIGTERM (Agent.kill's graceful first signal) and SIGINT both mean
	// "close the pane and go" — the herdr *server* owns the pane and the
	// claude process inside it, so exiting without closing the workspace
	// would leave an unsupervised claude running forever.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigs
		d.cleanup()
		os.Exit(0)
	}()

	if err := d.start(); err != nil {
		fmt.Fprintf(os.Stderr, "herdr-driver: %v\n", err)
		d.cleanup()
		return 1
	}
	d.emit(herdrMarkerInit, d.paneID)

	// watchAgent runs for the rest of the driver's life, independent of
	// runTurn's poll loop (which only runs during a turn), so a claude that
	// dies or is closed from the herdr UI while idle between turns is
	// noticed too — see watchAgent.
	//
	// relay blocks reading stdin, and nothing can interrupt that blocking
	// read from another goroutine, so the two race here instead: whichever
	// finishes first decides how the driver exits. If watchAgent wins,
	// relay is abandoned still blocked on stdin — harmless, since returning
	// from run() ends the process (RunHerdrDriver's caller does
	// os.Exit(RunHerdrDriver(...))) and takes every goroutine with it.
	watchStop := make(chan struct{})
	agentDead := make(chan struct{})
	go func() {
		if d.watchAgent(watchStop) {
			close(agentDead)
		}
	}()

	relayDone := make(chan struct{})
	go func() {
		d.relay(stdin)
		close(relayDone)
	}()

	select {
	case <-relayDone:
		close(watchStop)
		d.cleanup()
		return 0
	case <-agentDead:
		d.cleanup()
		return 1
	}
}

// herdrWorkspaceCreated is the shape of `herdr workspace create`'s response.
// Only the two IDs the driver needs are modelled; herdr documents these
// paths (.result.root_pane, .result.workspace) as the ones to read.
type herdrWorkspaceCreated struct {
	Result *struct {
		RootPane *struct {
			PaneID string `json:"pane_id"`
		} `json:"root_pane"`
		Workspace *struct {
			WorkspaceID string `json:"workspace_id"`
		} `json:"workspace"`
	} `json:"result"`
	Error *herdrError `json:"error"`
}

// herdrError is the error object herdr puts on stderr (exit status 1) when a
// command fails on the server side.
type herdrError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// start creates the workspace and launches claude in its pane.
func (d *herdrDriver) start() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve cwd: %w", err)
	}

	// Answer claude's trust-this-folder dialog ahead of time; a cwd claude
	// has not seen before otherwise blocks the session before the first
	// prompt (herdr_trust.go). Best-effort: when this fails the launch goes
	// ahead and the dialog, if it appears, surfaces as the agent_not_ready
	// wait below timing out — the same failure as before, now with a line
	// on the output stream saying why.
	if d.trustFile != "" {
		if terr := herdrTrustWorkDir(d.trustFile, cwd); terr != nil {
			d.emit(herdrMarkerErr, fmt.Sprintf("could not mark %s trusted in %s: %v (claude may block on its trust dialog)", cwd, d.trustFile, terr))
		}
	}

	// --env, not a plain os/exec environment variable on the driver itself:
	// claude runs inside the pane as a child of the herdr *server*, so it
	// inherits the server's environment, never the driver's. --env is the
	// mechanism that actually reaches it (herdrBackend.Env used to set
	// CLAUDE_NO_ANALYTICS on the driver process, where it had no effect).
	//
	// The blanked variables are the ones claude sets for its own child
	// processes. A herdr server started from inside a claude session
	// passes them on to every pane, and the claude in the pane then takes
	// itself for a nested child session — which, among other things, turns
	// transcript saving off, and the transcript is where this driver's
	// output comes from. Blanking them makes it a top-level session again.
	raw, err := d.herdr("workspace", "create", "--cwd", cwd, "--label", d.name,
		"--env", "CLAUDE_NO_ANALYTICS=true",
		"--env", "CLAUDE_CODE_CHILD_SESSION=",
		"--env", "CLAUDECODE=",
		"--env", "CLAUDE_CODE_ENTRYPOINT=",
		"--env", "CLAUDE_CODE_SESSION_ID=",
		"--env", "CLAUDE_PID=",
		"--no-focus")
	if err != nil {
		return fmt.Errorf("herdr workspace create: %w: %s", err, raw)
	}
	var created herdrWorkspaceCreated
	if jerr := json.Unmarshal([]byte(raw), &created); jerr != nil {
		return fmt.Errorf("herdr workspace create: unparseable response: %s", raw)
	}
	if created.Error != nil {
		return fmt.Errorf("herdr workspace create failed (%s): %s", created.Error.Code, created.Error.Message)
	}
	if created.Result == nil || created.Result.RootPane == nil || created.Result.RootPane.PaneID == "" {
		return fmt.Errorf("herdr workspace create: no root_pane.pane_id in response: %s", raw)
	}
	d.paneID = created.Result.RootPane.PaneID
	if created.Result.Workspace != nil {
		// Recorded before agent start so a failure there still tears the
		// workspace down instead of leaking it.
		d.workspaceID = created.Result.Workspace.WorkspaceID
	}

	args := append([]string{"agent", "start", d.name, "--kind", d.kind, "--pane", d.paneID,
		"--timeout", fmt.Sprint(herdrStartTimeoutMS), "--"}, d.agentArgs...)
	if raw, err := d.herdr(args...); err != nil {
		// herdr's documented "agent_not_ready" is the one recoverable case:
		// the name is already bound to the pane and the agent is merely
		// still busy booting, so wait for it to settle rather than giving up
		// on a session that is actually coming up. Any other failure (an
		// unsupported --kind, a pane not at a shell prompt) is a hard
		// failure and must not be reported as "waiting for the agent to
		// settle" — it never will.
		if herdrErrorCode(raw) != "agent_not_ready" {
			return fmt.Errorf("herdr agent start: %v: %s", err, raw)
		}
		d.emit(herdrMarkerErr, fmt.Sprintf("herdr agent start: %v: %s (agent_not_ready — waiting for it to become idle)", err, raw))
		// --until idle/done, not agent wait's plain default: that default
		// also accepts "blocked" as settled, and a blocked agent is not
		// actually ready — the first prompt would fail immediately with
		// agent_blocked.
		if raw, werr := d.herdr("agent", "wait", d.name, "--until", "idle", "--until", "done",
			"--timeout", fmt.Sprint(herdrStartTimeoutMS)); werr != nil {
			return fmt.Errorf("herdr agent start: agent never became ready: %w: %s", werr, raw)
		}
	}

	// Seed the fallback read's baseline while the agent is freshly idle, so
	// a first turn that has to fall back to the pane yields that turn and
	// not the whole boot banner as well. Best-effort: an empty baseline only
	// makes that first delta larger.
	if raw, rerr := d.herdr("agent", "read", d.name, "--source", herdrFullReadSource,
		"--lines", fmt.Sprint(herdrFullReadLines), "--format", "text"); rerr == nil {
		d.snapMu.Lock()
		d.fullSnapshot = raw
		d.snapMu.Unlock()
	}
	return nil
}

// relay reads base64 prompt lines from stdin and runs one turn per line,
// returning once stdin is at EOF (which is how Agent.kill and a clean
// shutdown both signal "no more work") *and* every prompt already read has
// had its turn.
//
// Reading and running are two goroutines with a queue between them, not one
// loop, because the engine writes follow-ups straight into this process's
// stdin pipe under a mutex and expects the write to return (Agent.sendInput,
// agent_lifecycle.go — the caller is an API request handler). If the only
// reader of that pipe were the turn loop, stdin would go unread for the
// length of the in-flight turn: up to herdrPromptTimeoutMS, half an hour by
// default. A follow-up that fits in the OS pipe buffer (64KiB on Linux) is
// absorbed by the kernel and the handler returns either way, but a bigger
// one — buildTask inlines whole --context-files, and Engine.ResumeWithHistory
// seeds an unbounded history — blocks the handler mid-write until the turn
// settles. Draining stdin here is what makes prime.md's "a follow-up sent
// while a turn is in flight is queued" true for a follow-up of any size.
//
// The queue is a slice rather than a buffered channel on purpose: any fixed
// capacity is a capacity that can fill, and a full channel puts the blocking
// write back exactly where it was.
//
// A bufio.Reader rather than a Scanner: prompt text is unbounded and a
// Scanner would silently stop at its token limit.
func (d *herdrDriver) relay(stdin io.Reader) {
	var (
		mu     sync.Mutex
		cond   = sync.NewCond(&mu)
		queue  []string
		closed bool
	)

	turns := make(chan struct{})
	go func() {
		defer close(turns)
		for {
			mu.Lock()
			for len(queue) == 0 && !closed {
				cond.Wait()
			}
			if len(queue) == 0 {
				mu.Unlock()
				return
			}
			text := queue[0]
			queue = queue[1:]
			mu.Unlock()
			d.runTurn(text)
		}
	}()

	r := bufio.NewReader(stdin)
	for {
		line, err := r.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, herdrTaskPrefix) {
			text, derr := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, herdrTaskPrefix))
			if derr != nil {
				d.emit(herdrMarkerErr, fmt.Sprintf("herdr-driver: undecodable prompt line: %v", derr))
			} else {
				mu.Lock()
				queue = append(queue, string(text))
				cond.Signal()
				mu.Unlock()
			}
		}
		if err != nil {
			break
		}
	}

	mu.Lock()
	closed = true
	cond.Broadcast()
	mu.Unlock()
	<-turns
}

// runTurn submits one prompt and reports the turn's outcome.
//
// While the prompt is in flight claude's transcript is tailed so the output
// stream shows progress as it happens; once the turn has settled the tail is
// drained one last time. Only when the transcript produced nothing for the
// turn is the pane read instead (emitFullSnapshot): its scrollback-inclusive
// snapshot, which herdr refuses while the agent is working and answers once
// it settles, diffed against the previous one.
//
// Ordering matters: the output is emitted before the state line, because
// the state line is what ParseEvent turns into a BackendResult and
// handleResult (agent_events.go) treats that as "turn complete" — it sets
// EndedAt, plays the completion sound and starts the worktree merge. Anything
// that reads the transcript on that notification must already be able to see
// the turn's output.
func (d *herdrDriver) runTurn(text string) {
	before := d.transcript.records

	stop := make(chan struct{})
	polled := make(chan struct{})
	go func() {
		defer close(polled)
		d.pollLoop(stop)
	}()

	state := d.prompt(text)

	close(stop)
	<-polled
	d.pollTranscript()

	if d.transcript.records == before {
		d.emitFullSnapshot()
	}
	d.emit(herdrMarkerState, state)
}

// prompt submits the text and returns the raw JSON that settled the turn.
// Text over herdrMaxPromptArgBytes cannot go through `herdr agent prompt` at
// all — it would be one argv entry over Linux's MAX_ARG_STRLEN — so it takes
// a different path (promptChunked) that never puts the text on a command
// line.
func (d *herdrDriver) prompt(text string) string {
	// Observed *before* anything is submitted, because both submission
	// paths can end up having to decide for themselves whether the turn
	// they sent actually ran, and neither the agent's state nor herdr's
	// own reply can answer that on its own — see settleTurn.
	before := d.observeAgent()
	if len(text) > herdrMaxPromptArgBytes {
		return d.promptChunked(text, before)
	}
	return d.promptDirect(text, before)
}

// herdrAgentGet is the shape of `herdr agent get` (and of the agent object
// every other command embeds). state_change_seq is herdr's own monotonic
// counter of lifecycle transitions for this agent, verified present on the
// installed herdr's agent objects; it is the only thing that can tell
// "the agent is idle because it never started this turn" apart from "the
// agent is idle because it already finished it".
type herdrAgentGet struct {
	Result *struct {
		Agent *struct {
			AgentStatus    string `json:"agent_status"`
			StateChangeSeq int64  `json:"state_change_seq"`
		} `json:"agent"`
	} `json:"result"`
}

// herdrObservation is one `herdr agent get`, parsed.
type herdrObservation struct {
	// raw is herdr's response verbatim, error responses included.
	raw string
	// ok is true only when a status was actually parsed out of raw.
	ok     bool
	err    error
	status string
	seq    int64
}

// observeAgent asks herdr for the agent's current state.
func (d *herdrDriver) observeAgent() herdrObservation {
	raw, err := d.herdr("agent", "get", d.name)
	obs := herdrObservation{raw: raw, err: err}
	if err != nil {
		return obs
	}
	var got herdrAgentGet
	if jerr := json.Unmarshal([]byte(raw), &got); jerr != nil || got.Result == nil || got.Result.Agent == nil {
		return obs
	}
	obs.ok = true
	obs.status = got.Result.Agent.AgentStatus
	obs.seq = got.Result.Agent.StateChangeSeq
	return obs
}

// herdrSettledStatus reports whether a status is one of herdr's settled,
// turn-is-over states. "blocked" is settled to herdr but is not a finished
// turn, so it is deliberately not here.
func herdrSettledStatus(status string) bool {
	return status == "idle" || status == "done"
}

// herdrTransitionTimeoutMS bounds the wait for the agent to be *seen*
// working after a submission herdr did not confirm itself. It only has to
// cover herdr's own detection latency, not the turn, so it is short — the
// wait for the turn to finish is a second wait, at the full per-turn timeout.
const herdrTransitionTimeoutMS = 30 * 1000

// settleTurn returns the raw JSON describing how a turn settled, for the two
// submission paths that do not get that from herdr directly: promptChunked
// (which types into the pane, so herdr never tracked a prompt at all) and
// promptDirect's agent_prompt_stalled recovery.
//
// It must not be a plain `herdr agent wait`. Without --until, wait matches
// herdr's default settled states — idle, done, blocked — and the agent is
// already sitting in one of those *before* the prompt is submitted. So a
// plain wait returns immediately with the pre-prompt state, and the driver
// reports a turn that may not have started as complete: handleResult then
// sets EndedAt, plays the completion sound and starts the worktree
// merge-back while claude is still working (or never started). start() gets
// this right for the analogous agent_not_ready recovery; this used not to.
//
// So a transition has to be confirmed first. The primary signal is herdr
// seeing the agent go "working". The fallback covers the one case that
// cannot produce: a turn that came and went inside the gap between the
// submission and this wait registering, which leaves the agent settled again
// with no "working" left to observe. state_change_seq distinguishes that
// from an agent that never moved — before is the observation taken before
// the submission, so a seq that has advanced is proof the agent did
// something in between.
func (d *herdrDriver) settleTurn(before herdrObservation) string {
	if raw, err := d.herdr("agent", "wait", d.name, "--until", "working",
		"--timeout", fmt.Sprint(herdrTransitionTimeoutMS)); err != nil {
		after := d.observeAgent()
		if before.ok && after.ok && after.seq > before.seq && herdrSettledStatus(after.status) {
			d.emit(herdrMarkerErr, "herdr-driver: the agent was never observed working, but its "+
				"state_change_seq advanced and it is settled again — treating the turn as having run inside "+
				"the observation gap")
			return after.raw
		}
		// Reported as a failed turn rather than guessed at: an agent that
		// never left its pre-prompt state is an agent whose prompt did not
		// take, and calling that a completed turn is how a merge-back gets
		// started on work that was never done.
		if raw == "" {
			raw = fmt.Sprintf(`{"error":{"code":"cli_failed","message":%q}}`, err.Error())
		}
		return raw
	}

	raw, err := d.herdr("agent", "wait", d.name, "--until", "idle", "--until", "done",
		"--timeout", fmt.Sprint(d.promptTimeout))
	if err != nil && raw == "" {
		raw = fmt.Sprintf(`{"error":{"code":"cli_failed","message":%q}}`, err.Error())
	}
	return raw
}

// promptChunked delivers an oversized prompt by typing it into the pane, the
// way a human pasting it would: several `herdr pane send-text` calls followed
// by a separate Enter, then settleTurn for the state `agent prompt --wait`
// would otherwise have returned directly. Verified against a live claude
// session: multiple send-text calls accumulate in the same unsubmitted input
// box, including one whose text contains a newline (bracketed paste covers a
// send-text call the same way it covers `agent prompt`, so an embedded
// newline does not submit early), and `agent send-keys <name> enter` then
// submits normally.
//
// Every chunk is under herdrPromptChunkBytes and the chunks concatenate to
// exactly text — both of which are herdrChunkText's job, and neither of
// which it used to do: it emitted an over-long single line whole (E2BIG at
// exec, the failure this path exists to avoid) and dropped the newline on
// every chunk boundary (one line silently glued to the next).
//
// `agent prompt` itself checks agent_blocked before sending anything;
// pane send-text has no such check, so it is replicated here by hand to
// avoid typing a prompt into a pane that is actually waiting on an approval
// dialog.
func (d *herdrDriver) promptChunked(text string, before herdrObservation) string {
	if before.err != nil {
		raw := before.raw
		if raw == "" {
			raw = fmt.Sprintf(`{"error":{"code":"cli_failed","message":%q}}`, before.err.Error())
		}
		return raw
	}
	if before.status == "blocked" {
		return `{"error":{"code":"agent_blocked","message":"agent is blocked awaiting approval or input"}}`
	}

	for _, chunk := range herdrChunkText(text, herdrPromptChunkBytes) {
		if raw, err := d.herdr("pane", "send-text", d.paneID, chunk); err != nil {
			return fmt.Sprintf(`{"error":{"code":"cli_failed","message":%q}}`, fmt.Sprintf("herdr pane send-text: %v: %s", err, raw))
		}
	}
	if raw, err := d.herdr("agent", "send-keys", d.name, "enter"); err != nil {
		return fmt.Sprintf(`{"error":{"code":"cli_failed","message":%q}}`, fmt.Sprintf("herdr agent send-keys enter: %v: %s", err, raw))
	}

	return d.settleTurn(before)
}

// promptDirect submits text (already known to fit in one argv entry) with a
// single `herdr agent prompt --wait` and returns the raw JSON that settled
// the turn.
func (d *herdrDriver) promptDirect(text string, before herdrObservation) string {
	raw, err := d.herdr("agent", "prompt", d.name, text, "--wait", "--timeout", fmt.Sprint(d.promptTimeout))
	if err == nil {
		return raw
	}

	// agent_prompt_stalled is not by itself a failed turn: it means herdr
	// did not observe a lifecycle change within its five-second window after
	// the prompt was delivered (documented in `herdr --skill`). The prompt
	// may well have gone in, so resolve the turn's real outcome rather than
	// declaring it failed and stranding the agent in AgentError with a live
	// claude still working in the pane. settleTurn is what does that
	// honestly: it confirms the agent actually left its pre-prompt state
	// before accepting an idle observation as this turn's completion.
	if herdrErrorCode(raw) == "agent_prompt_stalled" {
		d.emit(herdrMarkerErr, "herdr agent prompt reported agent_prompt_stalled (no lifecycle change within 5s); "+
			"confirming whether the turn started before resolving its settled state")
		if waited := d.settleTurn(before); waited != "" {
			return waited
		}
	}

	if raw == "" {
		// No JSON at all (herdr missing from PATH, server unreachable):
		// synthesise the same error shape so ParseEvent has one code path.
		raw = fmt.Sprintf(`{"error":{"code":"cli_failed","message":%q}}`, err.Error())
	}
	return raw
}

// herdrErrorCode extracts .error.code from a herdr response, or "".
func herdrErrorCode(raw string) string {
	var parsed struct {
		Error *herdrError `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &parsed); err != nil || parsed.Error == nil {
		return ""
	}
	return parsed.Error.Code
}

// watchAgent periodically confirms the claude session this driver started is
// still there, and exits the driver the moment it is not.
//
// Nothing else does this: pollLoop only runs during a turn (runTurn's stop
// channel), and relay just blocks on stdin between turns. If claude exits or
// crashes, or the pane is closed from the herdr UI, without this the driver
// stays alive indefinitely — the agent never reaches a terminal state, and
// since Engine.WorkDirOccupied asks whether the driver process has exited
// rather than the state label, the work directory stays occupied for the
// rest of the daemon's lifetime.
//
// agent_not_found is the one signal treated as conclusive: it is what herdr
// reports once a name is released, which happens exactly when the agent it
// was bound to exits or is replaced (herdr --skill). Any other error (a
// transient read failure, a mid-reload server) is not evidence the agent is
// gone and is ignored — the next tick tries again.
// watchAgent returns true the moment it confirms the agent is gone (having
// already emitted the error marker describing why), or false when stop or
// d.ctx.Done fires first — a normal shutdown, not a dead session.
func (d *herdrDriver) watchAgent(stop <-chan struct{}) bool {
	ticker := time.NewTicker(d.watchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return false
		case <-d.ctx.Done():
			return false
		case <-ticker.C:
			raw, err := d.herdr("agent", "get", d.name)
			if err == nil || herdrErrorCode(raw) != "agent_not_found" {
				continue
			}
			d.emit(herdrMarkerErr, fmt.Sprintf(
				"herdr agent get: %s — the claude session appears to have exited or its pane was closed; driver exiting", raw))
			return true
		}
	}
}

// pollLoop re-reads the transcript until stop is closed, so a long turn
// reports progress as it happens instead of arriving in one lump at the end.
func (d *herdrDriver) pollLoop(stop <-chan struct{}) {
	ticker := time.NewTicker(d.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			d.pollTranscript()
		}
	}
}

// pollTranscript forwards every transcript record claude has written since
// the previous poll. The poll loop and runTurn's final drain never overlap
// (runTurn waits for the loop to stop first), so the tailer needs no lock.
func (d *herdrDriver) pollTranscript() {
	err := d.transcript.poll(func(record string) {
		d.emit(herdrMarkerJSONL, record)
	})
	if err != nil {
		d.emit(herdrMarkerErr, fmt.Sprintf("claude transcript %s: %v", d.transcript.path, err))
	}
}

// emitFullSnapshot is the pane fallback: it reads the settled pane's
// scrollback-inclusive snapshot, emits what is new since the previous such
// read, and makes this read the next baseline.
//
// A failed read leaves the baseline alone on purpose: the next successful
// read then diffs against the last known-good snapshot and recovers the
// missed turn too, instead of starting from a gap. The most likely failure
// is herdr's own agent_not_idle, which is what a turn that ended in a
// timeout (claude still working in the pane) looks like from here.
func (d *herdrDriver) emitFullSnapshot() {
	raw, err := d.herdr("agent", "read", d.name, "--source", herdrFullReadSource,
		"--lines", fmt.Sprint(herdrFullReadLines), "--format", "text")
	if err != nil {
		d.emit(herdrMarkerErr, fmt.Sprintf("herdr agent read (pane fallback, no transcript record for this turn): %v: %s", err, raw))
		return
	}

	d.snapMu.Lock()
	delta, _ := herdrDiffSnapshot(d.fullSnapshot, raw)
	d.fullSnapshot = raw
	d.snapMu.Unlock()

	d.emitText(delta)
}

// emitText emits text as however many __SINGL_OUT__ lines it takes.
func (d *herdrDriver) emitText(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	for _, chunk := range herdrChunkText(text, herdrOutChunkBytes) {
		d.emit(herdrMarkerOut, chunk)
	}
}

// emit writes one base64 marker line to stdout.
func (d *herdrDriver) emit(marker, payload string) {
	if d.shuttingDown.Load() {
		return
	}
	d.outMu.Lock()
	defer d.outMu.Unlock()
	fmt.Fprintf(d.out, "%s%s\n", marker, base64.StdEncoding.EncodeToString([]byte(payload)))
	_ = d.out.Flush()
}

// herdr runs one herdr CLI command and returns its output, preferring stdout
// and falling back to stderr — herdr reports server errors as JSON on stderr
// with exit status 1, and callers want that JSON, not just the exit code.
func (d *herdrDriver) herdr(args ...string) (string, error) {
	cmd := exec.CommandContext(d.ctx, "herdr", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		out = strings.TrimSpace(stderr.String())
	}
	return out, err
}

// cleanup closes the workspace this driver created (which is what stops the
// claude process inside it) and cancels any in-flight herdr call. Safe to
// call more than once and from a signal handler.
func (d *herdrDriver) cleanup() {
	d.cleanupOnce.Do(func() {
		ws := d.workspaceID
		d.shuttingDown.Store(true)
		d.cancel()
		if ws == "" {
			return
		}
		// d.ctx is cancelled, so this last call needs its own: bounded,
		// because a herdr server that has stopped answering must not hold
		// the driver open past Agent.kill's grace period.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "herdr", "workspace", "close", ws)
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "herdr-driver: workspace close %s: %v: %s\n", ws, err, out)
		}
	})
}

// herdrStatusJSON is the shape of `herdr status --json`'s server section
// (verified against the installed herdr; the plain-text form this used to
// scrape for "status: running" also prints "compatible: no" for a running
// server whose protocol doesn't match this client, which the old substring
// check never looked for).
type herdrStatusJSON struct {
	Server struct {
		Running    bool `json:"running"`
		Compatible bool `json:"compatible"`
	} `json:"server"`
}

// herdrPreflight reports whether the herdr CLI and a compatible, running
// herdr server are both reachable. Without a server every
// `herdr workspace create` fails and each spawned agent dies immediately
// with a raw JSON error, which is a confusing way to learn about a one-line
// misconfiguration; a server running an incompatible protocol fails the same
// way on every subsequent call, just later.
func herdrPreflight() error {
	if _, err := exec.LookPath("herdr"); err != nil {
		return errors.New("herdr is not on PATH (install it from https://herdr.dev)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "herdr", "status", "--json").CombinedOutput()
	if err != nil {
		return fmt.Errorf("`herdr status` failed (%v): %s", err, strings.TrimSpace(string(out)))
	}
	var st herdrStatusJSON
	if jerr := json.Unmarshal(out, &st); jerr != nil {
		return fmt.Errorf("`herdr status --json`: unparseable response: %s", strings.TrimSpace(string(out)))
	}
	if !st.Server.Running {
		return errors.New("no herdr server is running (start one with `herdr` or `herdr server`)")
	}
	if !st.Server.Compatible {
		return errors.New("a herdr server is running but its protocol is incompatible with this herdr client " +
			"(run `herdr update`, or restart the herdr server so both sides match)")
	}
	return nil
}
