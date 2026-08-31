package git

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// PipelineStatus represents the status of a CI/CD pipeline
type PipelineStatus string

const (
	PipelinePending  PipelineStatus = "pending"
	PipelineRunning  PipelineStatus = "running"
	PipelineSuccess  PipelineStatus = "success"
	PipelineFailed   PipelineStatus = "failed"
	PipelineCanceled PipelineStatus = "canceled"
	PipelineSkipped  PipelineStatus = "skipped"
)

func (s PipelineStatus) String() string {
	return string(s)
}

// IsTerminal returns true if the pipeline is in a terminal state
func (s PipelineStatus) IsTerminal() bool {
	return s == PipelineSuccess || s == PipelineFailed || s == PipelineCanceled || s == PipelineSkipped
}

// Pipeline represents a CI/CD pipeline
type Pipeline struct {
	ID        int64          `json:"id"`
	Ref       string         `json:"ref"`
	SHA       string         `json:"sha"`
	Status    PipelineStatus `json:"status"`
	WebURL    string         `json:"web_url"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Duration  int            `json:"duration"` // seconds
	Jobs      []Job          `json:"jobs"`
}

// Job represents a CI/CD job
type Job struct {
	ID     int64          `json:"id"`
	Name   string         `json:"name"`
	Status PipelineStatus `json:"status"`
	Stage  string         `json:"stage"`
	WebURL string         `json:"web_url"`
}

// PipelineInfo holds pipeline information for a branch
type PipelineInfo struct {
	Branch      string         `json:"branch"`
	Pipeline    *Pipeline      `json:"pipeline"`
	HasPipeline bool           `json:"has_pipeline"`
	Status      PipelineStatus `json:"status"`
	// Detail explains why there is no pipeline when that is not simply
	// "nothing has run yet" — e.g. the instance has no Actions API, or the
	// forge CLI is not authenticated. Empty on the happy path.
	Detail string `json:"detail,omitempty"`
}

// GetPipelineStatus fetches the pipeline status for a branch
func GetPipelineStatus(repoPath, branch string) (*PipelineInfo, error) {
	// Gitea is resolved per repo: tea holds its own per-host credentials, so
	// the ambient gh/glab auth cannot answer for a Gitea remote.
	if DetectRemoteProvider(repoPath) == ProviderGitea {
		return giteaPipelineStatus(repoPath, branch)
	}

	// Detect forge type
	auth, err := DetectForgeAuth()
	if err != nil {
		return nil, fmt.Errorf("failed to detect forge auth: %w", err)
	}

	if !auth.Valid {
		return nil, fmt.Errorf("no valid forge authentication found")
	}

	info := &PipelineInfo{
		Branch:      branch,
		HasPipeline: false,
	}

	if auth.IsGitLab() {
		pipeline, err := getGitLabPipeline(repoPath, branch, auth)
		if err != nil {
			return info, nil // Return empty info on error
		}
		info.Pipeline = pipeline
		info.HasPipeline = pipeline != nil
		if pipeline != nil {
			info.Status = pipeline.Status
		}
		return info, nil
	}

	if auth.IsGitHub() {
		pipeline, err := getGitHubWorkflow(repoPath, branch, auth)
		if err != nil {
			return info, nil
		}
		info.Pipeline = pipeline
		info.HasPipeline = pipeline != nil
		if pipeline != nil {
			info.Status = pipeline.Status
		}
		return info, nil
	}

	return info, nil
}

// getGitLabPipeline fetches GitLab pipeline status
func getGitLabPipeline(repoPath, branch string, auth *ForgeAuth) (*Pipeline, error) {
	projectPath := getProjectPath(repoPath)
	if projectPath == "" {
		return nil, fmt.Errorf("could not determine project path")
	}

	apiURL := auth.APIURL
	if apiURL == "" {
		apiURL = "https://gitlab.com/api/v4"
	}
	url := fmt.Sprintf("%s/projects/%s/pipelines?ref=%s&per_page=1",
		apiURL, encodeProjectPath(projectPath), branch)

	var pipelines []map[string]interface{}
	if err := gitLabAPIGet(url, auth.AuthToken, &pipelines); err != nil {
		return nil, err
	}

	if len(pipelines) == 0 {
		return nil, nil
	}

	pipelineData := pipelines[0]
	pipeline := &Pipeline{
		ID:     int64(pipelineData["id"].(float64)),
		Ref:    pipelineData["ref"].(string),
		SHA:    pipelineData["sha"].(string),
		Status: PipelineStatus(pipelineData["status"].(string)),
		WebURL: pipelineData["web_url"].(string),
	}

	// Parse dates
	if created, ok := pipelineData["created_at"].(string); ok {
		pipeline.CreatedAt, _ = time.Parse(time.RFC3339, created)
	}
	if updated, ok := pipelineData["updated_at"].(string); ok {
		pipeline.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	}

	// Get jobs
	jobs, err := getGitLabJobs(apiURL, projectPath, pipeline.ID, auth.AuthToken)
	if err == nil {
		pipeline.Jobs = jobs
	}

	return pipeline, nil
}

// getGitLabJobs fetches jobs for a GitLab pipeline
func getGitLabJobs(apiURL, projectPath string, pipelineID int64, token string) ([]Job, error) {
	url := fmt.Sprintf("%s/projects/%s/pipelines/%d/jobs",
		apiURL, encodeProjectPath(projectPath), pipelineID)

	var jobsData []map[string]interface{}
	if err := gitLabAPIGet(url, token, &jobsData); err != nil {
		return nil, err
	}

	var jobs []Job
	for _, j := range jobsData {
		job := Job{
			ID:     int64(j["id"].(float64)),
			Name:   j["name"].(string),
			Status: PipelineStatus(j["status"].(string)),
			Stage:  j["stage"].(string),
		}
		if webURL, ok := j["web_url"].(string); ok {
			job.WebURL = webURL
		}
		jobs = append(jobs, job)
	}

	return jobs, nil
}

// getGitHubWorkflow fetches GitHub workflow status
func getGitHubWorkflow(repoPath, branch string, auth *ForgeAuth) (*Pipeline, error) {
	owner, repo := getGitHubOwnerRepo(repoPath)
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("could not determine owner/repo")
	}

	apiURL := auth.APIURL
	if apiURL == "" {
		apiURL = "https://api.github.com"
	}
	url := fmt.Sprintf("%s/repos/%s/%s/actions/runs?branch=%s&per_page=1",
		apiURL, owner, repo, branch)

	var response struct {
		WorkflowRuns []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			HeadSHA    string `json:"head_sha"`
			HTMLURL    string `json:"html_url"`
			CreatedAt  string `json:"created_at"`
			UpdatedAt  string `json:"updated_at"`
		} `json:"workflow_runs"`
	}

	if err := gitHubAPIGet(url, auth.AuthToken, &response); err != nil {
		return nil, err
	}

	if len(response.WorkflowRuns) == 0 {
		return nil, nil
	}

	run := response.WorkflowRuns[0]
	status := PipelineStatus(run.Status)
	if status == PipelineSuccess || status == PipelineRunning {
		// GitHub uses 'conclusion' for completed runs
		if run.Conclusion != "" {
			status = PipelineStatus(run.Conclusion)
		}
	}

	pipeline := &Pipeline{
		ID:     run.ID,
		Ref:    branch,
		SHA:    run.HeadSHA,
		Status: status,
		WebURL: run.HTMLURL,
		Jobs:   []Job{{Name: run.Name, Status: status}},
	}

	pipeline.CreatedAt, _ = time.Parse(time.RFC3339, run.CreatedAt)
	pipeline.UpdatedAt, _ = time.Parse(time.RFC3339, run.UpdatedAt)

	return pipeline, nil
}

// RetryPipeline retries a failed pipeline
func RetryPipeline(repoPath, branch string) error {
	if DetectRemoteProvider(repoPath) == ProviderGitea {
		return retryGiteaPipeline(repoPath, branch)
	}

	auth, err := DetectForgeAuth()
	if err != nil || !auth.Valid {
		return fmt.Errorf("no valid forge authentication")
	}

	if auth.IsGitLab() {
		projectPath := getProjectPath(repoPath)
		apiURL := auth.APIURL
		if apiURL == "" {
			apiURL = "https://gitlab.com/api/v4"
		}

		// Get pipeline ID first
		pipeline, err := getGitLabPipeline(repoPath, branch, auth)
		if err != nil || pipeline == nil {
			return fmt.Errorf("no pipeline found for branch %s", branch)
		}

		url := fmt.Sprintf("%s/projects/%s/pipelines/%d/retry",
			apiURL, encodeProjectPath(projectPath), pipeline.ID)

		if _, err = makeGitLabRequest("POST", url, auth.AuthToken, nil); err != nil {
			return fmt.Errorf("retry gitlab pipeline: %w", err)
		}
		return nil
	}

	if auth.IsGitHub() {
		owner, repo := getGitHubOwnerRepo(repoPath)
		apiURL := auth.APIURL
		if apiURL == "" {
			apiURL = "https://api.github.com"
		}

		// Get workflow run ID first
		pipeline, err := getGitHubWorkflow(repoPath, branch, auth)
		if err != nil || pipeline == nil {
			return fmt.Errorf("no pipeline found for branch %s", branch)
		}

		url := fmt.Sprintf("%s/repos/%s/%s/actions/runs/%d/rerun",
			apiURL, owner, repo, pipeline.ID)

		if _, err = makeGitHubRequest("POST", url, auth.AuthToken, nil); err != nil {
			return fmt.Errorf("retry github pipeline: %w", err)
		}
		return nil
	}

	return fmt.Errorf("unsupported forge type")
}

// GetBranchPipelineStatuses gets pipeline status for all branches
func GetBranchPipelineStatuses(repoPath string, branches []BranchInfo) (map[string]*PipelineInfo, error) {
	statuses := make(map[string]*PipelineInfo)

	repo, err := OpenRepo(repoPath)
	if err != nil {
		return nil, err
	}

	for _, branch := range branches {
		info, err := GetPipelineStatus(repoPath, branch.Name)
		if err != nil && info == nil {
			info = &PipelineInfo{Branch: branch.Name}
		}
		// A non-nil info alongside an error still carries Detail — the
		// explanation is more useful to the user than a blank row.
		statuses[branch.Name] = info
	}

	// Also check current branch
	if repo.CurrentBranch != "" {
		info, err := GetPipelineStatus(repoPath, repo.CurrentBranch)
		if err == nil && info != nil {
			statuses[repo.CurrentBranch] = info
		}
	}

	return statuses, nil
}

// FormatPipelineStatus formats a pipeline status for display
func FormatPipelineStatus(status PipelineStatus) string {
	switch status {
	case PipelineSuccess:
		return "✓ passed"
	case PipelineFailed:
		return "✗ failed"
	case PipelineRunning:
		return "● running"
	case PipelinePending:
		return "○ pending"
	case PipelineCanceled:
		return "⊘ canceled"
	case PipelineSkipped:
		return "⊝ skipped"
	default:
		return "? unknown"
	}
}

// PipelineStatusColor returns the lipgloss color for a status
func PipelineStatusColor(status PipelineStatus) string {
	switch status {
	case PipelineSuccess:
		return "86" // green
	case PipelineFailed:
		return "196" // red
	case PipelineRunning:
		return "220" // yellow
	case PipelinePending:
		return "244" // gray
	default:
		return "241"
	}
}

// Helper functions
func gitLabAPIGet(url, token string, result interface{}) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("gitlab api: create request: %w", err)
	}
	req.Header.Set("PRIVATE-TOKEN", token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("gitlab api: %w", err)
	}
	defer resp.Body.Close()

	return json.NewDecoder(resp.Body).Decode(result)
}

func gitHubAPIGet(url, token string, result interface{}) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("github api: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()

	return json.NewDecoder(resp.Body).Decode(result)
}
