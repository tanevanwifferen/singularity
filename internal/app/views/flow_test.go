package views

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
	"gitlab.com/tanevanwifferen1/singularity/internal/service/fake"
)

// testFlow mirrors the shape of the daemon's f1: three rounds, rejected
// twice and accepted on the third.
func testFlow() service.Flow {
	return service.Flow{
		ID:        "f1",
		QueueID:   "flow-f1",
		Title:     "smoke: retry backoff helper",
		Goal:      "add a retry backoff helper",
		WorkDir:   "/home/dev/singularity",
		MaxRounds: 3,
		State:     service.FlowAccepted,
		Rounds: []*service.FlowRound{
			{N: 1, State: service.FlowRoundRejected},
			{N: 2, State: service.FlowRoundRejected},
			{N: 3, State: service.FlowRoundAccepted},
		},
	}
}

// testTree is the flat, parent-linked node list Flow.Tree returns for
// testFlow — root first, then each round followed by its two steps, which is
// the order the daemon emits and the order the renderer relies on.
func testTree() *service.FlowTree {
	reject := &service.FlowVerdict{
		Decision: service.FlowReject,
		Summary:  "the cap is not a cap once jitter is on",
		Findings: []service.FlowFinding{
			{Severity: service.FlowSeverityBlocker, Detail: "jitter escapes the cap"},
			{Severity: service.FlowSeverityMajor, Detail: "overflow yields a zero delay"},
		},
	}
	accept := &service.FlowVerdict{
		Decision: service.FlowAccept,
		Summary:  "holds up under adversarial probing",
	}
	t := &service.FlowTree{FlowID: "f1", Nodes: []service.FlowTreeNode{
		{ID: "f1", Kind: service.FlowNodeFlow, Label: "smoke: retry backoff helper", State: "accepted"},
	}}
	rounds := []struct {
		n       int
		state   string
		work    string
		verdict *service.FlowVerdict
	}{
		{1, "rejected", "implement", reject},
		{2, "rejected", "fix", reject},
		{3, "accepted", "fix", accept},
	}
	for _, r := range rounds {
		roundID := "f1/r" + string(rune('0'+r.n))
		t.Nodes = append(t.Nodes,
			service.FlowTreeNode{ID: roundID, ParentID: "f1", Kind: service.FlowNodeRound,
				Label: "round " + string(rune('0'+r.n)), State: r.state, Round: r.n, Verdict: r.verdict},
			service.FlowTreeNode{ID: roundID + "/" + r.work, ParentID: roundID, Kind: service.FlowNodeStep,
				Label: r.work, State: "done", Round: r.n,
				TaskID: "t" + string(rune('0'+r.n)), AgentID: "agent-work-" + string(rune('0'+r.n)), AgentState: "complete"},
			service.FlowTreeNode{ID: roundID + "/review", ParentID: roundID, Kind: service.FlowNodeStep,
				Label: "review", State: "done", Round: r.n,
				TaskID: "r" + string(rune('0'+r.n)), AgentID: "agent-review-" + string(rune('0'+r.n)), AgentState: "complete"},
		)
	}
	return t
}

// loadedFlowsView returns a view holding testFlow and its tree, plus the
// stub the test can re-point.
func loadedFlowsView(t *testing.T) (*FlowsView, *fake.FlowStub) {
	t.Helper()
	stub := fake.NewFlowStub()
	stub.ListFn = func(context.Context, []service.FlowState) ([]service.Flow, error) {
		return []service.Flow{testFlow()}, nil
	}
	stub.TreeFn = func(_ context.Context, id string) (*service.FlowTree, error) {
		if id != "f1" {
			return nil, service.ErrNotFound
		}
		return testTree(), nil
	}
	svcs := fake.New()
	svcs.Flow = stub

	v := NewFlowsView("/home/dev/singularity")
	v.SetServices(svcs)
	v.SetSize(140, 30)
	cmd := v.Init()
	if cmd == nil {
		t.Fatal("Init returned no command")
	}
	v.Update(cmd())
	if len(v.flows) != 1 || v.selectedID != "f1" {
		t.Fatalf("after Init: flows=%d selected=%q, want 1 flow selected f1", len(v.flows), v.selectedID)
	}
	if len(v.treeNodes) != 10 {
		t.Fatalf("tree nodes = %d, want 10", len(v.treeNodes))
	}
	return v, stub
}

