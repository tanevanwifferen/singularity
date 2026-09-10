package views

import (
	"fmt"
	"path/filepath"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	"github.com/charmbracelet/lipgloss"
)

// flowChromeLines is what the two panes lose to the header, the flash line,
// the separator and the help line.
const flowChromeLines = 6

// flowTreeRow is one visible line of the tree pane: a node, the ascii prefix
// its position implies, and the two facts the renderer cannot re-derive from
// a flat list — whether it has children, and the verdict of the round a
// review step belongs to.
type flowTreeRow struct {
	node      service.FlowTreeNode
	prefix    string
	hasKids   bool
	collapsed bool
	verdict   *service.FlowVerdict
}

// buildFlowTreeRows turns the daemon's flat, parent-linked node list into the
// tree pane's rows — in one forward pass, with no recursion. That is the
// whole reason Flow.Tree returns the nodes flat: the daemon emits them
// root-first and in submit order, so a node's parent is always already
// placed, and its depth and branch characters follow from a running stack of
// one continuation segment per level.
//
// Nodes under a collapsed ancestor are dropped. A node whose parent is
// missing from the list is drawn as a root rather than silently discarded.
func buildFlowTreeRows(nodes []service.FlowTreeNode, collapsed map[string]bool) []flowTreeRow {
	kids := make(map[string]int, len(nodes))
	lastKid := make(map[string]string, len(nodes))
	for _, n := range nodes {
		if n.ParentID == "" {
			continue
		}
		kids[n.ParentID]++
		lastKid[n.ParentID] = n.ID
	}

	depth := make(map[string]int, len(nodes))
	hidden := make(map[string]bool, len(nodes))
	byID := make(map[string]service.FlowTreeNode, len(nodes))
	// segs[i] is the prefix segment contributed by the ancestor at depth
	// i+1: four spaces when it was its parent's last child, a pipe when
	// more siblings follow it.
	var segs []string
	rows := make([]flowTreeRow, 0, len(nodes))

	for _, n := range nodes {
		byID[n.ID] = n
		d := 0
		if n.ParentID != "" {
			if pd, ok := depth[n.ParentID]; ok {
				d = pd + 1
			}
			if hidden[n.ParentID] || collapsed[n.ParentID] {
				hidden[n.ID] = true
				depth[n.ID] = d
				continue
			}
		}
		depth[n.ID] = d

		prefix := ""
		if d > 0 {
			for len(segs) < d {
				segs = append(segs, "")
			}
			prefix = strings.Join(segs[:d-1], "")
			if lastKid[n.ParentID] == n.ID {
				prefix += "└── "
				segs[d-1] = "    "
			} else {
				prefix += "├── "
				segs[d-1] = "│   "
			}
		}

		row := flowTreeRow{
			node:      n,
			prefix:    prefix,
			hasKids:   kids[n.ID] > 0,
			collapsed: collapsed[n.ID],
		}
		// The verdict is the round's, never the step's, so the review step
		// borrows its parent's to show what the round concluded.
		if n.Kind == service.FlowNodeStep && n.Label == "review" {
			row.verdict = byID[n.ParentID].Verdict
		}
		rows = append(rows, row)
	}
	return rows
}

