package views

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// wireStartFlow completes the reverse wiring projectFlowsView leaves out:
// initProjectRouter also points the Workflows view back at the Flows view it
// was just given, so 'f' can jump there. Returns the WorkflowsView alongside
// what projectFlowsView already returns.
func wireStartFlow(fv *FlowsView) *WorkflowsView {
	wv := fv.workflows
	wv.SetFlowsView(fv)
	return wv
}

// Pressing 'f' on a workflow whose worktrees exist jumps straight to the
// Flows view with the start modal open and preselected on that workflow —
// the whole point of the entry point.
func TestHandleStartFlowOpensFlowsModal(t *testing.T) {
	base := t.TempDir()
	proj := flowTestProject()
	fv, _ := projectFlowsView(t, proj, base, "feat/wired")
	wv := wireStartFlow(fv)
	root := createWorkflowRoot(t, wv.workflows[0])

	_, cmd := wv.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if cmd == nil {
		t.Fatal("'f' produced no command")
	}
	msg, ok := cmd().(ViewChangeMsg)
	if !ok || msg.ViewName != "Flows" {
		t.Fatalf("command = %#v, want ViewChangeMsg{Flows}", msg)
	}
	if !fv.showStart {
		t.Error("'f' must open the Flows start modal")
	}
	if fv.workflowChoice == nil || fv.workflowChoice.RootDir != root {
		t.Fatalf("preselected = %v, want the workflow selected in the Workflows view (%q)", fv.workflowChoice, root)
	}
}

// With no workflow selected, 'f' flashes a hint instead of jumping anywhere.
func TestHandleStartFlowNoWorkflowSelected(t *testing.T) {
	proj := flowTestProject()
	fv, _ := projectFlowsView(t, proj, t.TempDir()) // no branches: empty workflow list
	wv := wireStartFlow(fv)

	_, cmd := wv.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if cmd != nil {
		t.Fatal("'f' with no workflow selected must not jump to the Flows view")
	}
	if fv.showStart {
		t.Error("the Flows start modal must not open")
	}
	if !strings.Contains(wv.workflowStatusMsg, "press 'w'") {
		t.Errorf("status = %q, want a hint to press 'w'", wv.workflowStatusMsg)
	}
}

// Pressing 'f' on a workflow whose worktrees were removed must refuse, not
// silently jump to a different workflow the user never selected — the Flows
// start modal's own loadWorkflowOptions never preselects a Missing row, so
// without this check the modal would open pointed at whatever OTHER usable
// workflow happens to exist.
func TestHandleStartFlowRefusesMissingRoot(t *testing.T) {
	base := t.TempDir()
	proj := flowTestProject()
	fv, _ := projectFlowsView(t, proj, base, "feat/gone", "feat/present")
	wv := wireStartFlow(fv)
	createWorkflowRoot(t, wv.workflows[1]) // only "feat/present" gets a root

	// selectedWorkflow defaults to 0: "feat/gone", whose root was never created.
	if wv.currentWorkflow().BranchName != "feat/gone" {
		t.Fatalf("selected workflow = %q, want feat/gone", wv.currentWorkflow().BranchName)
	}

	_, cmd := wv.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if cmd != nil {
		t.Fatal("'f' on a workflow with no worktrees on disk must not jump to the Flows view")
	}
	if fv.showStart {
		t.Error("the Flows start modal must not open on a substituted workflow")
	}
	if !strings.Contains(wv.workflowStatusMsg, "no worktrees on disk") {
		t.Errorf("status = %q, want the same refusal the picker gives for a Missing row", wv.workflowStatusMsg)
	}
}