// The tree pane is built from the flat node list in one forward pass: every
// node keeps its submit order, depth follows the parent link, and the branch
// characters come from a running stack rather than a recursive walk.
func TestBuildFlowTreeRowsFromFlatList(t *testing.T) {
	rows := buildFlowTreeRows(testTree().Nodes, map[string]bool{})
	if len(rows) != 10 {
		t.Fatalf("rows = %d, want 10", len(rows))
	}

	want := []struct {
		id     string
		prefix string
	}{
		{"f1", ""},
		{"f1/r1", "├── "},
		{"f1/r1/implement", "│   ├── "},
		{"f1/r1/review", "│   └── "},
		{"f1/r2", "├── "},
		{"f1/r2/fix", "│   ├── "},
		{"f1/r2/review", "│   └── "},
		{"f1/r3", "└── "},
		{"f1/r3/fix", "    ├── "},
		{"f1/r3/review", "    └── "},
	}
	for i, w := range want {
		if rows[i].node.ID != w.id {
			t.Fatalf("row %d id = %q, want %q", i, rows[i].node.ID, w.id)
		}
		if rows[i].prefix != w.prefix {
			t.Errorf("row %d (%s) prefix = %q, want %q", i, w.id, rows[i].prefix, w.prefix)
		}
	}
	if !rows[0].hasKids || rows[2].hasKids {
		t.Error("hasKids: the root must have children and a step must not")
	}
	// The verdict belongs to the round; the review step borrows it so the
	// round's conclusion sits on the line that produced it.
	if rows[3].verdict == nil || rows[3].verdict.Decision != service.FlowReject {
		t.Errorf("review step verdict = %v, want the round's reject", rows[3].verdict)
	}
	if rows[2].verdict != nil {
		t.Error("the work step must not carry a verdict")
	}
}

// A collapsed node hides its whole subtree, and a node whose parent is
// missing is still drawn rather than dropped.
func TestBuildFlowTreeRowsCollapseAndOrphans(t *testing.T) {
	rows := buildFlowTreeRows(testTree().Nodes, map[string]bool{"f1/r1": true})
	for _, r := range rows {
		if strings.HasPrefix(r.node.ID, "f1/r1/") {
			t.Fatalf("collapsed round still rendered %s", r.node.ID)
		}
	}
	if len(rows) != 8 {
		t.Errorf("rows with round 1 collapsed = %d, want 8", len(rows))
	}
	if !rows[1].collapsed {
		t.Error("the collapsed round must be marked collapsed")
	}

	if rows := buildFlowTreeRows(testTree().Nodes, map[string]bool{"f1": true}); len(rows) != 1 {
		t.Errorf("rows with the root collapsed = %d, want 1", len(rows))
	}

	orphan := []service.FlowTreeNode{{ID: "f9/r1/review", ParentID: "f9/r1", Kind: service.FlowNodeStep, Label: "review"}}
	rows = buildFlowTreeRows(orphan, nil)
	if len(rows) != 1 || rows[0].prefix != "" {
		t.Errorf("orphan rows = %+v, want one root-drawn row", rows)
	}
}

// The round count appears in all three places the design names: the list
// row, the tree pane header and the tree root.
func TestRoundCountShownInThreePlaces(t *testing.T) {
	v, _ := loadedFlowsView(t)

	row := v.renderFlowItem(v.flows[0], 0, true)
	if !strings.Contains(row, "round 3/3") {
		t.Errorf("list row = %q, want it to carry round 3/3", row)
	}
	pane := v.renderTreePane(90)
	if strings.Count(pane, "round 3/3") < 2 {
		t.Errorf("tree pane must show round 3/3 in both its header and its root:\n%s", pane)
	}
	full := v.View()
	if !strings.Contains(full, "round 3/3") {
		t.Errorf("view = %q, want the round count", full)
	}
}

