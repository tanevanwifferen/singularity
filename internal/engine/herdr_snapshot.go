package engine

import (
	"strings"
	"unicode/utf8"
)

// This file holds the two pure functions the herdr driver (herdr_driver.go)
// uses to turn successive pane reads into an output stream: the snapshot diff
// and the line-boundary chunker. They are separated out because they are the
// only part of the driver that is testable without a herdr server at all, and
// they are where the interesting behaviour lives — see
// testdata/herdr_pane_snapshot*.txt for the real snapshots the diff is
// measured against.

// herdrDiffSnapshot returns the lines of cur that are new relative to prev.
//
// A `herdr agent read` snapshot is not an append-only log: it is a rendering
// of a live TUI, so it ends with claude's redrawn input box and status lines,
// and the blank padding between blocks moves around between reads. Two real
// consecutive snapshots one turn apart are in testdata/ — the second does not
// start with the first, which is why a plain prefix comparison finds no
// common part and re-emits the entire transcript on every single turn, and
// its blank lines sit at different offsets, which is why comparing every line
// is not enough either.
//
// So the alignment is done over the *non-blank* lines only, from both ends:
//
//   - the common leading non-blank lines are the transcript already emitted
//     (blank padding shifting inside that region no longer breaks the match);
//   - failing that — the viewport scrolled, so nothing matches at the head —
//     the longest suffix of prev that is a prefix of cur gives the boundary;
//   - the common trailing non-blank lines are the unchanged frame at the
//     bottom.
//
// What is left between the two matches is the new output, returned verbatim
// (blank lines included; ParseEvent drops them). When the frame itself
// changed — a spinner, a token count, a "done" timestamp — its changed lines
// fall into that middle and are emitted again: a couple of duplicate status
// lines per poll, against re-emitting the whole transcript.
//
// If nothing matches at either end, the whole snapshot is re-emitted and
// lossy is true: cur could not be anchored to prev at all, so there is no
// basis for telling which of its lines are new. On the live viewport reads
// that is routine (more than the 39 rows an unattached, daemon-created pane
// renders arriving between two polls; see herdr_driver.go's
// herdrLiveReadSource), and it is why the caller reports it and falls back to
// the end-of-turn full read — emitFullSnapshot — which diffs the whole
// scrollback and does recover the rows the live stream could not anchor.
func herdrDiffSnapshot(prev, cur string) (delta string, lossy bool) {
	if prev == "" {
		return cur, false
	}
	if prev == cur {
		return "", false
	}
	prevLines := strings.Split(prev, "\n")
	curLines := strings.Split(cur, "\n")
	prevAt := significantLines(prevLines)
	curAt := significantLines(curLines)

	head := commonPrefixAt(prevLines, prevAt, curLines, curAt)
	if head == 0 {
		head = overlapAt(prevLines, prevAt, curLines, curAt)
	}

	// The tail scan must not walk back past the head match in either
	// snapshot, or the two ranges would overlap and drop real content.
	maxTail := len(curAt) - head
	if n := len(prevAt) - head; n < maxTail {
		maxTail = n
	}
	tail := 0
	for tail < maxTail &&
		prevLines[prevAt[len(prevAt)-1-tail]] == curLines[curAt[len(curAt)-1-tail]] {
		tail++
	}

	// head == 0 means neither an exact leading match nor a scrolled-window
	// overlap (overlapAt) could anchor cur's start to prev's end at all: cur
	// is treated as (almost) entirely new relative to prev with no basis for
	// telling genuinely-new content apart from old content that scrolled
	// past undetected — see the doc comment above.
	lossy = head == 0

	from := 0
	if head > 0 {
		from = curAt[head-1] + 1
	}
	to := len(curLines)
	if tail > 0 {
		to = curAt[len(curAt)-tail]
	}
	if from >= to {
		return "", lossy
	}
	return strings.Join(curLines[from:to], "\n"), lossy
}

// significantLines returns the indexes of the lines that carry content. The
// blank ones are skipped when aligning two snapshots because a TUI re-lays
// them out freely between reads.
func significantLines(lines []string) []int {
	at := make([]int, 0, len(lines))
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			at = append(at, i)
		}
	}
	return at
}

// commonPrefixAt counts the leading significant lines the two snapshots share.
func commonPrefixAt(a []string, aAt []int, b []string, bAt []int) int {
	n := 0
	for n < len(aAt) && n < len(bAt) && a[aAt[n]] == b[bAt[n]] {
		n++
	}
	return n
}

// overlapAt returns the number of significant lines at the end of a that also
// open b: where a scrolled window's old content ends and its new content
// begins. Longest first, so a short accidental match never wins over the real
// boundary.
func overlapAt(a []string, aAt []int, b []string, bAt []int) int {
	longest := len(aAt)
	if len(bAt) < longest {
		longest = len(bAt)
	}
	for k := longest; k > 0; k-- {
		match := true
		for i := 0; i < k; i++ {
			if a[aAt[len(aAt)-k+i]] != b[bAt[i]] {
				match = false
				break
			}
		}
		if match {
			return k
		}
	}
	return 0
}

// herdrChunkText splits s into pieces of at most limit bytes whose
// concatenation is exactly s again. Losslessness is the whole contract: the
// chunks are handed to callers that reassemble them by writing them out back
// to back — promptChunked types them into the pane with successive
// `herdr pane send-text` calls, emitSnapshot writes them as successive
// __SINGL_OUT__ lines — so a byte this function drops is a byte the agent
// never sees. It used to split on "\n" and rejoin with "\n" inside each
// chunk, which silently ate the newline on every chunk boundary and glued
// the last line of one chunk to the first line of the next.
//
// The split prefers a line boundary (the newline stays with the chunk it
// ends, so the pieces still concatenate to s), and falls back to a byte cut
// for a single line longer than limit. That fallback is not cosmetic: the
// limit promptChunked passes exists because Linux rejects an argv entry over
// MAX_ARG_STRLEN with E2BIG, and one long line is exactly what an inlined
// minified JSON/JS context file, a base64 blob or a wide CSV row looks like —
// emitting it whole (as this used to, on the theory that the limit only ever
// guarded a *snapshot*) reintroduced the failure the chunking exists to
// avoid. A byte cut is backed off to a rune boundary, because herdr's CLI
// takes its arguments as UTF-8 and a chunk ending mid-rune is not valid UTF-8.
func herdrChunkText(s string, limit int) []string {
	if limit <= 0 || len(s) <= limit {
		return []string{s}
	}
	var chunks []string
	for len(s) > limit {
		cut := limit
		if nl := strings.LastIndexByte(s[:limit], '\n'); nl >= 0 {
			cut = nl + 1
		} else {
			cut = herdrRuneBoundary(s, cut)
		}
		chunks = append(chunks, s[:cut])
		s = s[cut:]
	}
	if s != "" {
		chunks = append(chunks, s)
	}
	return chunks
}

// herdrRuneBoundary moves cut back to the start of the rune it lands inside,
// so a hard byte split never emits half a rune. A cut that cannot be backed
// off at all (limit smaller than the rune it lands in, or bytes that are not
// valid UTF-8 to begin with) is left where it was: keeping every byte matters
// more than the encoding staying well-formed.
func herdrRuneBoundary(s string, cut int) int {
	for i := cut; i > 0 && cut-i < utf8.UTFMax; i-- {
		if utf8.RuneStart(s[i]) {
			return i
		}
	}
	return cut
}
