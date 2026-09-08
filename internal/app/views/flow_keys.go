package views

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"

	tea "github.com/charmbracelet/bubbletea"
)

// Key handling for FlowsView: the two panes' navigation, the cancel
// confirmation and the start modal. Split from flow.go only for length; the
// view is one type.

// handleFlowKeyMsg dispatches a key to the modals first, then to whichever
// pane has focus.
func (v *FlowsView) handleFlowKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Flash messages clear on the next key, as they do in WorkflowsView.
	v.statusMsg = ""

	if v.showStart {
		return v, v.handleStartInput(msg)
	}
	if v.showContinue {
		return v, v.handleContinueInput(msg)
	}
	if handled, cmd := v.cancelConfirm.HandleKey(msg); handled {
		return v, cmd
	}
	if v.filter.IsActive() {
		v.filter.Update(msg)
		return v, v.syncSelectionFromCursor()
	}

	switch msg.String() {
	case "tab":
		if v.focus == focusFlowList {
			v.focus = focusFlowTree
		} else {
			v.focus = focusFlowList
		}
		return v, nil

	case "r":
		v.loading = true
		return v, v.RefreshCmd()

	case "n":
		v.openStartModal()
		return v, nil

	case "c":
		return v, v.confirmCancel()

	// Shift-c rather than a plain letter: every plain one this view wants
	// is taken (c is cancel, n start, a agent, r refresh, j/k/h/l/enter
	// navigation, / the filter), and an uppercase C collides with nothing
	// the app or the router claims either — their capitals are R, P, T and
	// the g submenu.
	case "C":
		v.openContinueModal()
		return v, nil

	case "a":
		return v, v.openSelectedAgent()

	case "/":
		v.filter.Update(msg)
		return v, nil

	case "j", "down":
		if v.focus == focusFlowTree {
			v.moveTreeCursor(1)
			return v, nil
		}
		v.filter.CursorDown()
		return v, v.syncSelectionFromCursor()

	case "k", "up":
		if v.focus == focusFlowTree {
			v.moveTreeCursor(-1)
			return v, nil
		}
		v.filter.CursorUp()
		return v, v.syncSelectionFromCursor()

	case "l", "right", "enter":
		if v.focus == focusFlowList {
			v.focus = focusFlowTree
			return v, nil
		}
		v.expandSelected()
		return v, nil

	case "h", "left":
		if v.focus == focusFlowTree {
			v.collapseSelected()
		}
		return v, nil

	case "esc":
		v.focus = focusFlowList
		return v, nil
	}
	return v, nil
}

// moveTreeCursor moves the tree cursor by delta, clamped to the visible rows.
func (v *FlowsView) moveTreeCursor(delta int) {
	rows := v.treeRows()
	if len(rows) == 0 {
		v.treeCursor = 0
		return
	}
	v.treeCursor = min(max(v.treeCursor+delta, 0), len(rows)-1)
}

// expandSelected unfolds the node under the cursor.
func (v *FlowsView) expandSelected() {
	if n, ok := v.selectedNode(); ok {
		delete(v.collapsed, n.ID)
	}
}

// collapseSelected folds the node under the cursor, or — for a leaf, where
// there is nothing to fold — its parent, moving the cursor up to it. That is
// the vim-tree behaviour `h` has everywhere else.
func (v *FlowsView) collapseSelected() {
	rows := v.treeRows()
	if v.treeCursor < 0 || v.treeCursor >= len(rows) {
		return
	}
	row := rows[v.treeCursor]
	if row.hasKids && !row.collapsed {
		v.collapsed[row.node.ID] = true
		return
	}
	if row.node.ParentID == "" {
		return
	}
	v.collapsed[row.node.ParentID] = true
	for i, r := range v.treeRows() {
		if r.node.ID == row.node.ParentID {
			v.treeCursor = i
			return
		}
	}
}

// openSelectedAgent jumps to the selected step's agent in the Agents view.
func (v *FlowsView) openSelectedAgent() tea.Cmd {
	n, ok := v.selectedNode()
	if !ok || n.Kind != service.FlowNodeStep || n.AgentID == "" {
		v.statusMsg = "No agent here — select a step that has been dispatched"
		return nil
	}
	agentID := n.AgentID
	return func() tea.Msg { return OpenAgentMsg{AgentID: agentID} }
}