// The tree renders every step's task state, agent and agent state, and the
// round's verdict summary on the review step.
func TestTreePaneRendersStepsAndVerdicts(t *testing.T) {
	v, _ := loadedFlowsView(t)
	pane := v.renderTreePane(120)

	for _, want := range []string{"implement", "review", "agent-work-1", "complete", "2 finding(s)", "holds up under adversarial probing"} {
		if !strings.Contains(pane, want) {
			t.Errorf("tree pane missing %q:\n%s", want, pane)
		}
	}
}

// tab toggles pane focus; j/k move the tree cursor; h collapses and l
// expands the node under it.
func TestFlowKeysNavigateTree(t *testing.T) {
	v, _ := loadedFlowsView(t)

	v.Update(tea.KeyMsg{Type: tea.KeyTab})
	if v.focus != focusFlowTree {
		t.Fatal("tab did not move focus to the tree pane")
	}
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if node, _ := v.selectedNode(); node.ID != "f1/r1" {
		t.Fatalf("after j the tree cursor is on %q, want f1/r1", node.ID)
	}
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if !v.collapsed["f1/r1"] {
		t.Fatal("h did not collapse the round")
	}
	if got := len(v.treeRows()); got != 8 {
		t.Fatalf("visible rows after collapse = %d, want 8", got)
	}
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	if v.collapsed["f1/r1"] {
		t.Fatal("l did not expand the round again")
	}
	v.Update(tea.KeyMsg{Type: tea.KeyTab})
	if v.focus != focusFlowList {
		t.Fatal("tab did not move focus back to the list")
	}
}

// 'a' on a step emits OpenAgentMsg for that step's agent; on a node with no
// agent it explains itself in the flash line instead.
func TestOpenAgentFromStep(t *testing.T) {
	v, _ := loadedFlowsView(t)
	v.Update(tea.KeyMsg{Type: tea.KeyTab})
	for i := 0; i < 2; i++ {
		v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	if node, _ := v.selectedNode(); node.ID != "f1/r1/implement" {
		t.Fatalf("cursor on %q, want f1/r1/implement", node.ID)
	}

	_, cmd := v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("'a' on a step produced no command")
	}
	msg, ok := cmd().(OpenAgentMsg)
	if !ok || msg.AgentID != "agent-work-1" {
		t.Fatalf("message = %#v, want OpenAgentMsg{agent-work-1}", msg)
	}

	v.treeCursor = 0 // the flow root, which has no agent
	_, cmd = v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd != nil {
		t.Error("'a' on the root must not open an agent")
	}
	if v.statusMsg == "" {
		t.Error("'a' on the root must explain itself in the flash line")
	}
}

