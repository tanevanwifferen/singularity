package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// flowTestServer serves the flow surface over TCP and records the last
// request path + decoded body, so the tests can assert what went on the
// wire. Same shape as queueTestServer, kept separate so a change to one
// surface's fixture cannot silently alter the other's assertions.
type flowTestServer struct {
	*httptest.Server
	path string
	body map[string]any
}

func newFlowTestServer(t *testing.T, data func(path string) any) *flowTestServer {
	t.Helper()
	fs := &flowTestServer{}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.path = r.URL.RequestURI()
		fs.body = nil
		if r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&fs.body)
		}
		_ = json.NewEncoder(w).Encode(api.APIResponse{Success: true, Data: data(r.URL.Path)})
	}))
	t.Cleanup(fs.Close)
	return fs
}

func TestFlowClientReads(t *testing.T) {
	fs := newFlowTestServer(t, func(path string) any {
		switch path {
		case "/api/flow/list":
			return api.FlowListResponse{Flows: []api.Flow{{ID: "f1", State: service.FlowRunning}}}
		case "/api/flow/get":
			return api.Flow{ID: "f1", Goal: "make it work"}
		case "/api/flow/tree":
			return api.FlowTreeResponse{Tree: api.FlowTree{
				FlowID: "f1",
				Nodes:  []api.FlowTreeNode{{ID: "f1", Kind: service.FlowNodeFlow}},
			}}
		}
		return nil
	})
	c := NewClient(fs.URL)
	ctx := context.Background()

	flows, err := c.FlowList(ctx, []api.FlowState{service.FlowRunning, service.FlowAccepted})
	if err != nil {
		t.Fatalf("FlowList: %v", err)
	}
	if len(flows) != 1 || flows[0].ID != "f1" {
		t.Errorf("FlowList = %+v", flows)
	}
	// Filters travel as query params, one `state` per value.
	if fs.path != "/api/flow/list?state=running&state=accepted" {
		t.Errorf("list path = %q", fs.path)
	}

	// No filter means no query string at all, not an empty one.
	if _, err := c.FlowList(ctx, nil); err != nil {
		t.Fatalf("FlowList(nil): %v", err)
	}
	if fs.path != "/api/flow/list" {
		t.Errorf("unfiltered list path = %q", fs.path)
	}

	f, err := c.FlowGet(ctx, "f/1")
	if err != nil {
		t.Fatalf("FlowGet: %v", err)
	}
	if f.Goal != "make it work" {
		t.Errorf("FlowGet = %+v", f)
	}
	if fs.path != "/api/flow/get?flow_id=f%2F1" {
		t.Errorf("get path = %q", fs.path)
	}

	tree, err := c.FlowTree(ctx, "f1")
	if err != nil {
		t.Fatalf("FlowTree: %v", err)
	}
	if tree.FlowID != "f1" || len(tree.Nodes) != 1 {
		t.Errorf("FlowTree = %+v", tree)
	}
	if fs.path != "/api/flow/tree?flow_id=f1" {
		t.Errorf("tree path = %q", fs.path)
	}
}

