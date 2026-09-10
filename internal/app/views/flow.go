package views

import (
	"fmt"
	"path/filepath"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"

	tea "github.com/charmbracelet/bubbletea"
)

// FlowInfo is one row of the flow list: everything the left pane shows,
// projected off a service.Flow so the list survives a refresh that replaces
// every flow record.
type FlowInfo struct {
	ID         string
	Label      string
	State      service.FlowState
	Round      int
	MaxRounds  int
	WorkDir    string
	Goal       string
	ReviewGoal string
}

// String is what components.Filter matches "/" input against, so filtering
// covers the id, the title, the state and the working directory — the four
// things a row shows.
func (f FlowInfo) String() string {
	return fmt.Sprintf("%s %s %s %s", f.ID, f.Label, f.State, filepath.Base(f.WorkDir))
}

// rounds is the "2/3" progress cell, shown in all three places a round count
// belongs: the list row, the tree root and the tree pane's header.
func (f FlowInfo) rounds() string {
	return fmt.Sprintf("round %d/%d", f.Round, f.MaxRounds)
}

// flowFocus tracks which of the two panes owns j/k.
type flowFocus int

const (
	focusFlowList flowFocus = iota
	focusFlowTree
)

// Start-modal fields, in tab order. The first is a select, not an input:
// a flow belongs in a workflow's worktrees, and the user already has a
// workflow list, so typing a path is the wrong affordance.
const (
	flowFieldWorkflow = iota
	flowFieldGoal
	flowFieldReview
	flowFieldRounds
	flowFieldCount
)

// flowsLoadedMsg carries one refresh pass: the flow list plus the tree of
// whichever flow was selected when the command was built. Both are fetched
// off the event loop and applied on it, so nothing the daemon returns is
// written to view state from a goroutine.
type flowsLoadedMsg struct {
	flows  []service.Flow
	tree   *service.FlowTree
	treeID string
	err    error
}

// flowTreeLoadedMsg carries a tree fetched on its own, after the list cursor
// moved to another flow.
type flowTreeLoadedMsg struct {
	flowID string
	nodes  []service.FlowTreeNode
	err    error
}

// flowActionMsg is the result of a Start, Continue or Cancel. Refusals land
// in the flash line rather than anywhere that can take the view down: an
// invalid work dir, a use_worktree option, an out-of-range max_rounds, a cap
// already at the ceiling and a work dir that has since been removed are all
// ordinary answers from the service.
type flowActionMsg struct {
	action string
	flowID string
	note   string
	err    error
}

// FlowsView shows adversarial review flows: a filterable list on the left,
// the selected flow's agent tree on the right.
type FlowsView struct {
	viewBase

	flows      []FlowInfo
	filter     *components.Filter[FlowInfo]
	selectedID string

	// Tree pane. collapsed holds node IDs the user folded; absent means
	// expanded, so a newly arrived round shows up already open.
	treeNodes  []service.FlowTreeNode
	treeCursor int
	collapsed  map[string]bool

	// expanded names the block — findings or request — that has taken the
	// tree's height, if one has, and blockScroll is its first visible line
	// then. One block expands at a time, so one offset serves both.
	expanded    flowBlock
	blockScroll int

	focus     flowFocus
	loading   bool
	err       error
	statusMsg string

	// Start modal, in the TextInput + ConfirmPrompt idiom WorkflowsView's
	// start modal uses — four fields instead of one. startInputs is indexed
	// by field; the flowFieldWorkflow slot stays empty because that field
	// is the workflow picker below, not something to type into.
	showStart   bool
	startField  int
	startInputs [flowFieldCount]components.TextInput

	// Workflow select for the start modal: the rows on offer, the one
	// chosen (nil until one is), and the picker overlay over them.
	showWorkflowPicker bool
	workflowOptions    []flowWorkflowOption
	workflowChoice     *flowWorkflowOption
	workflowPicker     *components.Filter[flowWorkflowOption]

	cancelConfirm components.ConfirmPrompt
	removeConfirm components.ConfirmPrompt

	// Continue modal: the one-field form 'C' opens over a flow that
	// finished without being accepted. continueTarget is the row the modal
	// is about, captured on open so a refresh landing behind it cannot
	// retarget the request at whatever the cursor has since moved to.
	showContinue   bool
	continueTarget FlowInfo
	continueRounds components.TextInput

	// workflows, when set (project mode), supplies the workflows the start
	// modal offers: a flow belongs in a workflow's worktrees, not in the
	// live checkout every other view is pointed at. In repo mode it is nil
	// and the modal falls back to repoPath, labelled as such.
	workflows *WorkflowsView
}

// NewFlowsView creates the flows view for the given repo path, which is also
// the work dir the start modal falls back to outside project mode.
func NewFlowsView(repoPath string) *FlowsView {
	v := &FlowsView{
		viewBase:  viewBase{repoPath: repoPath, width: 80, height: 24},
		collapsed: make(map[string]bool),
	}
	v.filter = components.NewFilter([]FlowInfo{}, v.renderFlowItem)
	v.filter.SetHeight(v.listHeight())
	return v
}

