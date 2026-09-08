package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/flow"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// newFlowSvc builds a service over a real flow.Manager with a real store and
// no queue. A nil queue is a supported manager configuration (NewManager's
// doc says so) and it is exactly what these tests want: no scheduler
// goroutine, no agents, and every flow parked in FlowPending until a test
// moves it, so the service's own behaviour is what is being asserted.
func newFlowSvc(t *testing.T) (*localFlowService, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := flow.NewStore(filepath.Join(dir, "flows"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return &localFlowService{mgr: flow.NewManager(nil, store)}, dir
}

// startFlow submits a valid request and fails the test if it is refused.
func startFlow(t *testing.T, s *localFlowService, workDir, goal string) *service.Flow {
	t.Helper()
	f, err := s.Start(context.Background(), service.FlowStartRequest{
		Title:   "t",
		Goal:    goal,
		WorkDir: workDir,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return f
}

func TestFlowServiceStartGetList(t *testing.T) {
	s, dir := newFlowSvc(t)
	ctx := context.Background()

	f := startFlow(t, s, dir, "do the thing")
	if f.ID == "" {
		t.Fatal("Start returned a flow with no ID")
	}
	if f.State != service.FlowPending {
		t.Errorf("state = %q, want %q", f.State, service.FlowPending)
	}
	if f.QueueID != "flow-"+f.ID {
		t.Errorf("queue ID = %q, want flow-%s", f.QueueID, f.ID)
	}
	if f.MaxRounds != 3 {
		t.Errorf("max rounds = %d, want the default 3", f.MaxRounds)
	}

	got, err := s.Get(ctx, f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != f.ID || got.Goal != "do the thing" {
		t.Errorf("Get returned %+v, want the flow Start made", got)
	}

	second := startFlow(t, s, dir, "another thing")
	all, err := s.List(ctx, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List returned %d flows, want 2", len(all))
	}
	if all[0].ID != f.ID || all[1].ID != second.ID {
		t.Errorf("List order = %s,%s; want %s,%s oldest first",
			all[0].ID, all[1].ID, f.ID, second.ID)
	}

	// A filter that matches nothing is an empty list, not an error — only
	// an unknown state is a request error.
	none, err := s.List(ctx, []service.FlowState{service.FlowAccepted})
	if err != nil {
		t.Fatalf("List(accepted): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("List(accepted) returned %d flows, want 0", len(none))
	}
	pending, err := s.List(ctx, []service.FlowState{service.FlowPending})
	if err != nil {
		t.Fatalf("List(pending): %v", err)
	}
	if len(pending) != 2 {
		t.Errorf("List(pending) returned %d flows, want 2", len(pending))
	}
}

func TestFlowServiceTree(t *testing.T) {
	s, dir := newFlowSvc(t)
	f := startFlow(t, s, dir, "do the thing")

	tree, err := s.Tree(context.Background(), f.ID)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if tree.FlowID != f.ID {
		t.Errorf("tree flow ID = %q, want %q", tree.FlowID, f.ID)
	}
	// A pending flow has submitted no rounds, so the tree is the root
	// alone — which is the point of StatePending being observable at all.
	if len(tree.Nodes) != 1 {
		t.Fatalf("tree has %d nodes, want just the root", len(tree.Nodes))
	}
	root := tree.Nodes[0]
	if root.Kind != service.FlowNodeFlow {
		t.Errorf("root kind = %q, want %q", root.Kind, service.FlowNodeFlow)
	}
	if root.ParentID != "" {
		t.Errorf("root parent = %q, want empty", root.ParentID)
	}
	if root.State != string(service.FlowPending) {
		t.Errorf("root state = %q, want %q", root.State, service.FlowPending)
	}
}

func TestFlowServiceCancelThenRemove(t *testing.T) {
	s, dir := newFlowSvc(t)
	ctx := context.Background()
	f := startFlow(t, s, dir, "do the thing")

	// Remove is refused while the flow is live: its record is the only
	// index of the tasks it created.
	if err := s.Remove(ctx, f.ID); !errors.Is(err, service.ErrConflict) {
		t.Fatalf("Remove of a running flow = %v, want ErrConflict", err)
	}

	if err := s.Cancel(ctx, f.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	got, err := s.Get(ctx, f.ID)
	if err != nil {
		t.Fatalf("Get after cancel: %v", err)
	}
	if got.State != service.FlowCancelled {
		t.Errorf("state after cancel = %q, want %q", got.State, service.FlowCancelled)
	}
	// Idempotent: a double-click on a confirm dialog is harmless.
	if err := s.Cancel(ctx, f.ID); err != nil {
		t.Errorf("second Cancel = %v, want nil", err)
	}

	if err := s.Remove(ctx, f.ID); err != nil {
		t.Fatalf("Remove of a cancelled flow: %v", err)
	}
	if _, err := s.Get(ctx, f.ID); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("Get after remove = %v, want ErrNotFound", err)
	}
}

// TestFlowServiceErrorMapping pins the flow sentinel → service sentinel
// table. Each case goes through the service, not mapFlowErr directly, so a
// method that forgot to call the mapper fails here.
func TestFlowServiceErrorMapping(t *testing.T) {
	s, dir := newFlowSvc(t)
	ctx := context.Background()
	live := startFlow(t, s, dir, "do the thing")

	// detail marks the cases where the flow package attaches context to its
	// sentinel; those must not lose it in translation. flow.ErrNotFound is
	// bare "not found" by design, so there is nothing to preserve there.
	cases := []struct {
		name   string
		call   func() error
		want   error
		detail bool
	}{
		{"get unknown flow", func() error { _, err := s.Get(ctx, "f404"); return err }, service.ErrNotFound, false},
		{"tree unknown flow", func() error { _, err := s.Tree(ctx, "f404"); return err }, service.ErrNotFound, false},
		{"cancel unknown flow", func() error { return s.Cancel(ctx, "f404") }, service.ErrNotFound, false},
		{"remove unknown flow", func() error { return s.Remove(ctx, "f404") }, service.ErrNotFound, false},
		{"remove live flow", func() error { return s.Remove(ctx, live.ID) }, service.ErrConflict, true},
		{"start with no goal", func() error {
			_, err := s.Start(ctx, service.FlowStartRequest{WorkDir: dir})
			return err
		}, service.ErrInvalidRequest, true},
		{"start with missing work dir", func() error {
			_, err := s.Start(ctx, service.FlowStartRequest{Goal: "g", WorkDir: filepath.Join(dir, "nope")})
			return err
		}, service.ErrInvalidRequest, true},
		{"start with out-of-range max rounds", func() error {
			_, err := s.Start(ctx, service.FlowStartRequest{Goal: "g", WorkDir: dir, MaxRounds: 99})
			return err
		}, service.ErrInvalidRequest, true},
		{"start asking for worktree isolation", func() error {
			_, err := s.Start(ctx, service.FlowStartRequest{
				Goal: "g", WorkDir: dir,
				Opts: service.TaskOptions{UseWorktree: true},
			})
			return err
		}, service.ErrInvalidRequest, true},
		{"list with unknown state", func() error {
			_, err := s.List(ctx, []service.FlowState{"nonsense"})
			return err
		}, service.ErrInvalidRequest, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			// The mapping wraps rather than replaces: the context the
			// flow package attached has to survive for the operator
			// to act on it.
			if tc.detail && err.Error() == tc.want.Error() {
				t.Errorf("err lost its original message: %v", err)
			}
		})
	}
}

// A "missing work dir" start must not surface as NOT_FOUND. The underlying
// os.Stat error says "no such file or directory", which mapErr's substring
// table would happily turn into a 404 about a resource that was never
// requested — the reason mapFlowErr exists at all.
func TestFlowServiceMissingWorkDirIsNotNotFound(t *testing.T) {
	s, dir := newFlowSvc(t)
	_, err := s.Start(context.Background(), service.FlowStartRequest{
		Goal: "g", WorkDir: filepath.Join(dir, "nope"),
	})
	if errors.Is(err, service.ErrNotFound) {
		t.Fatalf("err = %v, want BAD_REQUEST and not NOT_FOUND", err)
	}
}

// Continue maps its three refusals onto three different codes, which is the
// whole of what this layer has to get right: a state that forbids it is a
// CONFLICT, a request the manager cannot satisfy is a BAD_REQUEST, and an
// unknown flow is a NOT_FOUND.
func TestFlowServiceContinue(t *testing.T) {
	s, dir := newFlowSvc(t)
	ctx := context.Background()
	f := startFlow(t, s, dir, "do the thing")

	// A flow that has not finished is waited on or cancelled, not
	// continued — CONFLICT, the same code Remove answers for the same
	// reason.
	if _, err := s.Continue(ctx, f.ID, 3); !errors.Is(err, service.ErrConflict) {
		t.Fatalf("continue of a pending flow = %v, want ErrConflict", err)
	}
	if _, err := s.Continue(ctx, "f404", 3); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("continue of an unknown flow = %v, want ErrNotFound", err)
	}

	if err := s.Cancel(ctx, f.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	got, err := s.Continue(ctx, f.ID, 2)
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if got.ID != f.ID || got.State != service.FlowRunning {
		t.Errorf("continued flow = %s/%s, want %s running", got.ID, got.State, f.ID)
	}
	if got.MaxRounds != f.MaxRounds+2 {
		t.Errorf("max rounds = %d, want the original %d raised by 2", got.MaxRounds, f.MaxRounds)
	}
	if got.EndedAt != nil || got.Error != "" {
		t.Errorf("continued flow still carries %q / %v, want the cancellation cleared", got.Error, got.EndedAt)
	}
	// It is live again, so it is no longer continuable.
	if _, err := s.Continue(ctx, f.ID, 2); !errors.Is(err, service.ErrConflict) {
		t.Fatalf("continue of the revived flow = %v, want ErrConflict", err)
	}
}

// A vanished work dir is the caller's problem to fix, not a missing resource:
// BAD_REQUEST, and specifically not the NOT_FOUND that mapErr's substring
// table would make of "no such file or directory".
func TestFlowServiceContinueWithAVanishedWorkDir(t *testing.T) {
	s, dir := newFlowSvc(t)
	ctx := context.Background()
	tree := filepath.Join(dir, "worktree")
	if err := os.Mkdir(tree, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f := startFlow(t, s, tree, "do the thing")
	if err := s.Cancel(ctx, f.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := os.RemoveAll(tree); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}

	_, err := s.Continue(ctx, f.ID, 2)
	if !errors.Is(err, service.ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
	if errors.Is(err, service.ErrNotFound) {
		t.Fatalf("err = %v, want BAD_REQUEST and not NOT_FOUND", err)
	}
	if !strings.Contains(err.Error(), tree) {
		t.Errorf("err = %q, want it to name the missing path", err)
	}
}

// TestFlowServiceNilManager covers the degradation the daemon relies on:
// without a flow manager every method reports UNAVAILABLE instead of
// dereferencing nil.
func TestFlowServiceNilManager(t *testing.T) {
	s := &localFlowService{}
	ctx := context.Background()

	cases := map[string]func() error{
		"Start":    func() error { _, err := s.Start(ctx, service.FlowStartRequest{Goal: "g", WorkDir: "/tmp"}); return err },
		"Continue": func() error { _, err := s.Continue(ctx, "f1", 3); return err },
		"List":     func() error { _, err := s.List(ctx, nil); return err },
		"Get":      func() error { _, err := s.Get(ctx, "f1"); return err },
		"Tree":     func() error { _, err := s.Tree(ctx, "f1"); return err },
		"Cancel":   func() error { return s.Cancel(ctx, "f1") },
		"Remove":   func() error { return s.Remove(ctx, "f1") },
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, service.ErrUnavailable) {
				t.Fatalf("err = %v, want ErrUnavailable", err)
			}
		})
	}
}

// A cancelled context is refused before the manager is touched, so callers
// get service.ErrCanceled rather than a half-done mutation.
func TestFlowServiceCanceledContext(t *testing.T) {
	s, dir := newFlowSvc(t)
	f := startFlow(t, s, dir, "do the thing")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.Cancel(ctx, f.ID); !errors.Is(err, service.ErrCanceled) {
		t.Fatalf("Cancel with cancelled ctx = %v, want ErrCanceled", err)
	}
	if _, err := s.Get(context.Background(), f.ID); err != nil {
		t.Fatalf("flow was mutated despite the cancelled context: %v", err)
	}
}

// The service must satisfy the interface it claims to.
var _ service.FlowService = (*localFlowService)(nil)
