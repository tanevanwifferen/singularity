package git

import "testing"

func TestParseGitRemoteURL(t *testing.T) {
	tests := []struct {
		url      string
		wantHost string
		wantPath string
	}{
		{"git@gitlab.com:group/repo.git", "gitlab.com", "group/repo"},
		{"git@gitlab.example.nl:group/sub/repo.git", "gitlab.example.nl", "group/sub/repo"},
		{"https://gitlab.example.nl/group/repo.git", "gitlab.example.nl", "group/repo"},
		{"https://github.com/owner/repo", "github.com", "owner/repo"},
		{"ssh://git@gitlab.example.nl/group/repo.git", "gitlab.example.nl", "group/repo"},
		{"ssh://git@gitlab.example.nl:2222/group/repo.git", "gitlab.example.nl", "group/repo"},
		{"", "", ""},
		{"/local/path/repo", "", ""},
	}
	for _, tt := range tests {
		host, path := ParseGitRemoteURL(tt.url)
		if host != tt.wantHost || path != tt.wantPath {
			t.Errorf("ParseGitRemoteURL(%q) = (%q, %q), want (%q, %q)",
				tt.url, host, path, tt.wantHost, tt.wantPath)
		}
	}
}
