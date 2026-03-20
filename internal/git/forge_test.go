package git

import (
	"os"
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
	// even when gh/glab CLIs are not installed
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

func TestDetectForgeAuthEnvGitHub(t *testing.T) {
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

	auth := detectEnvAuth()
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

	auth := detectEnvAuth()
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

	auth := detectEnvAuth()
	if auth != nil {
		t.Error("Expected nil auth when no env vars are set")
	}
}

func TestDetectGitHubAuthNotInstalled(t *testing.T) {
	// This test just verifies the function doesn't crash when gh is not installed
	auth := detectGitHubAuth()
	// If gh is not installed, should return nil
	if auth != nil {
		t.Log("gh appears to be installed, skipping nil check")
	}
}

func TestDetectGitLabAuthNotInstalled(t *testing.T) {
	// This test just verifies the function doesn't crash when glab is not installed
	auth := detectGitLabAuth()
	// If glab is not installed, should return nil
	if auth != nil {
		t.Log("glab appears to be installed, skipping nil check")
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
