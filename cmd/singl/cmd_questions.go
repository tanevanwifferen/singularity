package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// cmdQuestions handles "singl questions": the operator's side of the
// ask_questions tool agents use to stop and ask.
func cmdQuestions(ctx context.Context, verb string, args []string) int {
	switch verb {
	case "list":
		return runQuestionsList(ctx, args)
	case "answer":
		return runQuestionsAnswer(ctx, args)
	default:
		return nounHelp("questions", verb)
	}
}

func runQuestionsList(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("questions-list", flag.ContinueOnError)
	agentID := fs.String("agent", "", "only this agent's questions")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	all, err := c.QuestionList(tctx)
	if err != nil {
		return die(err)
	}
	questions := []api.Question{}
	for _, q := range all {
		if *agentID == "" || q.AgentID == *agentID {
			questions = append(questions, q)
		}
	}
	if globals.json {
		return printJSON(questions)
	}
	return renderMarkdown(fmtQuestions(questions, time.Now()))
}

// fmtQuestions renders the question list grouped by agent, open questions
// with the command that answers them.
func fmtQuestions(questions []api.Question, now time.Time) string {
	open := 0
	for _, q := range questions {
		if !q.Answered {
			open++
		}
	}
	md := fmt.Sprintf("## Questions (%d open)\n\n", open)
	if len(questions) == 0 {
		return md + "_No agent is waiting for an answer._\n"
	}
	lastAgent := ""
	for _, q := range questions {
		if q.AgentID != lastAgent {
			md += fmt.Sprintf("### Agent `%s`\n\n", q.AgentID)
			lastAgent = q.AgentID
		}
		if q.Answered {
			md += fmt.Sprintf("- `%s` ✓ %s\n  answer: %s\n", q.ID, q.Text, q.Answer)
			continue
		}
		md += fmt.Sprintf("- `%s` %s  (asked %s ago)\n  `singl questions answer --id %s --message \"...\"`\n",
			q.ID, q.Text, fmtWaitDur(now.Sub(q.AskedAt)), q.ID)
	}
	return md + "\nAn agent continues once every one of its questions is answered.\n"
}

func runQuestionsAnswer(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("questions-answer", flag.ContinueOnError)
	id := fs.String("id", "", "question ID (required)")
	message := fs.String("message", "", "the answer (required)")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	if *id == "" || *message == "" {
		fmt.Fprintln(os.Stderr, "error: --id and --message are required")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	q, err := c.QuestionAnswer(tctx, *id, *message)
	if err != nil {
		return die(err)
	}
	// How many of the same agent's questions are still open decides whether
	// it is moving again; a failed lookup only costs that detail.
	remaining := -1
	if all, lerr := c.QuestionList(tctx); lerr == nil {
		remaining = 0
		for _, other := range all {
			if other.AgentID == q.AgentID && !other.Answered {
				remaining++
			}
		}
	}
	if globals.json {
		out := map[string]any{"status": "answered", "question": q}
		if remaining >= 0 {
			out["agent_open_questions"] = remaining
		}
		return printJSON(out)
	}
	md := fmt.Sprintf("Answered `%s`.", q.ID)
	switch {
	case remaining == 0:
		md += fmt.Sprintf(" Agent `%s` has no open questions left and continues.", q.AgentID)
	case remaining > 0:
		md += fmt.Sprintf(" Agent `%s` still waits on %d more.", q.AgentID, remaining)
	}
	return renderMarkdown(md + "\n")
}
