package jira

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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
	params := url.Values{}
	params.Set("jql", jql)
	params.Set("maxResults", strconv.Itoa(maxResults))
	params.Set("fields", "summary,description,status,priority,assignee,labels,issuetype,sprint")

	raw, err := c.get("/rest/api/2/search?" + params.Encode())
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

// get performs a GET request and returns the response body.
func (c *Client) get(path string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("jira: failed to create request: %w", err)
	}
	req.Header.Set("Authorization", c.authHeader)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jira: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("jira: failed to read response: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		return body, nil
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("jira: rate limited (429) — retry after a moment")
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("jira: authentication failed — check email/token")
	case http.StatusForbidden:
		return nil, fmt.Errorf("jira: forbidden — insufficient permissions")
	case http.StatusNotFound:
		return nil, fmt.Errorf("jira: resource not found")
	default:
		return nil, fmt.Errorf("jira: unexpected status %d: %s", resp.StatusCode, string(body))
	}
}
