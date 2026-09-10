package flow

import (
	"fmt"
	"strings"
)

// This file composes the three prompts a flow's steps run under. The builders
// are pure — flow values in, string out, no filesystem and no daemon — so the
// exact text is assertable in a test, which matters more here than anywhere
// else in the package: ReviewPrompt is the other half of ParseVerdict's
// contract, and a drift between the two is a review attempt burned per round.
//
// The idiom (a strings.Builder, sectioned with markdown headings) is
// internal/jira/prompts.go's, deliberately.

// ImplementPrompt is round 1's work task: do the flow's Goal, verbatim, in the
// flow's WorkDir. Nothing about review is mentioned — an implementer told it
// will be graded writes for the grader.
func ImplementPrompt(f *Flow) string {
	var b strings.Builder

	b.WriteString("You are implementing a change in an existing codebase.\n\n")
	writeGoal(&b, f.Goal)
	fmt.Fprintf(&b, "## Working directory\n\n%s\n\n", f.WorkDir)
	b.WriteString("All work happens in that directory. Do not create a branch, a worktree or a\n")
	b.WriteString("commit unless the goal asks for one.\n\n")

	b.WriteString("## When you are done\n\n")
	b.WriteString("Report what you changed: the files you touched and, for each, what changed and\n")
	b.WriteString("why. Say plainly what you did not do, and name anything you left unfinished or\n")
	b.WriteString("were unsure about.\n")

	return b.String()
}

// FixPrompt is round N>1's work task. prior holds every settled round in
// order, so prior[len(prior)-1] is the round that was just rejected and whose
// findings are the work; the earlier ones contribute a one-line summary each,
// which is what stops a fixer from undoing a fix made two rounds ago.
//
// Reviewer transcripts are deliberately absent: a verdict is bounded, a
// transcript is not, and a fresh fixer inherits nothing else either.
func FixPrompt(f *Flow, prior []*Round) string {
	var b strings.Builder

	round := len(prior) + 1
	fmt.Fprintf(&b, "You are fixing review findings on a change in an existing codebase. This is round %d.\n\n", round)
	writeGoal(&b, f.Goal)
	fmt.Fprintf(&b, "## Working directory\n\n%s\n\n", f.WorkDir)
	b.WriteString("The work from the previous rounds is already in that directory. Fix it in place.\n\n")

	if len(prior) > 1 {
		b.WriteString("## Earlier rounds\n\n")
		for _, r := range prior[:len(prior)-1] {
			fmt.Fprintf(&b, "- %s\n", roundSummaryLine(r))
		}
		b.WriteString("\nThose findings were already addressed. Do not undo those fixes.\n\n")
	}

	b.WriteString("## Findings to fix\n\n")
	var last *Round
	if len(prior) > 0 {
		last = prior[len(prior)-1]
	}
	if last != nil && last.Verdict != nil && last.Verdict.Summary != "" {
		fmt.Fprintf(&b, "The reviewer's summary: %s\n\n", last.Verdict.Summary)
	}
	if last == nil || last.Verdict == nil || len(last.Verdict.Findings) == 0 {
		b.WriteString("(the reviewer recorded no specific findings)\n\n")
	} else {
		for i, fd := range last.Verdict.Findings {
			fmt.Fprintf(&b, "%d. [%s] %s\n   %s\n", i+1, fd.Severity, findingLocation(fd), fd.Detail)
		}
		b.WriteString("\n")
	}

	b.WriteString("Address every blocker and major finding. Address the minor ones too unless doing\n")
	b.WriteString("so would conflict with the goal or with a fix from an earlier round — if you\n")
	b.WriteString("skip one, say which and why.\n\n")
	b.WriteString("If you believe a finding is wrong, do not silently ignore it: say so in your\n")
	b.WriteString("report and explain why the current code is correct.\n\n")

	b.WriteString("## When you are done\n\n")
	b.WriteString("Report what you changed: the files you touched and, for each, what changed and\n")
	b.WriteString("which finding it addresses. Name any finding you did not address.\n")

	return b.String()
}

