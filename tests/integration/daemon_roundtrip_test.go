//go:build integration

package integration

import (
	"errors"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// TestDaemonRoundtrip exercises one method per service capability against
// the live daemon. Each subtest is hermetic against the shared daemon by
// avoiding mutations to global daemon state.
func TestDaemonRoundtrip(t *testing.T) {
	requireGit(t)
	d := startTestDaemon(t)
	repo := repoFixture(t)

	t.Run("Status", func(t *testing.T) {
		ctx, cancel := shortCtx(t)
		defer cancel()
		st, err := d.Client.GetStatus(ctx)
		if err != nil {
			t.Fatalf("GetStatus: %v", err)
		}
		if st.Version == "" {
			t.Errorf("empty version")
		}
		if st.Server == "" {
			t.Errorf("empty server")
		}
	})

	t.Run("RepoOpen", func(t *testing.T) {
		ctx, cancel := shortCtx(t)
		defer cancel()
		info, err := d.Client.RepoOpen(ctx, repo)
		if err != nil {
			t.Fatalf("RepoOpen: %v", err)
		}
		if info == nil {
			t.Fatal("nil RepoInfo")
		}
		if info.CurrentBranch != "main" {
			t.Errorf("CurrentBranch=%q want main", info.CurrentBranch)
		}
		if info.HEAD == "" {
			t.Errorf("HEAD empty")
		}
	})

	t.Run("BranchList", func(t *testing.T) {
		ctx, cancel := shortCtx(t)
		defer cancel()
		branches, err := d.Client.BranchList(ctx, repo)
		if err != nil {
			t.Fatalf("BranchList: %v", err)
		}
		if len(branches) < 1 {
			t.Errorf("expected ≥1 branch, got %d", len(branches))
		}
		found := false
		for _, b := range branches {
			if b.Name == "main" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected branch 'main' in list, got %+v", branches)
		}
	})

	t.Run("CommitGenerateMessage_EmptyDiff", func(t *testing.T) {
		ctx, cancel := shortCtx(t)
		defer cancel()
		_, err := d.Client.CommitGenerateMessage(ctx, repo)
		// Empty diff → either an error or a generated stub; we only assert
		// the call did not panic or hang. The git layer returns "no staged
		// changes to commit"; that does not match any sentinel and is
		// surfaced as a plain error string.
		if err == nil {
			// Some claude fallback paths may return a synthesized message;
			// accept either.
			return
		}
		// Should NOT be ErrCanceled/Unavailable on a healthy daemon.
		if errors.Is(err, service.ErrCanceled) || errors.Is(err, service.ErrUnavailable) {
			t.Errorf("unexpected sentinel %v from CommitGenerateMessage", err)
		}
	})

	t.Run("ForgeDetect", func(t *testing.T) {
		ctx, cancel := shortCtx(t)
		defer cancel()
		_, err := d.Client.ForgeDetect(ctx)
		// In a clean test environment there is no GH/GL token configured.
		// ForgeService.Detect returns ErrNoForge in that case; allow either
		// success (developer machine has a token in env) or ErrNoForge —
		// just never panic.
		if err != nil && !errors.Is(err, service.ErrNoForge) {
			// Other errors are tolerated (e.g. transient) — record them.
			t.Logf("ForgeDetect returned non-NoForge error: %v", err)
		}
	})

	t.Run("AgentList_Empty", func(t *testing.T) {
		ctx, cancel := shortCtx(t)
		defer cancel()
		agents, err := d.Client.AgentList(ctx)
		if err != nil {
			t.Fatalf("AgentList: %v", err)
		}
		// No subprocess spawn in the test daemon; we expect an empty list.
		if len(agents) != 0 {
			t.Errorf("expected 0 agents, got %d", len(agents))
		}
	})

	t.Run("AgentStats", func(t *testing.T) {
		ctx, cancel := shortCtx(t)
		defer cancel()
		stats, err := d.Client.AgentStats(ctx)
		if err != nil {
			t.Fatalf("AgentStats: %v", err)
		}
		if stats.Total != 0 {
			t.Errorf("expected 0 total, got %d", stats.Total)
		}
		if stats.Active != 0 {
			t.Errorf("expected 0 active, got %d", stats.Active)
		}
	})

	t.Run("AgentMaxAgents", func(t *testing.T) {
		ctx, cancel := shortCtx(t)
		defer cancel()
		max, err := d.Client.AgentMaxAgents(ctx)
		if err != nil {
			t.Fatalf("AgentMaxAgents: %v", err)
		}
		if max <= 0 {
			t.Errorf("expected MaxAgents > 0, got %d", max)
		}
	})

	t.Run("ProjectList_NoLoader", func(t *testing.T) {
		ctx, cancel := shortCtx(t)
		defer cancel()
		_, err := d.Client.ProjectList(ctx)
		// No project loader is wired in startTestDaemon → expect
		// ErrUnavailable (cleanly mapped).
		if !errors.Is(err, service.ErrUnavailable) {
			t.Errorf("ProjectList: want ErrUnavailable, got %v", err)
		}
	})

	t.Run("BranchHEAD", func(t *testing.T) {
		ctx, cancel := shortCtx(t)
		defer cancel()
		head, err := d.Client.BranchHEAD(ctx, repo)
		if err != nil {
			t.Fatalf("BranchHEAD: %v", err)
		}
		if head == "" {
			t.Errorf("empty HEAD")
		}
	})
}

// TestStatusCarriesVersion is a thin smoke test that the api wire types
// survive a real HTTP roundtrip.
func TestStatusCarriesVersion(t *testing.T) {
	requireGit(t)
	d := startTestDaemon(t)
	ctx, cancel := shortCtx(t)
	defer cancel()

	st, err := d.Client.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	// Sanity check on the response envelope.
	var _ api.StatusResponse = *st
}
