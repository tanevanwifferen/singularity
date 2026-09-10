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
	if handled, cmd := v.removeConfirm.HandleKey(msg); handled {
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
	// navigation, / the filter, and g/G when a block is expanded, see
	// below), and an uppercase C collides with nothing the app or the
	// router claims either — their capitals are R, P, T; g stays the
	// router's Git submenu trigger except while this view has a block
	// expanded, when CapturesKey hands it to the scroll handling below
	// instead.
	case "C":
		v.openContinueModal()
		return v, nil

	// Shift-d for the same reason: it mirrors the agent view's remove key
	// (there "c" once the agent is stopped; here "c" is taken by cancel) and
	// collides with nothing else this view or the app claims.
	case "D":
		return v, v.confirmRemove()

	case "a":
		return v, v.openSelectedAgent()

	// An expanded block takes the tree's height and its j/k: it is a
	// second thing to scroll, and one pane scrolls at a time. f cycles
	// findings → request → collapsed, findings first because what the
	// reviewer said is what the view is opened for.
	case "f":
		v.expanded = v.nextExpanded()
		v.blockScroll = 0
		return v, nil

	case "/":
		v.filter.Update(msg)
		return v, nil

	case "j", "down":
		if v.expandedShown() {
			v.blockScroll++
			return v, nil
		}
		if v.focus == focusFlowTree {
			v.moveTreeCursor(1)
			return v, nil
		}
		v.filter.CursorDown()
		return v, v.syncSelectionFromCursor()

	case "k", "up":
		if v.expandedShown() {
			v.blockScroll = max(v.blockScroll-1, 0)
			return v, nil
		}
		if v.focus == focusFlowTree {
			v.moveTreeCursor(-1)
			return v, nil
		}
		v.filter.CursorUp()
		return v, v.syncSelectionFromCursor()

	// Vim-style paging for the expanded block: g/G jump to its top/bottom,
	// ctrl+d/ctrl+u (and pgdown/pgup) scroll it by half a page. G's offset
	// is left over-large on purpose — renderTreePane's clampBlockScroll
	// pulls it back to the last full page every render, the same clamp j
	// relies on to stop at the end.
	case "g":
		if v.expandedShown() {
			v.blockScroll = 0
		}
		return v, nil

	case "G":
		if v.expandedShown() {
			v.blockScroll = 1 << 30
		}
		return v, nil

	case "ctrl+d", "pgdown":
		if v.expandedShown() {
			v.blockScroll += max(v.expandedMaxLines()/2, 1)
		}
		return v, nil

	case "ctrl+u", "pgup":
		if v.expandedShown() {
			v.blockScroll = max(v.blockScroll-max(v.expandedMaxLines()/2, 1), 0)
		}
		return v, nil

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
		v.expanded = flowBlockNone
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

// confirmRemove puts flow removal behind a ConfirmPrompt: it deletes the
// flow's record. Flow.Remove refuses a flow that has not reached a terminal
// state (service.ErrConflict), so a still-running flow is cancelled first —
// which stops its tasks and terminates their agent processes, the same way
// confirmCancel does — before the record is removed.
func (v *FlowsView) confirmRemove() tea.Cmd {
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
	msg := fmt.Sprintf("Flow: %s — %s\nState: %s  %s\n", f.ID, f.Label, f.State, f.rounds())
	if !f.State.Terminal() {
		msg += "This stops the tasks and closes their agents, then deletes the flow."
	} else {
		msg += "This deletes the flow's record. Processes for settled tasks are already terminated."
	}
	v.removeConfirm.Show("Remove Flow", msg,
		func() tea.Cmd {
			flowID, state := f.ID, f.State
			return func() tea.Msg {
				ctx := v.ctx()
				if !state.Terminal() {
					if err := svc.Flow.Cancel(ctx, flowID); err != nil {
						return flowActionMsg{action: "remove", err: err}
					}
				}
				if err := svc.Flow.Remove(ctx, flowID); err != nil {
					return flowActionMsg{action: "remove", err: err}
				}
				return flowActionMsg{action: "remove",
					note: fmt.Sprintf("Flow %s removed", flowID)}
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