// ReviewPrompt is a round's review task. verdictPath is the absolute path in
// the daemon state directory — never the work tree, which an agent may be
// told to commit — that the daemon will read the verdict back from.
//
// The schema spelled out below is exactly what ParseVerdict accepts; change
// one and change the other.
func ReviewPrompt(f *Flow, verdictPath string) string {
	var b strings.Builder

	b.WriteString("You are reviewing a change another agent just made in an existing codebase.\n")
	b.WriteString("You are an adversarial reviewer: your job is to find what is wrong with the\n")
	b.WriteString("work, not to be agreeable about it.\n\n")

	b.WriteString("## What was asked of the implementer\n\n")
	b.WriteString(strings.TrimSpace(f.Goal))
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "## Working directory\n\n%s\n\n", f.WorkDir)
	b.WriteString("The change is in that directory. Read the code as it stands now; `git diff` and\n")
	b.WriteString("`git status` will show what is uncommitted.\n\n")

	if rg := strings.TrimSpace(f.ReviewGoal); rg != "" {
		b.WriteString("## Extra review focus\n\n")
		b.WriteString(rg)
		b.WriteString("\n\nWeigh this alongside the goal above; it narrows your attention, it does not\n")
		b.WriteString("replace the rest of the review.\n\n")
	}

	b.WriteString("## Your task\n\n")
	b.WriteString("Judge whether the work does what was asked, correctly. Look for defects that\n")
	b.WriteString("matter: wrong behaviour, unhandled cases, broken or missing tests, and parts of\n")
	b.WriteString("the goal that were not implemented. Do not raise matters of taste as defects.\n")
	b.WriteString("Do not modify the code — you review, you do not fix.\n\n")

	b.WriteString("## Output — this is mandatory\n\n")
	fmt.Fprintf(&b, "Write your verdict as JSON to this absolute path:\n\n%s\n\n", verdictPath)
	b.WriteString("Write it with the Bash tool:\n\n")
	fmt.Fprintf(&b, "```\ncat > %s << 'VERDICT_EOF'\n{ ... }\nVERDICT_EOF\n```\n\n", verdictPath)
	b.WriteString("The file must contain valid JSON and nothing else — no markdown fences, no\n")
	b.WriteString("commentary before or after it. The schema:\n\n")
	b.WriteString("```json\n")
	b.WriteString("{\n")
	b.WriteString("  \"verdict\": \"reject\",\n")
	b.WriteString("  \"summary\": \"Retry-After parsing ignores the HTTP-date form.\",\n")
	b.WriteString("  \"findings\": [\n")
	b.WriteString("    {\"severity\": \"blocker\", \"file\": \"internal/http/retry.go\", \"line\": 88,\n")
	b.WriteString("     \"detail\": \"strconv.Atoi on a Retry-After that may be an HTTP-date: returns 0 and retries immediately.\"},\n")
	b.WriteString("    {\"severity\": \"minor\", \"file\": \"internal/http/retry_test.go\",\n")
	b.WriteString("     \"detail\": \"No test covers the date form.\"}\n")
	b.WriteString("  ]\n")
	b.WriteString("}\n")
	b.WriteString("```\n\n")

	b.WriteString("Rules for the fields:\n\n")
	fmt.Fprintf(&b, "- `verdict` is required and must be exactly %q or %q. No other word is accepted.\n",
		DecisionAccept, DecisionReject)
	b.WriteString("- `summary` is one sentence saying why.\n")
	fmt.Fprintf(&b, "- `severity` is required on every finding and must be one of %q, %q or %q.\n",
		SeverityBlocker, SeverityMajor, SeverityMinor)
	b.WriteString("- `file` is a repository-relative path. `line` is 1-based and may be omitted when\n")
	b.WriteString("  the finding is about a file as a whole or about the change in general.\n")
	b.WriteString("- `detail` says what is wrong and, where it is not obvious, what would be right.\n")
	fmt.Fprintf(&b, "- A %q verdict MUST carry at least one finding. A rejection with an empty\n", DecisionReject)
	b.WriteString("  `findings` list gives the next round nothing to work from and is discarded.\n")
	fmt.Fprintf(&b, "- Do not %s while raising a %s or %s finding: that contradicts itself and is read\n",
		DecisionAccept, SeverityBlocker, SeverityMajor)
	fmt.Fprintf(&b, "  as a %s. Accept with no findings, or with %s findings only.\n",
		DecisionReject, SeverityMinor)
	b.WriteString("\nWriting this file is the only way your decision is recorded. If it is missing or\n")
	b.WriteString("malformed, your review does not count.\n")

	return b.String()
}

// CommitPrompt is the one-off task submitted the moment a round's verdict is
// accept: the implement/fix prompts above tell every work step not to commit
// (so a reviewer always reads a clean uncommitted diff), which means nothing
// ever commits the work the flow just spent rounds getting right. This is
// that missing step, run once, after acceptance.
func CommitPrompt(f *Flow) string {
	var b strings.Builder

	b.WriteString("A change in this repository was just reviewed and accepted. Your job is only\n")
	b.WriteString("to commit it — do not modify the code.\n\n")
	writeGoal(&b, f.Goal)
	fmt.Fprintf(&b, "## Working directory\n\n%s\n\n", f.WorkDir)

	b.WriteString("## Your task\n\n")
	b.WriteString("Run `git status` and `git diff` to see what changed. Stage everything relevant\n")
	b.WriteString("to the goal above and commit it with a message that describes what changed and\n")
	b.WriteString("why, in your own words — do not just repeat the goal verbatim. Do not push, and\n")
	b.WriteString("do not create or switch branches.\n\n")
	b.WriteString("If the working tree is already clean — nothing to commit — say so and stop;\n")
	b.WriteString("that is not an error.\n\n")

	b.WriteString("## When you are done\n\n")
	b.WriteString("Report the commit you made (or that there was nothing to commit).\n")

	return b.String()
}

// writeGoal renders the flow's Goal verbatim under its own heading — verbatim
// because every fixer must see the same words the implementer did, and a
// paraphrase drifts a little further each round.
func writeGoal(b *strings.Builder, goal string) {
	b.WriteString("## Goal\n\n")
	b.WriteString(strings.TrimSpace(goal))
	b.WriteString("\n\n")
}

// findingLocation renders a finding's file:line, or a placeholder when the
// reviewer scoped it to the change rather than a place in it.
func findingLocation(f Finding) string {
	switch {
	case f.File != "" && f.Line > 0:
		return fmt.Sprintf("%s:%d", f.File, f.Line)
	case f.File != "":
		return f.File
	default:
		return "(no file)"
	}
}

// roundSummaryLine is one earlier round in one line: enough for the fixer to
// know what was already dealt with, not enough to re-litigate it.
func roundSummaryLine(r *Round) string {
	if r.Verdict == nil {
		return fmt.Sprintf("Round %d: %s, no verdict recorded", r.N, r.State)
	}
	n := len(r.Verdict.Findings)
	noun := "findings"
	if n == 1 {
		noun = "finding"
	}
	line := fmt.Sprintf("Round %d: %s, %d %s", r.N, r.Verdict.Decision, n, noun)
	if s := strings.TrimSpace(r.Verdict.Summary); s != "" {
		line += " — " + s
	}
	return line
}