// 'n' opens the start modal with the workflow selected and the round cap
// prefilled, and Enter hands the composed request to the service. This view
// is repo mode, so the workflow select falls back to the repo path.
func TestStartFlowModalSubmits(t *testing.T) {
	v, stub := loadedFlowsView(t)
	var got service.FlowStartRequest
	stub.StartFn = func(_ context.Context, req service.FlowStartRequest) (*service.Flow, error) {
		got = req
		f := testFlow()
		f.ID = "f2"
		return &f, nil
	}

	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if !v.showStart {
		t.Fatal("'n' did not open the start modal")
	}
	if v.workflowChoice == nil || v.workflowChoice.RootDir != "/home/dev/singularity" {
		t.Errorf("work dir = %v, want the repo-mode fallback to the repo path", v.workflowChoice)
	}
	if v.startField != flowFieldWorkflow {
		t.Errorf("the modal opens on field %d, want the workflow select", v.startField)
	}
	if v.startInputs[flowFieldRounds].Value != "3" {
		t.Errorf("max rounds prefill = %q, want 3", v.startInputs[flowFieldRounds].Value)
	}
	if !v.CapturesInput() {
		t.Error("the open modal must capture input")
	}

	v.Update(tea.KeyMsg{Type: tea.KeyTab}) // → goal
	typeInto(v, "cap the delay")
	v.Update(tea.KeyMsg{Type: tea.KeyTab}) // → review focus
	typeInto(v, "overflow")

	_, cmd := v.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter produced no start command")
	}
	if v.showStart {
		t.Error("the modal must close once the request is on its way")
	}
	msg, ok := cmd().(flowActionMsg)
	if !ok || msg.err != nil {
		t.Fatalf("start result = %#v, want a successful flowActionMsg", msg)
	}
	if got.Goal != "cap the delay" || got.ReviewGoal != "overflow" ||
		got.WorkDir != "/home/dev/singularity" || got.MaxRounds != 3 {
		t.Errorf("start request = %+v, want the four modal fields", got)
	}

	v.Update(msg)
	if v.selectedID != "f2" || !strings.Contains(v.statusMsg, "f2") {
		t.Errorf("after a start: selected=%q status=%q, want the new flow reported", v.selectedID, v.statusMsg)
	}
}

// A refusal from the service — a bad work dir, worktree isolation, a
// max_rounds out of range — lands in the flash line and nowhere else.
func TestStartFlowRefusalGoesToFlashLine(t *testing.T) {
	v, stub := loadedFlowsView(t)
	stub.StartFn = func(context.Context, service.FlowStartRequest) (*service.Flow, error) {
		return nil, service.ErrInvalidRequest
	}

	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	v.Update(tea.KeyMsg{Type: tea.KeyTab})
	typeInto(v, "goal")
	_, cmd := v.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter produced no start command")
	}
	v.Update(cmd())

	if !strings.Contains(v.statusMsg, "refused") {
		t.Errorf("status = %q, want the refusal in the flash line", v.statusMsg)
	}
	if v.err != nil {
		t.Errorf("a refused start must not become a view error: %v", v.err)
	}
	if !strings.Contains(v.View(), "refused") {
		t.Error("the refusal must be visible in the rendered view")
	}
}

// Locally detectable nonsense never reaches the service: the modal stays
// open with the complaint on it.
func TestStartFlowLocalValidation(t *testing.T) {
	v, stub := loadedFlowsView(t)
	called := false
	stub.StartFn = func(context.Context, service.FlowStartRequest) (*service.Flow, error) {
		called = true
		return nil, nil
	}

	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if _, cmd := v.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Error("an empty goal must not start a flow")
	}
	if !v.showStart || !strings.Contains(v.statusMsg, "goal") {
		t.Errorf("empty goal: showStart=%v status=%q", v.showStart, v.statusMsg)
	}

	v.startInputs[flowFieldGoal].Set("goal")
	v.startInputs[flowFieldRounds].Set("many")
	if _, cmd := v.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Error("a non-numeric round cap must not start a flow")
	}
	if !v.showStart || !strings.Contains(v.statusMsg, "not a number") {
		t.Errorf("bad rounds: showStart=%v status=%q", v.showStart, v.statusMsg)
	}
	if called {
		t.Error("the service must not have been called at all")
	}
	if !strings.Contains(v.renderStartModal(), "not a number") {
		t.Error("the complaint must render on the modal the user is still in")
	}
}

// 'c' cancels behind a confirmation, and only the confirmed path calls the
// service.
func TestCancelFlowBehindConfirm(t *testing.T) {
	v, stub := loadedFlowsView(t)
	cancelled := ""
	stub.CancelFn = func(_ context.Context, id string) error {
		cancelled = id
		return nil
	}

	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if !v.cancelConfirm.Visible {
		t.Fatal("'c' did not raise the confirmation")
	}
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if v.cancelConfirm.Visible || cancelled != "" {
		t.Fatal("declining the confirmation must cancel nothing")
	}

	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	_, cmd := v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("confirming produced no cancel command")
	}
	msg, ok := cmd().(flowActionMsg)
	if !ok || msg.err != nil || cancelled != "f1" {
		t.Fatalf("cancel result = %#v, cancelled = %q", msg, cancelled)
	}
}

