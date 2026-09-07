package flow

import (
	"strings"
	"testing"
)

// The prompts are golden-tested in full rather than probed for substrings:
// they are a contract with an agent, and ReviewPrompt is specifically the
// other half of ParseVerdict's. A diff here is meant to be loud.

func testFlow() *Flow {
	return &Flow{
		ID:        "f3",
		QueueID:   "flow-f3",
		Goal:      "Make internal/http retry on 429 responses, honouring Retry-After.",
		WorkDir:   "/srv/work/singularity",
		MaxRounds: 3,
	}
}

// round1 and round2 are two rejected rounds, so a round-3 fix prompt has both
// a findings list to work from and an earlier round to avoid undoing.
func round1() *Round {
	return &Round{N: 1, State: RoundRejected, Verdict: &Verdict{
		Decision: DecisionReject,
		Summary:  "No retry at all on 429.",
		Findings: []Finding{{
			Severity: SeverityBlocker,
			File:     "internal/http/retry.go",
			Line:     12,
			Detail:   "429 falls through to the generic error path.",
		}},
	}}
}

func round2() *Round {
	return &Round{N: 2, State: RoundRejected, Verdict: &Verdict{
		Decision: DecisionReject,
		Summary:  "Retry-After parsing ignores the HTTP-date form.",
		Findings: []Finding{
			{
				Severity: SeverityBlocker,
				File:     "internal/http/retry.go",
				Line:     88,
				Detail:   "strconv.Atoi on a Retry-After that may be an HTTP-date: returns 0 and retries immediately.",
			},
			{
				Severity: SeverityMinor,
				File:     "internal/http/retry_test.go",
				Detail:   "No test covers the date form.",
			},
		},
	}}
}

const verdictPath = "/var/lib/singularity/flows/f3/r2-a1-verdict.json"

func TestImplementPrompt(t *testing.T) {
	const wantImplement = "" +
		"You are implementing a change in an existing codebase.\n" +
		"\n" +
		"## Goal\n" +
		"\n" +
		"Make internal/http retry on 429 responses, honouring Retry-After.\n" +
		"\n" +
		"## Working directory\n" +
		"\n" +
		"/srv/work/singularity\n" +
		"\n" +
		"All work happens in that directory. Do not create a branch, a worktree or a\n" +
		"commit unless the goal asks for one.\n" +
		"\n" +
		"## When you are done\n" +
		"\n" +
		"Report what you changed: the files you touched and, for each, what changed and\n" +
		"why. Say plainly what you did not do, and name anything you left unfinished or\n" +
		"were unsure about.\n" +
		""
	if got := ImplementPrompt(testFlow()); got != wantImplement {
		t.Errorf("ImplementPrompt():\n%s\nwant:\n%s", got, wantImplement)
	}
}

func TestFixPromptRound3(t *testing.T) {
	const wantFix = "" +
		"You are fixing review findings on a change in an existing codebase. This is round 3.\n" +
		"\n" +
		"## Goal\n" +
		"\n" +
		"Make internal/http retry on 429 responses, honouring Retry-After.\n" +
		"\n" +
		"## Working directory\n" +
		"\n" +
		"/srv/work/singularity\n" +
		"\n" +
		"The work from the previous rounds is already in that directory. Fix it in place.\n" +
		"\n" +
		"## Earlier rounds\n" +
		"\n" +
		"- Round 1: reject, 1 finding — No retry at all on 429.\n" +
		"\n" +
		"Those findings were already addressed. Do not undo those fixes.\n" +
		"\n" +
		"## Findings to fix\n" +
		"\n" +
		"The reviewer's summary: Retry-After parsing ignores the HTTP-date form.\n" +
		"\n" +
		"1. [blocker] internal/http/retry.go:88\n" +
		"   strconv.Atoi on a Retry-After that may be an HTTP-date: returns 0 and retries immediately.\n" +
		"2. [minor] internal/http/retry_test.go\n" +
		"   No test covers the date form.\n" +
		"\n" +
		"Address every blocker and major finding. Address the minor ones too unless doing\n" +
		"so would conflict with the goal or with a fix from an earlier round — if you\n" +
		"skip one, say which and why.\n" +
		"\n" +
		"If you believe a finding is wrong, do not silently ignore it: say so in your\n" +
		"report and explain why the current code is correct.\n" +
		"\n" +
		"## When you are done\n" +
		"\n" +
		"Report what you changed: the files you touched and, for each, what changed and\n" +
		"which finding it addresses. Name any finding you did not address.\n" +
		""
	got := FixPrompt(testFlow(), []*Round{round1(), round2()})
	if got != wantFix {
		t.Errorf("FixPrompt():\n%s\nwant:\n%s", got, wantFix)
	}
}

