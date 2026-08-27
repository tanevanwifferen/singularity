package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// ForgeType represents the type of code hosting service
type ForgeType int

const (
	ForgeUnknown ForgeType = iota
	ForgeGitHub
	ForgeGitLab
)

// ForgeAuth holds authentication info for a forge
type ForgeAuth struct {
	Type      ForgeType `json:"type"`
	Username  string    `json:"username"`
	AuthToken string    `json:"auth_token"` // Token or OAuth token
	APIURL    string    `json:"api_url"`
	Host      string    `json:"host,omitempty"` // forge hostname the credentials belong to
	Valid     bool      `json:"valid"`
	// Detail lists the credential sources that were checked when detection
	// failed (Valid == false), so error messages can say where was looked.
	// Empty on success.
	Detail string `json:"detail,omitempty"`
}

// DetectForgeAuth checks for existing authentication with GitHub or GitLab.
// Sources, in order: gh CLI, the glab CLI config file (per host), and the
// GITHUB_TOKEN / GITLAB_TOKEN environment variables.
func DetectForgeAuth() (*ForgeAuth, error) {
	return DetectForgeAuthForHost("")
}

// DetectForgeAuthForRepo resolves credentials preferring the host of the
// repo's origin remote — required for self-hosted forges (e.g. a private
// GitLab), where gitlab.com credentials would be useless.
func DetectForgeAuthForRepo(repoPath string) (*ForgeAuth, error) {
	return DetectForgeAuthForHost(OriginHost(repoPath))
}

// DetectForgeAuthForHost checks all credential sources, preferring ones that
// match the given forge host (empty host = no preference, legacy order).
// The returned auth has Valid == false and a populated Detail when nothing
// usable was found; the error is reserved for I/O-level failures.
func DetectForgeAuthForHost(host string) (*ForgeAuth, error) {
	sources := []func() (*ForgeAuth, string){
		detectGitHubAuth,
		func() (*ForgeAuth, string) { return detectGitLabAuth(host) },
		func() (*ForgeAuth, string) { return detectEnvAuth(host) },
	}
	// A non-GitHub host preference (self-hosted GitLab and friends) should
	// consult the glab config before the gh CLI.
	if host != "" && !strings.Contains(host, "github") {
		sources[0], sources[1] = sources[1], sources[0]
	}

	var tried []string
	for _, source := range sources {
		auth, note := source()
		if auth != nil {
			return auth, nil
		}
		if note != "" {
			tried = append(tried, note)
		}
	}
	return &ForgeAuth{Type: ForgeUnknown, Valid: false, Detail: strings.Join(tried, "; ")}, nil
}

// detectGitHubAuth checks gh CLI authentication. The note return describes
// why detection failed (for the Detail aggregation).
func detectGitHubAuth() (*ForgeAuth, string) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, "gh CLI (not installed)"
	}

	// Check auth status (we just need to ensure gh is authenticated)
	cmd := exec.Command("gh", "auth", "status", "--json", "user,hostname")
	if _, err := cmd.Output(); err != nil {
		return nil, "gh CLI (not authenticated — run `gh auth login`)"
	}

	// Get current user
	userCmd := exec.Command("gh", "api", "user", "--jq", ".login")
	userOutput, err := userCmd.Output()
	if err != nil {
		return nil, "gh CLI (authenticated but `gh api user` failed)"
	}
	username := strings.TrimSpace(string(userOutput))
	if username == "" {
		return nil, "gh CLI (authenticated but no username)"
	}

	// Get auth token
	tokenCmd := exec.Command("gh", "auth", "token")
	tokenOutput, err := tokenCmd.Output()
	if err != nil {
		return nil, "gh CLI (authenticated but `gh auth token` failed)"
	}

	return &ForgeAuth{
		Type:      ForgeGitHub,
		Username:  username,
		AuthToken: strings.TrimSpace(string(tokenOutput)),
		APIURL:    "https://api.github.com",
		Host:      "github.com",
		Valid:     true,
	}, ""
}

// glabHostEntry is one `hosts:` block from glab's config.yml.
type glabHostEntry struct {
	Name        string
	Token       string
	User        string
	APIHost     string
	APIProtocol string
}

// glabConfigPath returns the glab CLI config file location, honoring the
// GLAB_CONFIG_DIR and XDG_CONFIG_HOME overrides glab itself supports.
func glabConfigPath() string {
	if dir := os.Getenv("GLAB_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "config.yml")
	}
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(base, "glab-cli", "config.yml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "glab-cli", "config.yml")
}

// parseGlabConfig extracts the default host and per-host credentials from
// glab's config.yml. It parses only the YAML subset glab writes itself:
// top-level `key: value` pairs and a two-level `hosts:` mapping.
func parseGlabConfig(data string) (defaultHost string, hosts []glabHostEntry) {
	var current *glabHostEntry
	inHosts := false
	hostIndent := -1

	flush := func() {
		if current != nil {
			hosts = append(hosts, *current)
			current = nil
		}
	}

	for _, raw := range strings.Split(data, "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))

		if indent == 0 {
			flush()
			inHosts = trimmed == "hosts:"
			hostIndent = -1
			if !inHosts {
				if k, v, ok := strings.Cut(trimmed, ":"); ok && strings.TrimSpace(k) == "host" {
					defaultHost = strings.TrimSpace(v)
				}
			}
			continue
		}
		if !inHosts {
			continue
		}
		if hostIndent == -1 {
			hostIndent = indent
		}
		if indent == hostIndent && strings.HasSuffix(trimmed, ":") {
			flush()
			current = &glabHostEntry{Name: strings.TrimSuffix(trimmed, ":")}
			continue
		}
		if current == nil || indent <= hostIndent {
			continue
		}
		k, v, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		val := strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "token":
			current.Token = val
		case "user", "username":
			current.User = val
		case "api_host":
			current.APIHost = val
		case "api_protocol":
			current.APIProtocol = val
		}
	}
	flush()
	return defaultHost, hosts
}

