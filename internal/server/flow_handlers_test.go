package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
	"gitlab.com/tanevanwifferen1/singularity/internal/service/fake"
)

// newFlowTestServer builds a Server whose only wired capability is the flow
// stub, for the reason newQueueTestServer does not use server.New: the
// handler tests have no use for an engine or the WS wiring.
func newFlowTestServer(f service.FlowService) *Server {
	return &Server{Services: &service.Services{Flow: f}}
}

// sampleFlow is the flow the success-path stubs hand back. Pending with no
// rounds is what Start really returns — submitting round 1 is the
// reconciler's job.
func sampleFlow() service.Flow {
	return service.Flow{
		ID:        "f1",
		QueueID:   "flow-f1",
		Title:     "adversarial",
		Goal:      "do the thing",
		WorkDir:   "/w/flow",
		MaxRounds: 3,
		State:     service.FlowPending,
	}
}

// flowEndpoint names one route plus how to drive it, so the error-code and
// unavailability tables can walk every one of them without repeating the
// plumbing.
type flowEndpoint struct {
	name    string
	handler func(*Server) http.HandlerFunc
	req     func() *http.Request
}

func flowEndpoints() []flowEndpoint {
	return []flowEndpoint{
		{
			"start",
			func(s *Server) http.HandlerFunc { return s.handleFlowStart },
			func() *http.Request {
				return postJSON("/api/flow/start", `{"goal":"g","work_dir":"/w/flow"}`)
			},
		},
		{
			"continue",
			func(s *Server) http.HandlerFunc { return s.handleFlowContinue },
			func() *http.Request {
				return postJSON("/api/flow/continue", `{"flow_id":"f1","rounds":2}`)
			},
		},
		{
			"list",
			func(s *Server) http.HandlerFunc { return s.handleFlowList },
			func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/api/flow/list", nil)
			},
		},
		{
			"get",
			func(s *Server) http.HandlerFunc { return s.handleFlowGet },
			func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/api/flow/get?flow_id=f1", nil)
			},
		},
		{
			"tree",
			func(s *Server) http.HandlerFunc { return s.handleFlowTree },
			func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/api/flow/tree?flow_id=f1", nil)
			},
		},
		{
			"cancel",
			func(s *Server) http.HandlerFunc { return s.handleFlowCancel },
			func() *http.Request { return postJSON("/api/flow/cancel", `{"flow_id":"f1"}`) },
		},
		{
			"remove",
			func(s *Server) http.HandlerFunc { return s.handleFlowRemove },
			func() *http.Request { return postJSON("/api/flow/remove", `{"flow_id":"f1"}`) },
		},
	}
}