// View renders the flows view: list pane left, tree pane right.
func (v *FlowsView) View() string {
	th := theme.GetTheme()
	var s strings.Builder

	s.WriteString(th.DashboardTitle.Render(" Adversarial Review Flows "))
	if len(v.flows) > 0 {
		s.WriteString(th.MutedTextStyle.Render(fmt.Sprintf("  %d flow(s)", len(v.flows))))
	}
	if v.loading {
		s.WriteString(th.MutedTextStyle.Render("  loading…"))
	}
	s.WriteString("\n")

	if v.showStart {
		s.WriteString(v.renderStartModal())
		return s.String()
	}
	if v.showContinue {
		s.WriteString(v.renderContinueModal())
		return s.String()
	}
	if v.cancelConfirm.Visible {
		s.WriteString("\n")
		s.WriteString(v.cancelConfirm.Render(modalWidth(v.width)))
		return s.String()
	}
	if v.removeConfirm.Visible {
		s.WriteString("\n")
		s.WriteString(v.removeConfirm.Render(modalWidth(v.width)))
		return s.String()
	}

	s.WriteString(v.renderFlowFlash())

	if v.err != nil {
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" Error: %v", v.err)))
		s.WriteString("\n")
	}

	if len(v.flows) == 0 {
		s.WriteString("\n")
		s.WriteString(th.MutedTextStyle.Render(" No flows yet."))
		s.WriteString("\n\n")
		s.WriteString(th.MutedTextStyle.Render(" Press 'n' to start one: a flow runs implement → review → fix"))
		s.WriteString("\n")
		s.WriteString(th.MutedTextStyle.Render(" rounds over one working directory until a reviewer accepts."))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render(" " + v.ShortHelp()))
		return s.String()
	}

	listW := v.listWidth()
	treeW := max(v.width-listW-1, 20)
	left := lipgloss.NewStyle().Width(listW).Render(v.renderListPane())
	right := lipgloss.NewStyle().Width(treeW).Render(v.renderTreePane(treeW))
	s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right))
	s.WriteString("\n")
	s.WriteString(renderSeparator())
	s.WriteString(th.Help.Render(" " + v.ShortHelp()))

	return s.String()
}

// listWidth is the width of the left pane, wide enough for an id, a state
// and a round count without crowding out the tree.
func (v *FlowsView) listWidth() int {
	return min(max(v.width*2/5, 32), 44)
}

// renderFlowFlash renders the flash-message line: start refusals, cancel
// confirmations and the "no agent here" hint all land here.
func (v *FlowsView) renderFlowFlash() string {
	if v.statusMsg == "" {
		return ""
	}
	th := theme.GetTheme()
	style := th.DashboardAccentStyle
	if strings.Contains(v.statusMsg, "refused") || strings.Contains(v.statusMsg, "unavailable") {
		style = th.DashboardErrorStyle
	}
	return style.Render(" "+v.statusMsg) + "\n"
}

// renderListPane renders the flow list, which is a components.Filter list.
func (v *FlowsView) renderListPane() string {
	th := theme.GetTheme()
	var s strings.Builder

	title := " Flows "
	if v.focus == focusFlowList {
		title = ">Flows "
	}
	s.WriteString(th.DashboardTitle.Render(title))
	s.WriteString("\n")
	s.WriteString(v.filter.View())
	return s.String()
}

// renderFlowItem renders one flow row: id, title, state, round count and the
// basename of the work dir.
func (v *FlowsView) renderFlowItem(f FlowInfo, index int, selected bool) string {
	th := theme.GetTheme()

	prefix := "  "
	if selected {
		prefix = " >"
	}
	// The row is laid out from its fixed parts inwards: the id, state and
	// round count are the point of the row, so the title and the work dir
	// share whatever they leave.
	width := v.listWidth()
	base := filepath.Base(f.WorkDir)
	room := width - (len(prefix) + len(f.ID) + 1 + 2 + len(f.State) + 2 + len(f.rounds()))
	showBase := base != "" && base != "." && room >= 24
	if showBase {
		base = clipText(base, 18)
		room -= 2 + len([]rune(base))
	}

	var line strings.Builder
	line.WriteString(prefix)
	line.WriteString(th.BranchStyle.Render(f.ID))
	line.WriteString(" " + th.StatsStyle.Render(clipText(f.Label, max(room, 6))))
	line.WriteString("  " + flowStateStyle(string(f.State), th).Render(string(f.State)))
	line.WriteString("  " + th.MutedTextStyle.Render(f.rounds()))
	if showBase {
		line.WriteString("  " + th.MutedTextStyle.Render(base))
	}
	// Clipped rather than wrapped: a row that spills onto a second line
	// pushes the tree pane beside it out of alignment.
	return lipgloss.NewStyle().MaxWidth(width).Render(line.String())
}

