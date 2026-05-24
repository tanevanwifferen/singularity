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

	// DetectProvider returns the RemoteProvider for a repo (gh/gl/none),
	// driven by `git remote get-url origin` parsing.
	DetectProvider(ctx context.Context, repoPath string) (RemoteProvider, error)
}

// ForgeInfo is the lean projection of ForgeAuth safe to hand to TUI clients.
// Token is deliberately omitted; the daemon keeps it.
type ForgeInfo struct {
	Type    ForgeType `json:"type"`
	HasAuth bool      `json:"has_auth"`
	APIURL  string    `json:"api_url,omitempty"`
	User    string    `json:"user,omitempty"`
}
