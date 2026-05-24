package service

import "context"

// JiraService covers the Jira REST client (search/get) and the AI workflows
// (Refine/Create/Review). Per CALL-SITES gotcha #1, the AI workflow methods
// do NOT take an *engine.Engine — the daemon owns the engine internally and
// the methods return the spawned agentID. Clients then subscribe via
// AgentService.Subscribe(agentID).
//
// Also covers ParseJiraActions for the ApprovalView action file.
type JiraService interface {
	// SearchIssues runs a JQL query. maxResults <= 0 uses the server default.
	SearchIssues(ctx context.Context, jql string, maxResults int) (*SearchResult, error)

	// GetIssue fetches one issue by key.
	GetIssue(ctx context.Context, key string) (*Issue, error)

	// GetMyIssues fetches the caller's assigned issues for a project.
	GetMyIssues(ctx context.Context, projectKey string) (*SearchResult, error)

	// UpdateFields PATCHes arbitrary fields on an issue.
	UpdateFields(ctx context.Context, key string, fields map[string]any) error

	// AddComment posts a comment on an issue.
	AddComment(ctx context.Context, key, body string) error

	// CreateIssue creates a new issue and returns the canonical DTO.
	CreateIssue(ctx context.Context, project, issueType, summary, description, priority string) (*Issue, error)

	// LinkIssues creates a link of the given type between two issues.
	LinkIssues(ctx context.Context, inwardKey, outwardKey, linkType string) error

	// ParseActions reads a JiraAction list file (as written by the AI
	// workflows) and returns the parsed entries. The daemon resolves the
	// path; clients pass a daemon-side path or an opaque actions-file key.
	ParseActions(ctx context.Context, path string) ([]JiraAction, error)

	// --- AI workflows (daemon-owned engine; returns spawned agent ID) ---

	// RefineTicket spawns an agent that refines a ticket's description.
	// Returns the agent ID; subscribe to it via AgentService.Subscribe.
	RefineTicket(ctx context.Context, issue *Issue, repoPath, focus, actionsFile string) (agentID string, err error)

	// CreateStories spawns an agent that proposes child stories under an
	// epic, optionally seeded with rawText. Returns the agent ID.
	CreateStories(ctx context.Context, issue *Issue, rawText, project, repoPath, actionsFile string) (agentID string, err error)

	// RefineProposalWithContext spawns an agent that refines an existing
	// action proposal using user feedback. Returns the agent ID.
	RefineProposalWithContext(ctx context.Context, issue *Issue, existingActions []JiraAction, userFeedback, repoPath, actionsFile string) (agentID string, err error)

	// ReviewTickets spawns an agent that reviews a set of tickets per
	// instruction. Returns the agent ID.
	ReviewTickets(ctx context.Context, issues []Issue, repoPath, instruction, actionsFile string) (agentID string, err error)
}
