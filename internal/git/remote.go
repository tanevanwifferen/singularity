package git

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// RemoteProvider represents the type of git hosting provider
type RemoteProvider string

const (
	ProviderGitHub  RemoteProvider = "github"
	ProviderGitLab  RemoteProvider = "gitlab"
	ProviderGitea   RemoteProvider = "gitea"
	ProviderUnknown RemoteProvider = "unknown"
)

// ForgeOverrideConfigKey pins the provider for a single repository:
//
//	git config singularity.forge gitea
//
// This is the escape hatch for self-hosted Gitea/Forgejo instances on domains
// that look like nothing in particular (git.example.com).
const ForgeOverrideConfigKey = "singularity.forge"

// ForgeOverrideEnv pins the provider for every repo in this process. The
// per-repo git config wins over it, since it is the more specific statement.
const ForgeOverrideEnv = "SINGULARITY_FORGE"

// knownGiteaMarkers are host substrings that identify a Gitea or Forgejo
// remote without asking anyone. codeberg.org and gitea.com are the two big
// public instances; the bare "gitea"/"forgejo" markers cover the usual
// self-hosted naming (gitea.example.com), at the same confidence level as the
// existing "gitlab" substring check.
var knownGiteaMarkers = []string{
	"codeberg.org",
	"gitea.com",
	"gitea.io",
	"forgejo.org",
	"gitea",
	"forgejo",
}

// originRemoteURL returns the origin remote URL for a repo, or "" if there is
// none. Seam for tests.
var originRemoteURL = func(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// gitConfigGet reads a git config key for a repo, or "" if unset. Seam for
// tests.
var gitConfigGet = func(repoPath, key string) string {
	cmd := exec.Command("git", "-C", repoPath, "config", "--get", key)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// DetectRemoteProvider detects which forge hosts a repository's origin remote.
//
// Precedence, highest first:
//
//  1. the `singularity.forge` git config key (per-repo pin)
//  2. the SINGULARITY_FORGE environment variable (process-wide pin)
//  3. known host substrings in the origin URL — github.com and gitlab first,
//     exactly as before, then the Gitea/Forgejo markers
//  4. a probe of the origin host: a configured tea login (local, free), then
//     one short unauthenticated GET of the Gitea version endpoint
//
// Anything still unresolved is ProviderUnknown; the probe has a hard timeout
// and never blocks detection. Steps 3 and 4 leave GitHub and GitLab remotes
// untouched, so their detection behaves exactly as it did before Gitea existed.
func DetectRemoteProvider(repoPath string) RemoteProvider {
	if p := forgeOverride(repoPath); p != ProviderUnknown {
		return p
	}
	url := originRemoteURL(repoPath)
	if url == "" {
		return ProviderUnknown
	}
	return providerFromURL(url)
}

// forgeOverride returns the explicitly pinned provider for a repo, or
// ProviderUnknown when the user has pinned nothing.
func forgeOverride(repoPath string) RemoteProvider {
	if p := parseForgeOverride(gitConfigGet(repoPath, ForgeOverrideConfigKey)); p != ProviderUnknown {
		return p
	}
	return parseForgeOverride(os.Getenv(ForgeOverrideEnv))
}

// parseForgeOverride maps a user-supplied provider name onto a RemoteProvider,
// accepting both the forge name and its CLI name.
func parseForgeOverride(value string) RemoteProvider {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "github", "gh":
		return ProviderGitHub
	case "gitlab", "glab":
		return ProviderGitLab
	case "gitea", "forgejo", "tea":
		return ProviderGitea
	default:
		return ProviderUnknown
	}
}

// providerFromURL applies the host-substring layer and, failing that, the
// probe layer to a remote URL.
func providerFromURL(url string) RemoteProvider {
	if strings.Contains(url, "github.com") {
		return ProviderGitHub
	}
	if strings.Contains(url, "gitlab.com") || strings.Contains(url, "gitlab") {
		return ProviderGitLab
	}
	if isKnownGiteaURL(url) {
		return ProviderGitea
	}
	scheme, host := schemeAndHost(url)
	if host != "" && probeGiteaHost(scheme, host) {
		return ProviderGitea
	}
	return ProviderUnknown
}

// isKnownGiteaURL reports whether the remote URL names a host we already know
// to be Gitea or Forgejo.
func isKnownGiteaURL(url string) bool {
	lower := strings.ToLower(url)
	for _, marker := range knownGiteaMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// schemeAndHost splits the transport scheme and hostname out of a git remote
// URL. SSH remotes report "https" so the probe still targets the web API.
func schemeAndHost(rawURL string) (string, string) {
	_, host, _ := splitRemoteURL(rawURL)
	scheme := "https"
	if strings.HasPrefix(strings.ToLower(rawURL), "http://") {
		scheme = "http"
	}
	return scheme, host
}

// hostFromURL returns just the lowercased hostname of a git remote URL.
func hostFromURL(rawURL string) string {
	_, host, _ := splitRemoteURL(rawURL)
	return host
}

// parseRemoteURL splits a git remote URL into host, owner and repository name.
// Any component it cannot determine comes back empty.
func parseRemoteURL(rawURL string) (host, owner, repo string) {
	_, host, path := splitRemoteURL(rawURL)
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		return host, "", ""
	}
	// A Gitea path is always <owner>/<repo>; anything deeper is a subgroup
	// style URL we do not support, so take the first and last segments.
	return host, parts[0], parts[len(parts)-1]
}

// splitRemoteURL normalises the three remote URL shapes git accepts —
// https://host/path, ssh://user@host:port/path and the scp-style
// user@host:path — into (userinfo, host, path).
//
// An http(s) port stays part of the host, because self-hosted Gitea on :3000
// needs it to be reachable. An ssh:// port does not: it addresses the SSH
// daemon, not the web API we probe.
func splitRemoteURL(rawURL string) (user, host, path string) {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return "", "", ""
	}
	s = strings.TrimSuffix(s, ".git")

	scheme := ""
	if idx := strings.Index(s, "://"); idx >= 0 {
		scheme = strings.ToLower(s[:idx])
		s = s[idx+3:]
	}

	// Split off userinfo.
	if idx := strings.LastIndex(s, "@"); idx >= 0 {
		user = s[:idx]
		s = s[idx+1:]
	}

	if scheme == "" {
		// scp-style: ':' separates host from path and there is no port.
		if idx := strings.Index(s, ":"); idx >= 0 {
			return user, strings.ToLower(s[:idx]), strings.Trim(s[idx+1:], "/")
		}
		return user, strings.ToLower(s), ""
	}

	hostport := s
	if idx := strings.Index(s, "/"); idx >= 0 {
		hostport, path = s[:idx], strings.Trim(s[idx+1:], "/")
	}
	hostport = strings.ToLower(hostport)
	if scheme == "ssh" || scheme == "git" {
		if idx := strings.Index(hostport, ":"); idx >= 0 {
			hostport = hostport[:idx]
		}
	}
	return user, hostport, path
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

// CreateMergeRequestCLI creates a merge request using the appropriate CLI tool
// (gh, glab or tea). It generates an intelligent title and description via
// Claude before calling the CLI.
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
	case ProviderGitea:
		// tea defaults --head to the current branch, but being explicit keeps
		// the command reproducible from the caller's point of view. An empty
		// baseBranch lets tea fall back to the repo's default branch.
		url, _, err := createGiteaPull(repoPath, currentBranchName(repoPath), baseBranch,
			content.Title, content.Description)
		if err != nil {
			return nil, err
		}
		result.URL = url
		return result, nil
	default:
		return nil, fmt.Errorf("unknown provider, cannot create MR")
	}
}