// SetWorkflowsView wires the project-mode workflows view, whose workflows are
// the ones a flow started from here can run in.
func (v *FlowsView) SetWorkflowsView(wv *WorkflowsView) { v.workflows = wv }

// Init loads the flow list and the selected flow's tree. With no services
// wired there is nothing to wait for, so the view does not claim to be
// loading either.
func (v *FlowsView) Init() tea.Cmd {
	cmd := v.RefreshCmd()
	v.loading = cmd != nil
	return cmd
}

// RefreshCmd fetches the list and the selected flow's tree in one pass. The
// app calls it on the StreamTickMsg chain and on every AgentUpdateMsg, which
// is what makes the view live: every flow transition is caused by an agent
// event, and the tick covers the rest.
func (v *FlowsView) RefreshCmd() tea.Cmd {
	svc := v.services
	if svc == nil {
		return nil
	}
	selected := v.selectedID
	return func() tea.Msg {
		flows, err := svc.Flow.List(v.ctx(), nil)
		if err != nil {
			return flowsLoadedMsg{err: err}
		}
		if selected == "" && len(flows) > 0 {
			selected = flows[0].ID
		}
		msg := flowsLoadedMsg{flows: flows, treeID: selected}
		if selected != "" {
			// A tree error is not a list error: the flow may have been
			// removed between the two calls, and the list still stands.
			if t, terr := svc.Flow.Tree(v.ctx(), selected); terr == nil {
				msg.tree = t
			}
		}
		return msg
	}
}

// treeCmd fetches one flow's tree, used when the list cursor moves.
func (v *FlowsView) treeCmd(flowID string) tea.Cmd {
	svc := v.services
	if svc == nil || flowID == "" {
		return nil
	}
	return func() tea.Msg {
		t, err := svc.Flow.Tree(v.ctx(), flowID)
		if err != nil {
			return flowTreeLoadedMsg{flowID: flowID, err: err}
		}
		return flowTreeLoadedMsg{flowID: flowID, nodes: t.Nodes}
	}
}

// Refresh satisfies the app's synchronous refresh hook.
func (v *FlowsView) Refresh() error {
	svc := v.services
	if svc == nil {
		return service.ErrUnavailable
	}
	flows, err := svc.Flow.List(v.ctx(), nil)
	if err != nil {
		v.err = err
		return err
	}
	v.applyFlows(flowsLoadedMsg{flows: flows, treeID: v.selectedID})
	return nil
}

// Update handles messages for the flows view.
func (v *FlowsView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return v.handleFlowKeyMsg(msg)

	case flowsLoadedMsg:
		return v, v.applyFlows(msg)

	case flowTreeLoadedMsg:
		if msg.flowID == v.selectedID {
			if msg.err != nil {
				v.setTreeNodes(nil)
			} else {
				v.setTreeNodes(msg.nodes)
			}
		}

	case flowActionMsg:
		v.loading = false
		if msg.err != nil {
			v.statusMsg = fmt.Sprintf("Flow %s refused: %v", msg.action, msg.err)
			return v, nil
		}
		v.statusMsg = msg.note
		if msg.action == "remove" {
			v.setTreeNodes(nil)
			v.treeCursor = 0
		} else if msg.flowID != "" {
			v.selectedID = msg.flowID
		}
		return v, v.RefreshCmd()

	case tea.WindowSizeMsg:
		v.SetSize(msg.Width, msg.Height)

	case tea.MouseMsg:
		if v.filter != nil && v.filter.HandleMouse(msg) {
			return v, v.syncSelectionFromCursor()
		}
	}
	return v, nil
}

// applyFlows installs a refresh pass's result, keeping the cursor on the
// flow it was on. When that flow is gone — removed, typically — the cursor
// lands on a neighbour whose tree the refresh did not fetch, so the stale
// tree is dropped and a fetch for the new selection is returned.
func (v *FlowsView) applyFlows(msg flowsLoadedMsg) tea.Cmd {
	v.loading = false
	v.err = msg.err
	if msg.err != nil {
		return nil
	}
	flows := make([]FlowInfo, 0, len(msg.flows))
	for _, f := range msg.flows {
		flows = append(flows, flowInfoFrom(f))
	}
	v.flows = flows
	v.filter.SetItems(v.flows)

	before := v.selectedID
	if len(flows) == 0 {
		v.selectedID = ""
	} else if v.selectedID == "" {
		v.selectedID = flows[0].ID
	}
	v.syncListCursor()

	if msg.tree != nil && msg.treeID == v.selectedID {
		v.setTreeNodes(msg.tree.Nodes)
		return nil
	}
	if v.selectedID != before {
		v.setTreeNodes(nil)
		v.treeCursor = 0
		return v.treeCmd(v.selectedID)
	}
	return nil
}