// renderTreePane renders the selected flow's agent tree, with the round
// count repeated in the pane header — the second of the three places it
// belongs, after the list row and before the tree root.
func (v *FlowsView) renderTreePane(width int) string {
	th := theme.GetTheme()
	var s strings.Builder

	sel, haveSel := v.selectedFlow()

	title := " Tree "
	if v.focus == focusFlowTree {
		title = ">Tree "
	}
	s.WriteString(th.DashboardTitle.Render(title))
	if haveSel {
		s.WriteString(th.MutedTextStyle.Render(fmt.Sprintf(" %s  %s  %s  %s",
			sel.ID, clipText(sel.Label, max(width-40, 10)), sel.State, sel.rounds())))
	}
	s.WriteString("\n")

	rows := v.treeRows()
	if len(rows) == 0 {
		s.WriteString(th.MutedTextStyle.Render(" No rounds yet — the reconciler submits round 1."))
		s.WriteString("\n")
		return s.String()
	}

	start, end := calcViewport(v.height, flowChromeLines+1, v.treeCursor, len(rows))
	for i := start; i < end && i < len(rows); i++ {
		s.WriteString(v.renderTreeRow(rows[i], i == v.treeCursor, sel, width))
		s.WriteString("\n")
	}
	if end < len(rows) || start > 0 {
		s.WriteString(th.MutedTextStyle.Render(fmt.Sprintf(" %d-%d of %d nodes", start+1, end, len(rows))))
		s.WriteString("\n")
	}
	return s.String()
}

// renderTreeRow renders one tree line: the cursor, the fold marker, the
// ascii prefix and whatever the node's kind carries.
func (v *FlowsView) renderTreeRow(row flowTreeRow, selected bool, sel FlowInfo, width int) string {
	th := theme.GetTheme()

	cursor := "  "
	if selected && v.focus == focusFlowTree {
		cursor = " >"
	}
	marker := "  "
	if row.hasKids {
		marker = "▾ "
		if row.collapsed {
			marker = "▸ "
		}
	}

	// The marker sits between the branch characters and the label, where a
	// file tree puts it — before the prefix it would drift a column per
	// level.
	head := cursor + row.prefix + marker
	body := flowNodeText(row, sel, max(width-len([]rune(head))-1, 12), th)
	return lipgloss.NewStyle().MaxWidth(width).Render(head + body)
}

// flowNodeText renders a node's own content, which differs per kind: the
// root carries the flow's state and round count, a round its state and
// finding count, a step its task state, agent and agent state — plus, on a
// review step, the summary of the verdict its round produced.
func flowNodeText(row flowTreeRow, sel FlowInfo, width int, th theme.Theme) string {
	n := row.node
	style := flowStateStyle(n.State, th)

	switch n.Kind {
	case service.FlowNodeFlow:
		return fmt.Sprintf("%s  %s  %s  %s",
			th.BranchStyle.Render(n.ID),
			th.StatsStyle.Render(clipText(n.Label, max(width-34, 8))),
			style.Render(n.State),
			th.MutedTextStyle.Render(sel.rounds()))

	case service.FlowNodeRound:
		line := fmt.Sprintf("%s  %s", th.StatsStyle.Render(n.Label), style.Render(n.State))
		if n.Verdict != nil && len(n.Verdict.Findings) > 0 {
			line += th.MutedTextStyle.Render(fmt.Sprintf("  %d finding(s)", len(n.Verdict.Findings)))
		}
		return line

	default:
		head := fmt.Sprintf("%-10s %s", n.Label, style.Render(n.State))
		return head + flowStepCols(row, width-11-len([]rune(n.State)), th)
	}
}