// 'D' removes behind a confirmation, and only the confirmed path calls the
// service. A running flow is cancelled first, and then removed.
func TestRemoveFlowBehindConfirm(t *testing.T) {
	v, stub := loadedFlowsView(t)
	removed := ""
	cancelled := ""
	stub.CancelFn = func(_ context.Context, id string) error {
		cancelled = id
		return nil
	}
	stub.RemoveFn = func(_ context.Context, id string) error {
		removed = id
		return nil
	}

	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	if !v.removeConfirm.Visible {
		t.Fatal("'D' did not raise the confirmation")
	}

	// The prompt title must be rendered to the view so the user sees it.
	view := v.View()
	if !strings.Contains(view, "Remove Flow") {
		t.Errorf("view does not contain the prompt title 'Remove Flow':\n%s", view)
	}

	// Declining removes nothing.
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if v.removeConfirm.Visible || removed != "" || cancelled != "" {
		t.Fatal("declining the confirmation must remove nothing")
	}

	// Confirming removes the flow.
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	_, cmd := v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("confirming produced no remove command")
	}
	msg, ok := cmd().(flowActionMsg)
	if !ok || msg.err != nil || removed != "f1" {
		t.Fatalf("remove result = %#v, removed = %q", msg, removed)
	}

	// For a running flow, cancellation happens before removal.
	running := testFlow()
	running.State = service.FlowRunning
	running.Rounds = running.Rounds[:2]
	removed = ""
	cancelled = ""
	stub.ListFn = func(context.Context, []service.FlowState) ([]service.Flow, error) {
		return []service.Flow{running}, nil
	}
	v.Update(v.RefreshCmd()())
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	_, cmd = v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("confirming produced no remove command")
	}
	msg, ok = cmd().(flowActionMsg)
	if !ok || msg.err != nil {
		t.Fatalf("remove result = %#v", msg)
	}
	if cancelled != "f1" {
		t.Fatalf("running flow was not cancelled, cancelled = %q", cancelled)
	}
	if removed != "f1" {
		t.Fatalf("flow was not removed, removed = %q", removed)
	}
}

// A refresh keeps the cursor on the flow it was on and picks up the state
// the daemon now reports — this is what the app's tick and agent-event
// chains deliver.
func TestRefreshKeepsSelectionAndPicksUpState(t *testing.T) {
	v, stub := loadedFlowsView(t)
	running := testFlow()
	running.State = service.FlowRunning
	running.Rounds = running.Rounds[:2]
	second := testFlow()
	second.ID = "f2"
	stub.ListFn = func(context.Context, []service.FlowState) ([]service.Flow, error) {
		return []service.Flow{running, second}, nil
	}

	cmd := v.RefreshCmd()
	if cmd == nil {
		t.Fatal("RefreshCmd returned nothing with services wired")
	}
	v.Update(cmd())

	if len(v.flows) != 2 || v.selectedID != "f1" {
		t.Fatalf("after refresh: %d flows, selected %q", len(v.flows), v.selectedID)
	}
	if v.flows[0].State != service.FlowRunning || v.flows[0].Round != 2 {
		t.Errorf("row = %+v, want the running flow at round 2", v.flows[0])
	}
	if row := v.renderFlowItem(v.flows[0], 0, true); !strings.Contains(row, "round 2/3") {
		t.Errorf("list row = %q, want round 2/3", row)
	}

	// Moving the cursor selects the other flow and fetches its tree.
	_, cmd = v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if v.selectedID != "f2" {
		t.Fatalf("selected = %q after j, want f2", v.selectedID)
	}
	if cmd == nil {
		t.Fatal("moving the cursor did not fetch the newly selected flow's tree")
	}
	if msg, ok := cmd().(flowTreeLoadedMsg); !ok || msg.err == nil {
		t.Fatalf("tree message = %#v, want the stub's not-found for f2", msg)
	}
}