func TestFlowClientWrites(t *testing.T) {
	fs := newFlowTestServer(t, func(path string) any {
		switch path {
		case "/api/flow/start":
			return api.FlowStartResponse{Flow: api.Flow{ID: "f1", State: service.FlowPending}}
		case "/api/flow/continue":
			return api.FlowContinueResponse{Flow: api.Flow{
				ID: "f1", State: service.FlowRunning, MaxRounds: 5,
				Rounds: []*api.FlowRound{{N: 1}, {N: 2}, {N: 3}},
			}}
		}
		return nil
	})
	c := NewClient(fs.URL)
	ctx := context.Background()

	f, err := c.FlowStart(ctx, api.FlowStartRequest{Goal: "g", WorkDir: "/w", MaxRounds: 2})
	if err != nil {
		t.Fatalf("FlowStart: %v", err)
	}
	if f.ID != "f1" || f.State != service.FlowPending {
		t.Errorf("FlowStart = %+v", f)
	}
	if fs.path != "/api/flow/start" {
		t.Errorf("start path = %q", fs.path)
	}
	if fs.body["goal"] != "g" || fs.body["work_dir"] != "/w" {
		t.Errorf("start body = %v", fs.body)
	}

	// A continue names the flow and the extra rounds and nothing else: goal,
	// work dir and options are the flow's own, so there is nowhere on this
	// request to respecify them.
	cont, err := c.FlowContinue(ctx, "f1", 2)
	if err != nil {
		t.Fatalf("FlowContinue: %v", err)
	}
	if cont.State != service.FlowRunning || cont.MaxRounds != 5 || len(cont.Rounds) != 3 {
		t.Errorf("FlowContinue = %+v", cont)
	}
	if fs.path != "/api/flow/continue" {
		t.Errorf("continue path = %q", fs.path)
	}
	if fs.body["flow_id"] != "f1" || fs.body["rounds"] != float64(2) {
		t.Errorf("continue body = %v, want flow_id f1 and rounds 2", fs.body)
	}
	if len(fs.body) != 2 {
		t.Errorf("continue body carries more than the flow and its round count: %v", fs.body)
	}

	// An unasked-for round count travels as 0 rather than being filled in
	// client-side: the daemon's default of 3 more is the manager's to apply.
	if _, err := c.FlowContinue(ctx, "f1", 0); err != nil {
		t.Fatalf("FlowContinue(0): %v", err)
	}
	if fs.body["rounds"] != float64(0) {
		t.Errorf("unasked round count = %v, want 0", fs.body["rounds"])
	}

	cases := []struct {
		name     string
		call     func() error
		wantPath string
	}{
		{"cancel", func() error { return c.FlowCancel(ctx, "f1") }, "/api/flow/cancel"},
		{"remove", func() error { return c.FlowRemove(ctx, "f1") }, "/api/flow/remove"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if fs.path != tc.wantPath {
				t.Errorf("path = %q, want %q", fs.path, tc.wantPath)
			}
			if fs.body["flow_id"] != "f1" {
				t.Errorf("body = %v, want flow_id f1", fs.body)
			}
		})
	}
}

// TestFlowErrorCodeRoundTrip is the test that keeps a remote daemon
// indistinguishable from a local one: every coded failure the flow surface
// can produce must reach the caller as the same sentinel the local service
// would have returned, so `flow show --id nope` prints "no such flow"
// rather than a generic transport error. Every method is exercised against
// every code it is documented to answer with (design §6).
func TestFlowErrorCodeRoundTrip(t *testing.T) {
	// code is set per-subtest; the server echoes it back on every route.
	var code, msg string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusForFlowCode(code))
		_ = json.NewEncoder(w).Encode(api.APIResponse{Success: false, Code: code, Error: msg})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	ctx := context.Background()

	calls := map[string]func() error{
		"Start": func() error { _, err := c.FlowStart(ctx, api.FlowStartRequest{}); return err },
		// A remote CONFLICT — an accepted flow, or one still running — must
		// arrive as the very sentinel a local continue would have returned,
		// or `flow continue` prints a transport error instead of the reason.
		"Continue": func() error { _, err := c.FlowContinue(ctx, "nope", 2); return err },
		"List":     func() error { _, err := c.FlowList(ctx, nil); return err },
		"Get":      func() error { _, err := c.FlowGet(ctx, "nope"); return err },
		"Tree":     func() error { _, err := c.FlowTree(ctx, "nope"); return err },
		"Cancel":   func() error { return c.FlowCancel(ctx, "nope") },
		"Remove":   func() error { return c.FlowRemove(ctx, "nope") },
	}

	codes := []struct {
		code     string
		sentinel error
		message  string
	}{
		{api.ErrCodeNotFound, service.ErrNotFound, "no such flow: nope"},
		{api.ErrCodeBadRequest, service.ErrInvalidRequest, "goal is required"},
		{api.ErrCodeConflict, service.ErrConflict, "flow f1 is still running"},
		{api.ErrCodeUnavailable, service.ErrUnavailable, "flow manager not available"},
	}

	for _, tc := range codes {
		for name, call := range calls {
			t.Run(tc.code+"/"+name, func(t *testing.T) {
				code, msg = tc.code, tc.message
				err := call()
				if !errors.Is(err, tc.sentinel) {
					t.Fatalf("%s under %s = %v, want %v", name, tc.code, err, tc.sentinel)
				}
				if err.Error() != tc.message {
					t.Errorf("message lost: got %q, want %q", err, tc.message)
				}
			})
		}
	}
}

// statusForFlowCode picks the HTTP status the daemon pairs with a code, so
// the fixture fails the way the real server does rather than always 400.
func statusForFlowCode(code string) int {
	switch code {
	case api.ErrCodeNotFound:
		return http.StatusNotFound
	case api.ErrCodeConflict:
		return http.StatusConflict
	case api.ErrCodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadRequest
	}
}
