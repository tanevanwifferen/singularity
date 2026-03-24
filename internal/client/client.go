package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"singularity/internal/api"
	"singularity/internal/git"
	"github.com/gorilla/websocket"
)

// Client is the API client for connecting to the singularity server
type Client struct {
	serverURL string
	httpClient *http.Client
	wsConn     *websocket.Conn
	wsMux      sync.RWMutex
	onUpdate   func(event *api.WSMessage)
}

// NewClient creates a new API client
func NewClient(serverURL string) *Client {
	return &Client{
		serverURL: strings.TrimSuffix(serverURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SetUpdateHandler sets the callback for WebSocket events
func (c *Client) SetUpdateHandler(handler func(event *api.WSMessage)) {
	c.onUpdate = handler
}

// Connect establishes a WebSocket connection
func (c *Client) Connect() error {
	wsURL := strings.Replace(c.serverURL, "http://", "ws://", 1) + "/ws"
	if strings.HasPrefix(c.serverURL, "https://") {
		wsURL = strings.Replace(c.serverURL, "https://", "wss://", 1) + "/ws"
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}

	c.wsMux.Lock()
	c.wsConn = conn
	c.wsMux.Unlock()

	go c.wsReader()
	return nil
}

// Disconnect closes the WebSocket connection
func (c *Client) Disconnect() error {
	c.wsMux.Lock()
	defer c.wsMux.Unlock()

	if c.wsConn != nil {
		return c.wsConn.Close()
	}
	return nil
}

// wsReader reads WebSocket messages
func (c *Client) wsReader() {
	for {
		c.wsMux.RLock()
		conn := c.wsConn
		c.wsMux.RUnlock()

		if conn == nil {
			return
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var event api.WSMessage
		if err := json.Unmarshal(msg, &event); err != nil {
			continue
		}

		if c.onUpdate != nil {
			c.onUpdate(&event)
		}
	}
}

// SendWSMessage sends a WebSocket message
func (c *Client) SendWSMessage(msgType string, payload interface{}) error {
	c.wsMux.RLock()
	conn := c.wsConn
	c.wsMux.RUnlock()

	if conn == nil {
		return fmt.Errorf("not connected")
	}

	msg := api.WSMessage{Type: msgType, Payload: payload}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	return conn.WriteMessage(websocket.TextMessage, data)
}

// Subscribe subscribes to server events
func (c *Client) Subscribe() error {
	return c.SendWSMessage("subscribe", nil)
}

// RefreshRepo requests a repo refresh
func (c *Client) RefreshRepo() error {
	return c.SendWSMessage("refresh_repo", nil)
}

// Helper methods for HTTP requests

func (c *Client) doRequest(method, path string, body, result interface{}) error {
	url := c.serverURL + path

	var bodyReader *strings.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		bodyReader = strings.NewReader(string(data))
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp api.APIResponse
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err == nil {
			if errResp.Error != "" {
				return fmt.Errorf("API error: %s", errResp.Error)
			}
		}
		return fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	if result != nil {
		var apiResp api.APIResponse
		if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}

		if !apiResp.Success {
			return fmt.Errorf("API error: %s", apiResp.Error)
		}

		// Extract data
		if apiResp.Data != nil {
			data, err := json.Marshal(apiResp.Data)
			if err != nil {
				return fmt.Errorf("failed to marshal data: %w", err)
			}
			if err := json.Unmarshal(data, result); err != nil {
				return fmt.Errorf("failed to unmarshal result: %w", err)
			}
		}
	}

	return nil
}

// API methods

// GetStatus returns the server status
func (c *Client) GetStatus() (*api.StatusResponse, error) {
	var resp api.StatusResponse
	err := c.doRequest("GET", "/api/status", nil, &resp)
	return &resp, err
}

// OpenRepo opens a repository
func (c *Client) OpenRepo(path string) (*git.RepoInfo, error) {
	var repo git.RepoInfo
	err := c.doRequest("POST", "/api/repo/open", api.RepoRequest{Path: path}, &repo)
	return &repo, err
}

// GetRepoInfo returns repository info
func (c *Client) GetRepoInfo(path string) (*git.RepoInfo, error) {
	var repo git.RepoInfo
	err := c.doRequest("GET", "/api/repo/info?path="+path, nil, &repo)
	return &repo, err
}

// CompareBranches compares two branches
func (c *Client) CompareBranches(repoPath, branchA, branchB string) (*git.BranchComparison, error) {
	var comparison git.BranchComparison
	err := c.doRequest("POST", "/api/branch/compare", api.BranchComparisonRequest{
		RepoPath: repoPath,
		BranchA: branchA,
		BranchB: branchB,
	}, &comparison)
	return &comparison, err
}

// GetBranchDiff returns the diff between two branches
func (c *Client) GetBranchDiff(repoPath, branchA, branchB string) (*git.BranchDiff, error) {
	var diff git.BranchDiff
	err := c.doRequest("POST", "/api/branch/diff", api.BranchDiffRequest{
		RepoPath: repoPath,
		BranchA: branchA,
		BranchB: branchB,
	}, &diff)
	return &diff, err
}

// GenerateCommitMessage generates a commit message
func (c *Client) GenerateCommitMessage(repoPath string) (*git.CommitMessage, error) {
	var msg git.CommitMessage
	err := c.doRequest("POST", "/api/commit/message", api.CommitMessageRequest{RepoPath: repoPath}, &msg)
	return &msg, err
}

// CreateMR creates a merge request
func (c *Client) CreateMR(repoPath, sourceBranch, targetBranch, title, description string, reviewers []string) (*git.MergeRequest, error) {
	var mr git.MergeRequest
	err := c.doRequest("POST", "/api/mr/create", api.MRRequest{
		RepoPath: repoPath,
		SourceBranch: sourceBranch,
		TargetBranch: targetBranch,
		Title: title,
		Description: description,
		Reviewers: reviewers,
	}, &mr)
	return &mr, err
}

// GetForgeAuth returns forge authentication info
func (c *Client) GetForgeAuth() (*git.ForgeAuth, error) {
	var auth git.ForgeAuth
	err := c.doRequest("GET", "/api/forge/auth", nil, &auth)
	return &auth, err
}
