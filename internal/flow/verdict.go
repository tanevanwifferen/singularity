package flow

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrUnparseable marks every failure ParseVerdict can return. The caller does
// not act on the individual reasons — an unparseable verdict, a missing file
// and a rejection with nothing to fix are all the same event to a flow (§3.3):
// record a synthetic reject, re-review once, then error. The distinctions
// exist for the message a human reads.
var ErrUnparseable = errors.New("unparseable verdict")

// ParseVerdict reads a reviewer's verdict document.
//
// It is deliberately pure — bytes in, verdict out, no filesystem and no daemon
// — so every rule below is testable on its own. The caller supplies the bytes
// and, on error, embeds the returned message in the synthetic finding that
// stands in for the review the reviewer failed to deliver.
//
// The rules, all of which resolve ambiguity against the work rather than for
// it:
//
//   - Unknown top-level keys are tolerated. A model will add commentary next
//     to the fields it was asked for, and throwing away an otherwise valid
//     verdict over that would burn a review attempt.
//   - "verdict" must be exactly accept or reject after trimming and
//     lowercasing. Anything else, including a missing key, is unparseable:
//     guessing at "approved" or "LGTM" is the fail-open this design refuses.
//   - Unknown severity values normalise to major, so an invented grade is
//     treated as serious rather than silently discarded.
//   - reject with zero findings is unparseable, because a rejection that gives
//     the fix step nothing to work from is not a usable verdict.
//   - accept carrying a blocker or major finding is read as reject: a
//     self-contradicting verdict resolves against the work and never for it.
func ParseVerdict(data []byte) (*Verdict, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, fmt.Errorf("%w: empty document", ErrUnparseable)
	}

	// A plain Unmarshal into a struct ignores unknown keys, which is exactly
	// the tolerance rule; DisallowUnknownFields would invert it.
	var raw struct {
		Verdict  *string `json:"verdict"`
		Summary  string  `json:"summary"`
		Findings []struct {
			Severity string `json:"severity"`
			File     string `json:"file"`
			Line     int    `json:"line"`
			Detail   string `json:"detail"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON: %v", ErrUnparseable, err)
	}
	if raw.Verdict == nil {
		return nil, fmt.Errorf(`%w: no "verdict" field`, ErrUnparseable)
	}

	decision := Decision(strings.ToLower(strings.TrimSpace(*raw.Verdict)))
	if !decision.Valid() {
		return nil, fmt.Errorf("%w: verdict must be %q or %q, got %q",
			ErrUnparseable, DecisionAccept, DecisionReject, *raw.Verdict)
	}

	out := &Verdict{Decision: decision, Summary: strings.TrimSpace(raw.Summary)}
	for _, f := range raw.Findings {
		out.Findings = append(out.Findings, Finding{
			Severity: normaliseSeverity(f.Severity),
			File:     strings.TrimSpace(f.File),
			Line:     f.Line,
			Detail:   strings.TrimSpace(f.Detail),
		})
	}

	if out.Decision == DecisionReject && len(out.Findings) == 0 {
		return nil, fmt.Errorf("%w: reject with no findings leaves the fix step nothing to work from", ErrUnparseable)
	}
	if out.Decision == DecisionAccept && len(out.Blockers()) > 0 {
		out.Decision = DecisionReject
	}
	return out, nil
}

// normaliseSeverity maps a reviewer's severity word onto the three grades the
// flow understands, defaulting to major — see Severity's doc comment for why
// unknown means serious.
func normaliseSeverity(s string) Severity {
	sev := Severity(strings.ToLower(strings.TrimSpace(s)))
	if sev.Valid() {
		return sev
	}
	return SeverityMajor
}