// confirmCancel puts the cancel behind a ConfirmPrompt: it stops the flow's
// tasks, which is not something to do on a stray keystroke.
func (v *FlowsView) confirmCancel() tea.Cmd {
	f, ok := v.selectedFlow()
	if !ok {
		v.statusMsg = "No flow selected"
		return nil
	}
	svc := v.services
	if svc == nil {
		v.statusMsg = "Flow service unavailable"
		return nil
	}
	v.cancelConfirm.Show("Cancel Flow",
		fmt.Sprintf("Flow: %s — %s\nState: %s  %s\nThis stops the tasks the flow created.",
			f.ID, f.Label, f.State, f.rounds()),
		func() tea.Cmd {
			flowID := f.ID
			return func() tea.Msg {
				if err := svc.Flow.Cancel(v.ctx(), flowID); err != nil {
					return flowActionMsg{action: "cancel", err: err}
				}
				return flowActionMsg{action: "cancel", flowID: flowID,
					note: fmt.Sprintf("Flow %s cancelled", flowID)}
			}
		})
	return nil
}

// openStartModal opens the start modal with the workflow select loaded and
// the round cap at the daemon's own default.
func (v *FlowsView) openStartModal() {
	v.showStart = true
	v.showWorkflowPicker = false
	v.startField = flowFieldWorkflow
	for i := range v.startInputs {
		v.startInputs[i].Clear()
	}
	v.startInputs[flowFieldRounds].Set("3")
	v.loadWorkflowOptions()
}

// handleStartInput drives the start modal: tab/shift+tab between the four
// fields, enter to submit, esc to abandon — and, on the workflow field,
// space to open the picker, since that field is a select with no text to
// type into.
func (v *FlowsView) handleStartInput(msg tea.KeyMsg) tea.Cmd {
	if v.showWorkflowPicker {
		return v.handleWorkflowPicker(msg)
	}
	switch msg.String() {
	case "esc":
		v.showStart = false
		return nil
	case "tab", "down":
		v.startField = (v.startField + 1) % flowFieldCount
		return nil
	case "shift+tab", "up":
		v.startField = (v.startField - 1 + flowFieldCount) % flowFieldCount
		return nil
	case "enter":
		return v.submitStart()
	case " ":
		if v.startField == flowFieldWorkflow {
			v.showWorkflowPicker = true
			return nil
		}
		v.startInputs[v.startField].HandleKey(msg)
		return nil
	default:
		// The workflow field holds no input, so a stray keystroke on it
		// must not land in whatever field was last focused.
		if v.startField != flowFieldWorkflow {
			v.startInputs[v.startField].HandleKey(msg)
		}
		return nil
	}
}

// submitStart validates what it can locally and hands the rest to the
// service, whose refusals (a work dir that is not a directory, worktree
// isolation, a max_rounds outside 1..20) come back as a flash message. The
// work dir is the selected workflow's root, never typed — see
// flowWorkflowOptionFrom for why the root and not a repo worktree.
func (v *FlowsView) submitStart() tea.Cmd {
	goal := strings.TrimSpace(v.startInputs[flowFieldGoal].Value)
	review := strings.TrimSpace(v.startInputs[flowFieldReview].Value)
	roundsText := strings.TrimSpace(v.startInputs[flowFieldRounds].Value)

	// Nothing to run in is not an error to hand to the service: the modal
	// stays open saying where a workflow comes from.
	if v.workflowChoice == nil {
		if len(v.workflowOptions) == 0 {
			v.statusMsg = "No workflow to run in — create one in the Workflows view first"
		} else {
			v.statusMsg = "Select a workflow first — press space on the Workflow field"
		}
		return nil
	}
	workDir := v.workflowChoice.RootDir

	if goal == "" {
		v.statusMsg = "A goal is required"
		return nil
	}
	rounds := 0
	if roundsText != "" {
		n, err := strconv.Atoi(roundsText)
		if err != nil {
			v.statusMsg = fmt.Sprintf("Max rounds %q is not a number", roundsText)
			return nil
		}
		rounds = n
	}
	svc := v.services
	if svc == nil {
		v.statusMsg = "Flow service unavailable"
		return nil
	}

	v.showStart = false
	req := service.FlowStartRequest{
		Goal:       goal,
		ReviewGoal: review,
		WorkDir:    workDir,
		MaxRounds:  rounds,
	}
	return func() tea.Msg {
		f, err := svc.Flow.Start(v.ctx(), req)
		if err != nil {
			return flowActionMsg{action: "start", err: err}
		}
		return flowActionMsg{action: "start", flowID: f.ID,
			note: fmt.Sprintf("Flow %s started in %s", f.ID, filepath.Base(f.WorkDir))}
	}
}
