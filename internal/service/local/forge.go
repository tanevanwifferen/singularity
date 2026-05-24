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
	}
	if !auth.Valid {
		return info, service.ErrNoForge
	}
	return info, nil
}

// DetectProvider returns the RemoteProvider for a repo (gh/gl/none).
func (s *localForgeService) DetectProvider(ctx context.Context, repoPath string) (service.RemoteProvider, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	return git.DetectRemoteProvider(repoPath), nil
}
