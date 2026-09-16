package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/oneshot"
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
	// Gitea is resolved from the repo, not from the ambient forge auth: tea
	// carries its own per-host credentials, and a globally configured gh or
	// glab says nothing about a Gitea remote.
	if DetectRemoteProvider(repoPath) == ProviderGitea {
		return createGiteaPR(repoPath, sourceBranch, targetBranch, title, description)
	}

	// Detect forge credentials, preferring the repo's origin host so
	// self-hosted forges resolve to their own token.
	auth, err := DetectForgeAuthForRepo(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to detect forge auth: %w", err)
	}

	if !auth.Valid {
		return nil, noForgeAuthError(auth)
	}

	if auth.IsGitLab() {
		return createGitLabMR(repoPath, sourceBranch, targetBranch, title, description, reviewers, auth)
	}
	if auth.IsGitHub() {
		return createGitHubPR(repoPath, sourceBranch, targetBranch, title, description, auth)
	}

	return nil, fmt.Errorf("unsupported forge type: %s", auth.Type)
}

// createGiteaPR opens a pull request on a Gitea or Forgejo instance through
// tea. tea has no machine-readable output for `pulls create`, so the returned
// MergeRequest is assembled from the request plus the URL tea prints.
func createGiteaPR(repoPath, sourceBranch, targetBranch, title, description string) (*MergeRequest, error) {
	url, gr, err := createGiteaPull(repoPath, sourceBranch, targetBranch, title, description)
	if err != nil {
		return nil, err
	}
	return &MergeRequest{
		Number:       giteaPullNumber(url),
		Title:        title,
		Description:  description,
		SourceBranch: sourceBranch,
		TargetBranch: targetBranch,
		Author:       gr.Login.User,
		State:        "open",
		URL:          url,
		WebURL:       url,
	}, nil
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
	Title       string `json:"title"`
	Description string `json:"description"`
}

// GenerateMRContent generates an intelligent MR title and description.
// It collects the commit log and diff stat between baseBranch and
// sourceBranch, then asks the configured coding-agent CLI for a concise
// title and a structured description. Falls back to branch-name-based
// defaults if it is unavailable.
//
// sourceBranch may be "" to mean "whatever is currently checked out"
// (resolved as "HEAD"), which is what callers use when they are about to
// create an MR/PR from the branch they already have checked out. Callers
// that let a user pick a source branch that isn't necessarily checked out
// (e.g. the PR TUI) must pass that branch name explicitly so the diff
// describes the right branch instead of silently describing HEAD.
func GenerateMRContent(repoPath, sourceBranch, baseBranch string) (*MRContent, error) {
	if baseBranch == "" {
		baseBranch = "main"
	}
	sourceRef := sourceBranch
	if sourceRef == "" {
		sourceRef = "HEAD"
	}

	// Fetch origin to ensure we compare against the latest remote state
	_ = exec.Command("git", "-C", repoPath, "fetch", "origin", baseBranch).Run()

	// Use origin/branch to compare against the remote, not a potentially stale local ref
	originBase := fmt.Sprintf("origin/%s", baseBranch)

	// Collect commit log: commits on sourceRef not yet on the base branch
	logCmd := exec.Command("git", "-C", repoPath, "log",
		"--oneline", "--no-decorate",
		fmt.Sprintf("%s..%s", originBase, sourceRef))
	logOut, err := logCmd.Output()
	if err != nil || strings.TrimSpace(string(logOut)) == "" {
		// Nothing to describe
		return fallbackMRContent(repoPath, sourceBranch, baseBranch), nil
	}
	commits := strings.TrimSpace(string(logOut))

	// Collect diff stat using three-dot notation to compare from the merge base,
	// not the tip of the base branch. This avoids showing unrelated changes on
	// the base branch as removals, keeping the scope accurate.
	statCmd := exec.Command("git", "-C", repoPath, "diff", "--stat",
		fmt.Sprintf("%s...%s", originBase, sourceRef))
	statOut, _ := statCmd.Output()
	stat := strings.TrimSpace(string(statOut))

	// GenerateMRTitle and GenerateMRDescription both land here for the same
	// branch comparison, back-to-back. Cache by the commit log + diff stat
	// (the actual inputs to the LLM prompt) so the second call reuses the
	// first call's oneshot answer instead of issuing a second LLM request
	// with its own, potentially divergent, title/description pair.
	cache := GetGlobalCache()
	key := mrContentCacheKey(repoPath, baseBranch, commits, stat)
	if cached, ok := cache.Get(key); ok {
		if content, ok := cached.(*MRContent); ok && content != nil {
			log.Printf("[mr] cache hit for content hash: %s", key)
			return content, nil
		}
	}

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

	content := callAgentForMR(repoPath, prompt)
	if content != nil {
		cache.Set(key, content)
		return content, nil
	}
	fallback := fallbackMRContent(repoPath, sourceBranch, baseBranch)
	cache.Set(key, fallback)
	return fallback, nil
}

