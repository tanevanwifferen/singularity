package views

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
	"gitlab.com/tanevanwifferen1/singularity/internal/service/fake"
)

// terminatingAgents serves a fixed set of agents and records which ones the
// workflows view terminates, together with the workflow's state at that
// moment so a test can tell whether the kill came before the teardown.
type terminatingAgents struct {
	service.AgentService

	snaps      []service.AgentSnapshot
	terminated []string
	stateAt    map[string]service.WorkflowState
	wf         *service.FeatureWorkflow
	failFor    string
}

func (a *terminatingAgents) List(context.Context) ([]service.AgentSnapshot, error) {
	return a.snaps, nil
}

func (a *terminatingAgents) Get(context.Context, string) (*service.AgentSnapshot, error) {
	return nil, service.ErrNotFound
}

func (a *terminatingAgents) Terminate(_ context.Context, id string) error {
	a.terminated = append(a.terminated, id)
	if a.stateAt == nil {
		a.stateAt = make(map[string]service.WorkflowState)
	}
	// The command runs synchronously in the test goroutine, so reading
	// the state field without the workflow's lock is race-free here.
	a.stateAt[id] = a.wf.State
	if id == a.failFor {
		return errors.New("process would not die")
	}
	return nil
}

// deletableWorkflowsView returns a workflows view over one workflow whose
// worktrees were never created, so RemoveAllWorktrees touches nothing on
// disk, wired to an agent service the test controls.
func deletableWorkflowsView(t *testing.T) (*WorkflowsView, *service.FeatureWorkflow, *terminatingAgents) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	proj := flowTestProject()
	v := NewWorkflowsView(proj)
	wf := service.NewFeatureWorkflow(proj, "feat/kill-me", t.TempDir())
	wf.SetWorkflowAgentID("recorded")
	v.workflows = append(v.workflows, wf)
	v.rebuildFilter()

	agents := &terminatingAgents{wf: wf}
	svcs := fake.New()
	svcs.Agent = agents
	v.SetServices(svcs)
	return v, wf, agents
}

// Deleting a workflow from the TUI kills every agent still working inside
// it, recorded on the workflow or not, before the worktrees are removed, and
// leaves agents working elsewhere alone.
func TestRemoveWorkflowTerminatesAgentsBeforeWorktrees(t *testing.T) {
	v, wf, agents := deletableWorkflowsView(t)
	dir := wf.WorkflowDir()
	agents.snaps = []service.AgentSnapshot{
		{ID: "in-repo", WorkDir: filepath.Join(dir, "pbd-api")},
		{ID: "at-root", WorkDir: dir},
		{ID: "sibling", WorkDir: dir + "-2"},
		{ID: "elsewhere", WorkDir: t.TempDir()},
	}

	msg, ok := v.removeWorkflowCmd(wf)().(worktreesRemovedMsg)
	if !ok || msg.err != nil {
		t.Fatalf("removeWorkflowCmd returned %#v", msg)
	}
	want := map[string]bool{"recorded": true, "in-repo": true, "at-root": true}
	if len(agents.terminated) != len(want) {
		t.Errorf("terminated %v, want exactly %v", agents.terminated, want)
	}
	for _, id := range agents.terminated {
		if !want[id] {
			t.Errorf("terminated %s, which is not in the workflow", id)
		}
		if st := agents.stateAt[id]; st == service.WorkflowCleaningUp {
			t.Errorf("agent %s terminated only after worktree removal had begun", id)
		}
	}

	v.Update(msg)
	if len(v.workflows) != 0 {
		t.Errorf("workflow still listed after removal: %d", len(v.workflows))
	}
	if !strings.Contains(v.workflowStatusMsg, "removed") {
		t.Errorf("status = %q, want a removal confirmation", v.workflowStatusMsg)
	}
}