// Without a service container the view degrades to an empty list instead of
// panicking — the state every view is in before the router wires services.
func TestFlowsViewWithoutServices(t *testing.T) {
	v := NewFlowsView("/tmp/repo")
	if cmd := v.Init(); cmd != nil {
		t.Error("Init must produce no command without services")
	}
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	v.startInputs[flowFieldGoal].Set("goal")
	if _, cmd := v.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Error("starting a flow without services must not produce a command")
	}
	if !strings.Contains(v.statusMsg, "unavailable") {
		t.Errorf("status = %q, want the unavailable notice", v.statusMsg)
	}
	v.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if out := v.View(); !strings.Contains(out, "No flows yet") {
		t.Errorf("view = %q, want the empty state", out)
	}
}

// typeInto sends each rune of s to the view as a key press.
func typeInto(v *FlowsView, s string) {
	for _, r := range s {
		v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// rejectedFlowsView is loadedFlowsView's flow after the reviewer kept
// rejecting to the cap: the state a continue is for.
func rejectedFlowsView(t *testing.T) (*FlowsView, *fake.FlowStub) {
	t.Helper()
	v, stub := loadedFlowsView(t)
	f := testFlow()
	f.State = service.FlowRejected
	f.Rounds[2].State = service.FlowRoundRejected
	stub.ListFn = func(context.Context, []service.FlowState) ([]service.Flow, error) {
		return []service.Flow{f}, nil
	}
	cmd := v.RefreshCmd()
	if cmd == nil {
		t.Fatal("RefreshCmd returned nothing with services wired")
	}
	v.Update(cmd())
	if v.flows[0].State != service.FlowRejected {
		t.Fatalf("row state = %q, want rejected", v.flows[0].State)
	}
	return v, stub
}

// 'C' on a rejected flow opens the continue modal prefilled with the
// daemon's own default, shows what the extra rounds buy, and hands the
// increment to the service on Enter.
func TestContinueFlowOnRejectedFlow(t *testing.T) {
	v, stub := rejectedFlowsView(t)
	gotID, gotRounds := "", -1
	stub.ContinueFn = func(_ context.Context, id string, extra int) (*service.Flow, error) {
		gotID, gotRounds = id, extra
		f := testFlow()
		f.State = service.FlowRunning
		f.MaxRounds = 5
		return &f, nil
	}

	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'C'}})
	if !v.showContinue {
		t.Fatalf("'C' did not open the continue modal (status %q)", v.statusMsg)
	}
	if v.continueTarget.ID != "f1" {
		t.Errorf("modal targets %q, want f1", v.continueTarget.ID)
	}
	if v.continueRounds.Value != "3" {
		t.Errorf("extra rounds prefill = %q, want the CLI's default of 3", v.continueRounds.Value)
	}
	if !v.CapturesInput() {
		t.Error("the open modal must capture input")
	}

	// The modal shows where the flow stopped, the cap it would reach and
	// the round it resumes at — and that nothing else is being changed.
	modal := v.renderContinueModal()
	for _, want := range []string{"f1", "rejected", "round 3/3", "6 rounds (was 3)", "round 4 of 6", "goal"} {
		if !strings.Contains(modal, want) {
			t.Errorf("continue modal missing %q:\n%s", want, modal)
		}
	}

	// Two rounds instead of three: the field is the increment, not a cap.
	v.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if !strings.Contains(v.renderContinueModal(), "5 rounds (was 3)") {
		t.Errorf("modal did not follow the typed count:\n%s", v.renderContinueModal())
	}

	_, cmd := v.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter produced no continue command")
	}
	if v.showContinue {
		t.Error("the modal must close once the request is on its way")
	}
	msg, ok := cmd().(flowActionMsg)
	if !ok || msg.err != nil {
		t.Fatalf("continue result = %#v, want a successful flowActionMsg", msg)
	}
	if gotID != "f1" || gotRounds != 2 {
		t.Errorf("service called with (%q, %d), want (f1, 2)", gotID, gotRounds)
	}

	v.Update(msg)
	if !strings.Contains(v.statusMsg, "capped at 5 rounds") || !strings.Contains(v.statusMsg, "round 4") {
		t.Errorf("status = %q, want the raised cap and the resuming round", v.statusMsg)
	}
}