// mrContentCacheKey generates a cache key from the repo, base branch, and the
// commit log + diff stat that feed the prompt (not the LLM answer, which is
// what we're trying to avoid recomputing).
func mrContentCacheKey(repoPath, baseBranch, commits, stat string) string {
	hash := sha256.Sum256([]byte(commits + "\x00" + stat))
	return cacheKey("mrcontent", repoPath, baseBranch, hex.EncodeToString(hash[:16]))
}

// callAgentForMR runs a one-shot prompt on the configured backend and parses
// the JSON response. Returns nil on any failure so the caller can fall back.
func callAgentForMR(repoPath, prompt string) *MRContent {
	raw, err := oneShotPrompt(context.Background(), oneshot.Request{
		Prompt:  prompt,
		Dir:     repoPath,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		log.Printf("[mr] agent generation failed: %v", err)
		return nil
	}

	// Extract JSON object in case the answer is wrapped in markdown fences
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
// It uses sourceBranch when given (the branch the caller actually asked
// about); only when sourceBranch is "" does it fall back to inspecting
// repoPath's checked-out HEAD.
func fallbackMRContent(repoPath, sourceBranch, baseBranch string) *MRContent {
	branch := sourceBranch
	if branch == "" {
		branchCmd := exec.Command("git", "-C", repoPath, "rev-parse", "--abbrev-ref", "HEAD")
		branchOut, _ := branchCmd.Output()
		branch = strings.TrimSpace(string(branchOut))
	}
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

// GenerateMRTitle generates a title for a merge request via the same
// oneshot-backed generator as GenerateMRContent, so the title and the
// description are always drawn from one real (or one fallback) source
// instead of a separate, dead code path. GenerateMRContent caches by commit
// log + diff stat, so calling this back-to-back with GenerateMRDescription
// (as callers typically do) issues only one oneshot LLM call, and both
// values come from the same generated MRContent. sourceBranch is passed
// through to GenerateMRContent so the diff describes the branch the caller
// actually asked about, not whatever happens to be checked out in repoPath.
func GenerateMRTitle(repoPath, sourceBranch, targetBranch string) (string, error) {
	content, err := GenerateMRContent(repoPath, sourceBranch, targetBranch)
	if err != nil {
		return "", err
	}
	return content.Title, nil
}

// GenerateMRDescription generates a description for a merge request via the
// same oneshot-backed generator as GenerateMRTitle. See GenerateMRTitle for
// how sourceBranch is used.
func GenerateMRDescription(repoPath, sourceBranch, targetBranch string) (string, error) {
	content, err := GenerateMRContent(repoPath, sourceBranch, targetBranch)
	if err != nil {
		return "", err
	}
	return content.Description, nil
}

// noForgeAuthError builds an actionable "no auth" error: which sources were
// checked (auth.Detail) and how to fix it.
func noForgeAuthError(auth *ForgeAuth) error {
	detail := ""
	if auth != nil && auth.Detail != "" {
		detail = " — checked: " + auth.Detail
	}
	return fmt.Errorf("no valid forge authentication found%s. Fix: `glab auth login --hostname <gitlab-host>`, `gh auth login`, or set GITLAB_TOKEN/GITHUB_TOKEN", detail)
}

// Helper functions

// getProjectPath extracts the "group/subgroup/repo" path from the repo's
// origin remote, for any forge host (not just gitlab.com).
func getProjectPath(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	_, path := ParseGitRemoteURL(string(output))
	return path
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
