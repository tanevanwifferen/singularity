package jira

import (
	"encoding/json"
	"strings"
)

// Issue represents a Jira issue.
type Issue struct {
	Key         string   `json:"key"`
	Summary     string   `json:"summary"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Priority    string   `json:"priority"`
	Assignee    string   `json:"assignee"`
	Labels      []string `json:"labels"`
	Type        string   `json:"type"`
	Sprint      string   `json:"sprint"`
}

// SearchResult holds the response from a Jira search query.
type SearchResult struct {
	Total  int     `json:"total"`
	Issues []Issue `json:"issues"`
}

// apiSearchResponse is the raw JSON structure returned by /rest/api/3/search/jql.
type apiSearchResponse struct {
	Total  int        `json:"total"`
	Issues []apiIssue `json:"issues"`
}

type apiIssue struct {
	Key    string         `json:"key"`
	Fields apiIssueFields `json:"fields"`
}

type apiIssueFields struct {
	Summary     string          `json:"summary"`
	Description json.RawMessage `json:"description"`
	Status      apiNamedObject  `json:"status"`
	Priority    apiNamedObject  `json:"priority"`
	Assignee    *apiDisplayName `json:"assignee"`
	Labels      []string        `json:"labels"`
	IssueType   apiNamedObject  `json:"issuetype"`
	Sprint      *apiSprint      `json:"sprint"`
}

type apiNamedObject struct {
	Name string `json:"name"`
}

type apiDisplayName struct {
	DisplayName string `json:"displayName"`
}

type apiSprint struct {
	Name string `json:"name"`
}

// adfNode is a minimal representation of Atlassian Document Format nodes.
type adfNode struct {
	Type    string    `json:"type"`
	Text    string    `json:"text"`
	Content []adfNode `json:"content"`
}

// extractADFText recursively extracts plain text from an ADF document.
func extractADFText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	// Try plain string first (Jira Server / older API versions)
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Parse as ADF object
	var node adfNode
	if err := json.Unmarshal(raw, &node); err != nil {
		return ""
	}
	return extractADFNodeText(node)
}

func extractADFNodeText(node adfNode) string {
	if node.Type == "text" {
		return node.Text
	}
	var parts []string
	for _, child := range node.Content {
		if t := extractADFNodeText(child); t != "" {
			parts = append(parts, t)
		}
	}
	sep := ""
	if node.Type == "paragraph" || node.Type == "doc" {
		sep = "\n"
	}
	return strings.Join(parts, sep)
}

func toIssue(a apiIssue) Issue {
	issue := Issue{
		Key:         a.Key,
		Summary:     a.Fields.Summary,
		Description: strings.TrimSpace(extractADFText(a.Fields.Description)),
		Status:      a.Fields.Status.Name,
		Priority:    a.Fields.Priority.Name,
		Labels:      a.Fields.Labels,
		Type:        a.Fields.IssueType.Name,
	}
	if a.Fields.Assignee != nil {
		issue.Assignee = a.Fields.Assignee.DisplayName
	}
	if a.Fields.Sprint != nil {
		issue.Sprint = a.Fields.Sprint.Name
	}
	return issue
}
