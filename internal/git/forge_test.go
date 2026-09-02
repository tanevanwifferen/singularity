package git

import (
	"os"
	"strings"
	"testing"
)

func TestForgeTypeString(t *testing.T) {
	tests := []struct {
		forgeType ForgeType
		expected  string
	}{
		{ForgeUnknown, "Unknown"},
		{ForgeGitHub, "GitHub"},
		{ForgeGitLab, "GitLab"},
		{ForgeGitea, "Gitea"},
		{ForgeType(100), "Unknown"}, // Invalid type
	}

	for _, tt := range tests {
		result := tt.forgeType.String()
		if result != tt.expected {
			t.Errorf("ForgeType(%d).String() = %q, want %q", tt.forgeType, result, tt.expected)
		}
	}
}

func TestForgeAuthIsGitHub(t *testing.T) {
	auth := &ForgeAuth{Type: ForgeGitHub}
	if !auth.IsGitHub() {
		t.Error("Expected IsGitHub() to return true for GitHub auth")
	}

	auth.Type = ForgeGitLab
	if auth.IsGitHub() {
		t.Error("Expected IsGitHub() to return false for GitLab auth")
	}
}

func TestForgeAuthIsGitLab(t *testing.T) {
	auth := &ForgeAuth{Type: ForgeGitLab}
	if !auth.IsGitLab() {
		t.Error("Expected IsGitLab() to return true for GitLab auth")
	}

	auth.Type = ForgeGitHub
	if auth.IsGitLab() {
		t.Error("Expected IsGitLab() to return false for GitHub auth")
	}
}

func TestDetectForgeAuthNoCLI(t *testing.T) {
	// This test checks that DetectForgeAuth returns a valid result
	// even when gh/glab/tea CLIs are not installed
	installedCLIs(t)
	fakeCLI(t, nil)
	auth, err := DetectForgeAuth()
	if err != nil {
		t.Fatalf("DetectForgeAuth failed: %v", err)
	}

	// Should return a valid auth struct, possibly with Valid=false
	if auth == nil {
		t.Fatal("Expected non-nil auth")
	}

	// If no CLI is available and no env vars are set, should be invalid
	if !auth.Valid {
		// This is expected when no forge CLI or env vars are configured
		t.Log("No forge authentication detected (expected in test environment)")
	}
}

// isolateForgeEnv makes DetectForgeAuth hermetic: no gh/glab binaries on
// PATH and an empty glab config dir, so only env vars can produce auth.
// Never let these tests read the developer's real credentials — a real
// token in a test-failure message would leak into logs.
func isolateForgeEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", "")
	t.Setenv("GLAB_CONFIG_DIR", t.TempDir())
}

func TestDetectForgeAuthEnvGitHub(t *testing.T) {
	isolateForgeEnv(t)
	// Set GitHub environment variables
	os.Setenv("GITHUB_TOKEN", "test-token")
	os.Setenv("GITHUB_USERNAME", "testuser")
	defer func() {
		os.Unsetenv("GITHUB_TOKEN")
		os.Unsetenv("GITHUB_USERNAME")
	}()

	auth, err := DetectForgeAuth()
	if err != nil {
		t.Fatalf("DetectForgeAuth failed: %v", err)
	}

	if auth.Type != ForgeGitHub {
		t.Errorf("Expected ForgeGitHub, got %v", auth.Type)
	}

	if auth.Username != "testuser" {
		t.Errorf("Expected username=testuser, got %s", auth.Username)
	}

	if auth.AuthToken != "test-token" {
		t.Errorf("Expected token=test-token, got %s", auth.AuthToken)
	}

	if !auth.Valid {
		t.Error("Expected Valid=true when env vars are set")
	}
}

func TestDetectForgeAuthEnvGitLab(t *testing.T) {
	isolateForgeEnv(t)
	// Set GitLab environment variables
	os.Setenv("GITLAB_TOKEN", "glab-test-token")
	os.Setenv("GITLAB_USERNAME", "glabuser")
	defer func() {
		os.Unsetenv("GITLAB_TOKEN")
		os.Unsetenv("GITLAB_USERNAME")
	}()

	auth, err := DetectForgeAuth()
	if err != nil {
		t.Fatalf("DetectForgeAuth failed: %v", err)
	}

	if auth.Type != ForgeGitLab {
		t.Errorf("Expected ForgeGitLab, got %v", auth.Type)
	}

	if auth.Username != "glabuser" {
		t.Errorf("Expected username=glabuser, got %s", auth.Username)
	}

	if !auth.Valid {
		t.Error("Expected Valid=true when env vars are set")
	}
}