// 'C' on an accepted flow says why it is not on offer, in the flash line —
// it neither opens the modal nor calls the service.
func TestContinueRefusedOnAcceptedAndRunningFlows(t *testing.T) {
	v, stub := loadedFlowsView(t) // testFlow is accepted
	called := false
	stub.ContinueFn = func(context.Context, string, int) (*service.Flow, error) {
		called = true
		return nil, nil
	}

	if _, cmd := v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'C'}}); cmd != nil {
		t.Error("'C' on an accepted flow must not produce a command")
	}
	if v.showContinue {
		t.Error("'C' on an accepted flow must not open the modal")
	}
	if !strings.Contains(v.statusMsg, "accepted") || !strings.Contains(v.statusMsg, "nothing to continue") {
		t.Errorf("status = %q, want the accepted-flow explanation", v.statusMsg)
	}
	if !strings.Contains(v.View(), "nothing to continue") {
		t.Error("the explanation must be visible in the rendered view")
	}

	// And a flow that has not finished is something to wait for or cancel,
	// not something to continue.
	running := testFlow()
	running.State = service.FlowRunning
	running.Rounds = running.Rounds[:2]
	stub.ListFn = func(context.Context, []service.FlowState) ([]service.Flow, error) {
		return []service.Flow{running}, nil
	}
	v.Update(v.RefreshCmd()())
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'C'}})
	if v.showContinue {
		t.Error("'C' on a running flow must not open the modal")
	}
	if !strings.Contains(v.statusMsg, "running") || !strings.Contains(v.statusMsg, "cancel it first") {
		t.Errorf("status = %q, want the not-finished explanation", v.statusMsg)
	}
	if called {
		t.Error("the service must not have been called at all")
	}
}

// A refusal from the service — a cap already at the ceiling, a work dir that
// has since been removed — lands in the flash line and nowhere else.
func TestContinueServiceRefusalGoesToFlashLine(t *testing.T) {
	v, stub := rejectedFlowsView(t)
	stub.ContinueFn = func(context.Context, string, int) (*service.Flow, error) {
		return nil, service.ErrInvalidRequest
	}

	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'C'}})
	_, cmd := v.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter produced no continue command")
	}
	v.Update(cmd())

	if !strings.Contains(v.statusMsg, "refused") {
		t.Errorf("status = %q, want the refusal in the flash line", v.statusMsg)
	}
	if v.err != nil {
		t.Errorf("a refused continue must not become a view error: %v", v.err)
	}
	if !strings.Contains(v.View(), "refused") {
		t.Error("the refusal must be visible in the rendered view")
	}

	// Locally detectable nonsense never reaches the service: the modal
	// stays open with the complaint on it.
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'C'}})
	v.continueRounds.Set("plenty")
	if _, cmd := v.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Error("a non-numeric round count must not reach the service")
	}
	if !v.showContinue || !strings.Contains(v.statusMsg, "not a number") {
		t.Errorf("bad rounds: showContinue=%v status=%q", v.showContinue, v.statusMsg)
	}
	if !strings.Contains(v.renderContinueModal(), "not a number") {
		t.Error("the complaint must render on the modal the user is still in")
	}
	v.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if v.showContinue {
		t.Error("Esc must abandon the modal")
	}
}
