package client

import (
	"context"
	"net/url"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// JiraSearchIssues calls Jira.SearchIssues.
func (c *Client) JiraSearchIssues(ctx context.Context, jql string, maxResults int) (*api.SearchResult, error) {
	var res api.SearchResult
	if err := c.post(ctx, "/api/jira/search", api.JiraSearchRequest{JQL: jql, MaxResults: maxResults}, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// JiraGetIssue calls Jira.GetIssue.
func (c *Client) JiraGetIssue(ctx context.Context, key string) (*api.Issue, error) {
	var issue api.Issue
	if err := c.get(ctx, "/api/jira/issue?key="+url.QueryEscape(key), &issue); err != nil {
		return nil, err
	}
	return &issue, nil
}

// JiraGetMyIssues calls Jira.GetMyIssues.
func (c *Client) JiraGetMyIssues(ctx context.Context, project string) (*api.SearchResult, error) {
	var res api.SearchResult
	if err := c.get(ctx, "/api/jira/my?project="+url.QueryEscape(project), &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// JiraUpdateFields calls Jira.UpdateFields.
func (c *Client) JiraUpdateFields(ctx context.Context, key string, fields map[string]any) error {
	return c.post(ctx, "/api/jira/update", api.JiraUpdateRequest{Key: key, Fields: fields}, nil)
}

// JiraAddComment calls Jira.AddComment.
func (c *Client) JiraAddComment(ctx context.Context, key, body string) error {
	return c.post(ctx, "/api/jira/comment", api.JiraCommentRequest{Key: key, Body: body}, nil)
}

// JiraCreateIssue calls Jira.CreateIssue.
func (c *Client) JiraCreateIssue(ctx context.Context, project, issueType, summary, description, priority string) (*api.Issue, error) {
	req := api.JiraCreateRequest{Project: project, IssueType: issueType, Summary: summary, Description: description, Priority: priority}
	var issue api.Issue
	if err := c.post(ctx, "/api/jira/create", req, &issue); err != nil {
		return nil, err
	}
	return &issue, nil
}

// JiraLinkIssues calls Jira.LinkIssues.
func (c *Client) JiraLinkIssues(ctx context.Context, inwardKey, outwardKey, linkType string) error {
	return c.post(ctx, "/api/jira/link", api.JiraLinkRequest{InwardKey: inwardKey, OutwardKey: outwardKey, LinkType: linkType}, nil)
}

// JiraParseActions calls Jira.ParseActions.
func (c *Client) JiraParseActions(ctx context.Context, path string) ([]api.JiraAction, error) {
	var resp api.JiraActionsResponse
	if err := c.get(ctx, "/api/jira/actions?path="+url.QueryEscape(path), &resp); err != nil {
		return nil, err
	}
	return resp.Actions, nil
}

// JiraRefineTicket calls Jira.RefineTicket. Returns the spawned agent ID;
// subscribe to it via AgentSubscribe.
func (c *Client) JiraRefineTicket(ctx context.Context, issue *api.Issue, repoPath, focus, actionsFile string) (string, error) {
	req := api.JiraRefineTicketRequest{Issue: issue, RepoPath: repoPath, Focus: focus, ActionsFile: actionsFile}
	var resp api.AgentStartResponse
	if err := c.post(ctx, "/api/jira/ai/refine", req, &resp); err != nil {
		return "", err
	}
	return resp.AgentID, nil
}

// JiraCreateStories calls Jira.CreateStories.
func (c *Client) JiraCreateStories(ctx context.Context, issue *api.Issue, rawText, project, repoPath, actionsFile string) (string, error) {
	req := api.JiraCreateStoriesRequest{Issue: issue, RawText: rawText, Project: project, RepoPath: repoPath, ActionsFile: actionsFile}
	var resp api.AgentStartResponse
	if err := c.post(ctx, "/api/jira/ai/stories", req, &resp); err != nil {
		return "", err
	}
	return resp.AgentID, nil
}

// JiraRefineProposalWithContext calls Jira.RefineProposalWithContext.
func (c *Client) JiraRefineProposalWithContext(ctx context.Context, issue *api.Issue, existingActions []api.JiraAction, userFeedback, repoPath, actionsFile string) (string, error) {
	req := api.JiraRefineProposalRequest{Issue: issue, ExistingActions: existingActions, UserFeedback: userFeedback, RepoPath: repoPath, ActionsFile: actionsFile}
	var resp api.AgentStartResponse
	if err := c.post(ctx, "/api/jira/ai/refine_proposal", req, &resp); err != nil {
		return "", err
	}
	return resp.AgentID, nil
}

// JiraReviewTickets calls Jira.ReviewTickets.
func (c *Client) JiraReviewTickets(ctx context.Context, issues []api.Issue, repoPath, instruction, actionsFile string) (string, error) {
	req := api.JiraReviewTicketsRequest{Issues: issues, RepoPath: repoPath, Instruction: instruction, ActionsFile: actionsFile}
	var resp api.AgentStartResponse
	if err := c.post(ctx, "/api/jira/ai/review", req, &resp); err != nil {
		return "", err
	}
	return resp.AgentID, nil
}