// TestHandleFlowStartHappyPath covers POST /api/flow/start including the
// work_dir cleaning the handler owes the service: the flow manager checks
// that the directory exists, but it must never be handed a raw request path.
func TestHandleFlowStartHappyPath(t *testing.T) {
	var got service.FlowStartRequest
	stub := fake.NewFlowStub()
	stub.StartFn = func(_ context.Context, req service.FlowStartRequest) (*service.Flow, error) {
		got = req
		f := sampleFlow()
		return &f, nil
	}
	s := newFlowTestServer(stub)

	rec := httptest.NewRecorder()
	body := `{"title":"adversarial","goal":"do the thing","review_goal":"look at locking",
	          "work_dir":"/w/flow/./sub","max_rounds":5,
	          "opts":{"model":"opus"},"review_opts":{"model":"sonnet"}}`
	s.handleFlowStart(rec, postJSON("/api/flow/start", body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got.WorkDir != "/w/flow/sub" {
		t.Errorf("service got work_dir %q, want the cleaned /w/flow/sub", got.WorkDir)
	}
	if got.Goal != "do the thing" || got.ReviewGoal != "look at locking" {
		t.Errorf("goals decoded wrong: %+v", got)
	}
	if got.MaxRounds != 5 {
		t.Errorf("max_rounds = %d, want 5", got.MaxRounds)
	}
	if got.Opts.Model != "opus" || got.ReviewOpts.Model != "sonnet" {
		t.Errorf("opts decoded wrong: opts %+v review_opts %+v", got.Opts, got.ReviewOpts)
	}
	resp := decodeResp(t, rec)
	if !resp.Success {
		t.Fatalf("success = false, error %q", resp.Error)
	}
	data, _ := json.Marshal(resp.Data)
	for _, want := range []string{`"flow":{`, `"id":"f1"`, `"queue_id":"flow-f1"`, `"state":"pending"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("response missing %s: %s", want, data)
		}
	}
}

// TestHandleFlowContinueHappyPath covers POST /api/flow/continue: the flow
// id and the extra-round count reach the service unaltered, and the raised
// record comes back wrapped under "flow" the way start's does.
func TestHandleFlowContinueHappyPath(t *testing.T) {
	var gotID string
	var gotRounds int
	stub := fake.NewFlowStub()
	stub.ContinueFn = func(_ context.Context, flowID string, extraRounds int) (*service.Flow, error) {
		gotID, gotRounds = flowID, extraRounds
		f := sampleFlow()
		f.State = service.FlowRunning
		f.MaxRounds = 5
		return &f, nil
	}
	s := newFlowTestServer(stub)

	rec := httptest.NewRecorder()
	s.handleFlowContinue(rec, postJSON("/api/flow/continue", `{"flow_id":"f1","rounds":2}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if gotID != "f1" || gotRounds != 2 {
		t.Errorf("service got (%q, %d), want (f1, 2)", gotID, gotRounds)
	}
	resp := decodeResp(t, rec)
	if !resp.Success {
		t.Fatalf("success = false, error %q", resp.Error)
	}
	data, _ := json.Marshal(resp.Data)
	for _, want := range []string{`"flow":{`, `"id":"f1"`, `"state":"running"`, `"max_rounds":5`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("response missing %s: %s", want, data)
		}
	}
}

// TestHandleFlowContinueDefaultsRounds pins the one thing the handler must
// not do: fill in a round count. An omitted `rounds` reaches the service as
// 0, which is what the flow manager reads as "the default, 3" — a handler
// that substituted 3 itself would put a second copy of that default on the
// edge, to drift from the manager's the day it changes.
func TestHandleFlowContinueDefaultsRounds(t *testing.T) {
	got := -1
	stub := fake.NewFlowStub()
	stub.ContinueFn = func(_ context.Context, _ string, extraRounds int) (*service.Flow, error) {
		got = extraRounds
		f := sampleFlow()
		return &f, nil
	}
	s := newFlowTestServer(stub)

	rec := httptest.NewRecorder()
	s.handleFlowContinue(rec, postJSON("/api/flow/continue", `{"flow_id":"f1"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got != 0 {
		t.Errorf("service got rounds %d, want 0 so the manager applies its own default", got)
	}
}

// TestHandleFlowListHappyPath checks the state filter reaches the service and
// the flows come back under the documented key.
func TestHandleFlowListHappyPath(t *testing.T) {
	var got []service.FlowState
	stub := fake.NewFlowStub()
	stub.ListFn = func(_ context.Context, states []service.FlowState) ([]service.Flow, error) {
		got = states
		return []service.Flow{sampleFlow()}, nil
	}
	s := newFlowTestServer(stub)

	rec := httptest.NewRecorder()
	s.handleFlowList(rec, httptest.NewRequest(http.MethodGet, "/api/flow/list?state=running,accepted", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if len(got) != 2 || got[0] != service.FlowRunning || got[1] != service.FlowAccepted {
		t.Errorf("service got states %v, want [running accepted]", got)
	}
	data, _ := json.Marshal(decodeResp(t, rec).Data)
	if !strings.Contains(string(data), `"flows":[`) || !strings.Contains(string(data), `"id":"f1"`) {
		t.Errorf("response missing the flows: %s", data)
	}
}

// TestHandleFlowGetHappyPath: the flow travels bare in Data, as the table
// says (`*api.Flow`, not an envelope).
func TestHandleFlowGetHappyPath(t *testing.T) {
	var gotID string
	stub := fake.NewFlowStub()
	stub.GetFn = func(_ context.Context, flowID string) (*service.Flow, error) {
		gotID = flowID
		f := sampleFlow()
		return &f, nil
	}
	s := newFlowTestServer(stub)

	rec := httptest.NewRecorder()
	s.handleFlowGet(rec, httptest.NewRequest(http.MethodGet, "/api/flow/get?flow_id=f1", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if gotID != "f1" {
		t.Errorf("service got flow_id %q, want f1", gotID)
	}
	data, _ := json.Marshal(decodeResp(t, rec).Data)
	if strings.Contains(string(data), `"flow":{`) {
		t.Errorf("get must return the flow bare, not wrapped: %s", data)
	}
	if !strings.Contains(string(data), `"id":"f1"`) {
		t.Errorf("response missing the flow: %s", data)
	}
}

// TestHandleFlowTreeHappyPath covers the flat node list, wrapped under
// "tree" the way queue graph is wrapped under "graph".
func TestHandleFlowTreeHappyPath(t *testing.T) {
	var gotID string
	stub := fake.NewFlowStub()
	stub.TreeFn = func(_ context.Context, flowID string) (*service.FlowTree, error) {
		gotID = flowID
		return &service.FlowTree{
			FlowID: "f1",
			Nodes: []service.FlowTreeNode{
				{ID: "f1", Kind: service.FlowNodeFlow, Label: "adversarial", State: "running"},
				{ID: "f1/r1", ParentID: "f1", Kind: service.FlowNodeRound, Label: "round 1", State: "running", Round: 1},
				{ID: "f1/r1/review", ParentID: "f1/r1", Kind: service.FlowNodeStep, Label: "review",
					State: "running", Round: 1, TaskID: "t2", AgentID: "a2", AgentState: "running"},
			},
		}, nil
	}
	s := newFlowTestServer(stub)

	rec := httptest.NewRecorder()
	s.handleFlowTree(rec, httptest.NewRequest(http.MethodGet, "/api/flow/tree?flow_id=f1", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if gotID != "f1" {
		t.Errorf("service got flow_id %q, want f1", gotID)
	}
	data, _ := json.Marshal(decodeResp(t, rec).Data)
	for _, want := range []string{
		`"tree":{`, `"flow_id":"f1"`, `"nodes":[`,
		`"id":"f1/r1/review"`, `"parent_id":"f1/r1"`, `"kind":"step"`,
		`"task_id":"t2"`, `"agent_id":"a2"`, `"agent_state":"running"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("response missing %s: %s", want, data)
		}
	}
}

// TestFlowMutationsHappyPath covers cancel and remove: the flow_id reaches
// the service and the reply is a bare success, no data.
func TestFlowMutationsHappyPath(t *testing.T) {
	cases := []struct {
		name    string
		handler func(*Server, *fake.FlowStub) (http.HandlerFunc, *string)
	}{
		{"cancel", func(s *Server, stub *fake.FlowStub) (http.HandlerFunc, *string) {
			got := new(string)
			stub.CancelFn = func(_ context.Context, flowID string) error { *got = flowID; return nil }
			return s.handleFlowCancel, got
		}},
		{"remove", func(s *Server, stub *fake.FlowStub) (http.HandlerFunc, *string) {
			got := new(string)
			stub.RemoveFn = func(_ context.Context, flowID string) error { *got = flowID; return nil }
			return s.handleFlowRemove, got
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := fake.NewFlowStub()
			s := newFlowTestServer(stub)
			handler, got := tc.handler(s, stub)

			rec := httptest.NewRecorder()
			handler(rec, postJSON("/api/flow/"+tc.name, `{"flow_id":"f1"}`))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}
			if *got != "f1" {
				t.Errorf("service got flow_id %q, want f1", *got)
			}
			resp := decodeResp(t, rec)
			if !resp.Success {
				t.Fatalf("success = false, error %q", resp.Error)
			}
			if resp.Data != nil {
				t.Errorf("data = %v, want none on a bare-success mutation", resp.Data)
			}
		})
	}
}

// TestParseFlowStates covers both spellings of the repeatable list filter,
// parseTaskStates' contract applied to flow states.
func TestParseFlowStates(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  []service.FlowState
	}{
		{"absent", "", nil},
		{"single", "?state=running", []service.FlowState{service.FlowRunning}},
		{"repeated", "?state=running&state=accepted",
			[]service.FlowState{service.FlowRunning, service.FlowAccepted}},
		{"comma", "?state=running,accepted",
			[]service.FlowState{service.FlowRunning, service.FlowAccepted}},
		{"spaces", "?state=running,%20accepted",
			[]service.FlowState{service.FlowRunning, service.FlowAccepted}},
		{"empty entries", "?state=&state=,,", nil},
		// Unknown values reach the manager on purpose: it answers
		// BAD_REQUEST naming the offending state rather than returning an
		// empty list that looks like "no such flows".
		{"unknown passthrough", "?state=bogus", []service.FlowState{service.FlowState("bogus")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/flow/list"+tc.query, nil)
			got := parseFlowStates(r.URL.Query()["state"])
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestFlowHandlerValidation covers what the handlers must reject before
// dispatching: a missing flow_id, a traversal-laden work_dir, a malformed
// body, the wrong method. None of them may reach the service — the stub has
// no hooks set, so a dispatch would surface as a 503 instead.
func TestFlowHandlerValidation(t *testing.T) {
	cases := []struct {
		name       string
		handler    func(*Server) http.HandlerFunc
		req        *http.Request
		wantStatus int
	}{
		{
			"start with traversal work_dir",
			func(s *Server) http.HandlerFunc { return s.handleFlowStart },
			postJSON("/api/flow/start", `{"goal":"g","work_dir":"/w/../etc"}`),
			http.StatusBadRequest,
		},
		{
			"start with malformed body",
			func(s *Server) http.HandlerFunc { return s.handleFlowStart },
			postJSON("/api/flow/start", `{"goal":`),
			http.StatusBadRequest,
		},
		{
			"get without flow_id",
			func(s *Server) http.HandlerFunc { return s.handleFlowGet },
			httptest.NewRequest(http.MethodGet, "/api/flow/get", nil),
			http.StatusBadRequest,
		},
		{
			"tree without flow_id",
			func(s *Server) http.HandlerFunc { return s.handleFlowTree },
			httptest.NewRequest(http.MethodGet, "/api/flow/tree", nil),
			http.StatusBadRequest,
		},
		{
			"continue without flow_id",
			func(s *Server) http.HandlerFunc { return s.handleFlowContinue },
			postJSON("/api/flow/continue", `{"rounds":2}`),
			http.StatusBadRequest,
		},
		{
			"continue with malformed body",
			func(s *Server) http.HandlerFunc { return s.handleFlowContinue },
			postJSON("/api/flow/continue", `{"flow_id":`),
			http.StatusBadRequest,
		},
		{
			"cancel without flow_id",
			func(s *Server) http.HandlerFunc { return s.handleFlowCancel },
			postJSON("/api/flow/cancel", `{}`),
			http.StatusBadRequest,
		},
		{
			"remove without flow_id",
			func(s *Server) http.HandlerFunc { return s.handleFlowRemove },
			postJSON("/api/flow/remove", `{}`),
			http.StatusBadRequest,
		},
		{
			"start via GET",
			func(s *Server) http.HandlerFunc { return s.handleFlowStart },
			httptest.NewRequest(http.MethodGet, "/api/flow/start", nil),
			http.StatusMethodNotAllowed,
		},
		{
			"cancel via GET",
			func(s *Server) http.HandlerFunc { return s.handleFlowCancel },
			httptest.NewRequest(http.MethodGet, "/api/flow/cancel", nil),
			http.StatusMethodNotAllowed,
		},
		{
			"continue via GET",
			func(s *Server) http.HandlerFunc { return s.handleFlowContinue },
			httptest.NewRequest(http.MethodGet, "/api/flow/continue", nil),
			http.StatusMethodNotAllowed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dispatched := false
			stub := fake.NewFlowStub()
			stub.StartFn = func(context.Context, service.FlowStartRequest) (*service.Flow, error) {
				dispatched = true
				f := sampleFlow()
				return &f, nil
			}
			stub.GetFn = func(context.Context, string) (*service.Flow, error) {
				dispatched = true
				f := sampleFlow()
				return &f, nil
			}
			stub.TreeFn = func(context.Context, string) (*service.FlowTree, error) {
				dispatched = true
				return &service.FlowTree{FlowID: "f1"}, nil
			}
			stub.ContinueFn = func(context.Context, string, int) (*service.Flow, error) {
				dispatched = true
				f := sampleFlow()
				return &f, nil
			}
			stub.CancelFn = func(context.Context, string) error { dispatched = true; return nil }
			stub.RemoveFn = func(context.Context, string) error { dispatched = true; return nil }
			s := newFlowTestServer(stub)

			rec := httptest.NewRecorder()
			tc.handler(s)(rec, tc.req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if dispatched {
				t.Error("service was dispatched to despite an invalid request")
			}
			resp := decodeResp(t, rec)
			if resp.Success {
				t.Error("success = true on a rejected request")
			}
			if tc.wantStatus == http.StatusBadRequest && resp.Code != api.ErrCodeBadRequest {
				t.Errorf("code = %q, want %q", resp.Code, api.ErrCodeBadRequest)
			}
		})
	}
}

// TestFlowServiceErrMapping walks every code section 6 documents per
// endpoint. The point is that a sentinel from the flow manager travels out as
// its wire code rather than collapsing to INTERNAL: an unknown flow_id is a
// 404, a non-terminal flow refusing removal is a 409, and bad input is a 400
// even though the manager and not the handler decided so.
func TestFlowServiceErrMapping(t *testing.T) {
	cases := []struct {
		endpoint string
		err      error
		wantCode string
	}{
		{"start", service.ErrInvalidRequest, api.ErrCodeBadRequest},
		{"start", service.ErrUnavailable, api.ErrCodeUnavailable},
		// A continue answers with every code the surface has: CONFLICT for
		// an accepted or still-live flow, BAD_REQUEST for a round count
		// that will not fit under the ceiling or a work dir that has since
		// gone, NOT_FOUND for an id the daemon has no record of.
		{"continue", service.ErrConflict, api.ErrCodeConflict},
		{"continue", service.ErrInvalidRequest, api.ErrCodeBadRequest},
		{"continue", service.ErrNotFound, api.ErrCodeNotFound},
		{"continue", service.ErrUnavailable, api.ErrCodeUnavailable},
		{"list", service.ErrInvalidRequest, api.ErrCodeBadRequest},
		{"list", service.ErrUnavailable, api.ErrCodeUnavailable},
		{"get", service.ErrNotFound, api.ErrCodeNotFound},
		{"get", service.ErrInvalidRequest, api.ErrCodeBadRequest},
		{"get", service.ErrUnavailable, api.ErrCodeUnavailable},
		{"tree", service.ErrNotFound, api.ErrCodeNotFound},
		{"tree", service.ErrInvalidRequest, api.ErrCodeBadRequest},
		{"tree", service.ErrUnavailable, api.ErrCodeUnavailable},
		{"cancel", service.ErrNotFound, api.ErrCodeNotFound},
		{"cancel", service.ErrInvalidRequest, api.ErrCodeBadRequest},
		{"cancel", service.ErrUnavailable, api.ErrCodeUnavailable},
		{"remove", service.ErrNotFound, api.ErrCodeNotFound},
		{"remove", service.ErrConflict, api.ErrCodeConflict},
		{"remove", service.ErrUnavailable, api.ErrCodeUnavailable},
	}
	byName := map[string]flowEndpoint{}
	for _, ep := range flowEndpoints() {
		byName[ep.name] = ep
	}
	for _, tc := range cases {
		t.Run(tc.endpoint+"/"+tc.wantCode, func(t *testing.T) {
			err := tc.err
			stub := fake.NewFlowStub()
			stub.StartFn = func(context.Context, service.FlowStartRequest) (*service.Flow, error) {
				return nil, err
			}
			stub.ContinueFn = func(context.Context, string, int) (*service.Flow, error) {
				return nil, err
			}
			stub.ListFn = func(context.Context, []service.FlowState) ([]service.Flow, error) {
				return nil, err
			}
			stub.GetFn = func(context.Context, string) (*service.Flow, error) { return nil, err }
			stub.TreeFn = func(context.Context, string) (*service.FlowTree, error) { return nil, err }
			stub.CancelFn = func(context.Context, string) error { return err }
			stub.RemoveFn = func(context.Context, string) error { return err }

			ep := byName[tc.endpoint]
			s := newFlowTestServer(stub)
			rec := httptest.NewRecorder()
			ep.handler(s)(rec, ep.req())

			resp := decodeResp(t, rec)
			if resp.Code != tc.wantCode {
				t.Errorf("code = %q, want %q (body %s)", resp.Code, tc.wantCode, rec.Body.String())
			}
			if rec.Code != api.HTTPStatusForCode(tc.wantCode) {
				t.Errorf("status = %d, want %d", rec.Code, api.HTTPStatusForCode(tc.wantCode))
			}
		})
	}
}

// TestFlowHandlersUnavailableWithoutManager locks the degradation the design
// asks for: every /api/flow/* route answers UNAVAILABLE — never a panic —
// when the daemon has no flow manager. Two shapes of "no manager" exist and
// both must behave: no service layer at all, and a Services whose Flow is
// nil because the flow state directory would not open.
func TestFlowHandlersUnavailableWithoutManager(t *testing.T) {
	servers := map[string]*Server{
		"no services":   {},
		"no flow field": {Services: &service.Services{}},
		// A wired-but-managerless service reports it itself; the unset stub
		// stands in for localFlowService with a nil manager.
		"nil manager": newFlowTestServer(fake.NewFlowStub()),
	}
	for name, s := range servers {
		for _, ep := range flowEndpoints() {
			t.Run(name+"/"+ep.name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				ep.handler(s)(rec, ep.req())

				if rec.Code != http.StatusServiceUnavailable {
					t.Fatalf("status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
				}
				resp := decodeResp(t, rec)
				if resp.Code != api.ErrCodeUnavailable {
					t.Errorf("code = %q, want %q", resp.Code, api.ErrCodeUnavailable)
				}
			})
		}
	}
}

// TestFlowChangeHookBroadcastsWholeFlow covers the hook the daemon hands to
// flow.Manager.OnFlowChange: one frame per change, carrying the whole flow so
// a view needs no follow-up fetch.
func TestFlowChangeHookBroadcastsWholeFlow(t *testing.T) {
	workDir := t.TempDir()
	s := New("", workDir)
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	frames := wsFrames(t, srv)

	f := sampleFlow()
	f.State = service.FlowRunning
	s.FlowChangeHook()(f)

	msg := awaitFrame(t, frames, api.WSEventFlowUpdated)
	payload, ok := msg.Payload.(map[string]interface{})
	if !ok {
		t.Fatalf("payload is %T, want object", msg.Payload)
	}
	flow, ok := payload["flow"].(map[string]interface{})
	if !ok {
		t.Fatalf("payload has no flow object: %v", payload)
	}
	if flow["id"] != "f1" || flow["state"] != "running" {
		t.Errorf("frame carried %v, want the whole flow f1/running", flow)
	}
	if flow["goal"] != "do the thing" || flow["work_dir"] != "/w/flow" {
		t.Errorf("frame is not the whole flow: %v", flow)
	}
}
