package jira

import (
	"fmt"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
)

// RefineTicket launches an agent that explores the codebase and rewrites a Jira ticket
// to be implementation-ready. The agent writes its proposed changes to .jira-actions.json
// in the working directory (i.e. filepath.Join(repoPath, ".jira-actions.json")).
func RefineTicket(eng *engine.Engine, issue *Issue, repoPath string) (string, error) {
	prompt := buildRefinePrompt(issue, repoPath)
	return eng.StartAgent(repoPath, prompt, engine.AgentOptions{
		Model:        "sonnet",
		MaxTurns:     15,
		AllowedTools: []string{"Read", "Grep", "Glob", "Bash"},
		Summary:      fmt.Sprintf("Refine: %s", issue.Key),
	})
}

// buildRefinePrompt constructs the system prompt for the refine-ticket agent.
func buildRefinePrompt(issue *Issue, repoPath string) string {
	var b strings.Builder

	b.WriteString("You are analyzing a Jira ticket to make it implementation-ready.\n\n")

	b.WriteString("## Ticket\n\n")
	fmt.Fprintf(&b, "Key:         %s\n", issue.Key)
	fmt.Fprintf(&b, "Summary:     %s\n", issue.Summary)
	fmt.Fprintf(&b, "Type:        %s\n", issue.Type)
	fmt.Fprintf(&b, "Priority:    %s\n", issue.Priority)
	fmt.Fprintf(&b, "Status:      %s\n", issue.Status)
	if len(issue.Labels) > 0 {
		fmt.Fprintf(&b, "Labels:      %s\n", strings.Join(issue.Labels, ", "))
	}
	b.WriteString("\n### Description\n\n")
	if issue.Description != "" {
		b.WriteString(issue.Description)
	} else {
		b.WriteString("(no description provided)")
	}
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "## Repository\n\nPath: %s\n\n", repoPath)

	b.WriteString("## Your Task\n\n")
	b.WriteString("Explore the codebase thoroughly and rewrite this ticket so that a developer can\n")
	b.WriteString("implement it without needing additional context. Specifically:\n\n")
	b.WriteString("1. Read relevant files, grep for types, functions, and patterns mentioned in the ticket.\n")
	b.WriteString("2. Identify the affected files (with line numbers where appropriate).\n")
	b.WriteString("3. Describe the current behavior and what needs to change.\n")
	b.WriteString("4. Note edge cases and potential risks.\n")
	b.WriteString("5. Write clear acceptance criteria.\n\n")
	b.WriteString("If the ticket is already well-specified, leave it mostly unchanged.\n\n")
	b.WriteString("If the ticket is too large to implement in a single PR, create sub-tickets\n")
	b.WriteString("as additional `create_issue` actions.\n\n")

	projectKey := issue.Key
	if idx := strings.Index(issue.Key, "-"); idx > 0 {
		projectKey = issue.Key[:idx]
	}

	b.WriteString("## Output\n\n")
	b.WriteString("Write your proposed changes to `.jira-actions.json` in the current directory.\n")
	b.WriteString("Use the Bash tool:\n\n")
	b.WriteString("```\ncat > .jira-actions.json << 'ACTIONS_EOF'\n[\n  ...\n]\nACTIONS_EOF\n```\n\n")
	b.WriteString("The file must be a JSON array of action objects. Supported types:\n\n")

	b.WriteString("**Update the ticket description:**\n")
	fmt.Fprintf(&b, "```json\n{\"type\": \"update_field\", \"issue_key\": \"%s\", \"fields\": {\"description\": \"new description...\"}, \"reason\": \"why you're changing this\"}\n```\n\n", issue.Key)

	b.WriteString("**Add a comment:**\n")
	fmt.Fprintf(&b, "```json\n{\"type\": \"comment\", \"issue_key\": \"%s\", \"body\": \"comment text\", \"reason\": \"why\"}\n```\n\n", issue.Key)

	b.WriteString("**Create sub-tickets:**\n")
	fmt.Fprintf(&b, "```json\n{\"type\": \"create_issue\", \"project\": \"%s\", \"issue_type\": \"Task\", \"summary\": \"...\", \"description\": \"...\", \"priority\": \"Medium\", \"link_to\": \"%s\", \"link_type\": \"is_child_of\", \"reason\": \"why\", \"order\": 1, \"depends_on_order\": []}\n```\n\n", projectKey, issue.Key)

	b.WriteString("Always output at least one action (even if it is just an `update_field` with minor\n")
	b.WriteString("improvements). Do not leave the file empty.\n")

	return b.String()
}

