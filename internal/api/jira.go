package api

import "gitlab.com/tanevanwifferen1/singularity/internal/service"

// Jira DTOs alias the canonical types from internal/jira (re-exported via
// internal/service).
type (
	Issue        = service.Issue
	SearchResult = service.SearchResult
	JiraAction   = service.JiraAction
)

// JiraSearchRequest is the body for POST /api/jira/search.
type JiraSearchRequest struct {
	JQL        string `json:"jql"`
	MaxResults int    `json:"max_results"`
}

// JiraUpdateRequest is the body for POST /api/jira/update.
type JiraUpdateRequest struct {
	Key    string         `json:"key"`
	Fields map[string]any `json:"fields"`
}

// JiraCommentRequest is the body for POST /api/jira/comment.
type JiraCommentRequest struct {
	Key  string `json:"key"`
	Body string `json:"body"`
}

// JiraCreateRequest is the body for POST /api/jira/create.
type JiraCreateRequest struct {
	Project     string `json:"project"`
	IssueType   string `json:"issue_type"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
}

// JiraLinkRequest is the body for POST /api/jira/link.
type JiraLinkRequest struct {
	InwardKey  string `json:"inward_key"`
	OutwardKey string `json:"outward_key"`
	LinkType   string `json:"link_type"`
}

// JiraActionsResponse is the body for GET /api/jira/actions.
type JiraActionsResponse struct {
	Actions []JiraAction `json:"actions"`
}

// JiraRefineTicketRequest is the body for POST /api/jira/ai/refine.
type JiraRefineTicketRequest struct {
	Issue       *Issue `json:"issue"`
	RepoPath    string `json:"repo_path"`
	Focus       string `json:"focus"`
	ActionsFile string `json:"actions_file"`
}

// JiraCreateStoriesRequest is the body for POST /api/jira/ai/stories.
type JiraCreateStoriesRequest struct {
	Issue       *Issue `json:"issue"`
	RawText     string `json:"raw_text"`
	Project     string `json:"project"`
	RepoPath    string `json:"repo_path"`
	ActionsFile string `json:"actions_file"`
}

// JiraRefineProposalRequest is the body for POST /api/jira/ai/refine_proposal.
type JiraRefineProposalRequest struct {
	Issue           *Issue       `json:"issue"`
	ExistingActions []JiraAction `json:"existing_actions"`
	UserFeedback    string       `json:"user_feedback"`
	RepoPath        string       `json:"repo_path"`
	ActionsFile     string       `json:"actions_file"`
}

// JiraReviewTicketsRequest is the body for POST /api/jira/ai/review.
type JiraReviewTicketsRequest struct {
	Issues      []Issue `json:"issues"`
	RepoPath    string  `json:"repo_path"`
	Instruction string  `json:"instruction"`
	ActionsFile string  `json:"actions_file"`
}
