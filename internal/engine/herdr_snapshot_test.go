package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// paneSnapshot loads one of the two real `herdr agent read` snapshots in
// testdata/. They were captured from a pane running interactive claude, one
// turn apart, and they are what makes the diff testable: a synthetic
// pure-append pair passes with any prefix comparison, while these do not
// start with each other at all (each ends with claude's redrawn input box and
// status lines) and even lay their blank padding out differently.
func paneSnapshot(t *testing.T, n int) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", fmt.Sprintf("herdr_pane_snapshot%d.txt", n)))
	if err != nil {
		t.Fatalf("read snapshot fixture: %v", err)
	}
	return string(data)
}

func TestHerdrDiffSnapshotRealPaneSnapshots(t *testing.T) {
	first, second := paneSnapshot(t, 1), paneSnapshot(t, 2)

	// A first read has nothing to diff against, so all of it is new.
	if got, lossy := herdrDiffSnapshot("", first); got != first || lossy {
		t.Errorf("first snapshot: got=%q lossy=%v, want the full snapshot and lossy=false", got, lossy)
	}

	delta, lossy := herdrDiffSnapshot(first, second)
	if lossy {
		t.Error("lossy = true for two real, one-turn-apart snapshots that do overlap")
	}

	// The second turn's prompt and reply are new and must be emitted.
	for _, want := range []string{"single word DONE", "● DONE"} {
		if !strings.Contains(delta, want) {
			t.Errorf("delta is missing %q\ndelta:\n%s", want, delta)
		}
	}
	// The first turn is old and must not be replayed — the whole point.
	for _, unwanted := range []string{"List the numbers 1 to 120", "Claude Code v", "  119"} {
		if strings.Contains(delta, unwanted) {
			t.Errorf("delta replays the previous turn (%q)\ndelta:\n%s", unwanted, delta)
		}
	}
	// The unchanged frame at the bottom is not new either.
	if strings.Contains(delta, "bypass permissions on") {
		t.Errorf("delta re-emits the unchanged status frame\ndelta:\n%s", delta)
	}
	if lines := len(strings.Split(strings.TrimSpace(delta), "\n")); lines > 8 {
		t.Errorf("delta is %d lines of a %d-line snapshot, want only the new turn\ndelta:\n%s",
			lines, len(strings.Split(second, "\n")), delta)
	}
}

func TestHerdrDiffSnapshotPureAppend(t *testing.T) {
	got, lossy := herdrDiffSnapshot("line one\nline two\n", "line one\nline two\nline three\n")
	if got != "line three\n" || lossy {
		t.Errorf("delta = %q lossy=%v, want %q and lossy=false", got, lossy, "line three\n")
	}
}

func TestHerdrDiffSnapshotIdenticalIsEmpty(t *testing.T) {
	if got, lossy := herdrDiffSnapshot("a\nb\n", "a\nb\n"); got != "" || lossy {
		t.Errorf("delta = %q lossy=%v, want empty and lossy=false for an unchanged snapshot", got, lossy)
	}
}

func TestHerdrDiffSnapshotScrollingWindow(t *testing.T) {
	// claude renders on the alternate screen, whose rows never reach herdr's
	// host scrollback, so a long conversation's snapshot is a window that
	// slides: prev's tail is cur's head.
	prev := "one\ntwo\nthree\nfour"
	cur := "three\nfour\nfive\nsix"
	got, lossy := herdrDiffSnapshot(prev, cur)
	if got != "five\nsix" || lossy {
		t.Errorf("delta = %q lossy=%v, want %q and lossy=false: the window slid by less than its own height, so prev's tail is still cur's head", got, lossy, "five\nsix")
	}
}

func TestHerdrDiffSnapshotScrolledPastViewportLosesContent(t *testing.T) {
	// The case that actually happens on the small viewport an unattached,
	// daemon-created pane gets (see herdr_driver.go's herdrLiveReadSource): more
	// than a whole viewport's worth of new lines arrive between two polls, so
	// unlike TestHerdrDiffSnapshotScrollingWindow, prev's tail does not even
	// appear anywhere in cur any more. "five" and "six" scrolled past
	// entirely and are unrecoverable — they never left the alternate screen,
	// so they never reached herdr's host scrollback either. The fallback (the
	// whole new snapshot re-emitted) does not replay them; it just avoids
	// silently dropping "seven" through "ten", and the caller must be able to
	// tell this happened instead of mistaking the re-emit for ordinary new
	// output that just happens to be long.
	prev := "one\ntwo\nthree\nfour\nfive\nsix"
	cur := "seven\neight\nnine\nten"
	delta, lossy := herdrDiffSnapshot(prev, cur)
	if !lossy {
		t.Error("lossy = false, want true: no overlap between prev and cur means the gap between them cannot be trusted")
	}
	if delta != cur {
		t.Errorf("delta = %q, want the whole new snapshot (%q) re-emitted since nothing anchors the diff", delta, cur)
	}
}

func TestHerdrDiffSnapshotUnrelatedFallsBackToWhole(t *testing.T) {
	// Re-emitting the whole snapshot here is the least-bad fallback, not a
	// correctness guarantee: it avoids permanently dropping "gamma"/"delta",
	// but if "alpha"/"beta" scrolled past rather than being replaced outright
	// there would be no way to tell from this pair, and no way to recover
	// them either way — see TestHerdrDiffSnapshotScrolledPastViewportLosesContent.
	got, lossy := herdrDiffSnapshot("alpha\nbeta", "gamma\ndelta")
	if got != "gamma\ndelta" || !lossy {
		t.Errorf("delta = %q lossy=%v, want the whole snapshot and lossy=true when nothing matches", got, lossy)
	}
}

