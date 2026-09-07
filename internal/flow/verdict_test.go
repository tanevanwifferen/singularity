package flow

import (
	"errors"
	"reflect"
	"testing"
)

// TestParseVerdict covers every rule in §3.2 of the design. The cases that
// matter most are the two where a well-formed document is still not taken at
// face value: reject-with-no-findings, which a fix step cannot act on, and
// accept-carrying-a-blocker, which contradicts itself and resolves against the
// work.
func TestParseVerdict(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  *Verdict // nil means "expect an error"
	}{
		{
			name:  "accept with no findings",
			input: `{"verdict":"accept","summary":"Looks right."}`,
			want:  &Verdict{Decision: DecisionAccept, Summary: "Looks right."},
		},
		{
			name: "reject with findings",
			input: `{"verdict":"reject","summary":"Retry-After parsing ignores the HTTP-date form.",
			  "findings":[
			    {"severity":"blocker","file":"internal/http/retry.go","line":88,"detail":"strconv.Atoi on an HTTP-date returns 0."},
			    {"severity":"minor","file":"internal/http/retry_test.go","detail":"No test covers the date form."}]}`,
			want: &Verdict{
				Decision: DecisionReject,
				Summary:  "Retry-After parsing ignores the HTTP-date form.",
				Findings: []Finding{
					{Severity: SeverityBlocker, File: "internal/http/retry.go", Line: 88, Detail: "strconv.Atoi on an HTTP-date returns 0."},
					{Severity: SeverityMinor, File: "internal/http/retry_test.go", Detail: "No test covers the date form."},
				},
			},
		},
		{
			name:  "accept contradicted by a blocker becomes reject",
			input: `{"verdict":"accept","findings":[{"severity":"blocker","detail":"nil deref"}]}`,
			want: &Verdict{
				Decision: DecisionReject,
				Findings: []Finding{{Severity: SeverityBlocker, Detail: "nil deref"}},
			},
		},
		{
			name:  "accept contradicted by a major becomes reject",
			input: `{"verdict":"accept","findings":[{"severity":"major","detail":"unbounded retry"}]}`,
			want: &Verdict{
				Decision: DecisionReject,
				Findings: []Finding{{Severity: SeverityMajor, Detail: "unbounded retry"}},
			},
		},
		{
			// A minor is advisory: it does not overturn an accept, or no
			// reviewer could ever pass work while noting a nit.
			name:  "accept with only a minor stays accept",
			input: `{"verdict":"accept","findings":[{"severity":"minor","detail":"stray comment"}]}`,
			want: &Verdict{
				Decision: DecisionAccept,
				Findings: []Finding{{Severity: SeverityMinor, Detail: "stray comment"}},
			},
		},
		{
			name:  "reject with zero findings is unparseable",
			input: `{"verdict":"reject","summary":"not good enough"}`,
			want:  nil,
		},
		{
			name:  "reject with an empty findings array is unparseable",
			input: `{"verdict":"reject","findings":[]}`,
			want:  nil,
		},
		{
			name:  "unknown severity normalises to major",
			input: `{"verdict":"reject","findings":[{"severity":"catastrophic","detail":"boom"},{"severity":"","detail":"unsaid"}]}`,
			want: &Verdict{
				Decision: DecisionReject,
				Findings: []Finding{
					{Severity: SeverityMajor, Detail: "boom"},
					{Severity: SeverityMajor, Detail: "unsaid"},
				},
			},
		},
		{
			// Normalised to major, which is blocking, so this also
			// contradicts the accept.
			name:  "unknown severity is blocking enough to overturn an accept",
			input: `{"verdict":"accept","findings":[{"severity":"nit-ish?","detail":"unclear"}]}`,
			want: &Verdict{
				Decision: DecisionReject,
				Findings: []Finding{{Severity: SeverityMajor, Detail: "unclear"}},
			},
		},
		{
			name: "unknown top-level and per-finding keys are tolerated",
			input: `{"verdict":"accept","summary":"fine","confidence":0.9,
			  "notes":["I also read the tests"],
			  "findings":[{"severity":"minor","detail":"nit","suggested_fix":"rename it"}]}`,
			want: &Verdict{
				Decision: DecisionAccept,
				Summary:  "fine",
				Findings: []Finding{{Severity: SeverityMinor, Detail: "nit"}},
			},
		},
		{
			name:  "verdict word is trimmed and lowercased",
			input: `{"verdict":"  ReJeCt\n","findings":[{"severity":"BLOCKER","detail":"race"}]}`,
			want: &Verdict{
				Decision: DecisionReject,
				Findings: []Finding{{Severity: SeverityBlocker, Detail: "race"}},
			},
		},
		{
			name:  "an unrecognised verdict word is unparseable",
			input: `{"verdict":"LGTM","summary":"ship it"}`,
			want:  nil,
		},
		{
			name:  "a missing verdict key is unparseable",
			input: `{"summary":"I reviewed it"}`,
			want:  nil,
		},
		{
			name:  "malformed JSON is unparseable",
			input: `{"verdict":"accept", "summary": }`,
			want:  nil,
		},
		{
			name:  "prose instead of JSON is unparseable",
			input: "The code looks good to me!",
			want:  nil,
		},
		{
			name:  "empty input is unparseable",
			input: "",
			want:  nil,
		},
		{
			name:  "whitespace-only input is unparseable",
			input: "   \n\t ",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseVerdict([]byte(tt.input))
			if tt.want == nil {
				if err == nil {
					t.Fatalf("ParseVerdict() = %+v, want an error", got)
				}
				if got != nil {
					t.Errorf("ParseVerdict() returned %+v alongside its error; callers must not see a half-verdict", got)
				}
				if !errors.Is(err, ErrUnparseable) {
					t.Errorf("error %v does not wrap ErrUnparseable", err)
				}
				// The message is embedded in a synthetic finding a human
				// reads, so it has to say more than the sentinel does.
				if len(err.Error()) <= len(ErrUnparseable.Error())+2 {
					t.Errorf("error %q is not descriptive", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseVerdict() error = %v, want a verdict", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseVerdict() =\n  %+v\nwant\n  %+v", got, tt.want)
			}
		})
	}
}

