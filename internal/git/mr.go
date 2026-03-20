package git

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
)

// MergeRequest represents a merge/pull request
type MergeRequest struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	SourceBranch string   `json:"source_branch"`
	TargetBranch string   `json:"target_branch"`
	Author      string    `json:"author"`
	State       string    `json:"state"`
	URL         string    `json:"url"`
	WebURL      string    `json:"web_url"`
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
		return createGitLabMR(repoPath, sourceBranch, targetBranch, title, description, auth)
	}
	if auth.IsGitHub() {
		return createGitHubPR(repoPath, sourceBranch, targetBranch, title, description, auth)
	}

	return nil, fmt.Errorf("unsupported forge type: %s", auth.Type)
}

// createGitLabMR creates a merge request on GitLab
func createGitLabMR(repoPath, sourceBranch, targetBranch, title, description string, auth *ForgeAuth) (*MergeRequest, error) {
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
	SHA      string
	Subject  string
	Author   string
	Date     string
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
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(method, url, strings.NewReader(string(jsonBody)))
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

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GitLab API error: %d", resp.StatusCode)
	}

	buf := new(strings.Builder)
	_, err = io.Copy(buf, resp.Body)
	if err != nil {
		return nil, err
	}

	return []byte(buf.String()), nil
}

func makeGitHubRequest(method, url, token string, body map[string]interface{}) ([]byte, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(method, url, strings.NewReader(string(jsonBody)))
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

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GitHub API error: %d", resp.StatusCode)
	}

	buf := new(strings.Builder)
	_, err = io.Copy(buf, resp.Body)
	if err != nil {
		return nil, err
	}

	return []byte(buf.String()), nil
}
