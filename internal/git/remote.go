package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// RemoteProvider represents the type of git hosting provider
type RemoteProvider string

const (
	ProviderGitHub  RemoteProvider = "github"
	ProviderGitLab  RemoteProvider = "gitlab"
	ProviderUnknown RemoteProvider = "unknown"
)

// DetectRemoteProvider detects whether a repo is hosted on GitHub or GitLab
func DetectRemoteProvider(repoPath string) RemoteProvider {
	cmd := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin")
	output, err := cmd.Output()
	if err != nil {
		return ProviderUnknown
	}
	url := strings.TrimSpace(string(output))
	if strings.Contains(url, "github.com") {
		return ProviderGitHub
	}
	if strings.Contains(url, "gitlab.com") || strings.Contains(url, "gitlab") {
		return ProviderGitLab
	}
	return ProviderUnknown
}

// CreateMergeRequestCLI creates a merge request using the appropriate CLI tool (gh or glab).
// It generates an intelligent title and description via Claude before calling the CLI.
func CreateMergeRequestCLI(repoPath string, provider RemoteProvider, baseBranch string) (string, error) {
	content, err := GenerateMRContent(repoPath, baseBranch)
	if err != nil || content == nil {
		content = &MRContent{Title: "Merge feature branch", Description: ""}
	}

	switch provider {
	case ProviderGitHub:
		cmd := exec.Command("gh", "pr", "create",
			"--title", content.Title,
			"--body", content.Description,
			"--assignee", "@me")
		cmd.Dir = repoPath
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("gh pr create failed: %s", strings.TrimSpace(string(output)))
		}
		return strings.TrimSpace(string(output)), nil
	case ProviderGitLab:
		cmd := exec.Command("glab", "mr", "create", "--yes",
			"--title", content.Title,
			"--description", content.Description,
			"--assignee", "@me")
		cmd.Dir = repoPath
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("glab mr create failed: %s", strings.TrimSpace(string(output)))
		}
		return strings.TrimSpace(string(output)), nil
	default:
		return "", fmt.Errorf("unknown provider, cannot create MR")
	}
}
