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

// ParseGitRemoteURL splits a git remote URL into its host and project path
// ("group/subgroup/repo", without the .git suffix). Supports the scp-like
// syntax (git@host:path), ssh:// and http(s):// URLs. Returns empty strings
// when the URL cannot be parsed.
func ParseGitRemoteURL(rawURL string) (host, path string) {
	url := strings.TrimSpace(rawURL)
	if url == "" {
		return "", ""
	}
	// Scheme form: ssh://git@host[:port]/path, https://host/path, git://host/path
	if i := strings.Index(url, "://"); i >= 0 {
		rest := url[i+3:]
		if at := strings.Index(rest, "@"); at >= 0 {
			rest = rest[at+1:]
		}
		slash := strings.Index(rest, "/")
		if slash < 0 {
			return rest, ""
		}
		host = rest[:slash]
		path = rest[slash+1:]
	} else if at := strings.Index(url, "@"); at >= 0 {
		// scp-like: git@host:group/repo.git
		rest := url[at+1:]
		colon := strings.Index(rest, ":")
		if colon < 0 {
			return rest, ""
		}
		host = rest[:colon]
		path = rest[colon+1:]
	} else {
		return "", ""
	}
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i] // strip port
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	return host, path
}

// OriginHost returns the hostname of a repo's origin remote ("gitlab.proxy.nl",
// "github.com", …), or "" when there is no parsable origin.
func OriginHost(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	host, _ := ParseGitRemoteURL(string(output))
	return host
}

// MRResult holds the URL and content of a created merge request.
type MRResult struct {
	URL     string     `json:"url"`
	Content *MRContent `json:"content,omitempty"`
}

// CreateMergeRequestCLI creates a merge request using the appropriate CLI tool (gh or glab).
// It generates an intelligent title and description via Claude before calling the CLI.
func CreateMergeRequestCLI(repoPath string, provider RemoteProvider, baseBranch string) (*MRResult, error) {
	content, err := GenerateMRContent(repoPath, baseBranch)
	if err != nil || content == nil {
		content = &MRContent{Title: "Merge feature branch", Description: ""}
	}

	result := &MRResult{Content: content}

	switch provider {
	case ProviderGitHub:
		cmd := exec.Command("gh", "pr", "create",
			"--title", content.Title,
			"--body", content.Description,
			"--assignee", "@me")
		cmd.Dir = repoPath
		output, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("gh pr create failed: %s", strings.TrimSpace(string(output)))
		}
		result.URL = extractURL(string(output))
		return result, nil
	case ProviderGitLab:
		cmd := exec.Command("glab", "mr", "create", "--yes",
			"--title", content.Title,
			"--description", content.Description,
			"--assignee", "@me")
		cmd.Dir = repoPath
		output, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("glab mr create failed: %s", strings.TrimSpace(string(output)))
		}
		result.URL = extractURL(string(output))
		return result, nil
	default:
		return nil, fmt.Errorf("unknown provider, cannot create MR")
	}
}

// extractURL extracts the first https:// URL from CLI output, falling back to
// the full trimmed output if none is found.
func extractURL(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "https://") {
			return line
		}
	}
	return strings.TrimSpace(output)
}
