package git

import (
	"os"
	"os/exec"
	"strings"
)

// ForgeType represents the type of code hosting service
type ForgeType int

const (
	ForgeUnknown ForgeType = iota
	ForgeGitHub
	ForgeGitLab
	ForgeGitea
)

// ForgeAuth holds authentication info for a forge
type ForgeAuth struct {
	Type      ForgeType `json:"type"`
	Username  string    `json:"username"`
	AuthToken string    `json:"auth_token"` // Token or OAuth token
	APIURL    string    `json:"api_url"`
	Valid     bool      `json:"valid"`
	// Hint is an actionable remedy shown to the user when the forge CLI is
	// missing or unauthenticated. Empty when everything is set up.
	Hint string `json:"hint,omitempty"`
}

// DetectForgeAuth checks for existing authentication with GitHub, GitLab or
// Gitea. Gitea is probed last so that an existing gh/glab setup — or the
// GITHUB_TOKEN/GITLAB_TOKEN environment variables — keeps winning exactly as
// it did before Gitea support was added.
func DetectForgeAuth() (*ForgeAuth, error) {
	// Try GitHub CLI first
	if auth := detectGitHubAuth(); auth != nil {
		return auth, nil
	}

	// Try GitLab CLI
	if auth := detectGitLabAuth(); auth != nil {
		return auth, nil
	}

	// Check environment variables as fallback
	if auth := detectEnvAuth(); auth != nil {
		return auth, nil
	}

	// Try the tea CLI (Gitea / Forgejo)
	if auth := detectGiteaAuth(); auth != nil {
		return auth, nil
	}

	return &ForgeAuth{Type: ForgeUnknown, Valid: false, Hint: forgeSetupHint()}, nil
}

// forgeSetupHint names the three CLIs a user can set up when none is found.
func forgeSetupHint() string {
	return "no forge CLI authenticated — run `gh auth login`, `glab auth login`, " +
		"or `tea logins add --name <host> --url https://<host> --token <token>`"
}

// detectGiteaAuth reports the Gitea login tea holds locally. The token stays
// in tea's own config: every Gitea call in this package goes through tea, so
// we deliberately never read it.
func detectGiteaAuth() *ForgeAuth {
	if !hasForgeCLI(teaBin) {
		return nil
	}
	login, ok := teaDefaultLogin()
	if !ok {
		return &ForgeAuth{Type: ForgeGitea, Valid: false, Hint: teaLoginHint("")}
	}
	apiURL := strings.TrimSuffix(strings.TrimSpace(login.URL), "/")
	if apiURL != "" {
		apiURL += "/api/v1"
	}
	return &ForgeAuth{
		Type:     ForgeGitea,
		Username: login.User,
		APIURL:   apiURL,
		Valid:    true,
	}
}

// detectGitHubAuth checks gh CLI authentication
func detectGitHubAuth() *ForgeAuth {
	// Check if gh is installed
	if _, err := exec.LookPath("gh"); err != nil {
		return nil
	}

	// Check auth status (we just need to ensure gh is authenticated)
	cmd := exec.Command("gh", "auth", "status", "--json", "user,hostname")
	_, err := cmd.Output()
	if err != nil {
		return nil
	}

	// Get current user
	userCmd := exec.Command("gh", "api", "user", "--jq", ".login")
	userOutput, err := userCmd.Output()
	if err != nil {
		return nil
	}

	username := strings.TrimSpace(string(userOutput))
	if username == "" {
		return nil
	}

	// Get auth token
	tokenCmd := exec.Command("gh", "auth", "token")
	tokenOutput, err := tokenCmd.Output()
	if err != nil {
		return nil
	}

	return &ForgeAuth{
		Type:      ForgeGitHub,
		Username:  username,
		AuthToken: strings.TrimSpace(string(tokenOutput)),
		APIURL:    "https://api.github.com",
		Valid:     true,
	}
}

// detectGitLabAuth checks glab CLI authentication
func detectGitLabAuth() *ForgeAuth {
	// Check if glab is installed
	if _, err := exec.LookPath("glab"); err != nil {
		return nil
	}

	// Check config for authentication
	cmd := exec.Command("glab", "config", "get", "username")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	username := strings.TrimSpace(string(output))
	if username == "" {
		return nil
	}

	// Get API token
	tokenCmd := exec.Command("glab", "config", "get", "gitlab_token")
	tokenOutput, err := tokenCmd.Output()
	if err != nil {
		return nil
	}

	token := strings.TrimSpace(string(tokenOutput))
	if token == "" {
		return nil
	}

	return &ForgeAuth{
		Type:      ForgeGitLab,
		Username:  username,
		AuthToken: token,
		APIURL:    "https://gitlab.com/api/v4",
		Valid:     true,
	}
}

// detectEnvAuth checks environment variables for forge authentication
func detectEnvAuth() *ForgeAuth {
	// Check GitHub token
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		username := os.Getenv("GITHUB_USERNAME")
		if username == "" {
			username = os.Getenv("GITHUB_USER")
		}
		return &ForgeAuth{
			Type:      ForgeGitHub,
			Username:  username,
			AuthToken: token,
			APIURL:    "https://api.github.com",
			Valid:     true,
		}
	}

	// Check GitLab token
	if token := os.Getenv("GITLAB_TOKEN"); token != "" {
		username := os.Getenv("GITLAB_USERNAME")
		if username == "" {
			username = os.Getenv("GITLAB_USER")
		}
		return &ForgeAuth{
			Type:      ForgeGitLab,
			Username:  username,
			AuthToken: token,
			APIURL:    os.Getenv("GITLAB_API_URL"),
			Valid:     true,
		}
	}

	return nil
}

// IsGitHub returns true if the auth is for GitHub
func (a *ForgeAuth) IsGitHub() bool {
	return a.Type == ForgeGitHub
}

// IsGitLab returns true if the auth is for GitLab
func (a *ForgeAuth) IsGitLab() bool {
	return a.Type == ForgeGitLab
}

// IsGitea returns true if the auth is for Gitea (or Forgejo)
func (a *ForgeAuth) IsGitea() bool {
	return a.Type == ForgeGitea
}

// String returns a string representation of the forge type
func (f ForgeType) String() string {
	switch f {
	case ForgeGitHub:
		return "GitHub"
	case ForgeGitLab:
		return "GitLab"
	case ForgeGitea:
		return "Gitea"
	default:
		return "Unknown"
	}
}