// flowStepCols renders a step's columns after its label and task state: the
// task id, the agent and its state, and — on a review step — the verdict its
// round produced. Each column is measured unstyled, because ANSI codes make
// the rendered string useless for measuring, and the narrow-pane order of
// sacrifice is task id, then agent: a review step never loses the verdict,
// which is what the round was for.
func flowStepCols(row flowTreeRow, room int, th theme.Theme) string {
	n := row.node
	type col struct{ text, styled string }
	var task, agent, verdict col

	if n.TaskID != "" {
		task = col{"  " + n.TaskID, th.MutedTextStyle.Render("  " + n.TaskID)}
	}
	if n.AgentID != "" {
		agent = col{"  " + n.AgentID, "  " + th.BranchStyle.Render(n.AgentID)}
		if n.AgentState != "" {
			agent.text += "  " + n.AgentState
			agent.styled += "  " + flowStateStyle(n.AgentState, th).Render(n.AgentState)
		}
	}
	verdictStyle := th.MutedTextStyle
	if row.verdict != nil {
		verdictStyle = flowStateStyle(string(row.verdict.Decision), th)
		text := "  " + string(row.verdict.Decision)
		if k := len(row.verdict.Findings); k > 0 {
			text += fmt.Sprintf(": %d finding(s)", k)
		}
		verdict = col{text, verdictStyle.Render(text)}
	}

	used := func() int {
		return len([]rune(task.text)) + len([]rune(agent.text)) + len([]rune(verdict.text))
	}
	if used() > room {
		task = col{}
	}
	if used() > room {
		agent = col{}
	}
	out := task.styled + agent.styled + verdict.styled

	// The verdict's prose summary is the first column to give up space and
	// the last to get it: it is the only one a wider terminal simply adds.
	if row.verdict != nil && row.verdict.Summary != "" {
		if spare := room - used() - 3; spare >= 12 {
			out += verdictStyle.Render(" — " + clipText(row.verdict.Summary, spare))
		}
	}
	return out
}

// flowStateStyle colours a state name. It takes a plain string because the
// three node kinds report from three different enums — a flow's state, a
// round's, and a queued task's — which is exactly why FlowTreeNode.State is
// a string too.
func flowStateStyle(state string, th theme.Theme) lipgloss.Style {
	switch state {
	case "accepted", "accept", "done", "complete":
		return th.DashboardAccentStyle
	case "rejected", "reject", "failed", "errored", "error":
		return th.DashboardErrorStyle
	case "running", "pending", "ready", "starting", "routing":
		return th.InfoStyle
	case "blocked", "waiting_human":
		return th.WarningStyle
	default:
		return th.MutedTextStyle
	}
}

// renderStartModal renders the start-a-flow form: the workflow select, goal,
// optional review focus and the round cap, in the TextInput idiom
// WorkflowsView's start modal uses. With the picker open it renders that
// instead — one modal at a time, as WorktreeView's branch picker does.
func (v *FlowsView) renderStartModal() string {
	if v.showWorkflowPicker {
		return v.renderWorkflowPicker()
	}
	th := theme.GetTheme()
	width := modalWidth(v.width)
	field := func(idx int, label string) string {
		marker := "  "
		if idx == v.startField {
			marker = "> "
		}
		value := v.startInputs[idx].Value
		if idx == v.startField {
			value = v.startInputs[idx].RenderPlain()
		}
		return fmt.Sprintf("%s%-13s %s", marker, label, value)
	}

	wfMarker := "  "
	wfHint := ""
	if v.startField == flowFieldWorkflow {
		wfMarker = "> "
		wfHint = "   (space to choose)"
	}
	lines := []string{
		"",
		fmt.Sprintf("%s%-13s %s%s", wfMarker, "Workflow:", v.workflowFieldValue(), wfHint),
	}
	lines = append(lines, v.workflowSummaryLines(width-4)...)
	lines = append(lines,
		field(flowFieldGoal, "Goal:"),
		field(flowFieldReview, "Review focus:"),
		field(flowFieldRounds, "Max rounds:"),
		"",
		"  Every round runs in the workflow root, which is not a",
		"  git repo itself: each repo is a subdirectory of it. So",
		"  implementer and reviewer both see every repo, and a",
		"  cross-repo change is reviewed as one change.",
	)
	if v.statusMsg != "" {
		lines = append(lines, "", "  "+v.statusMsg)
	}
	lines = append(lines, "", "  Enter: Start  Space: Choose workflow  Tab: Next field  Esc: Cancel")
	return renderModal("Start Adversarial Review Flow", lines, width) + "\n" +
		th.Help.Render(" Review focus is optional; max rounds must be 1–20.")
}

// clipText shortens s to at most width runes, ellipsising when it cuts.
func clipText(s string, width int) string {
	r := []rune(s)
	if width <= 1 || len(r) <= width {
		return s
	}
	return string(r[:width-1]) + "…"
}