// CreateStories launches an agent that explores the codebase and breaks a requirement
// into implementable stories. The agent writes proposed stories to .jira-actions.json.
// If issue is nil, rawText is used as the requirement description.
func CreateStories(eng *engine.Engine, issue *Issue, rawText string, project string, repoPath string) (string, error) {
	prompt := buildCreatePrompt(issue, rawText, project, repoPath)
	return eng.StartAgent(repoPath, prompt, engine.AgentOptions{
		Model:        "sonnet",
		MaxTurns:     20,
		AllowedTools: []string{"Read", "Grep", "Glob", "Bash"},
		Summary:      fmt.Sprintf("Create stories: %s", summarize(issue, rawText)),
	})
}

// buildCreatePrompt constructs the system prompt for the create-stories agent.
func buildCreatePrompt(issue *Issue, rawText string, project string, repoPath string) string {
	var b strings.Builder

	b.WriteString("You are breaking down a requirement or epic into implementable stories.\n\n")

	b.WriteString("## Requirement\n\n")
	if issue != nil {
		fmt.Fprintf(&b, "Key:         %s\n", issue.Key)
		fmt.Fprintf(&b, "Summary:     %s\n", issue.Summary)
		fmt.Fprintf(&b, "Type:        %s\n", issue.Type)
		fmt.Fprintf(&b, "Priority:    %s\n", issue.Priority)
		fmt.Fprintf(&b, "Status:      %s\n", issue.Status)
		if len(issue.Labels) > 0 {
			fmt.Fprintf(&b, "Labels:      %s\n", strings.Join(issue.Labels, ", "))
		}
		b.WriteString("\n### Description\n\n")
		if issue.Description != "" {
			b.WriteString(issue.Description)
		} else {
			b.WriteString("(no description provided)")
		}
		b.WriteString("\n\n")
	} else {
		b.WriteString(rawText)
		b.WriteString("\n\n")
	}

	fmt.Fprintf(&b, "## Repository\n\nPath: %s\n\n", repoPath)
	fmt.Fprintf(&b, "## Project Key\n\n%s\n\n", project)

	b.WriteString("## Your Task\n\n")
	b.WriteString("Explore the codebase thoroughly to understand the architecture, find relevant\n")
	b.WriteString("modules, and identify the seams where changes need to happen. Then break the\n")
	b.WriteString("requirement into stories that are:\n\n")
	b.WriteString("- Independently implementable (no story blocks another unless expressed via `depends_on_order`)\n")
	b.WriteString("- Sized for 1–3 hours of focused implementation each\n")
	b.WriteString("- Grounded in the actual codebase (reference specific files and modules)\n")
	b.WriteString("- Complete with acceptance criteria\n\n")
	b.WriteString("Guidelines:\n\n")
	b.WriteString("1. Read the relevant source files; grep for types, interfaces, and functions that will be touched.\n")
	b.WriteString("2. Identify foundational or infrastructure changes and give them lower `order` numbers.\n")
	b.WriteString("3. Use `depends_on_order` to express which stories must be completed before another can start.\n")
	b.WriteString("4. For each story, name the specific files and functions to create or modify.\n")
	b.WriteString("5. Include clear acceptance criteria in each story's description.\n\n")

	linkTo := ""
	if issue != nil {
		linkTo = issue.Key
	}

	b.WriteString("## Output\n\n")
	b.WriteString("Write your proposed stories to `.jira-actions.json` in the current directory.\n")
	b.WriteString("Use the Bash tool:\n\n")
	b.WriteString("```\ncat > .jira-actions.json << 'ACTIONS_EOF'\n[\n  ...\n]\nACTIONS_EOF\n```\n\n")
	b.WriteString("The file must be a JSON array of `create_issue` objects:\n\n")
	fmt.Fprintf(&b, "```json\n{\"type\": \"create_issue\", \"project\": \"%s\", \"issue_type\": \"Story\", \"summary\": \"...\", \"description\": \"...\", \"priority\": \"Medium\", \"link_to\": \"%s\", \"link_type\": \"is_child_of\", \"reason\": \"why this story\", \"order\": 1, \"depends_on_order\": []}\n```\n\n", project, linkTo)
	b.WriteString("`issue_type` should be `\"Story\"` for user-facing work and `\"Task\"` for infrastructure/\n")
	b.WriteString("internal work. Use `\"order\": 1` for the first story to implement, `\"order\": 2` for\n")
	b.WriteString("the second, etc. Set `\"depends_on_order\": [1]` if a story requires order 1 to be done first.\n\n")
	b.WriteString("Aim for 3–8 stories. Do not output an empty array.\n")

	return b.String()
}

// summarize returns a short label for the agent summary line.
func summarize(issue *Issue, rawText string) string {
	if issue != nil {
		return issue.Key
	}
	if len(rawText) <= 40 {
		return rawText
	}
	return rawText[:40] + "..."
}
