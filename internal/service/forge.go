package service

import "context"

// ForgeService exposes forge-credential detection. Per CALL-SITES §2.11,
// the daemon owns the actual ForgeAuth (which carries a token); the client
// typically only needs to know provider + has-auth. Both shapes are
// available — views may use the lean ForgeInfo and avoid pulling secrets
// over the wire.
type ForgeService interface {
	// DetectAuth returns the full ForgeAuth struct (token included). Use
	// from daemon-internal code only — clients should prefer Detect.
	// Returns ErrNoForge when no credentials are configured.
	DetectAuth(ctx context.Context) (*ForgeAuth, error)

	// Detect returns the lean ForgeInfo DTO (provider + has-token bool)
	// suitable for sending to the TUI without leaking the token.
	Detect(ctx context.Context) (*ForgeInfo, error)

	// DetectProvider returns the RemoteProvider for a repo (gh/gl/tea/none),
	// driven by the layered origin-URL detection in internal/git.
	DetectProvider(ctx context.Context, repoPath string) (RemoteProvider, error)

	// ProviderInfo returns the detected provider plus, for Gitea remotes,
	// whether tea is installed and logged in for the repo's host — with an
	// actionable hint when it is not. Never fails on a missing CLI; the
	// absence is reported in the result.
	ProviderInfo(ctx context.Context, repoPath string) (*ForgeProviderInfo, error)
}

// ForgeProviderInfo is the repo-scoped provider report behind
// "singl forge provider".
type ForgeProviderInfo struct {
	Provider RemoteProvider `json:"provider"`
	// CLI is the command-line tool that drives this provider (gh, glab, tea).
	CLI string `json:"cli,omitempty"`
	// CLIInstalled reports whether that tool is on $PATH.
	CLIInstalled bool `json:"cli_installed"`
	// HasLogin reports whether the tool has credentials for the repo host.
	HasLogin bool `json:"has_login"`
	// Host is the origin remote's hostname.
	Host string `json:"host,omitempty"`
	// User is the account the tool is logged in as, when known.
	User string `json:"user,omitempty"`
	// Hint is the exact command to run when something is missing.
	Hint string `json:"hint,omitempty"`
}

// ForgeInfo is the lean projection of ForgeAuth safe to hand to TUI clients.
// Token is deliberately omitted; the daemon keeps it.
type ForgeInfo struct {
	Type    ForgeType `json:"type"`
	HasAuth bool      `json:"has_auth"`
	APIURL  string    `json:"api_url,omitempty"`
	User    string    `json:"user,omitempty"`
	// Hint is the actionable remedy when HasAuth is false.
	Hint string `json:"hint,omitempty"`
}
