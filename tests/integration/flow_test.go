//go:build integration

package integration

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// waitForFlowState polls the daemon over HTTP until flowID reports want.
// Polling rather than a WS subscription, for the reason waitForTaskState
// polls: it is the read path an orchestrating caller actually uses, and the
// reconciler's fallback tick is a second wide, so the first pass over a
// pending flow is not synchronous with the start call that created it.
func waitForFlowState(t *testing.T, d *testDaemon, flowID string, want api.FlowState) api.Flow {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last api.FlowState
	for time.Now().Before(deadline) {
		ctx, cancel := shortCtx(t)
		f, err := d.Client.FlowGet(ctx, flowID)
		cancel()
		if err != nil {
			t.Fatalf("FlowGet(%s): %v", flowID, err)
		}
		if f.State == want {
			return *f
		}
		last = f.State
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("flow %s never reached %s (last seen %s)", flowID, want, last)
	return api.Flow{}
}

// startFlow starts one flow against a fresh repo and returns it plus the repo.
func startFlow(t *testing.T, d *testDaemon, title string) (api.Flow, string) {
	t.Helper()
	repo := repoFixture(t)
	ctx, cancel := shortCtx(t)
	defer cancel()
	f, err := d.Client.FlowStart(ctx, api.FlowStartRequest{
		Title:   title,
		Goal:    "make the thing work",
		WorkDir: repo,
	})
	if err != nil {
		t.Fatalf("FlowStart: %v", err)
	}
	return *f, repo
}

// TestFlowListRoundTrip is the join the flow halves were missing: the client
// was tested against an HTTP stub and the handlers against a fake service,
// so neither notices if the two envelopes stop agreeing. Here the daemon is
// composed exactly as internal/daemon.Run composes it — real queue manager,
// real flow manager over it, real reconciler — and every assertion is made
// through the wire.
func TestFlowListRoundTrip(t *testing.T) {
	d := startTestDaemon(t)

	ctx, cancel := shortCtx(t)
	defer cancel()

	// The endpoint is served and answers before anything exists: an empty
	// list, not a 404 and not a nil-map panic.
	flows, err := d.Client.FlowList(ctx, nil)
	if err != nil {
		t.Fatalf("FlowList on empty daemon: %v", err)
	}
	if len(flows) != 0 {
		t.Fatalf("FlowList on empty daemon = %d flows, want 0", len(flows))
	}

	started, repo := startFlow(t, d, "round trip")
	if started.ID == "" {
		t.Fatal("FlowStart returned a flow with no ID")
	}
	if started.State != service.FlowPending {
		t.Errorf("new flow state = %s, want %s", started.State, service.FlowPending)
	}
	if started.QueueID != "flow-"+started.ID {
		t.Errorf("queue_id = %q, want %q", started.QueueID, "flow-"+started.ID)
	}
	if started.WorkDir != repo {
		t.Errorf("work_dir = %q, want %q", started.WorkDir, repo)
	}

	// The record round-trips through list...
	flows, err = d.Client.FlowList(ctx, nil)
	if err != nil {
		t.Fatalf("FlowList: %v", err)
	}
	if len(flows) != 1 || flows[0].ID != started.ID {
		t.Fatalf("FlowList = %+v, want just %s", flows, started.ID)
	}
	if flows[0].Title != "round trip" {
		t.Errorf("listed title = %q, want %q", flows[0].Title, "round trip")
	}

	// ...and the reconciler, which is the half that only exists once the
	// daemon wires it, moves the flow off pending and submits round 1 into
	// the flow's own queue.
	running := waitForFlowState(t, d, started.ID, service.FlowRunning)
	if len(running.Rounds) != 1 {
		t.Fatalf("running flow has %d rounds, want 1", len(running.Rounds))
	}
	r1 := running.Rounds[0]
	if r1.WorkTaskID == "" || r1.ReviewTaskID == "" {
		t.Fatalf("round 1 = %+v, want both task IDs set", r1)
	}

	// Those tasks are ordinary queue tasks in flow-<id>, reachable with no
	// flow-specific code — which is the whole point of building on the
	// queue rather than beside it.
	tasks, err := d.Client.QueueList(ctx, running.QueueID, nil)
	if err != nil {
		t.Fatalf("QueueList(%s): %v", running.QueueID, err)
	}
	if len(tasks) != 2 {
		t.Fatalf("queue %s has %d tasks, want 2", running.QueueID, len(tasks))
	}

	// Persistence lands under Paths.Flows, the directory this unit added.
	rec := filepath.Join(d.Paths.Flows, started.ID+".json")
	if _, err := os.Stat(rec); err != nil {
		t.Errorf("no flow record at %s: %v", rec, err)
	}

	// The tree is derived from the same record plus the queue's task states.
	tree, err := d.Client.FlowTree(ctx, started.ID)
	if err != nil {
		t.Fatalf("FlowTree: %v", err)
	}
	if len(tree.Nodes) == 0 {
		t.Error("FlowTree returned no nodes")
	}

	// Cancel is terminal and visible through the state filter both ways.
	if err := d.Client.FlowCancel(ctx, started.ID); err != nil {
		t.Fatalf("FlowCancel: %v", err)
	}
	cancelled, err := d.Client.FlowGet(ctx, started.ID)
	if err != nil {
		t.Fatalf("FlowGet after cancel: %v", err)
	}
	if cancelled.State != service.FlowCancelled {
		t.Fatalf("state after cancel = %s, want %s", cancelled.State, service.FlowCancelled)
	}
	if got, err := d.Client.FlowList(ctx, []api.FlowState{service.FlowCancelled}); err != nil {
		t.Fatalf("FlowList(cancelled): %v", err)
	} else if len(got) != 1 {
		t.Errorf("FlowList(cancelled) = %d flows, want 1", len(got))
	}
	if got, err := d.Client.FlowList(ctx, []api.FlowState{service.FlowRunning}); err != nil {
		t.Fatalf("FlowList(running): %v", err)
	} else if len(got) != 0 {
		t.Errorf("FlowList(running) = %d flows, want 0", len(got))
	}

	// Remove is allowed now the flow is terminal, and takes the record with
	// it.
	if err := d.Client.FlowRemove(ctx, started.ID); err != nil {
		t.Fatalf("FlowRemove: %v", err)
	}
	if _, err := d.Client.FlowGet(ctx, started.ID); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("FlowGet after remove: err = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(rec); !os.IsNotExist(err) {
		t.Errorf("flow record still at %s after remove (err=%v)", rec, err)
	}
}

// TestFlowServedWithoutPersistence covers the degraded path the daemon
// promises: a flow state directory it cannot create disables persistence and
// nothing else. The daemon still comes up, /api/flow/list still answers, and
// flows still run — in memory, for this lifetime only.
//
// The directory is blocked with a regular file rather than a chmod, so the
// case holds when the tests run as root.
func TestFlowServedWithoutPersistence(t *testing.T) {
	blocked := filepath.Join(shortTempDir(t), "flows")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}

	d := startTestDaemonFlowsAt(t, blocked)

	ctx, cancel := shortCtx(t)
	defer cancel()
	if _, err := d.Client.FlowList(ctx, nil); err != nil {
		t.Fatalf("FlowList with persistence disabled: %v", err)
	}

	started, _ := startFlow(t, d, "no persistence")
	running := waitForFlowState(t, d, started.ID, service.FlowRunning)
	if len(running.Rounds) != 1 {
		t.Errorf("running flow has %d rounds, want 1", len(running.Rounds))
	}

	// Nothing was written: the blocker is still the file we put there.
	info, err := os.Stat(blocked)
	if err != nil {
		t.Fatalf("stat blocker: %v", err)
	}
	if info.IsDir() {
		t.Errorf("%s became a directory; the no-op store wrote something", blocked)
	}
}
