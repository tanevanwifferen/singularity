package jira

import (
	"fmt"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
)

// RefineTicket launches an agent that explores the codebase and rewrites a Jira ticket
// to be implementation-ready. The agent writes its proposed changes to the specified
// actionsFile (or ".jira-actions.json" if empty) in the working directory.
func RefineTicket(eng *engine.Engine, issue *Issue, repoPath string, focus string, actionsFile string) (string, error) {
	if actionsFile == "" {
		actionsFile = ".jira-actions.json"
	}
	prompt := buildRefinePrompt(issue, repoPath, focus, actionsFile)
	return eng.StartAgent(repoPath, prompt, engine.AgentOptions{
		Model:        "sonnet",
		MaxTurns:     15,
		AllowedTools: []string{"Read", "Grep", "Glob", "Bash"},
		Summary:      fmt.Sprintf("Refine: %s", issue.Key),
	})
}

// buildRefinePrompt constructs the system prompt for the refine-ticket agent.
func buildRefinePrompt(issue *Issue, repoPath string, focus string, actionsFile string) string {
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

	if focus != "" {
		b.WriteString("## Focus\n\n")
		fmt.Fprintf(&b, "The user wants you to pay special attention to the following:\n\n> %s\n\n", focus)
		b.WriteString("Prioritize this area when analyzing the ticket and proposing changes.\n\n")
	}

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
	fmt.Fprintf(&b, "Write your proposed changes to `%s` in the current directory.\n", actionsFile)
	b.WriteString("Use the Bash tool:\n\n")
	fmt.Fprintf(&b, "```\ncat > %s << 'ACTIONS_EOF'\n[\n  ...\n]\nACTIONS_EOF\n```\n\n", actionsFile)
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
// into implementable stories. The agent writes proposed stories to the specified
// actionsFile (or ".jira-actions.json" if empty).
// If issue is nil, rawText is used as the requirement description.
func CreateStories(eng *engine.Engine, issue *Issue, rawText string, project string, repoPath string, actionsFile string) (string, error) {
	if actionsFile == "" {
		actionsFile = ".jira-actions.json"
	}
	prompt := buildCreatePrompt(issue, rawText, project, repoPath, actionsFile)
	return eng.StartAgent(repoPath, prompt, engine.AgentOptions{
		Model:        "sonnet",
		MaxTurns:     20,
		AllowedTools: []string{"Read", "Grep", "Glob", "Bash"},
		Summary:      fmt.Sprintf("Create stories: %s", summarize(issue, rawText)),
	})
}

// buildCreatePrompt constructs the system prompt for the create-stories agent.
func buildCreatePrompt(issue *Issue, rawText string, project string, repoPath string, actionsFile string) string {
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
	fmt.Fprintf(&b, "Write your proposed stories to `%s` in the current directory.\n", actionsFile)
	b.WriteString("Use the Bash tool:\n\n")
	fmt.Fprintf(&b, "```\ncat > %s << 'ACTIONS_EOF'\n[\n  ...\n]\nACTIONS_EOF\n```\n\n", actionsFile)
	b.WriteString("The file must be a JSON array of `create_issue` objects:\n\n")
	fmt.Fprintf(&b, "```json\n{\"type\": \"create_issue\", \"project\": \"%s\", \"issue_type\": \"Story\", \"summary\": \"...\", \"description\": \"...\", \"priority\": \"Medium\", \"link_to\": \"%s\", \"link_type\": \"is_child_of\", \"reason\": \"why this story\", \"order\": 1, \"depends_on_order\": []}\n```\n\n", project, linkTo)
	b.WriteString("`issue_type` should be `\"Story\"` for user-facing work and `\"Task\"` for infrastructure/\n")
	b.WriteString("internal work. Use `\"order\": 1` for the first story to implement, `\"order\": 2` for\n")
	b.WriteString("the second, etc. Set `\"depends_on_order\": [1]` if a story requires order 1 to be done first.\n\n")
	b.WriteString("Aim for 3–8 stories. Do not output an empty array.\n")

	return b.String()
}

// RefineProposalWithContext launches an agent that reviews an existing proposal
// alongside the Jira ticket and codebase, then rewrites the proposal to improve it.
// The existing actions are passed as context so the agent can build on prior work.
func RefineProposalWithContext(eng *engine.Engine, issue *Issue, existingActions []JiraAction, repoPath string, actionsFile string) (string, error) {
	if actionsFile == "" {
		actionsFile = fmt.Sprintf(".jira-actions-%s.json", issue.Key)
	}
	prompt := buildRefineProposalWithContextPrompt(issue, existingActions, repoPath, actionsFile)
	return eng.StartAgent(repoPath, prompt, engine.AgentOptions{
		Model:        "sonnet",
		MaxTurns:     15,
		AllowedTools: []string{"Read", "Grep", "Glob", "Bash"},
		Summary:      fmt.Sprintf("Refine: %s", issue.Key),
	})
}

// buildRefineProposalWithContextPrompt builds a prompt for iterating on an existing proposal.
func buildRefineProposalWithContextPrompt(issue *Issue, existingActions []JiraAction, repoPath string, actionsFile string) string {
	var b strings.Builder

	b.WriteString("You are reviewing and improving an existing Jira ticket proposal.\n\n")

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

	b.WriteString("## Existing Proposal\n\n")
	b.WriteString("A previous agent already produced the following proposed actions. Use these as a\n")
	b.WriteString("starting point — keep what is correct and improve what is incomplete or inaccurate.\n\n")
	for i, action := range existingActions {
		fmt.Fprintf(&b, "### Action %d: %s\n", i+1, action.Type)
		if action.IssueKey != "" {
			fmt.Fprintf(&b, "Issue:  %s\n", action.IssueKey)
		}
		if action.Summary != "" {
			fmt.Fprintf(&b, "Summary: %s\n", action.Summary)
		}
		if action.Body != "" {
			fmt.Fprintf(&b, "Body:\n%s\n", action.Body)
		}
		if action.Reason != "" {
			fmt.Fprintf(&b, "Reason: %s\n", action.Reason)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Your Task\n\n")
	b.WriteString("Explore the codebase to verify and improve the existing proposal. Specifically:\n\n")
	b.WriteString("1. Re-read the relevant files referenced in the proposal to verify accuracy.\n")
	b.WriteString("2. Check whether any referenced files, functions, or modules have changed.\n")
	b.WriteString("3. Identify gaps: missing acceptance criteria, unhandled edge cases, unclear steps.\n")
	b.WriteString("4. Improve descriptions so a developer can implement without additional context.\n")
	b.WriteString("5. If the proposal is already excellent, output it unchanged.\n\n")

	projectKey := issue.Key
	if idx := strings.Index(issue.Key, "-"); idx > 0 {
		projectKey = issue.Key[:idx]
	}

	b.WriteString("## Output\n\n")
	fmt.Fprintf(&b, "Write the improved proposal to `%s` in the current directory.\n", actionsFile)
	b.WriteString("Use the Bash tool:\n\n")
	fmt.Fprintf(&b, "```\ncat > %s << 'ACTIONS_EOF'\n[\n  ...\n]\nACTIONS_EOF\n```\n\n", actionsFile)
	b.WriteString("The file must be a JSON array of action objects. Supported types:\n\n")

	b.WriteString("**Update the ticket description:**\n")
	fmt.Fprintf(&b, "```json\n{\"type\": \"update_field\", \"issue_key\": \"%s\", \"fields\": {\"description\": \"new description...\"}, \"reason\": \"why you're changing this\"}\n```\n\n", issue.Key)

	b.WriteString("**Add a comment:**\n")
	fmt.Fprintf(&b, "```json\n{\"type\": \"comment\", \"issue_key\": \"%s\", \"body\": \"comment text\", \"reason\": \"why\"}\n```\n\n", issue.Key)

	b.WriteString("**Create sub-tickets:**\n")
	fmt.Fprintf(&b, "```json\n{\"type\": \"create_issue\", \"project\": \"%s\", \"issue_type\": \"Task\", \"summary\": \"...\", \"description\": \"...\", \"priority\": \"Medium\", \"link_to\": \"%s\", \"link_type\": \"is_child_of\", \"reason\": \"why\", \"order\": 1, \"depends_on_order\": []}\n```\n\n", projectKey, issue.Key)

	b.WriteString("Always output at least one action. Do not leave the file empty.\n")

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
