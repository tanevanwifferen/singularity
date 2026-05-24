package api

import "gitlab.com/tanevanwifferen1/singularity/internal/service"

// Forge DTOs alias the canonical types.
type (
	ForgeAuth = service.ForgeAuth
	ForgeInfo = service.ForgeInfo
	ForgeType = service.ForgeType
)

// ForgeProviderResponse is the body for GET /api/forge/provider.
type ForgeProviderResponse struct {
	Provider RemoteProvider `json:"provider"`
}