// An agent that cannot be stopped keeps its worktrees: the removal is
// aborted, the workflow stays, and the user is told why.
func TestRemoveWorkflowAbortsWhenAgentSurvives(t *testing.T) {
	v, wf, agents := deletableWorkflowsView(t)
	agents.snaps = []service.AgentSnapshot{{ID: "stuck", WorkDir: filepath.Join(wf.WorkflowDir(), "pbd-api")}}
	agents.failFor = "stuck"

	msg, ok := v.removeWorkflowCmd(wf)().(worktreesRemovedMsg)
	if !ok || msg.err == nil {
		t.Fatalf("removeWorkflowCmd returned %#v, want an error", msg)
	}
	if wf.State == service.WorkflowCleaningUp {
		t.Error("worktree removal started although an agent is still alive")
	}

	v.Update(msg)
	if len(v.workflows) != 1 {
		t.Errorf("workflow dropped despite failed removal: %d left", len(v.workflows))
	}
	if !strings.Contains(v.workflowStatusMsg, "Cannot delete") || !strings.Contains(v.workflowStatusMsg, "stuck") {
		t.Errorf("status = %q, want the termination failure surfaced", v.workflowStatusMsg)
	}
}

func TestOneLine(t *testing.T) {
	cases := []struct {
		name, in string
		max      int
		want     string
	}{
		{"first non-blank line", "\n  \nfirst\nsecond", 40, "first"},
		{"fits", "short", 5, "short"},
		{"cut with ellipsis", "abcdefgh", 4, "abc…"},
		{"rune safe", "héllo wörld", 6, "héllo…"},
		{"non-positive max", "abc", 0, ""},
	}
	for _, c := range cases {
		if got := oneLine(c.in, c.max); got != c.want {
			t.Errorf("%s: oneLine(%q, %d) = %q, want %q", c.name, c.in, c.max, got, c.want)
		}
	}
}

func TestMRLink(t *testing.T) {
	const url = "https://gitlab.com/group/proj/-/merge_requests/12"
	cases := map[string]string{
		"plain url":            url,
		"url after text":       "Creating...\nView merge request: " + url + "\n",
		"http url":             "see http://host/pr/1 now",
		"no url uses one line": "\nsomething odd\nmore",
		"trailing period":      "View merge request: " + url + ".",
	}
	want := map[string]string{
		"plain url":            url,
		"url after text":       url,
		"http url":             "http://host/pr/1",
		"no url uses one line": "something odd",
		"trailing period":      url,
	}
	for name, in := range cases {
		if got := mrLink(in, 40); got != want[name] {
			t.Errorf("%s: mrLink = %q, want %q", name, got, want[name])
		}
	}
	// A URL longer than the budget is never cut.
	if got := mrLink(url, 10); got != url {
		t.Errorf("long url was cut: %q", got)
	}
}

func TestRenderRepoDetailStaysOneLinePerField(t *testing.T) {
	proj := flowTestProject()
	v := NewWorkflowsView(proj)
	v.width = 80
	wf := service.NewFeatureWorkflow(proj, "feat/x", t.TempDir())
	wf.Repos = map[string]*service.WorkflowRepo{
		"web": {
			RepoName: "web",
			MRURL:    "Creating merge request\nView merge request: https://gitlab.com/g/web/-/merge_requests/3\nmore",
			Error:    "first error line\nsecond line\nthird line",
		},
	}
	out := v.renderRepoDetail(wf)
	if !strings.Contains(out, "https://gitlab.com/g/web/-/merge_requests/3") {
		t.Errorf("URL missing from output: %q", out)
	}
	for _, junk := range []string{"Creating merge request", "second line", "third line", "more"} {
		if strings.Contains(out, junk) {
			t.Errorf("output leaked %q: %q", junk, out)
		}
	}
	// blank lead + one status row + one MR row
	if n := strings.Count(strings.TrimRight(out, "\n"), "\n"); n != 2 {
		t.Errorf("expected 3 lines, got %d newlines: %q", n, out)
	}
}

func TestMRSummaryLinesAreOneLine(t *testing.T) {
	proj := flowTestProject()
	v := NewWorkflowsView(proj)
	v.width = 80
	wf := service.NewFeatureWorkflow(proj, "feat/x", t.TempDir())
	wf.Repos = map[string]*service.WorkflowRepo{
		"web": {RepoName: "web", MRTitle: "Long LLM title", MRURL: "noise\nView: https://h/o/r/-/merge_requests/9\n"},
	}
	v.workflows = append(v.workflows, wf)
	v.rebuildFilter()
	v.handleMRDoneMsg()
	if len(v.mrSummaryLines) != 1 || strings.Contains(v.mrSummaryLines[0], "\n") ||
		!strings.Contains(v.mrSummaryLines[0], "https://h/o/r/-/merge_requests/9") ||
		strings.Contains(v.mrSummaryLines[0], "noise") {
		t.Errorf("bad summary lines: %q", v.mrSummaryLines)
	}
}
