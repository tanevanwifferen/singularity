package engine

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The herdr backend's output comes from claude's own session transcript, not
// from the pane. claude writes every message of an interactive session to
// <config dir>/projects/<encoded cwd>/<session id>.jsonl as it happens, and
// each record carries the same `type` + `message.content` shape as a
// stream-json event: assistant text and tool_use blocks, user tool_result
// blocks. The driver launches claude with a session id it chose itself
// (`--session-id`), so it knows which file to tail without herdr's help, and
// forwards each new record to the engine on a __SINGL_JSONL__ marker line,
// where herdrTranscriptEvents turns it into the same BackendText /
// BackendToolUse / BackendToolResult events the claude backend produces.
//
// That is what makes the herdr backend's output readable. It used to be
// built by diffing successive reads of the rendered pane, and a TUI is not a
// log: claude repaints its whole working block (spinner, elapsed-time
// counter, the tool call in flight) on every tick, so each poll re-emitted
// that block as if it were new output, and a redraw that shifted the
// viewport by more than its height re-emitted the whole screen. The pane is
// now read only as a fallback, once a turn has settled, when the transcript
// produced nothing for it (see runTurn) — which happens when transcript
// saving is off for the claude in the pane.

// herdrMarkerJSONL carries one base64 transcript record.
const herdrMarkerJSONL = "__SINGL_JSONL__"

// herdrClaudeConfigDir is where claude keeps its sessions: $CLAUDE_CONFIG_DIR
// when set, else ~/.claude.
func herdrClaudeConfigDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

// newSessionUUID returns a random version-4 UUID in the canonical form
// claude's --session-id requires.
func newSessionUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Unrecoverable in practice; a name-derived fallback still yields a
		// syntactically valid UUID unique enough within one daemon.
		copy(b[:], []byte(randomHex(16)))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// herdrTranscriptTailer follows one claude session transcript. The file does
// not exist until claude has written its first record, and claude encodes
// the working directory into the parent directory's name by a rule that is
// its own to change, so the tailer finds the file by its session id under
// every project directory instead of predicting the path.
type herdrTranscriptTailer struct {
	configDir string
	sessionID string

	path    string
	offset  int64
	partial []byte
	// records counts the complete records forwarded so far; runTurn uses
	// the per-turn delta to decide whether the pane fallback is needed.
	records int
}

func newHerdrTranscriptTailer(configDir, sessionID string) *herdrTranscriptTailer {
	return &herdrTranscriptTailer{configDir: configDir, sessionID: sessionID}
}

// locate finds the transcript file once it exists. Returns "" while it does
// not, which is not an error: claude has simply not written anything yet.
func (t *herdrTranscriptTailer) locate() string {
	if t.path != "" {
		return t.path
	}
	if t.sessionID == "" {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(t.configDir, "projects", "*", t.sessionID+".jsonl"))
	if len(matches) == 0 {
		return ""
	}
	t.path = matches[0]
	return t.path
}

// poll reads whatever claude has appended since the previous call and hands
// each complete record to emit. A record still being written (no trailing
// newline yet) is held back until the next poll.
func (t *herdrTranscriptTailer) poll(emit func(record string)) error {
	path := t.locate()
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(t.offset, 0); err != nil {
		return err
	}
	buf := make([]byte, 256*1024)
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			t.offset += int64(n)
			t.partial = append(t.partial, buf[:n]...)
			for {
				nl := bytes.IndexByte(t.partial, '\n')
				if nl < 0 {
					break
				}
				line := strings.TrimSpace(string(t.partial[:nl]))
				t.partial = t.partial[nl+1:]
				if line == "" {
					continue
				}
				t.records++
				emit(line)
			}
		}
		if rerr != nil {
			return nil
		}
	}
}

// herdrTranscriptEvents turns one transcript record into backend events.
// Assistant records are parsed exactly as the claude backend parses its
// stream-json assistant events; user records contribute their tool_result
// blocks (the initial prompt and follow-ups are plain strings there, and the
// engine already logs those itself as user_input). Everything else claude
// writes to the file — attachments, queue operations, sidechain (sub-agent)
// records, its own bookkeeping — is ignored.
func herdrTranscriptEvents(record string) []*BackendEvent {
	var rec struct {
		Type        string `json:"type"`
		IsSidechain bool   `json:"isSidechain"`
		Message     struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(record), &rec); err != nil {
		return []*BackendEvent{{Kind: BackendError, Content: fmt.Sprintf("herdr driver: unparseable transcript record: %v", err)}}
	}
	if rec.IsSidechain {
		return []*BackendEvent{{Kind: BackendIgnore}}
	}
	switch rec.Type {
	case "assistant":
		var event map[string]interface{}
		if err := json.Unmarshal([]byte(record), &event); err != nil {
			return []*BackendEvent{{Kind: BackendIgnore}}
		}
		events, _ := (&claudeBackend{}).parseAssistantEvent(event)
		return events
	case "user":
		return herdrToolResultEvents(rec.Message.Content)
	default:
		return []*BackendEvent{{Kind: BackendIgnore}}
	}
}

// herdrToolResultEvents extracts tool_result blocks from a user record's
// content, which is a string for a typed prompt and a block list for tool
// results. A block's own content is likewise either a string or a list of
// text blocks.
func herdrToolResultEvents(content json.RawMessage) []*BackendEvent {
	var blocks []struct {
		Type      string          `json:"type"`
		ToolUseID string          `json:"tool_use_id"`
		IsError   bool            `json:"is_error"`
		Content   json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return []*BackendEvent{{Kind: BackendIgnore}}
	}
	var events []*BackendEvent
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		events = append(events, &BackendEvent{
			Kind:    BackendToolResult,
			Content: herdrFlattenContent(b.Content),
			ToolID:  b.ToolUseID,
			IsError: b.IsError,
		})
	}
	if len(events) == 0 {
		return []*BackendEvent{{Kind: BackendIgnore}}
	}
	return events
}

func herdrFlattenContent(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}