// apiURL builds the REST API base URL for a glab host entry.
func (h glabHostEntry) apiURL() string {
	proto := h.APIProtocol
	if proto == "" {
		proto = "https"
	}
	apiHost := h.APIHost
	if apiHost == "" {
		apiHost = h.Name
	}
	return proto + "://" + apiHost + "/api/v4"
}

// detectGitLabAuth reads glab's config file directly (per-host token layout),
// preferring the given host. Falls back to the glab binary when the file is
// missing. The note return describes why detection failed.
func detectGitLabAuth(preferredHost string) (*ForgeAuth, string) {
	cfgPath := glabConfigPath()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if auth := detectGitLabAuthViaBinary(preferredHost); auth != nil {
			return auth, ""
		}
		return nil, fmt.Sprintf("glab config %s (not found)", cfgPath)
	}

	defaultHost, hosts := parseGlabConfig(string(data))
	var withToken []glabHostEntry
	for _, h := range hosts {
		if h.Token != "" {
			withToken = append(withToken, h)
		}
	}
	if len(withToken) == 0 {
		return nil, fmt.Sprintf("glab config %s (no host with a token — run `glab auth login --hostname <host>`)", cfgPath)
	}

	pick := func() *glabHostEntry {
		if preferredHost != "" {
			for i := range withToken {
				if withToken[i].Name == preferredHost {
					return &withToken[i]
				}
			}
			return nil
		}
		for i := range withToken {
			if withToken[i].Name == defaultHost {
				return &withToken[i]
			}
		}
		sort.Slice(withToken, func(i, j int) bool { return withToken[i].Name < withToken[j].Name })
		return &withToken[0]
	}

	entry := pick()
	if entry == nil {
		names := make([]string, len(withToken))
		for i, h := range withToken {
			names[i] = h.Name
		}
		return nil, fmt.Sprintf("glab config %s (no token for host %q; hosts with token: %s)",
			cfgPath, preferredHost, strings.Join(names, ", "))
	}

	return &ForgeAuth{
		Type:      ForgeGitLab,
		Username:  entry.User,
		AuthToken: entry.Token,
		APIURL:    entry.apiURL(),
		Host:      entry.Name,
		Valid:     true,
	}, ""
}

// detectGitLabAuthViaBinary asks the glab binary for a token — the fallback
// for setups without a readable config file (e.g. keyring-backed tokens).
func detectGitLabAuthViaBinary(preferredHost string) *ForgeAuth {
	if _, err := exec.LookPath("glab"); err != nil {
		return nil
	}
	host := preferredHost
	if host == "" {
		if out, err := exec.Command("glab", "config", "get", "host").Output(); err == nil {
			host = strings.TrimSpace(string(out))
		}
	}
	args := []string{"config", "get", "token"}
	if host != "" {
		args = append(args, "--host", host)
	}
	out, err := exec.Command("glab", args...).Output()
	if err != nil {
		return nil
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return nil
	}
	if host == "" {
		host = "gitlab.com"
	}
	return &ForgeAuth{
		Type:      ForgeGitLab,
		AuthToken: token,
		APIURL:    "https://" + host + "/api/v4",
		Host:      host,
		Valid:     true,
	}
}

// detectEnvAuth checks environment variables for forge authentication.
// The note return describes why detection failed.
func detectEnvAuth(preferredHost string) (*ForgeAuth, string) {
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
			Host:      "github.com",
			Valid:     true,
		}, ""
	}

	// Check GitLab token
	if token := os.Getenv("GITLAB_TOKEN"); token != "" {
		username := os.Getenv("GITLAB_USERNAME")
		if username == "" {
			username = os.Getenv("GITLAB_USER")
		}
		apiURL := os.Getenv("GITLAB_API_URL")
		host := os.Getenv("GITLAB_HOST")
		if host == "" {
			host = preferredHost
		}
		if apiURL == "" && host != "" {
			apiURL = "https://" + host + "/api/v4"
		}
		return &ForgeAuth{
			Type:      ForgeGitLab,
			Username:  username,
			AuthToken: token,
			APIURL:    apiURL,
			Host:      host,
			Valid:     true,
		}, ""
	}

	return nil, "env GITHUB_TOKEN/GITLAB_TOKEN (unset)"
}

// IsGitHub returns true if the auth is for GitHub
func (a *ForgeAuth) IsGitHub() bool {
	return a.Type == ForgeGitHub
}

// IsGitLab returns true if the auth is for GitLab
func (a *ForgeAuth) IsGitLab() bool {
	return a.Type == ForgeGitLab
}

// String returns a string representation of the forge type
func (f ForgeType) String() string {
	switch f {
	case ForgeGitHub:
		return "GitHub"
	case ForgeGitLab:
		return "GitLab"
	default:
		return "Unknown"
	}
}
