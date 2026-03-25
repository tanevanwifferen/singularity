package jira

import (
	"encoding/json"
	"testing"
)

// --- extractADFText ---

func TestExtractADFText_NilOrEmpty(t *testing.T) {
	if got := extractADFText(nil); got != "" {
		t.Errorf("nil raw: got %q, want empty", got)
	}
	if got := extractADFText(json.RawMessage("null")); got != "" {
		t.Errorf("null raw: got %q, want empty", got)
	}
	if got := extractADFText(json.RawMessage("")); got != "" {
		t.Errorf("empty raw: got %q, want empty", got)
	}
}

func TestExtractADFText_PlainString(t *testing.T) {
	raw := json.RawMessage(`"hello world"`)
	got := extractADFText(raw)
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestExtractADFText_SimpleADF(t *testing.T) {
	// ADF doc with a single paragraph containing a text node
	raw := json.RawMessage(`{
		"type": "doc",
		"content": [{
			"type": "paragraph",
			"content": [{
				"type": "text",
				"text": "Hello ADF"
			}]
		}]
	}`)
	got := extractADFText(raw)
	if got != "Hello ADF" {
		t.Errorf("got %q, want %q", got, "Hello ADF")
	}
}

func TestExtractADFText_MultiParagraph(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "doc",
		"content": [
			{"type": "paragraph", "content": [{"type": "text", "text": "First"}]},
			{"type": "paragraph", "content": [{"type": "text", "text": "Second"}]}
		]
	}`)
	got := extractADFText(raw)
	// Paragraphs are joined with newline separator
	if got != "First\nSecond" {
		t.Errorf("got %q, want %q", got, "First\nSecond")
	}
}

func TestExtractADFText_InvalidJSON(t *testing.T) {
	raw := json.RawMessage(`{not valid json`)
	got := extractADFText(raw)
	// Invalid JSON that can't be parsed as string or ADF should return ""
	if got != "" {
		t.Errorf("invalid JSON: got %q, want empty", got)
	}
}

func TestExtractADFNodeText_TextNode(t *testing.T) {
	node := adfNode{Type: "text", Text: "direct text"}
	got := extractADFNodeText(node)
	if got != "direct text" {
		t.Errorf("got %q, want %q", got, "direct text")
	}
}

func TestExtractADFNodeText_EmptyChildren(t *testing.T) {
	node := adfNode{Type: "paragraph", Content: []adfNode{}}
	got := extractADFNodeText(node)
	if got != "" {
		t.Errorf("empty children: got %q, want empty", got)
	}
}

func TestExtractADFNodeText_SkipsEmptyChildren(t *testing.T) {
	node := adfNode{
		Type: "paragraph",
		Content: []adfNode{
			{Type: "text", Text: "A"},
			{Type: "text", Text: ""},
			{Type: "text", Text: "B"},
		},
	}
	got := extractADFNodeText(node)
	// Empty text nodes are skipped; paragraph uses \n separator
	if got != "A\nB" {
		t.Errorf("got %q, want %q", got, "A\nB")
	}
}

// --- toIssue ---

func TestToIssue_BasicFields(t *testing.T) {
	a := apiIssue{
		Key: "PROJ-1",
		Fields: apiIssueFields{
			Summary:   "Fix the thing",
			Status:    apiNamedObject{Name: "In Progress"},
			Priority:  apiNamedObject{Name: "High"},
			IssueType: apiNamedObject{Name: "Bug"},
			Labels:    []string{"backend", "urgent"},
		},
	}

	issue := toIssue(a)

	if issue.Key != "PROJ-1" {
		t.Errorf("Key: got %q", issue.Key)
	}
	if issue.Summary != "Fix the thing" {
		t.Errorf("Summary: got %q", issue.Summary)
	}
	if issue.Status != "In Progress" {
		t.Errorf("Status: got %q", issue.Status)
	}
	if issue.Priority != "High" {
		t.Errorf("Priority: got %q", issue.Priority)
	}
	if issue.Type != "Bug" {
		t.Errorf("Type: got %q", issue.Type)
	}
	if len(issue.Labels) != 2 || issue.Labels[0] != "backend" {
		t.Errorf("Labels: got %v", issue.Labels)
	}
}

func TestToIssue_NilAssignee(t *testing.T) {
	a := apiIssue{Key: "X-1", Fields: apiIssueFields{Assignee: nil}}
	issue := toIssue(a)
	if issue.Assignee != "" {
		t.Errorf("nil assignee: got %q, want empty", issue.Assignee)
	}
}

func TestToIssue_WithAssignee(t *testing.T) {
	a := apiIssue{
		Key: "X-2",
		Fields: apiIssueFields{
			Assignee: &apiDisplayName{DisplayName: "Jane Doe"},
		},
	}
	issue := toIssue(a)
	if issue.Assignee != "Jane Doe" {
		t.Errorf("Assignee: got %q, want %q", issue.Assignee, "Jane Doe")
	}
}

func TestToIssue_WithSprint(t *testing.T) {
	a := apiIssue{
		Key: "X-3",
		Fields: apiIssueFields{
			Sprint: &apiSprint{Name: "Sprint 5"},
		},
	}
	issue := toIssue(a)
	if issue.Sprint != "Sprint 5" {
		t.Errorf("Sprint: got %q, want %q", issue.Sprint, "Sprint 5")
	}
}

func TestToIssue_PlainStringDescription(t *testing.T) {
	a := apiIssue{
		Key: "X-4",
		Fields: apiIssueFields{
			Description: json.RawMessage(`"Plain text description"`),
		},
	}
	issue := toIssue(a)
	if issue.Description != "Plain text description" {
		t.Errorf("Description: got %q", issue.Description)
	}
}