func TestReviewPrompt(t *testing.T) {
	const wantReview = "" +
		"You are reviewing a change another agent just made in an existing codebase.\n" +
		"You are an adversarial reviewer: your job is to find what is wrong with the\n" +
		"work, not to be agreeable about it.\n" +
		"\n" +
		"## What was asked of the implementer\n" +
		"\n" +
		"Make internal/http retry on 429 responses, honouring Retry-After.\n" +
		"\n" +
		"## Working directory\n" +
		"\n" +
		"/srv/work/singularity\n" +
		"\n" +
		"The change is in that directory. Read the code as it stands now; `git diff` and\n" +
		"`git status` will show what is uncommitted.\n" +
		"\n" +
		"## Your task\n" +
		"\n" +
		"Judge whether the work does what was asked, correctly. Look for defects that\n" +
		"matter: wrong behaviour, unhandled cases, broken or missing tests, and parts of\n" +
		"the goal that were not implemented. Do not raise matters of taste as defects.\n" +
		"Do not modify the code — you review, you do not fix.\n" +
		"\n" +
		"## Output — this is mandatory\n" +
		"\n" +
		"Write your verdict as JSON to this absolute path:\n" +
		"\n" +
		"/var/lib/singularity/flows/f3/r2-a1-verdict.json\n" +
		"\n" +
		"Write it with the Bash tool:\n" +
		"\n" +
		"```\n" +
		"cat > /var/lib/singularity/flows/f3/r2-a1-verdict.json << 'VERDICT_EOF'\n" +
		"{ ... }\n" +
		"VERDICT_EOF\n" +
		"```\n" +
		"\n" +
		"The file must contain valid JSON and nothing else — no markdown fences, no\n" +
		"commentary before or after it. The schema:\n" +
		"\n" +
		"```json\n" +
		"{\n" +
		"  \"verdict\": \"reject\",\n" +
		"  \"summary\": \"Retry-After parsing ignores the HTTP-date form.\",\n" +
		"  \"findings\": [\n" +
		"    {\"severity\": \"blocker\", \"file\": \"internal/http/retry.go\", \"line\": 88,\n" +
		"     \"detail\": \"strconv.Atoi on a Retry-After that may be an HTTP-date: returns 0 and retries immediately.\"},\n" +
		"    {\"severity\": \"minor\", \"file\": \"internal/http/retry_test.go\",\n" +
		"     \"detail\": \"No test covers the date form.\"}\n" +
		"  ]\n" +
		"}\n" +
		"```\n" +
		"\n" +
		"Rules for the fields:\n" +
		"\n" +
		"- `verdict` is required and must be exactly \"accept\" or \"reject\". No other word is accepted.\n" +
		"- `summary` is one sentence saying why.\n" +
		"- `severity` is required on every finding and must be one of \"blocker\", \"major\" or \"minor\".\n" +
		"- `file` is a repository-relative path. `line` is 1-based and may be omitted when\n" +
		"  the finding is about a file as a whole or about the change in general.\n" +
		"- `detail` says what is wrong and, where it is not obvious, what would be right.\n" +
		"- A \"reject\" verdict MUST carry at least one finding. A rejection with an empty\n" +
		"  `findings` list gives the next round nothing to work from and is discarded.\n" +
		"- Do not accept while raising a blocker or major finding: that contradicts itself and is read\n" +
		"  as a reject. Accept with no findings, or with minor findings only.\n" +
		"\n" +
		"Writing this file is the only way your decision is recorded. If it is missing or\n" +
		"malformed, your review does not count.\n" +
		""
	got := ReviewPrompt(testFlow(), verdictPath)
	if got != wantReview {
		t.Errorf("ReviewPrompt():\n%s\nwant:\n%s", got, wantReview)
	}
	if !strings.Contains(got, verdictPath) {
		t.Errorf("ReviewPrompt() does not name the verdict path %q", verdictPath)
	}
	if strings.Contains(got, "Extra review focus") {
		t.Error("ReviewPrompt() emitted a focus section for an empty ReviewGoal")
	}
}

