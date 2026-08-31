package api

import "gitlab.com/tanevanwifferen1/singularity/internal/service"

// Forge DTOs alias the canonical types.
type (
	ForgeAuth         = service.ForgeAuth
	ForgeInfo         = service.ForgeInfo
	ForgeType         = service.ForgeType
	ForgeProviderInfo = service.ForgeProviderInfo
)

// ForgeProviderResponse is the body for GET /api/forge/provider. The CLI
// fields are additive: older clients that only read Provider keep working.
type ForgeProviderResponse struct {
	Provider     RemoteProvider `json:"provider"`
	CLI          string         `json:"cli,omitempty"`
	CLIInstalled bool           `json:"cli_installed"`
	HasLogin     bool           `json:"has_login"`
	Host         string         `json:"host,omitempty"`
	User         string         `json:"user,omitempty"`
	Hint         string         `json:"hint,omitempty"`
}