// currentBranchName returns the checked-out branch, or "" on a detached HEAD.
func currentBranchName(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(output))
	if branch == "HEAD" {
		return ""
	}
	return branch
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

// ProviderStatus reports the provider detected for a repo together with the
// state of the CLI that drives it. Everything here is answered from local
// state — git config, $PATH and each CLI's own credential store — so the
// report stays cheap enough to run on every `singl forge provider`.
type ProviderStatus struct {
	Provider     RemoteProvider
	CLI          string
	CLIInstalled bool
	HasLogin     bool
	Host         string
	User         string
	Hint         string
}

// DetectProviderStatus resolves the provider for a repo and describes whether
// its CLI is usable, with the exact command to run when it is not.
func DetectProviderStatus(repoPath string) *ProviderStatus {
	provider := DetectRemoteProvider(repoPath)
	st := &ProviderStatus{
		Provider: provider,
		Host:     hostFromURL(originRemoteURL(repoPath)),
	}

	switch provider {
	case ProviderGitHub:
		st.CLI = "gh"
		st.CLIInstalled = hasForgeCLI("gh")
		if !st.CLIInstalled {
			st.Hint = "gh is not installed — install GitHub's CLI (https://cli.github.com) to create pull requests"
			return st
		}
		// `gh auth token` reads the local credential store; unlike
		// `gh auth status` it does not call the API.
		if res := runForgeCommand(repoPath, "gh", "auth", "token"); res.Err == nil &&
			strings.TrimSpace(res.Stdout) != "" {
			st.HasLogin = true
		} else {
			st.Hint = "gh is not authenticated — run: gh auth login"
		}
	case ProviderGitLab:
		st.CLI = "glab"
		st.CLIInstalled = hasForgeCLI("glab")
		if !st.CLIInstalled {
			st.Hint = "glab is not installed — install GitLab's CLI (https://gitlab.com/gitlab-org/cli) to create merge requests"
			return st
		}
		if auth, _ := detectGitLabAuth(OriginHost(repoPath)); auth != nil {
			st.HasLogin = true
			st.User = auth.Username
		} else {
			st.Hint = "glab is not authenticated — run: glab auth login"
		}
	case ProviderGitea:
		gitea := GiteaCLIStatus(repoPath)
		st.CLI = teaBin
		st.CLIInstalled = gitea.Installed
		st.HasLogin = gitea.HasLogin
		st.User = gitea.User
		st.Hint = gitea.Hint
		if gitea.Host != "" {
			st.Host = gitea.Host
		}
	default:
		st.Hint = "could not tell which forge hosts this repo — pin it with " +
			"`git config " + ForgeOverrideConfigKey + " github|gitlab|gitea` or the " +
			ForgeOverrideEnv + " environment variable"
	}
	return st
}