func TestHerdrChunkTextSplitsOnLineBoundaries(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		sb.WriteString(strings.Repeat("x", 99))
		sb.WriteByte('\n')
	}
	chunks := herdrChunkText(sb.String(), 1000)
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want the oversized text split up", len(chunks))
	}
	for i, c := range chunks {
		if len(c) > 1000 {
			t.Errorf("chunk %d is %d bytes, over the 1000-byte limit", i, len(c))
		}
		for _, line := range strings.Split(strings.TrimSuffix(c, "\n"), "\n") {
			if len(line) != 99 {
				t.Errorf("chunk %d split a line in half: %q", i, line)
			}
		}
	}
	if rejoined := strings.Join(chunks, ""); rejoined != sb.String() {
		t.Error("chunks do not concatenate back to the original text")
	}
}

// The chunks are reassembled by writing them out back to back — successive
// `herdr pane send-text` calls type them into the pane with nothing in
// between — so anything the chunker drops is a byte the agent never sees.
// This used to split on "\n" and rejoin with "\n" *inside* each chunk, which
// ate the separator at every boundary: herdrChunkText("aaa\nbbb\nccc\nddd", 8)
// returned ["aaa\nbbb", "ccc\nddd"], and typing those two into the pane gave
// claude "aaa\nbbbccc\nddd".
func TestHerdrChunkTextKeepsBoundaryNewlines(t *testing.T) {
	for _, tc := range []struct {
		name  string
		text  string
		limit int
	}{
		{"the reviewer's case", "aaa\nbbb\nccc\nddd", 8},
		{"boundary exactly on a newline", "abc\ndef\n", 4},
		{"trailing newline", "ab\ncd\n", 3},
		{"no trailing newline", "ab\ncd", 3},
		{"blank lines", "a\n\n\nb\n\nc", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chunks := herdrChunkText(tc.text, tc.limit)
			for i, c := range chunks {
				if len(c) > tc.limit {
					t.Errorf("chunk %d is %d bytes, over the %d-byte limit: %q", i, len(c), tc.limit, c)
				}
			}
			if got := strings.Join(chunks, ""); got != tc.text {
				t.Errorf("chunks %q concatenate to %q, want the original %q", chunks, got, tc.text)
			}
		})
	}
}

// A single line longer than the limit used to be emitted whole, on the theory
// that the limit only ever guarded a *snapshot*. promptChunked passes a limit
// that exists because Linux rejects an argv entry over MAX_ARG_STRLEN
// (131072) with E2BIG, and one long line is exactly what an inlined minified
// JSON context file or a base64 blob looks like — so the oversized chunk went
// straight into the `herdr pane send-text` argv that this whole code path
// exists to keep small enough, and the turn failed with an opaque cli_failed.
func TestHerdrChunkTextSplitsAnOverlongLine(t *testing.T) {
	text := strings.Repeat("x", 300000)
	chunks := herdrChunkText(text, herdrPromptChunkBytes)
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks for a %d-byte single line, want it split up", len(chunks), len(text))
	}
	for i, c := range chunks {
		if len(c) > herdrPromptChunkBytes {
			t.Errorf("chunk %d is %d bytes, over the %d-byte limit — execve would reject it with E2BIG",
				i, len(c), herdrPromptChunkBytes)
		}
	}
	if got := strings.Join(chunks, ""); got != text {
		t.Errorf("chunks concatenate to %d bytes, want the original %d", len(got), len(text))
	}
}

// herdr's CLI takes its arguments as UTF-8, so a hard byte split has to back
// off to a rune boundary rather than hand it half a rune.
func TestHerdrChunkTextSplitsOnRuneBoundaries(t *testing.T) {
	text := strings.Repeat("é", 100) // two bytes each, no newline anywhere
	chunks := herdrChunkText(text, 7)
	if got := strings.Join(chunks, ""); got != text {
		t.Fatalf("chunks concatenate to %q, want the original", got)
	}
	for i, c := range chunks {
		if len(c) > 7 {
			t.Errorf("chunk %d is %d bytes, over the limit", i, len(c))
		}
		if !utf8.ValidString(c) {
			t.Errorf("chunk %d is not valid UTF-8: %q", i, c)
		}
	}
}

func TestHerdrChunkTextShortTextIsOneChunk(t *testing.T) {
	if chunks := herdrChunkText("a\nb\n", 1000); len(chunks) != 1 || chunks[0] != "a\nb\n" {
		t.Errorf("chunks = %q, want the text unchanged in one piece", chunks)
	}
}

func TestHerdrErrorCode(t *testing.T) {
	if got := herdrErrorCode(`{"error":{"code":"agent_prompt_stalled","message":"no change"}}`); got != "agent_prompt_stalled" {
		t.Errorf("code = %q, want agent_prompt_stalled", got)
	}
	if got := herdrErrorCode(`{"result":{"agent":{"agent_status":"done"}}}`); got != "" {
		t.Errorf("code = %q, want empty for a success response", got)
	}
	if got := herdrErrorCode("not json at all"); got != "" {
		t.Errorf("code = %q, want empty for unparseable output", got)
	}
}
