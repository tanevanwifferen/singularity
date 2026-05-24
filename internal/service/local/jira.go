package local

import (
	"context"
	"fmt"

	"gitlab.com/tanevanwifferen1/singularity/internal/config"
	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
	"gitlab.com/tanevanwifferen1/singularity/internal/jira"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// localJiraService implements service.JiraService. Holds the shared engine
// (for AI workflows) plus a lazily-constructed *jira.Client.
type localJiraService struct {
	eng    *engine.Engine
	cfg    config.JiraConfig
	client *jira.Client
}

// newJiraService constructs a local jira service. If cfg is incomplete the
// client stays nil and every Jira call returns ErrUnavailable.
func newJiraService(eng *engine.Engine, cfg config.JiraConfig) *localJiraService {
	svc := &localJiraService{eng: eng, cfg: cfg}
	if cfg.Enabled && cfg.BaseURL != "" && cfg.APIToken != "" {
		svc.client = jira.NewClient(cfg.BaseURL, cfg.Email, cfg.APIToken)
	}
	return svc
}

// requireClient is the gate every Jira-data method funnels through.
func (s *localJiraService) requireClient() (*jira.Client, error) {
	if s == nil || s.client == nil {
		return nil, service.ErrUnavailable
	}
	return s.client, nil
}

// requireEngine gates AI workflow methods.
func (s *localJiraService) requireEngine() (*engine.Engine, error) {
	if s == nil || s.eng == nil {
		return nil, service.ErrUnavailable
	}
	return s.eng, nil
}

// SearchIssues runs a JQL query.
func (s *localJiraService) SearchIssues(ctx context.Context, jql string, maxResults int) (*service.SearchResult, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	c, err := s.requireClient()
	if err != nil {
		return nil, err
	}
	res, err := c.SearchIssues(jql, maxResults)
	if err != nil {
		return nil, wrapErr(err)
	}
	return res, nil
}

// GetIssue fetches one issue by key.
func (s *localJiraService) GetIssue(ctx context.Context, key string) (*service.Issue, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	c, err := s.requireClient()
	if err != nil {
		return nil, err
	}
	iss, err := c.GetIssue(key)
	if err != nil {
		return nil, wrapErr(err)
	}
	return iss, nil
}

// GetMyIssues fetches the caller's assigned issues for a project.
func (s *localJiraService) GetMyIssues(ctx context.Context, projectKey string) (*service.SearchResult, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	c, err := s.requireClient()
	if err != nil {
		return nil, err
	}
	res, err := c.GetMyIssues(projectKey)
	if err != nil {
		return nil, wrapErr(err)
	}
	return res, nil
}

// UpdateFields PATCHes arbitrary fields on an issue.
func (s *localJiraService) UpdateFields(ctx context.Context, key string, fields map[string]any) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	c, err := s.requireClient()
	if err != nil {
		return err
	}
	return wrapErr(c.UpdateFields(key, fields))
}

// AddComment posts a comment on an issue.
func (s *localJiraService) AddComment(ctx context.Context, key, body string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	c, err := s.requireClient()
	if err != nil {
		return err
	}
	return wrapErr(c.AddComment(key, body))
}

// CreateIssue creates a new issue and returns the canonical DTO.
func (s *localJiraService) CreateIssue(ctx context.Context, project, issueType, summary, description, priority string) (*service.Issue, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	c, err := s.requireClient()
	if err != nil {
		return nil, err
	}
	iss, err := c.CreateIssue(project, issueType, summary, description, priority)
	if err != nil {
		return nil, wrapErr(err)
	}
	return iss, nil
}

// LinkIssues creates a link between two issues.
func (s *localJiraService) LinkIssues(ctx context.Context, inwardKey, outwardKey, linkType string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	c, err := s.requireClient()
	if err != nil {
		return err
	}
	return wrapErr(c.LinkIssues(inwardKey, outwardKey, linkType))
}

// ParseActions reads a JiraAction list file and returns the parsed entries.
func (s *localJiraService) ParseActions(ctx context.Context, path string) ([]service.JiraAction, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	actions, err := jira.ParseJiraActions(path)
	if err != nil {
		return nil, wrapErr(err)
	}
	return actions, nil
}

// RefineTicket spawns an agent that refines a ticket's description.
func (s *localJiraService) RefineTicket(ctx context.Context, issue *service.Issue, repoPath, focus, actionsFile string) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	eng, err := s.requireEngine()
	if err != nil {
		return "", err
	}
	if issue == nil {
		return "", fmt.Errorf("issue is required")
	}
	id, err := jira.RefineTicket(eng, issue, repoPath, focus, actionsFile)
	if err != nil {
		return id, mapAgentStartErr(err)
	}
	return id, nil
}

// CreateStories spawns an agent that proposes child stories under an epic.
func (s *localJiraService) CreateStories(ctx context.Context, issue *service.Issue, rawText, project, repoPath, actionsFile string) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	eng, err := s.requireEngine()
	if err != nil {
		return "", err
	}
	id, err := jira.CreateStories(eng, issue, rawText, project, repoPath, actionsFile)
	if err != nil {
		return id, mapAgentStartErr(err)
	}
	return id, nil
}

// RefineProposalWithContext spawns an agent that refines an existing action proposal.
func (s *localJiraService) RefineProposalWithContext(ctx context.Context, issue *service.Issue, existingActions []service.JiraAction, userFeedback, repoPath, actionsFile string) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	eng, err := s.requireEngine()
	if err != nil {
		return "", err
	}
	id, err := jira.RefineProposalWithContext(eng, issue, existingActions, userFeedback, repoPath, actionsFile)
	if err != nil {
		return id, mapAgentStartErr(err)
	}
	return id, nil
}

// ReviewTickets spawns an agent that reviews a set of tickets.
func (s *localJiraService) ReviewTickets(ctx context.Context, issues []service.Issue, repoPath, instruction, actionsFile string) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	eng, err := s.requireEngine()
	if err != nil {
		return "", err
	}
	id, err := jira.ReviewTickets(eng, issues, repoPath, instruction, actionsFile)
	if err != nil {
		return id, mapAgentStartErr(err)
	}
	return id, nil
}