func TestDetectEnvAuthGitHubUserEnv(t *testing.T) {
	// GITHUB_USER should be used when GITHUB_USERNAME is not set
	os.Setenv("GITHUB_TOKEN", "test-token")
	os.Setenv("GITHUB_USER", "user-from-github-user")
	os.Unsetenv("GITHUB_USERNAME")
	defer func() {
		os.Unsetenv("GITHUB_TOKEN")
		os.Unsetenv("GITHUB_USER")
	}()

	auth, _ := detectEnvAuth("")
	if auth == nil {
		t.Fatal("Expected non-nil auth from detectEnvAuth")
	}

	if auth.Username != "user-from-github-user" {
		t.Errorf("Expected username=user-from-github-user, got %s", auth.Username)
	}
}

func TestDetectEnvAuthGitLabUserEnv(t *testing.T) {
	// GITLAB_USER should be used when GITLAB_USERNAME is not set
	os.Setenv("GITLAB_TOKEN", "test-token")
	os.Setenv("GITLAB_USER", "user-from-gitlab-user")
	os.Unsetenv("GITLAB_USERNAME")
	defer func() {
		os.Unsetenv("GITLAB_TOKEN")
		os.Unsetenv("GITLAB_USER")
	}()

	auth, _ := detectEnvAuth("")
	if auth == nil {
		t.Fatal("Expected non-nil auth from detectEnvAuth")
	}

	if auth.Username != "user-from-gitlab-user" {
		t.Errorf("Expected username=user-from-gitlab-user, got %s", auth.Username)
	}
}

func TestDetectEnvAuthNoToken(t *testing.T) {
	// Clear all forge-related env vars
	os.Unsetenv("GITHUB_TOKEN")
	os.Unsetenv("GITHUB_USERNAME")
	os.Unsetenv("GITHUB_USER")
	os.Unsetenv("GITLAB_TOKEN")
	os.Unsetenv("GITLAB_USERNAME")
	os.Unsetenv("GITLAB_USER")

	auth, _ := detectEnvAuth("")
	if auth != nil {
		t.Error("Expected nil auth when no env vars are set")
	}
}

func TestDetectGitHubAuthNotInstalled(t *testing.T) {
	isolateForgeEnv(t)
	auth, note := detectGitHubAuth()
	if auth != nil {
		t.Error("expected nil auth with empty PATH")
	}
	if note == "" {
		t.Error("expected a tried-source note for the Detail aggregation")
	}
}

func TestDetectGitLabAuthNotInstalled(t *testing.T) {
	isolateForgeEnv(t)
	auth, note := detectGitLabAuth("")
	if auth != nil {
		t.Error("expected nil auth with empty PATH and empty config dir")
	}
	if note == "" {
		t.Error("expected a tried-source note for the Detail aggregation")
	}
}

func TestForgeAuthIsGitea(t *testing.T) {
	auth := &ForgeAuth{Type: ForgeGitea}
	if !auth.IsGitea() {
		t.Error("Expected IsGitea() to return true for Gitea auth")
	}
	if auth.IsGitHub() || auth.IsGitLab() {
		t.Error("Gitea auth must not report as GitHub or GitLab")
	}
}

func TestDetectGiteaAuthNotInstalled(t *testing.T) {
	installedCLIs(t)
	fakeCLI(t, nil)

	if auth, _ := detectGiteaAuth(); auth != nil {
		t.Errorf("Expected nil auth when tea is not installed, got %+v", auth)
	}
}

func TestDetectGiteaAuthNoLoginGivesHint(t *testing.T) {
	installedCLIs(t, teaBin)
	fakeCLI(t, func(recordedCall) cliResult { return cliResult{Stdout: "[]"} })

	auth, _ := detectGiteaAuth()
	if auth == nil {
		t.Fatal("Expected a Gitea auth struct when tea is installed but has no login")
	}
	if auth.Valid {
		t.Error("Expected Valid=false with no tea login")
	}
	if !strings.Contains(auth.Hint, "tea logins add") {
		t.Errorf("Hint = %q, want the tea logins add command", auth.Hint)
	}
}

func TestDetectGiteaAuthWithLogin(t *testing.T) {
	installedCLIs(t, teaBin)
	fakeCLI(t, func(recordedCall) cliResult {
		return cliResult{Stdout: teaLoginsJSON(teaLogin{
			Name: "example", URL: "https://gitea.example.com", User: "dev", Default: "true",
		})}
	})

	auth, _ := detectGiteaAuth()
	if auth == nil || !auth.Valid {
		t.Fatalf("Expected a valid Gitea auth, got %+v", auth)
	}
	if auth.Type != ForgeGitea {
		t.Errorf("Type = %v, want ForgeGitea", auth.Type)
	}
	if auth.Username != "dev" {
		t.Errorf("Username = %q, want dev", auth.Username)
	}
	if auth.APIURL != "https://gitea.example.com/api/v1" {
		t.Errorf("APIURL = %q, want https://gitea.example.com/api/v1", auth.APIURL)
	}
	// tea keeps the token in its own store; we must never surface one.
	if auth.AuthToken != "" {
		t.Error("Gitea auth must not carry a token — tea owns the credential")
	}
}