// flowInfoFrom projects a flow record onto its list row. The label mirrors
// the daemon's own tree-root label: the title, or the first line of the goal.
func flowInfoFrom(f service.Flow) FlowInfo {
	label := strings.TrimSpace(f.Title)
	if label == "" {
		label = strings.TrimSpace(f.Goal)
		if i := strings.IndexByte(label, '\n'); i >= 0 {
			label = strings.TrimSpace(label[:i])
		}
	}
	return FlowInfo{
		ID:         f.ID,
		Label:      label,
		State:      f.State,
		Round:      len(f.Rounds),
		MaxRounds:  f.MaxRounds,
		WorkDir:    f.WorkDir,
		Goal:       f.Goal,
		ReviewGoal: f.ReviewGoal,
	}
}

// setTreeNodes installs a tree and clamps the cursor onto it.
func (v *FlowsView) setTreeNodes(nodes []service.FlowTreeNode) {
	v.treeNodes = nodes
	rows := v.treeRows()
	if v.treeCursor >= len(rows) {
		v.treeCursor = max(len(rows)-1, 0)
	}
}

// treeRows returns the visible rows of the tree pane.
func (v *FlowsView) treeRows() []flowTreeRow {
	return buildFlowTreeRows(v.treeNodes, v.collapsed)
}

// selectedFlow returns the flow under the list cursor.
func (v *FlowsView) selectedFlow() (FlowInfo, bool) {
	item, idx := v.filter.SelectedItem()
	if idx < 0 {
		return FlowInfo{}, false
	}
	return item, true
}

// selectedNode returns the tree node under the tree cursor.
func (v *FlowsView) selectedNode() (service.FlowTreeNode, bool) {
	rows := v.treeRows()
	if v.treeCursor < 0 || v.treeCursor >= len(rows) {
		return service.FlowTreeNode{}, false
	}
	return rows[v.treeCursor].node, true
}

// syncListCursor moves the list cursor back onto v.selectedID after the
// items were replaced or the filter changed.
func (v *FlowsView) syncListCursor() {
	for i, f := range v.filter.FilteredItems() {
		if f.ID == v.selectedID {
			v.filter.SelectAt(i)
			return
		}
	}
	// The selected flow filtered out or disappeared: follow the cursor.
	if item, idx := v.filter.SelectedItem(); idx >= 0 {
		v.selectedID = item.ID
	}
}

// syncSelectionFromCursor adopts whatever the list cursor now points at,
// fetching that flow's tree when the selection actually changed.
func (v *FlowsView) syncSelectionFromCursor() tea.Cmd {
	item, idx := v.filter.SelectedItem()
	if idx < 0 || item.ID == v.selectedID {
		return nil
	}
	v.selectedID = item.ID
	v.setTreeNodes(nil)
	v.treeCursor = 0
	v.expanded = flowBlockNone
	v.blockScroll = 0
	return v.treeCmd(item.ID)
}

// listHeight is the height available to the flow list pane.
func (v *FlowsView) listHeight() int {
	return max(v.height-flowChromeLines, 4)
}

// SetSize updates the dimensions and the list pane's height — and the
// workflow picker's, which may be open across a resize.
func (v *FlowsView) SetSize(width, height int) {
	v.viewBase.SetSize(width, height)
	v.filter.SetHeight(v.listHeight())
	if v.workflowPicker != nil {
		v.workflowPicker.SetHeight(v.workflowPickerHeight())
	}
}

// CapturesInput reports the modes in which global keys must not be stolen.
func (v *FlowsView) CapturesInput() bool {
	return v.showStart || v.showContinue || v.cancelConfirm.Visible || v.removeConfirm.Visible || v.filter.IsActive()
}

// CapturesKey claims tab, which toggles pane focus rather than cycling views.
func (v *FlowsView) CapturesKey(key string) bool { return key == "tab" }

// ShortHelp returns the status-bar help line.
func (v *FlowsView) ShortHelp() string {
	return "n:start  C:continue  c:cancel  D:remove  a:agent  tab:pane  l/h:expand f:findings  r:refresh  /:filter"
}

// KeyBindings returns the view's bindings for the help overlay.
func (v *FlowsView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "n", Description: "Start a flow"},
		{Key: "C", Description: "Continue the selected flow with more rounds (rejected/errored/cancelled only)"},
		{Key: "c", Description: "Cancel the selected flow"},
		{Key: "D", Description: "Remove the selected flow (cancels it first if still running, closing its agents)"},
		{Key: "a", Description: "Open the selected step's agent"},
		{Key: "r", Description: "Refresh flows"},
		{Key: "Tab", Description: "Switch focus between list and tree"},
		{Key: "j/k", Description: "Navigate"},
		{Key: "l/→/Enter", Description: "Expand tree node (or focus tree)"},
		{Key: "h/←", Description: "Collapse tree node"},
		{Key: "f", Description: "Expand the findings block over the tree (j/k scroll it)"},
		{Key: "/", Description: "Filter flows"},
		{Key: "Esc", Description: "Back to the flow list"},
	}
}
