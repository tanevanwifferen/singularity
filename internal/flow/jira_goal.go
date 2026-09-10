package flow

import (
	"fmt"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/jira"
)

// GoalFromIssue turns a fetched Jira issue into a flow's title and goal, so
// `flow start --jira PROJ-123` needs no separate --prompt: the issue's
// summary and description (which is where acceptance criteria typically
// live, Jira having no dedicated field for them — see
// internal/jira/prompts.go's buildRefinePrompt, which treats them the same
// way) become the implementer's task, verbatim, the way ImplementPrompt
// requires every fixer to see the same words.
//
// title is empty when the issue carries neither a key nor a summary; goal is
// empty only when the issue is entirely blank. Callers keep an explicit
// --title/--prompt by only falling back to these when their own flag is
// unset.
func GoalFromIssue(issue *jira.Issue) (title, goal string) {
	if issue == nil {
		return "", ""
	}
	key := strings.TrimSpace(issue.Key)
	summary := strings.TrimSpace(issue.Summary)

	switch {
	case key != "" && summary != "":
		title = fmt.Sprintf("%s: %s", key, summary)
	case summary != "":
		title = summary
	default:
		title = key
	}

	var b strings.Builder
	if key != "" {
		fmt.Fprintf(&b, "Jira issue: %s\n", key)
	}
	if summary != "" {
		fmt.Fprintf(&b, "Summary: %s\n", summary)
	}
	if desc := strings.TrimSpace(issue.Description); desc != "" {
		b.WriteString("\n")
		b.WriteString(desc)
		b.WriteString("\n")
	}
	goal = strings.TrimSpace(b.String())
	return title, goal
}