func TestForgeAuthFields(t *testing.T) {
	auth := &ForgeAuth{
		Type:      ForgeGitHub,
		Username:  "testuser",
		AuthToken: "secret-token",
		APIURL:    "https://api.github.com",
		Valid:     true,
	}

	if auth.Type != ForgeGitHub {
		t.Error("Expected Type=ForgeGitHub")
	}
	if auth.Username != "testuser" {
		t.Error("Expected Username=testuser")
	}
	if auth.AuthToken != "secret-token" {
		t.Error("Expected AuthToken=secret-token")
	}
	if auth.APIURL != "https://api.github.com" {
		t.Error("Expected APIURL=https://api.github.com")
	}
	if !auth.Valid {
		t.Error("Expected Valid=true")
	}
}

func TestParseGlabConfig(t *testing.T) {
	data := `# comment
git_protocol: ssh
host: gitlab.com
hosts:
    gitlab.com:
        # Your GitLab access token.
        token:
        api_host: gitlab.com
    gitlab.example.nl:
        token: secret-token
        api_host: gitlab.example.nl
        api_protocol: https
        user: someone
last_seen_version: v1.0.0
`
	defaultHost, hosts := parseGlabConfig(data)
	if defaultHost != "gitlab.com" {
		t.Errorf("defaultHost = %q, want gitlab.com", defaultHost)
	}
	if len(hosts) != 2 {
		t.Fatalf("len(hosts) = %d, want 2", len(hosts))
	}
	if hosts[0].Name != "gitlab.com" || hosts[0].Token != "" {
		t.Errorf("hosts[0] = %+v, want gitlab.com without token", hosts[0])
	}
	h := hosts[1]
	if h.Name != "gitlab.example.nl" || h.Token != "secret-token" || h.User != "someone" {
		t.Errorf("hosts[1] = %+v", h)
	}
	if got := h.apiURL(); got != "https://gitlab.example.nl/api/v4" {
		t.Errorf("apiURL = %q", got)
	}
}

// writeGlabConfig writes a config.yml into a temp GLAB_CONFIG_DIR and
// isolates PATH so only the file-based detection can succeed.
func writeGlabConfig(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/config.yml", []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	t.Setenv("GLAB_CONFIG_DIR", dir)
}

func TestDetectGitLabAuthHostPreference(t *testing.T) {
	writeGlabConfig(t, `host: gitlab.com
hosts:
    gitlab.com:
        token:
    gitlab.example.nl:
        token: example-token
        user: someone
`)

	// Preferring the self-hosted instance finds its token.
	auth, note := detectGitLabAuth("gitlab.example.nl")
	if auth == nil {
		t.Fatalf("expected auth, got note %q", note)
	}
	if auth.Host != "gitlab.example.nl" || auth.AuthToken != "example-token" {
		t.Errorf("auth = %+v", auth)
	}
	if auth.APIURL != "https://gitlab.example.nl/api/v4" {
		t.Errorf("APIURL = %q", auth.APIURL)
	}

	// No preference: the default host has no token, so the only tokened
	// host wins.
	auth, _ = detectGitLabAuth("")
	if auth == nil || auth.Host != "gitlab.example.nl" {
		t.Errorf("no-preference auth = %+v", auth)
	}

	// Preferring a host without a token must fail with a note naming the
	// host and the hosts that do carry one.
	auth, note = detectGitLabAuth("gitlab.com")
	if auth != nil {
		t.Errorf("expected nil auth for tokenless host, got %+v", auth)
	}
	if !strings.Contains(note, "gitlab.com") || !strings.Contains(note, "gitlab.example.nl") {
		t.Errorf("note should name the missing and available hosts: %q", note)
	}
}

func TestDetectForgeAuthForHostPrefersGlabForNonGitHub(t *testing.T) {
	writeGlabConfig(t, `hosts:
    gitlab.example.nl:
        token: example-token
`)
	auth, err := DetectForgeAuthForHost("gitlab.example.nl")
	if err != nil {
		t.Fatal(err)
	}
	if !auth.Valid || !auth.IsGitLab() || auth.Host != "gitlab.example.nl" {
		t.Errorf("auth = %+v", auth)
	}
}

func TestDetectForgeAuthDetailListsSources(t *testing.T) {
	isolateForgeEnv(t)
	for _, v := range []string{"GITHUB_TOKEN", "GITLAB_TOKEN"} {
		t.Setenv(v, "")
		os.Unsetenv(v)
	}
	auth, err := DetectForgeAuth()
	if err != nil {
		t.Fatal(err)
	}
	if auth.Valid {
		t.Fatal("expected invalid auth in isolated env")
	}
	for _, want := range []string{"gh CLI", "glab config", "GITLAB_TOKEN"} {
		if !strings.Contains(auth.Detail, want) {
			t.Errorf("Detail missing %q: %q", want, auth.Detail)
		}
	}
}
