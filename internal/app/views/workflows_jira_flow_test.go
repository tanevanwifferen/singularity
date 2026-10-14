package views

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
	"gitlab.com/tanevanwifferen1/singularity/internal/service/fake"
)

func newJiraFlowWorkflowsView(t *testing.T, stub *fake.FlowStub) *WorkflowsView {
	t.Helper()
	old := createWorkflowWorktrees
	createWorkflowWorktrees = func(*service.FeatureWorkflow) error { return nil }
	t.Cleanup(func() { createWorkflowWorktrees = old })

	svcs := fake.New()
	svcs.Flow = stub
	v := NewWorkflowsView(&service.Project{Name: "proj"})
	v.SetServices(svcs)
	v.jiraConfirmIssue = jiraFlowTestIssue()
	return v
}

func TestWorkflowsJiraConfirmTabTogglesFlowMode(t *testing.T) {
	v := newJiraFlowWorkflowsView(t, fake.NewFlowStub())

	v.handleJiraWorkflowConfirm(tea.KeyMsg{Type: tea.KeyTab})
	if !v.jiraFlowMode {
		t.Fatal("first tab did not enable flow mode")
	}
	v.handleJiraWorkflowConfirm(tea.KeyMsg{Type: tea.KeyTab})
	if v.jiraFlowMode {
		t.Fatal("second tab did not return to agent mode")
	}
}

func TestWorkflowsJiraConfirmEscResetsFlowMode(t *testing.T) {
	v := newJiraFlowWorkflowsView(t, fake.NewFlowStub())
	v.jiraFlowMode = true

	v.handleJiraWorkflowConfirm(tea.KeyMsg{Type: tea.KeyEsc})
	if v.jiraFlowMode || v.jiraConfirmIssue != nil {
		t.Error("esc did not reset the modal state")
	}
}

func TestWorkflowsJiraConfirmEnterInFlowModeStartsFlow(t *testing.T) {
	var got service.FlowStartRequest
	stub := fake.NewFlowStub()
	stub.StartFn = func(_ context.Context, req service.FlowStartRequest) (*service.Flow, error) {
		got = req
		return &service.Flow{ID: "f1"}, nil
	}
	v := newJiraFlowWorkflowsView(t, stub)
	v.jiraFlowMode = true
	v.jiraExtraInput.Insert("be careful")

	cmd := v.handleJiraWorkflowConfirm(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter in flow mode returned no command")
	}
	if v.jiraFlowMode || v.jiraConfirmIssue != nil {
		t.Error("modal state not reset after Enter")
	}
	msg, ok := cmd().(flowFromJiraDoneMsg)
	if !ok {
		t.Fatalf("result is not a flowFromJiraDoneMsg")
	}
	if !strings.Contains(msg.status, "Flow started") {
		t.Errorf("status = %q, want a flow-started message", msg.status)
	}
	wf := v.currentWorkflow()
	if wf == nil {
		t.Fatal("new workflow is not selected")
	}
	if got.WorkDir != wf.WorkflowDir() {
		t.Errorf("WorkDir = %q, want %q", got.WorkDir, wf.WorkflowDir())
	}
	if got.IssueKey != "ABC-7" {
		t.Errorf("IssueKey = %q, want ABC-7", got.IssueKey)
	}
	if !strings.Contains(got.Goal, "be careful") {
		t.Errorf("Goal missing custom instructions: %q", got.Goal)
	}
}

func TestWorkflowsJiraFlowFailureStaysOnStatusLine(t *testing.T) {
	stub := fake.NewFlowStub()
	stub.StartFn = func(context.Context, service.FlowStartRequest) (*service.Flow, error) {
		return nil, errors.New("refused")
	}
	v := newJiraFlowWorkflowsView(t, stub)
	v.jiraFlowMode = true

	cmd := v.handleJiraWorkflowConfirm(tea.KeyMsg{Type: tea.KeyEnter})
	v.Update(cmd())
	if !strings.Contains(v.workflowStatusMsg, "Flow start failed: refused") {
		t.Errorf("status = %q, want the flow failure to survive the update", v.workflowStatusMsg)
	}
	if strings.Contains(v.workflowStatusMsg, "spawn an agent") {
		t.Errorf("status = %q still tells the user to spawn an agent", v.workflowStatusMsg)
	}
}

func TestWorkflowsJiraConfirmCtrlEIgnoredInFlowMode(t *testing.T) {
	v := newJiraFlowWorkflowsView(t, fake.NewFlowStub())
	v.workflows = append(v.workflows, service.NewFeatureWorkflow(v.proj, "existing", v.workflowBaseDir))
	v.selectedWorkflow = 0
	v.rebuildFilter()
	v.jiraFlowMode = true

	if cmd := v.handleJiraWorkflowConfirm(tea.KeyMsg{Type: tea.KeyCtrlE}); cmd != nil {
		t.Error("ctrl+e started something in flow mode")
	}
	if v.jiraConfirmIssue == nil || !v.jiraFlowMode {
		t.Error("ctrl+e changed the modal state in flow mode")
	}
	if !strings.Contains(v.renderJiraConfirmModal(), "agent-only") {
		t.Error("modal does not explain that ctrl+e is agent-only in flow mode")
	}
}
