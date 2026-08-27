package local

import (
	"context"
	"fmt"

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
		return auth, noForgeErr(auth)
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
		return info, noForgeErr(auth)
	}
	return info, nil
}

// noForgeErr wraps ErrNoForge with the detection detail (which credential
// sources were checked) and a fix hint, so the message reaching the client
// says where was looked instead of only "no forge auth detected".
func noForgeErr(auth *service.ForgeAuth) error {
	if auth == nil || auth.Detail == "" {
		return service.ErrNoForge
	}
	return fmt.Errorf("%w — checked: %s. Fix: `glab auth login --hostname <gitlab-host>`, `gh auth login`, or set GITLAB_TOKEN/GITHUB_TOKEN", service.ErrNoForge, auth.Detail)
}

// DetectProvider returns the RemoteProvider for a repo (gh/gl/none).
func (s *localForgeService) DetectProvider(ctx context.Context, repoPath string) (service.RemoteProvider, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	return git.DetectRemoteProvider(repoPath), nil
}