func TestVerdictBlockers(t *testing.T) {
	v := &Verdict{Findings: []Finding{
		{Severity: SeverityMinor, Detail: "nit"},
		{Severity: SeverityBlocker, Detail: "crash"},
		{Severity: SeverityMajor, Detail: "leak"},
	}}
	got := v.Blockers()
	if len(got) != 2 || got[0].Detail != "crash" || got[1].Detail != "leak" {
		t.Errorf("Blockers() = %+v, want the blocker and the major in order", got)
	}
	if b := (&Verdict{}).Blockers(); b != nil {
		t.Errorf("Blockers() on an empty verdict = %+v, want nil", b)
	}
}

func TestStatePredicates(t *testing.T) {
	terminal := map[State]bool{
		StatePending: false, StateRunning: false,
		StateAccepted: true, StateRejected: true,
		StateErrored: true, StateCancelled: true,
	}
	for s, want := range terminal {
		if !s.Valid() {
			t.Errorf("State(%q).Valid() = false, want true", s)
		}
		if got := s.Terminal(); got != want {
			t.Errorf("State(%q).Terminal() = %v, want %v", s, got, want)
		}
	}
	for _, s := range []State{"", "done", "blocked", "Accepted"} {
		if s.Valid() {
			t.Errorf("State(%q).Valid() = true, want false", s)
		}
		if s.Terminal() {
			t.Errorf("State(%q).Terminal() = true, want false", s)
		}
	}
}

func TestRoundStatePredicates(t *testing.T) {
	terminal := map[RoundState]bool{
		RoundRunning: false, RoundAccepted: true,
		RoundRejected: true, RoundErrored: true,
	}
	for s, want := range terminal {
		if !s.Valid() {
			t.Errorf("RoundState(%q).Valid() = false, want true", s)
		}
		if got := s.Terminal(); got != want {
			t.Errorf("RoundState(%q).Terminal() = %v, want %v", s, got, want)
		}
	}
	for _, s := range []RoundState{"", "pending", "cancelled"} {
		if s.Valid() {
			t.Errorf("RoundState(%q).Valid() = true, want false", s)
		}
		if s.Terminal() {
			t.Errorf("RoundState(%q).Terminal() = true, want false", s)
		}
	}
}

// A flow's options are queue options, so a queue-shaped decision like
// RouteEnabled must hold for a flow too rather than being reimplemented.
func TestTaskOptionsIsQueueType(t *testing.T) {
	var o TaskOptions
	if !o.RouteEnabled() {
		t.Error("zero TaskOptions should route")
	}
	o.Effort = "medium"
	if o.RouteEnabled() {
		t.Error("pinned effort should suppress routing")
	}
}

func TestFlowCloneIsDeep(t *testing.T) {
	on := true
	f := &Flow{
		ID:      "f1",
		QueueID: "flow-f1",
		Opts:    TaskOptions{ContextFiles: []string{"a.go"}, SmartRoute: &on},
		Rounds: []*Round{{
			N:       1,
			State:   RoundRejected,
			Verdict: &Verdict{Decision: DecisionReject, Findings: []Finding{{Severity: SeverityBlocker, Detail: "boom"}}},
		}},
	}
	c := f.Clone()
	c.Opts.ContextFiles[0] = "b.go"
	*c.Opts.SmartRoute = false
	c.Rounds[0].Verdict.Findings[0].Detail = "changed"
	c.Rounds[0].State = RoundAccepted

	if f.Opts.ContextFiles[0] != "a.go" {
		t.Error("Clone shares ContextFiles backing array")
	}
	if !*f.Opts.SmartRoute {
		t.Error("Clone shares the SmartRoute pointer")
	}
	if f.Rounds[0].Verdict.Findings[0].Detail != "boom" {
		t.Error("Clone shares verdict findings")
	}
	if f.Rounds[0].State != RoundRejected {
		t.Error("Clone shares round pointers")
	}
	if f.CurrentRound() != f.Rounds[0] {
		t.Error("CurrentRound should return the last round")
	}
	if (&Flow{}).CurrentRound() != nil {
		t.Error("CurrentRound on a flow with no rounds should be nil")
	}
}
