package local

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// localForgeService implements service.ForgeService. DetectAuth returns the
// raw ForgeAuth (token included) — only daemon-internal code should call it;
// clients prefer the lean Detect() projection.
type localForgeService struct{}

// DetectAuth returns the full ForgeAuth struct.
func (s *localForgeService) DetectAuth(ctx context.Context) (*service.ForgeAuth, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	auth, err := git.DetectForgeAuth()
	if err != nil {
		return nil, wrapErr(err)
	}
	if auth == nil || !auth.Valid {
		return auth, service.ErrNoForge
	}
	return auth, nil
}

// Detect returns the lean ForgeInfo (no token).
func (s *localForgeService) Detect(ctx context.Context) (*service.ForgeInfo, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	auth, err := git.DetectForgeAuth()
	if err != nil {
		return nil, wrapErr(err)
	}
	if auth == nil {
		return nil, service.ErrNoForge
	}
	info := &service.ForgeInfo{
		Type:    auth.Type,
		HasAuth: auth.Valid,
		APIURL:  auth.APIURL,
		User:    auth.Username,
		Hint:    auth.Hint,
	}
	if !auth.Valid {
		return info, service.ErrNoForge
	}
	return info, nil
}

// DetectProvider returns the RemoteProvider for a repo (gh/gl/tea/none).
func (s *localForgeService) DetectProvider(ctx context.Context, repoPath string) (service.RemoteProvider, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	return git.DetectRemoteProvider(repoPath), nil
}

// ProviderInfo returns the provider plus the state of the CLI that drives it.
func (s *localForgeService) ProviderInfo(ctx context.Context, repoPath string) (*service.ForgeProviderInfo, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	st := git.DetectProviderStatus(repoPath)
	return &service.ForgeProviderInfo{
		Provider:     st.Provider,
		CLI:          st.CLI,
		CLIInstalled: st.CLIInstalled,
		HasLogin:     st.HasLogin,
		Host:         st.Host,
		User:         st.User,
		Hint:         st.Hint,
	}, nil
}
