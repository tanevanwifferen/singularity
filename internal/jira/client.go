package jira

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client is an HTTP client for the Jira REST API v2.
type Client struct {
	baseURL    string
	authHeader string
	http       *http.Client
}

// NewClient creates a new Jira client.
// For Jira Cloud, provide email + apiToken.
// For Jira Server/Data Center with a PAT, leave email empty and pass the token as apiToken.
func NewClient(baseURL, email, apiToken string) *Client {
	var auth string
	if email != "" {
		auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(email+":"+apiToken))
	} else {
		auth = "Bearer " + apiToken
	}
	return &Client{
		baseURL:    baseURL,
		authHeader: auth,
		http:       &http.Client{Timeout: 15 * time.Second},
	}
}

// SearchIssues executes a JQL query and returns up to maxResults issues.
func (c *Client) SearchIssues(jql string, maxResults int) (*SearchResult, error) {
	body := map[string]interface{}{
		"jql":        jql,
		"maxResults": maxResults,
		"fields":     []string{"summary", "description", "status", "priority", "assignee", "labels", "issuetype", "sprint"},
	}

	raw, err := c.post("/rest/api/3/search/jql", body)
	if err != nil {
		return nil, err
	}

	var resp apiSearchResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("jira: failed to parse search response: %w", err)
	}

	result := &SearchResult{Total: resp.Total}
	for _, a := range resp.Issues {
		result.Issues = append(result.Issues, toIssue(a))
	}
	return result, nil
}

// GetIssue fetches a single issue by key (e.g. "PROJ-123").
func (c *Client) GetIssue(key string) (*Issue, error) {
	params := url.Values{}
	params.Set("fields", "summary,description,status,priority,assignee,labels,issuetype,sprint")

	raw, err := c.get("/rest/api/2/issue/" + url.PathEscape(key) + "?" + params.Encode())
	if err != nil {
		return nil, err
	}

	var a apiIssue
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("jira: failed to parse issue response: %w", err)
	}

	issue := toIssue(a)
	return &issue, nil
}

// GetMyIssues returns open issues assigned to the current user in the given project.
// If projectKey is empty, it searches across all projects.
func (c *Client) GetMyIssues(projectKey string) (*SearchResult, error) {
	jql := "assignee = currentUser() AND resolution = Unresolved ORDER BY updated DESC"
	if projectKey != "" {
		jql = "project = " + projectKey + " AND " + jql
	}
	return c.SearchIssues(jql, 50)
}

// doRequest executes an HTTP request and returns the response body and status code.
// It handles transport-level errors and reads the full body. Status-code checking
// is left to callers so each method can accept different success codes.
func (c *Client) doRequest(method, path string, body io.Reader) ([]byte, int, error) {
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, 0, fmt.Errorf("jira: failed to create request: %w", err)
	}
	req.Header.Set("Authorization", c.authHeader)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("jira: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("jira: failed to read response: %w", err)
	}
	return respBody, resp.StatusCode, nil
}

// statusError converts a non-success HTTP status into the canonical Jira error.
func statusError(code int, body []byte) error {
	switch code {
	case http.StatusTooManyRequests:
		return fmt.Errorf("jira: rate limited (429) — retry after a moment")
	case http.StatusUnauthorized:
		return fmt.Errorf("jira: authentication failed — check email/token")
	case http.StatusForbidden:
		return fmt.Errorf("jira: forbidden — insufficient permissions")
	case http.StatusNotFound:
		return fmt.Errorf("jira: resource not found")
	default:
		return fmt.Errorf("jira: unexpected status %d: %s", code, string(body))
	}
}

// post performs a POST request with a JSON body and returns the response body.
func (c *Client) post(path string, payload interface{}) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("jira: failed to marshal request: %w", err)
	}

	body, code, err := c.doRequest(http.MethodPost, path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if code == http.StatusOK || code == http.StatusCreated {
		return body, nil
	}
	return nil, statusError(code, body)
}

// put performs a PUT request with a JSON body and returns the response body.
func (c *Client) put(path string, payload interface{}) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("jira: failed to marshal request: %w", err)
	}

	body, code, err := c.doRequest(http.MethodPut, path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if code == http.StatusOK || code == http.StatusNoContent {
		return body, nil
	}
	return nil, statusError(code, body)
}

// UpdateFields updates arbitrary fields on an issue via PUT /rest/api/2/issue/{key}.
func (c *Client) UpdateFields(key string, fields map[string]interface{}) error {
	_, err := c.put("/rest/api/2/issue/"+url.PathEscape(key), map[string]interface{}{"fields": fields})
	return err
}

// AddComment posts a plain-text comment to an issue.
func (c *Client) AddComment(key string, body string) error {
	_, err := c.post("/rest/api/2/issue/"+url.PathEscape(key)+"/comment", map[string]interface{}{"body": body})
	return err
}

// CreateIssue creates a new issue and returns the created issue with Key, Summary, and Type populated.
func (c *Client) CreateIssue(project, issueType, summary, description, priority string) (*Issue, error) {
	payload := map[string]interface{}{
		"fields": map[string]interface{}{
			"project":     map[string]interface{}{"key": project},
			"issuetype":   map[string]interface{}{"name": issueType},
			"summary":     summary,
			"description": description,
			"priority":    map[string]interface{}{"name": priority},
		},
	}

	raw, err := c.post("/rest/api/2/issue", payload)
	if err != nil {
		return nil, err
	}

	var created struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		return nil, fmt.Errorf("jira: failed to parse create issue response: %w", err)
	}

	return &Issue{
		Key:     created.Key,
		Summary: summary,
		Type:    issueType,
	}, nil
}

// LinkIssues creates an issue link between two issues.
func (c *Client) LinkIssues(inwardKey, outwardKey, linkType string) error {
	payload := map[string]interface{}{
		"type":         map[string]interface{}{"name": linkType},
		"inwardIssue":  map[string]interface{}{"key": inwardKey},
		"outwardIssue": map[string]interface{}{"key": outwardKey},
	}
	_, err := c.post("/rest/api/2/issueLink", payload)
	return err
}

// get performs a GET request and returns the response body.
func (c *Client) get(path string) ([]byte, error) {
	body, code, err := c.doRequest(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if code == http.StatusOK {
		return body, nil
	}
	return nil, statusError(code, body)
}
