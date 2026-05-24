//go:build integration

package integration

import (
	"errors"
	"path/filepath"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// TestErrorCodeRoundtrip drives endpoints that should return well-known
// sentinels, then verifies the client-side errors.Is mapping. This
// validates the api.ErrCode* ↔ service.Err* matrix listed in
// docs/design/WIRE-CONTRACT.md §1.
func TestErrorCodeRoundtrip(t *testing.T) {
	requireGit(t)
	d := startTestDaemon(t)

	t.Run("RepoOpen_NonexistentPath_NotFound", func(t *testing.T) {
		ctx, cancel := shortCtx(t)
		defer cancel()
		// A path that obviously isn't a git repo. The local repo service
		// returns "not a git repository: ..." which mapErr translates to
		// service.ErrNotFound on the server side.
		bogus := filepath.Join(t.TempDir(), "definitely-not-a-repo")
		_, err := d.Client.RepoOpen(ctx, bogus)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, service.ErrNotFound) {
			t.Errorf("got %v; want errors.Is(err, ErrNotFound)", err)
		}
	})

	t.Run("AgentGet_UnknownID_NotFound", func(t *testing.T) {
		ctx, cancel := shortCtx(t)
		defer cancel()
		_, err := d.Client.AgentGet(ctx, "agent-that-does-not-exist")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, service.ErrNotFound) {
			t.Errorf("got %v; want errors.Is(err, ErrNotFound)", err)
		}
	})

	t.Run("ProjectStatus_NoLoader_Unavailable", func(t *testing.T) {
		ctx, cancel := shortCtx(t)
		defer cancel()
		_, err := d.Client.ProjectStatus(ctx, "anything")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, service.ErrUnavailable) {
			t.Errorf("got %v; want errors.Is(err, ErrUnavailable)", err)
		}
	})

	t.Run("BranchHEAD_BogusRepo_NotFound", func(t *testing.T) {
		// Fixed in REVIEW.md P1.11: mapErr now matches "exit status 128"
		// and returns ErrNotFound, so the wire code is NOT_FOUND.
		ctx, cancel := shortCtx(t)
		defer cancel()
		bogus := filepath.Join(t.TempDir(), "still-not-a-repo")
		_, err := d.Client.BranchHEAD(ctx, bogus)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, service.ErrNotFound) {
			t.Errorf("got %v; want errors.Is(err, ErrNotFound)", err)
		}
	})
}