func TestReviewPromptWithReviewGoal(t *testing.T) {
	const wantReviewFocus = "" +
		"You are reviewing a change another agent just made in an existing codebase.\n" +
		"You are an adversarial reviewer: your job is to find what is wrong with the\n" +
		"work, not to be agreeable about it.\n" +
		"\n" +
		"## What was asked of the implementer\n" +
		"\n" +
		"Make internal/http retry on 429 responses, honouring Retry-After.\n" +
		"\n" +
		"## Working directory\n" +
		"\n" +
		"/srv/work/singularity\n" +
		"\n" +
		"The change is in that directory. Read the code as it stands now; `git diff` and\n" +
		"`git status` will show what is uncommitted.\n" +
		"\n" +
		"## Extra review focus\n" +
		"\n" +
		"Pay particular attention to the clock handling in the date branch.\n" +
		"\n" +
		"Weigh this alongside the goal above; it narrows your attention, it does not\n" +
		"replace the rest of the review.\n" +
		"\n" +
		"## Your task\n" +
		"\n" +
		"Judge whether the work does what was asked, correctly. Look for defects that\n" +
		"matter: wrong behaviour, unhandled cases, broken or missing tests, and parts of\n" +
		"the goal that were not implemented. Do not raise matters of taste as defects.\n" +
		"Do not modify the code — you review, you do not fix.\n" +
		"\n" +
		"## Output — this is mandatory\n" +
		"\n" +
		"Write your verdict as JSON to this absolute path:\n" +
		"\n" +
		"/var/lib/singularity/flows/f3/r2-a1-verdict.json\n" +
		"\n" +
		"Write it with the Bash tool:\n" +
		"\n" +
		"```\n" +
		"cat > /var/lib/singularity/flows/f3/r2-a1-verdict.json << 'VERDICT_EOF'\n" +
		"{ ... }\n" +
		"VERDICT_EOF\n" +
		"```\n" +
		"\n" +
		"The file must contain valid JSON and nothing else — no markdown fences, no\n" +
		"commentary before or after it. The schema:\n" +
		"\n" +
		"```json\n" +
		"{\n" +
		"  \"verdict\": \"reject\",\n" +
		"  \"summary\": \"Retry-After parsing ignores the HTTP-date form.\",\n" +
		"  \"findings\": [\n" +
		"    {\"severity\": \"blocker\", \"file\": \"internal/http/retry.go\", \"line\": 88,\n" +
		"     \"detail\": \"strconv.Atoi on a Retry-After that may be an HTTP-date: returns 0 and retries immediately.\"},\n" +
		"    {\"severity\": \"minor\", \"file\": \"internal/http/retry_test.go\",\n" +
		"     \"detail\": \"No test covers the date form.\"}\n" +
		"  ]\n" +
		"}\n" +
		"```\n" +
		"\n" +
		"Rules for the fields:\n" +
		"\n" +
		"- `verdict` is required and must be exactly \"accept\" or \"reject\". No other word is accepted.\n" +
		"- `summary` is one sentence saying why.\n" +
		"- `severity` is required on every finding and must be one of \"blocker\", \"major\" or \"minor\".\n" +
		"- `file` is a repository-relative path. `line` is 1-based and may be omitted when\n" +
		"  the finding is about a file as a whole or about the change in general.\n" +
		"- `detail` says what is wrong and, where it is not obvious, what would be right.\n" +
		"- A \"reject\" verdict MUST carry at least one finding. A rejection with an empty\n" +
		"  `findings` list gives the next round nothing to work from and is discarded.\n" +
		"- Do not accept while raising a blocker or major finding: that contradicts itself and is read\n" +
		"  as a reject. Accept with no findings, or with minor findings only.\n" +
		"\n" +
		"Writing this file is the only way your decision is recorded. If it is missing or\n" +
		"malformed, your review does not count.\n" +
		""
	f := testFlow()
	f.ReviewGoal = "Pay particular attention to the clock handling in the date branch."
	got := ReviewPrompt(f, verdictPath)
	if got != wantReviewFocus {
		t.Errorf("ReviewPrompt() with ReviewGoal:\n%s\nwant:\n%s", got, wantReviewFocus)
	}
	if !strings.Contains(got, verdictPath) {
		t.Errorf("ReviewPrompt() does not name the verdict path %q", verdictPath)
	}
}

// TestReviewPromptAcceptsItsOwnExample guards the one coupling this file exists for:
// the schema the reviewer is handed must be a document ParseVerdict accepts.
func TestReviewPromptAcceptsItsOwnExample(t *testing.T) {
	prompt := ReviewPrompt(testFlow(), verdictPath)
	start := strings.Index(prompt, "```json\n")
	if start < 0 {
		t.Fatal("ReviewPrompt() carries no json example")
	}
	body := prompt[start+len("```json\n"):]
	end := strings.Index(body, "```")
	if end < 0 {
		t.Fatal("unterminated json example")
	}
	v, err := ParseVerdict([]byte(body[:end]))
	if err != nil {
		t.Fatalf("ParseVerdict rejects the example the prompt hands the reviewer: %v", err)
	}
	if v.Decision != DecisionReject || len(v.Findings) != 2 {
		t.Errorf("parsed example = %+v, want a reject with 2 findings", v)
	}
}
