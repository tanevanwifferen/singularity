//go:build integration

package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// TestSubscribeAndRefresh verifies the WebSocket plumbing:
//
//  1. Connect to /ws.
//  2. Subscribe — server replies with a "subscribed" frame.
//  3. Send refresh_repo with our repo path — server broadcasts repo_update.
func TestSubscribeAndRefresh(t *testing.T) {
	requireGit(t)
	d := startTestDaemon(t)
	repo := repoFixture(t)

	events := make(chan *api.WSMessage, 16)
	var once sync.Once
	d.Client.SetUpdateHandler(func(ev *api.WSMessage) {
		// Forward every WS frame so the test can inspect ordering.
		select {
		case events <- ev:
		default:
		}
		once.Do(func() {}) // keep linter quiet
	})

	if err := d.Client.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := d.Client.Subscribe(); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Expect a "subscribed" ack first.
	if !waitForEvent(t, events, api.WSEventSubscribed, 2*time.Second) {
		t.Fatalf("never received %q ack", api.WSEventSubscribed)
	}

	// Trigger refresh with an explicit path payload (the daemon falls back
	// to s.repoPath when omitted, which is empty in tests).
	if err := d.Client.SendWSMessage(api.WSMsgRefreshRepo, api.RefreshRepoPayload{Path: repo}); err != nil {
		t.Fatalf("SendWSMessage refresh_repo: %v", err)
	}

	if !waitForEvent(t, events, api.WSEventRepoUpdate, 2*time.Second) {
		t.Fatalf("never received %q after refresh_repo", api.WSEventRepoUpdate)
	}
}

// TestStreamCancelClosesChannel verifies that calling the cancel closure
// returned by a streaming endpoint closes the channel cleanly.
//
// We exercise ProjectDiscoverWorkflowsAllRepos against a daemon with no
// project loader — the call must either return ErrUnavailable up-front
// or, if it accepts the request, terminate cleanly when cancel() runs.
func TestStreamCancelClosesChannel(t *testing.T) {
	requireGit(t)
	d := startTestDaemon(t)

	if err := d.Client.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ch, streamCancel, err := d.Client.ProjectDiscoverWorkflowsAllRepos(ctx, "anything", nil)
	if err != nil {
		// No project loader → ErrUnavailable / ErrNotFound up-front is the
		// expected outcome and we have nothing to cancel. Validate the
		// sentinel to keep the error wiring honest.
		if errors.Is(err, service.ErrUnavailable) || errors.Is(err, service.ErrNotFound) {
			return
		}
		t.Fatalf("unexpected error: %v", err)
	}

	// Stream was opened — exercise cancel().
	streamCancel()

	// Drain until the channel closes; bail out via timeout if it never does.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // closed cleanly — success.
			}
		case <-deadline:
			t.Fatal("stream channel not closed within 2s of cancel()")
		}
	}
}

// TestStreamSurvivesRequestContext verifies REVIEW.md P0.2: the channel
// returned by a subscribe handler must stay open after the POST handler
// returns. We exercise Agent.SubscribeAll, then verify the channel does not
// close for at least 200ms — proof that the daemon-side subscription is no
// longer tied to r.Context().
func TestStreamSurvivesRequestContext(t *testing.T) {
	requireGit(t)
	d := startTestDaemon(t)

	if err := d.Client.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ch, streamCancel, err := d.Client.AgentSubscribeAll(ctx)
	if err != nil {
		t.Fatalf("AgentSubscribeAll: %v", err)
	}
	defer streamCancel()

	// The channel must remain open for at least 200ms after the POST
	// returns. Before P0.2 was fixed, the daemon canceled the subscription
	// when the HTTP handler exited, closing the channel immediately.
	select {
	case _, ok := <-ch:
		if !ok {
			t.Fatal("stream channel closed within 200ms of subscription — P0.2 regression")
		}
		// A real event slipped through (unlikely in this test fixture, but
		// safe — receiving data is also a sign the stream is healthy).
	case <-time.After(200 * time.Millisecond):
		// Expected: still open, no events.
	}
}

// waitForEvent blocks until an event of the given type arrives on ch
// or timeout elapses. Returns true on hit.
func waitForEvent(t *testing.T, ch <-chan *api.WSMessage, want string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-ch:
			if ev != nil && ev.Type == want {
				return true
			}
		case <-deadline:
			return false
		}
	}
}
