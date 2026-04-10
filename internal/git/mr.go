package git

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// ErrMRAlreadyExists is returned when a merge/pull request already exists for the branch.
var ErrMRAlreadyExists = errors.New("a merge request already exists for this branch")

// MergeRequest represents a merge/pull request
type MergeRequest struct {
	Number       int    `json:"number"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	Author       string `json:"author"`
	State        string `json:"state"`
	URL          string `json:"url"`
	WebURL       string `json:"web_url"`
}

// CreateMR creates a merge request
func CreateMR(repoPath, sourceBranch, targetBranch, title, description string, reviewers []string) (*MergeRequest, error) {
	// Detect forge type
	auth, err := DetectForgeAuth()
	if err != nil {
		return nil, fmt.Errorf("failed to detect forge auth: %w", err)
	}

	if !auth.Valid {
		return nil, fmt.Errorf("no valid forge authentication found")
	}

	if auth.IsGitLab() {
		return createGitLabMR(repoPath, sourceBranch, targetBranch, title, description, reviewers, auth)
	}
	if auth.IsGitHub() {
		return createGitHubPR(repoPath, sourceBranch, targetBranch, title, description, auth)
	}

	return nil, fmt.Errorf("unsupported forge type: %s", auth.Type)
}

// getCurrentGitLabUserID fetches the current authenticated user's ID from GitLab.
func getCurrentGitLabUserID(apiURL, token string) (int, error) {
	resp, err := makeGitLabRequest("GET", apiURL+"/user", token, nil)
	if err != nil {
		return 0, err
	}
	var user map[string]interface{}
	if err := json.Unmarshal(resp, &user); err != nil {
		return 0, err
	}
	id, ok := user["id"].(float64)
	if !ok {
		return 0, fmt.Errorf("user id not found in response")
	}
	return int(id), nil
}

// getCurrentGitHubLogin fetches the current authenticated user's login from GitHub.
func getCurrentGitHubLogin(apiURL, token string) (string, error) {
	resp, err := makeGitHubRequest("GET", apiURL+"/user", token, nil)
	if err != nil {
		return "", err
	}
	var user map[string]interface{}
	if err := json.Unmarshal(resp, &user); err != nil {
		return "", err
	}
	login, ok := user["login"].(string)
	if !ok {
		return "", fmt.Errorf("login not found in response")
	}
	return login, nil
}

// createGitLabMR creates a merge request on GitLab
func createGitLabMR(repoPath, sourceBranch, targetBranch, title, description string, reviewers []string, auth *ForgeAuth) (*MergeRequest, error) {
	// Get project path from remote
	projectPath := getProjectPath(repoPath)
	if projectPath == "" {
		return nil, fmt.Errorf("could not determine project path from git remote")
	}

	// Build API URL
	apiURL := auth.APIURL
	if apiURL == "" {
		apiURL = "https://gitlab.com/api/v4"
	}
	url := fmt.Sprintf("%s/projects/%s/merge_requests", apiURL, encodeProjectPath(projectPath))

	// Build request body
	body := map[string]interface{}{
		"source_branch": sourceBranch,
		"target_branch": targetBranch,
		"title":         title,
		"description":   description,
	}

	if len(reviewers) > 0 {
		body["reviewer_ids"] = reviewers
	}

	// Assign to current user
	if userID, err := getCurrentGitLabUserID(apiURL, auth.AuthToken); err == nil {
		body["assignee_id"] = userID
	}

	// Make request
	resp, err := makeGitLabRequest("POST", url, auth.AuthToken, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create MR: %w", err)
	}

	// Parse response
	var mrData map[string]interface{}
	if err := json.Unmarshal(resp, &mrData); err != nil {
		return nil, fmt.Errorf("failed to parse MR response: %w", err)
	}

	mr := &MergeRequest{
		Number:       int(mrData["iid"].(float64)),
		Title:        mrData["title"].(string),
		Description:  mrData["description"].(string),
		SourceBranch: mrData["source_branch"].(string),
		TargetBranch: mrData["target_branch"].(string),
		State:        mrData["state"].(string),
		URL:          mrData["web_url"].(string),
		WebURL:       mrData["web_url"].(string),
	}

	if author, ok := mrData["author"].(map[string]interface{}); ok {
		mr.Author = author["username"].(string)
	}

	return mr, nil
}

// createGitHubPR creates a pull request on GitHub
func createGitHubPR(repoPath, sourceBranch, targetBranch, title, description string, auth *ForgeAuth) (*MergeRequest, error) {
	// Get owner/repo from remote
	owner, repo := getGitHubOwnerRepo(repoPath)
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("could not determine owner/repo from git remote")
	}

	// Build API URL
	apiURL := auth.APIURL
	if apiURL == "" {
		apiURL = "https://api.github.com"
	}
	url := fmt.Sprintf("%s/repos/%s/%s/pulls", apiURL, owner, repo)

	// Build request body
	body := map[string]interface{}{
		"head":  sourceBranch,
		"base":  targetBranch,
		"title": title,
		"body":  description,
	}

	// Assign to current user
	if login, err := getCurrentGitHubLogin(apiURL, auth.AuthToken); err == nil {
		body["assignees"] = []string{login}
	}

	// Make request
	resp, err := makeGitHubRequest("POST", url, auth.AuthToken, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create PR: %w", err)
	}

	// Parse response
	var prData map[string]interface{}
	if err := json.Unmarshal(resp, &prData); err != nil {
		return nil, fmt.Errorf("failed to parse PR response: %w", err)
	}

	mr := &MergeRequest{
		Number:       int(prData["number"].(float64)),
		Title:        prData["title"].(string),
		Description:  prData["body"].(string),
		SourceBranch: prData["head"].(map[string]interface{})["ref"].(string),
		TargetBranch: prData["base"].(map[string]interface{})["ref"].(string),
		State:        prData["state"].(string),
		URL:          prData["html_url"].(string),
		WebURL:       prData["html_url"].(string),
	}

	if user, ok := prData["user"].(map[string]interface{}); ok {
		mr.Author = user["login"].(string)
	}

	return mr, nil
}

// MRContent holds a generated MR title and description.
type MRContent struct {
	Title       string
	Description string
}

// GenerateMRContent generates an intelligent MR title and description using Claude.
// It collects the commit log and diff stat between baseBranch and HEAD, then
// asks Claude to produce a concise title and a structured description.
// Falls back to branch-name-based defaults if Claude is unavailable.
func GenerateMRContent(repoPath, baseBranch string) (*MRContent, error) {
	if baseBranch == "" {
		baseBranch = "main"
	}

	// Collect commit log
	logCmd := exec.Command("git", "-C", repoPath, "log",
		"--oneline", "--no-decorate",
		fmt.Sprintf("%s..HEAD", baseBranch))
	logOut, err := logCmd.Output()
	if err != nil || strings.TrimSpace(string(logOut)) == "" {
		// Nothing to describe
		return fallbackMRContent(repoPath, baseBranch), nil
	}
	commits := strings.TrimSpace(string(logOut))

	// Collect diff stat (file-level summary, not full diff)
	statCmd := exec.Command("git", "-C", repoPath, "diff", "--stat",
		fmt.Sprintf("%s..HEAD", baseBranch))
	statOut, _ := statCmd.Output()
	stat := strings.TrimSpace(string(statOut))

	prompt := fmt.Sprintf(`You are writing a merge request for a software project.

Commits being merged (newest first):
%s

Diff summary:
%s

Generate a merge request title and description. Respond with ONLY valid JSON in this exact format:
{"title":"<short imperative title, max 72 chars>","description":"<markdown description with ## Summary and ## Changes sections>"}

Rules:
- Title must be a short imperative sentence (e.g. "Add retry logic for flaky tests")
- Description must use markdown with a ## Summary section (2-4 bullet points) and a ## Changes section listing key files/modules touched
- No filler text, no "This PR", no "This MR"
- Do not include any text outside the JSON`,
		commits, stat)

	content := callClaudeForMR(repoPath, prompt)
	if content != nil {
		return content, nil
	}
	return fallbackMRContent(repoPath, baseBranch), nil
}

// callClaudeForMR invokes claude --print and parses the JSON response.
func callClaudeForMR(repoPath, prompt string) *MRContent {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", "--model", "haiku", "--print",
		"--dangerously-skip-permissions", "-p", prompt)
	cmd.Dir = repoPath

	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil
	}

	raw := strings.TrimSpace(out.String())
	// Extract JSON object in case Claude wraps it in markdown fences
	if i := strings.Index(raw, "{"); i >= 0 {
		if j := strings.LastIndex(raw, "}"); j > i {
			raw = raw[i : j+1]
		}
	}

	var result struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil || result.Title == "" {
		return nil
	}
	return &MRContent{Title: result.Title, Description: result.Description}
}

// fallbackMRContent generates a basic title/description from the branch name.
func fallbackMRContent(repoPath, baseBranch string) *MRContent {
	branchCmd := exec.Command("git", "-C", repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	branchOut, _ := branchCmd.Output()
	branch := strings.TrimSpace(string(branchOut))
	if branch == "" || branch == "HEAD" {
		branch = "feature"
	}
	// Turn branch name into a readable title (replace - and _ with spaces)
	title := strings.NewReplacer("-", " ", "_", " ", "/", ": ").Replace(branch)
	return &MRContent{
		Title:       title,
		Description: fmt.Sprintf("## Summary\n\nMerges `%s` into `%s`.", branch, baseBranch),
	}
}

// GenerateMRTitle generates a title for a merge request from commits
func GenerateMRTitle(repoPath, sourceBranch, targetBranch string) (string, error) {
	commits, err := GetCommitsBetween(repoPath, targetBranch, sourceBranch)
	if err != nil {
		return "", err
	}

	if len(commits) == 0 {
		return fmt.Sprintf("Merge %s into %s", sourceBranch, targetBranch), nil
	}

	// Use the first commit message as the title
	firstCommit := commits[0]
	title := firstCommit.Subject

	// Add count if multiple commits
	if len(commits) > 1 {
		title = fmt.Sprintf("%s (+%d more)", title, len(commits)-1)
	}

	return title, nil
}

// GenerateMRDescription generates a description for a merge request
func GenerateMRDescription(repoPath, sourceBranch, targetBranch string) (string, error) {
	commits, err := GetCommitsBetween(repoPath, targetBranch, sourceBranch)
	if err != nil {
		return "", err
	}

	if len(commits) == 0 {
		return "", nil
	}

	var desc strings.Builder
	desc.WriteString("## Summary\n\n")

	for _, commit := range commits {
		desc.WriteString(fmt.Sprintf("- %s (%s)\n", commit.Subject, commit.SHA[:7]))
	}

	// Add diff stats
	diff, err := GetBranchDiff(repoPath, targetBranch, sourceBranch)
	if err == nil {
		desc.WriteString("\n## Changes\n\n")
		desc.WriteString(fmt.Sprintf("- %d files changed\n", diff.FilesChanged))
		desc.WriteString(fmt.Sprintf("- %d additions, %d deletions\n", diff.TotalAdditions, diff.TotalDeletions))
	}

	return desc.String(), nil
}

// GetCommitsBetween returns commits between two branches
func GetCommitsBetween(repoPath, fromBranch, toBranch string) ([]CommitInfo, error) {
	// Simple implementation using git log
	// In production, use git API for better formatting
	_ = fromBranch // suppress unused warning
	_ = toBranch
	_ = repoPath

	// Placeholder - would use git CLI
	return []CommitInfo{}, nil
}

// CommitInfo holds commit information
type CommitInfo struct {
	SHA     string
	Subject string
	Author  string
	Date    string
}

// Helper functions
func getProjectPath(repoPath string) string {
	// Get remote URL and extract project path
	cmd := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	url := strings.TrimSpace(string(output))
	url = strings.TrimPrefix(url, "https://gitlab.com/")
	url = strings.TrimPrefix(url, "git@gitlab.com:")
	url = strings.TrimSuffix(url, ".git")

	return url
}

func getGitHubOwnerRepo(repoPath string) (string, string) {
	cmd := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin")
	output, err := cmd.Output()
	if err != nil {
		return "", ""
	}

	url := strings.TrimSpace(string(output))
	url = strings.TrimPrefix(url, "https://github.com/")
	url = strings.TrimPrefix(url, "git@github.com:")
	url = strings.TrimSuffix(url, ".git")

	parts := strings.Split(url, "/")
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return "", ""
}

func encodeProjectPath(path string) string {
	return strings.ReplaceAll(path, "/", "%2F")
}

func makeGitLabRequest(method, url, token string, body map[string]interface{}) ([]byte, error) {
	var req *http.Request
	var err error
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		req, err = http.NewRequest(method, url, strings.NewReader(string(jsonBody)))
	} else {
		req, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		return nil, err
	}

	req.Header.Set("PRIVATE-TOKEN", token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	buf := new(strings.Builder)
	_, _ = io.Copy(buf, resp.Body)
	respBody := buf.String()

	if resp.StatusCode == http.StatusConflict {
		return nil, ErrMRAlreadyExists
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GitLab API error: %d", resp.StatusCode)
	}

	return []byte(respBody), nil
}

func makeGitHubRequest(method, url, token string, body map[string]interface{}) ([]byte, error) {
	var req *http.Request
	var err error
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		req, err = http.NewRequest(method, url, strings.NewReader(string(jsonBody)))
	} else {
		req, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	buf := new(strings.Builder)
	_, _ = io.Copy(buf, resp.Body)
	respBody := buf.String()

	if resp.StatusCode == http.StatusConflict {
		return nil, ErrMRAlreadyExists
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GitHub API error: %d", resp.StatusCode)
	}

	return []byte(respBody), nil
}
