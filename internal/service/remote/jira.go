package remote

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// remoteJiraService implements service.JiraService.
//
// AI-workflow methods do NOT take an *engine.Engine — the daemon owns
// the engine and returns the spawned agent ID. Clients then subscribe to
// the agent via AgentService.Subscribe(agentID).
type remoteJiraService struct {
	c *client.Client
}

// SearchIssues runs a JQL query.
func (s *remoteJiraService) SearchIssues(ctx context.Context, jql string, maxResults int) (*service.SearchResult, error) {
	return s.c.JiraSearchIssues(ctx, jql, maxResults)
}

// GetIssue fetches one issue by key.
func (s *remoteJiraService) GetIssue(ctx context.Context, key string) (*service.Issue, error) {
	return s.c.JiraGetIssue(ctx, key)
}

// GetMyIssues fetches the caller's assigned issues for a project.
func (s *remoteJiraService) GetMyIssues(ctx context.Context, projectKey string) (*service.SearchResult, error) {
	return s.c.JiraGetMyIssues(ctx, projectKey)
}

// UpdateFields PATCHes arbitrary fields on an issue.
func (s *remoteJiraService) UpdateFields(ctx context.Context, key string, fields map[string]any) error {
	return s.c.JiraUpdateFields(ctx, key, fields)
}

// AddComment posts a comment on an issue.
func (s *remoteJiraService) AddComment(ctx context.Context, key, body string) error {
	return s.c.JiraAddComment(ctx, key, body)
}

// CreateIssue creates a new issue.
func (s *remoteJiraService) CreateIssue(ctx context.Context, project, issueType, summary, description, priority string) (*service.Issue, error) {
	return s.c.JiraCreateIssue(ctx, project, issueType, summary, description, priority)
}

// LinkIssues creates a link of the given type between two issues.
func (s *remoteJiraService) LinkIssues(ctx context.Context, inwardKey, outwardKey, linkType string) error {
	return s.c.JiraLinkIssues(ctx, inwardKey, outwardKey, linkType)
}

// ParseActions reads a JiraAction list file and returns the parsed entries.
func (s *remoteJiraService) ParseActions(ctx context.Context, path string) ([]service.JiraAction, error) {
	return s.c.JiraParseActions(ctx, path)
}

// RefineTicket spawns an agent that refines a ticket's description.
func (s *remoteJiraService) RefineTicket(ctx context.Context, issue *service.Issue, repoPath, focus, actionsFile string) (string, error) {
	return s.c.JiraRefineTicket(ctx, issue, repoPath, focus, actionsFile)
}

// CreateStories spawns an agent that proposes child stories under an epic.
func (s *remoteJiraService) CreateStories(ctx context.Context, issue *service.Issue, rawText, project, repoPath, actionsFile string) (string, error) {
	return s.c.JiraCreateStories(ctx, issue, rawText, project, repoPath, actionsFile)
}

// RefineProposalWithContext spawns an agent that refines an existing proposal.
func (s *remoteJiraService) RefineProposalWithContext(ctx context.Context, issue *service.Issue, existingActions []service.JiraAction, userFeedback, repoPath, actionsFile string) (string, error) {
	return s.c.JiraRefineProposalWithContext(ctx, issue, existingActions, userFeedback, repoPath, actionsFile)
}

// ReviewTickets spawns an agent that reviews a set of tickets.
func (s *remoteJiraService) ReviewTickets(ctx context.Context, issues []service.Issue, repoPath, instruction, actionsFile string) (string, error) {
	return s.c.JiraReviewTickets(ctx, issues, repoPath, instruction, actionsFile)
}
