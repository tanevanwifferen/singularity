package views

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
	"gitlab.com/tanevanwifferen1/singularity/internal/service/fake"
)

// stubBranchTaken makes every workflow branch look already taken and fails the
// test if a refused create still reaches worktree creation.
func stubBranchTaken(t *testing.T) {
	t.Helper()
	oldCheck, oldCreate := checkWorkflowBranch, createWorkflowWorktrees
	checkWorkflowBranch = func(wf *service.FeatureWorkflow) error {
		return errors.New("branch \"" + wf.BranchName + "\" already exists in 1 repo(s): api (local)")
	}
	createWorkflowWorktrees = func(*service.FeatureWorkflow) error {
		t.Error("worktrees created for a refused workflow")
		return nil
	}
	t.Cleanup(func() { checkWorkflowBranch, createWorkflowWorktrees = oldCheck, oldCreate })
}

func TestWorkflowStartRefusesExistingBranch(t *testing.T) {
	stubBranchTaken(t)
	v := NewWorkflowsView(&service.Project{Name: "proj"})
	v.SetServices(fake.New())
	v.showWorkflowStart = true
	v.workflowBranchInput.Insert("main")

	if cmd := v.handleWorkflowStartInput(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Error("a refused create still returned a command")
	}
	if len(v.workflows) != 0 {
		t.Fatalf("refused workflow was added to the view: %+v", v.workflows)
	}
	if !strings.Contains(v.workflowStatusMsg, "already exists") || !strings.Contains(v.workflowStatusMsg, "main") {
		t.Errorf("status = %q, want the refusal shown", v.workflowStatusMsg)
	}
}

func TestWorkflowFromJiraRefusesExistingBranch(t *testing.T) {
	stubBranchTaken(t)
	v := NewWorkflowsView(&service.Project{Name: "proj"})
	v.SetServices(fake.New())
	issue := jiraFlowTestIssue()

	if cmd := v.startFlowFromJira(issue, ""); cmd != nil {
		t.Error("flow from a refused workflow returned a command")
	}
	if cmd := v.startWorkflowFromJira(issue, ""); cmd != nil {
		t.Error("agent from a refused workflow returned a command")
	}
	if len(v.workflows) != 0 {
		t.Fatalf("refused workflow was added to the view: %+v", v.workflows)
	}
	if !strings.Contains(v.workflowStatusMsg, "Workflow not created") {
		t.Errorf("status = %q, want the refusal shown", v.workflowStatusMsg)
	}
}
