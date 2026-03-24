package jira

import (
	"encoding/json"
	"fmt"
	"os"
)

type JiraAction struct {
	Type           string         `json:"type"`             // "update_field", "comment", "create_issue"
	IssueKey       string         `json:"issue_key"`        // target issue (empty for create)
	Body           string         `json:"body"`             // for comment
	Project        string         `json:"project"`          // for create
	IssueType      string         `json:"issue_type"`       // for create: "Story", "Task", "Bug"
	Summary        string         `json:"summary"`          // for create
	Description    string         `json:"description"`      // for create
	Priority       string         `json:"priority"`         // for create
	LinkTo         string         `json:"link_to"`          // parent epic key
	LinkType       string         `json:"link_type"`        // "is_child_of", "relates_to", "blocks"
	Fields         map[string]any `json:"fields"`           // for update_field
	Reason         string         `json:"reason"`           // AI explanation shown to user
	Order          int            `json:"order"`            // suggested implementation sequence
	DependsOnOrder []int          `json:"depends_on_order"` // which orders must come first
}

func ParseJiraActions(filePath string) ([]JiraAction, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading jira actions file: %w", err)
	}

	var actions []JiraAction
	if err := json.Unmarshal(data, &actions); err != nil {
		return nil, fmt.Errorf("parsing jira actions JSON: %w", err)
	}

	for i, action := range actions {
		switch action.Type {
		case "update_field":
			if action.IssueKey == "" {
				return nil, fmt.Errorf("action %d (update_field): missing issue_key", i)
			}
			if len(action.Fields) == 0 {
				return nil, fmt.Errorf("action %d (update_field): missing fields", i)
			}
		case "comment":
			if action.IssueKey == "" {
				return nil, fmt.Errorf("action %d (comment): missing issue_key", i)
			}
			if action.Body == "" {
				return nil, fmt.Errorf("action %d (comment): missing body", i)
			}
		case "create_issue":
			if action.Project == "" {
				return nil, fmt.Errorf("action %d (create_issue): missing project", i)
			}
			if action.IssueType == "" {
				return nil, fmt.Errorf("action %d (create_issue): missing issue_type", i)
			}
			if action.Summary == "" {
				return nil, fmt.Errorf("action %d (create_issue): missing summary", i)
			}
		default:
			return nil, fmt.Errorf("action %d: unknown type %q", i, action.Type)
		}
	}

	if err := os.Remove(filePath); err != nil {
		return nil, fmt.Errorf("removing jira actions file: %w", err)
	}

	return actions, nil
}
