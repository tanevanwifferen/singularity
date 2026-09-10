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
