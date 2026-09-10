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
	// Total is the number of issues in Issues. The JQL search endpoint pages
	// with a cursor and no longer reports a grand total, so this is a count of
	// what came back, not of everything the query matches.
	Total  int     `json:"total"`
	Issues []Issue `json:"issues"`
}

// apiSearchResponse is the raw JSON structure returned by /rest/api/2/search/jql.
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

	// extra keeps every field verbatim so custom fields can be read by id.
	// Sprint has no fixed name — it lives on a per-instance customfield_NNNNN
	// key that the client resolves at runtime.
	extra map[string]json.RawMessage
}

// UnmarshalJSON decodes the known fields and keeps the raw map alongside them.
func (f *apiIssueFields) UnmarshalJSON(data []byte) error {
	type plain apiIssueFields
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*f = apiIssueFields(p)
	return json.Unmarshal(data, &f.extra)
}

type apiNamedObject struct {
	Name string `json:"name"`
}

type apiDisplayName struct {
	DisplayName string `json:"displayName"`
}

// apiSprint is one entry of the Jira Software sprint custom field, whose value
// is an array: an issue carries every sprint it has ever been in.
type apiSprint struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// sprintName picks the active sprint out of a raw sprint custom-field value,
// falling back to the last (most recent) entry when none is active.
func sprintName(raw json.RawMessage) string {
	var sprints []apiSprint
	if err := json.Unmarshal(raw, &sprints); err != nil || len(sprints) == 0 {
		return ""
	}
	for _, s := range sprints {
		if strings.EqualFold(s.State, "active") {
			return s.Name
		}
	}
	return sprints[len(sprints)-1].Name
}

// adfNode is a minimal representation of Atlassian Document Format nodes.
type adfNode struct {
	Type    string    `json:"type"`
	Text    string    `json:"text"`
	Attrs   adfAttrs  `json:"attrs"`
	Content []adfNode `json:"content"`
}

// adfAttrs holds the attributes of the leaf nodes that carry text of their own.
type adfAttrs struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

// adfInlineParents are the block nodes whose children are inline content. Their
// runs concatenate — a sentence broken into several text nodes by formatting
// marks must come back out as one sentence, not one line per run.
var adfInlineParents = map[string]bool{
	"paragraph": true,
	"heading":   true,
	"codeBlock": true,
}

// extractADFText renders a Jira description field as plain text. The field is a
// wiki-markup string on the v2 API and an ADF document on v3, so both are
// accepted.
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
	switch node.Type {
	case "text":
		return node.Text
	case "hardBreak":
		return "\n"
	case "mention", "emoji":
		return node.Attrs.Text
	case "inlineCard":
		return node.Attrs.URL
	case "rule":
		return "---"
	}

	sep := "\n"
	if adfInlineParents[node.Type] {
		sep = ""
	}
	parts := make([]string, 0, len(node.Content))
	for _, child := range node.Content {
		parts = append(parts, extractADFNodeText(child))
	}
	text := strings.Join(parts, sep)
	if node.Type == "listItem" {
		text = "- " + text
	}
	return text
}

// toIssue maps a raw API issue onto the canonical DTO. sprintFieldID is the
// custom-field id the sprint lives under, empty when it could not be resolved.
func toIssue(a apiIssue, sprintFieldID string) Issue {
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
	if sprintFieldID != "" {
		issue.Sprint = sprintName(a.Fields.extra[sprintFieldID])
	}
	return issue
}
