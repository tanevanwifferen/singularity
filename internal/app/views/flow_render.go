package views

import (
	"fmt"
	"path/filepath"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	"github.com/charmbracelet/lipgloss"
)

// flowChromeLines is what the list pane loses to the header, the flash
// line, the separator and the help line. It counts the flash line even when
// there is none: the list's height is fixed at SetSize, and a list one row
// short is harmless where a list one row long would push the view past the
// terminal. The tree pane, sized at render time, counts what View actually
// emits — see treeChromeLines.
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
	// Both panes end in a newline; joined as they are, that trailing
	// newline becomes a blank row between the panes and the separator.
	left := lipgloss.NewStyle().Width(listW).Render(strings.TrimSuffix(v.renderListPane(), "\n"))
	right := lipgloss.NewStyle().Width(treeW).Render(strings.TrimSuffix(v.renderTreePane(treeW), "\n"))
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
// belongs, after the list row and before the tree root. Above the tree sit
// two blocks: the request the flow was started with, and the latest round's
// findings. Their height comes out of the same budget as the tree's, so the
// pane as a whole never grows past v.height.
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
	// The footer is the one line of the pane whose presence depends on the
	// budget it is part of: it appears exactly when the rows do not all fit.
	// So the budget is drawn up twice — first without it, and again with it
	// reserved only when the rows overflow the tree room it left.
	blockLines := 0
	footer := false
	if haveSel {
		requestMax, findingsMax := v.blockBudget(len(rows), sel, width, footer)
		blockLines = v.blockHeight(sel, width, requestMax, findingsMax)
		if len(rows) > v.height-v.treeChromeLines(footer)-blockLines {
			footer = true
			requestMax, findingsMax = v.blockBudget(len(rows), sel, width, footer)
			blockLines = v.blockHeight(sel, width, requestMax, findingsMax)
		}
		if requestMax > 0 {
			s.WriteString(v.renderFlowRequestBlock(sel, width, requestMax))
		}
		if findingsMax > 0 {
			s.WriteString(v.renderFlowFindingsBlock(width, findingsMax))
		}
	}

	if len(rows) == 0 {
		s.WriteString(th.MutedTextStyle.Render(" No rounds yet — the reconciler submits round 1."))
		s.WriteString("\n")
		return s.String()
	}

	start, end := calcViewport(v.height, v.treeChromeLines(footer)+blockLines, v.treeCursor, len(rows))
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

// treeChromeLines is what the tree pane's rows lose to the lines View
// actually emits around them: the title, the flash and error lines when
// there is anything to flash, the pane's own header, the separator and the
// help line — and the "%d-%d of %d nodes" footer when footer says the rows
// overflow. Counting the flash line when it is absent, as a constant would,
// leaves rows of the terminal blank while the blocks are being clipped for
// want of them.
func (v *FlowsView) treeChromeLines(footer bool) int {
	n := 1 + 1 + 1 + 1 // title, pane header, separator, help
	if v.statusMsg != "" {
		n++
	}
	if v.err != nil {
		n++
	}
	if footer {
		n++
	}
	return n
}

// flowTreeMinRows is the fewest tree rows the two blocks may leave visible
// — a root and a round with its two steps — before they start giving up
// lines of their own. With a block expanded the tree keeps only the root:
// the point of expanding is to read the block.
const (
	flowTreeMinRows         = 4
	flowTreeMinRowsExpanded = 1
)

// flowBlock names one of the two blocks above the tree.
type flowBlock int

const (
	flowBlockNone flowBlock = iota
	flowBlockFindings
	flowBlockRequest
)

// nextExpanded is the state f moves to: findings, then request, then
// collapsed — skipping the findings block while no verdict has landed,
// since there is nothing in it to read.
func (v *FlowsView) nextExpanded() flowBlock {
	switch v.expanded {
	case flowBlockNone:
		if _, verdict := v.latestVerdict(); verdict != nil {
			return flowBlockFindings
		}
		return flowBlockRequest
	case flowBlockFindings:
		return flowBlockRequest
	default:
		return flowBlockNone
	}
}

// blockBudget splits the tree pane's height between the request and the
// findings blocks, returning how many lines each may draw under its header
// — every line, the findings block's "round N: decision" line and any
// "+N more" marker included, so the tree can be charged exactly. Zero for
// both means the pane is too short for the blocks at all and the tree gets
// the whole pane.
//
// The tree keeps flowTreeMinRows when the height allows, then gives them
// up one by one down to a single row before the blocks fall below one line
// each; the findings block has first claim on the rest, because what the
// reviewer said is what the user opens this view for; the request gets
// what the findings leave, and at least a third of the space when both
// want more than there is. An expanded block takes everything but one line
// of the other.
func (v *FlowsView) blockBudget(rowCount int, sel FlowInfo, width int, footer bool) (requestMax, findingsMax int) {
	// The "No rounds yet" line stands in for the rows when there are none.
	rowCount = max(rowCount, 1)
	treeReserve := min(rowCount, flowTreeMinRows)
	if v.expanded != flowBlockNone {
		treeReserve = min(rowCount, flowTreeMinRowsExpanded)
	}
	// Two header lines, then one line each.
	const blockFloor = 2 + 2
	room := v.height - v.treeChromeLines(footer)
	if room-treeReserve < blockFloor {
		treeReserve = min(treeReserve, max(room-blockFloor, 0))
	}
	content := room - treeReserve - 2
	if content < 2 || treeReserve < 1 {
		return 0, 0
	}

	requestNeed := max(len(v.requestLines(sel, width)), 1)
	findingsNeed := 1
	if _, verdict := v.latestVerdict(); verdict != nil {
		findingsNeed = 1 + max(len(v.findingsLines(width)), 1)
	}
	switch v.expanded {
	case flowBlockFindings:
		return 1, max(content-1, 1)
	case flowBlockRequest:
		return max(content-1, 1), 1
	}
	requestFloor := min(requestNeed, max(content/3, 1))
	findingsMax = min(findingsNeed, max(content-requestFloor, 1))
	requestMax = min(requestNeed, max(content-findingsMax, 1))
	return requestMax, findingsMax
}

// blockHeight is the number of lines the two blocks draw under a budget:
// what renderTreePane charges the tree before it renders them.
func (v *FlowsView) blockHeight(sel FlowInfo, width, requestMax, findingsMax int) int {
	n := 0
	if requestMax > 0 {
		n += strings.Count(v.renderFlowRequestBlock(sel, width, requestMax), "\n")
	}
	if findingsMax > 0 {
		n += strings.Count(v.renderFlowFindingsBlock(width, findingsMax), "\n")
	}
	return n
}

// renderFlowRequestBlock renders the flow's original goal — the request the
// implementer was given — and the review focus, if the flow narrowed one,
// as a wrapped multiline block above the tree: the first of the two blocks
// the flow view leads with. It draws at most maxLines lines under its
// header; expanded, it takes the height the tree gave up and scrolls with
// j/k.
func (v *FlowsView) renderFlowRequestBlock(sel FlowInfo, width, maxLines int) string {
	th := theme.GetTheme()
	var s strings.Builder

	lines := v.requestLines(sel, width)
	expanded := v.expanded == flowBlockRequest
	hint := ""
	switch {
	case expanded:
		hint = "  (f: collapse  j/k: scroll)"
	case len(lines) > maxLines && v.expanded == flowBlockFindings:
		hint = "  (f: expand)"
	case len(lines) > maxLines:
		hint = "  (f f: expand)"
	}
	s.WriteString(th.MutedTextStyle.Render(" Request" + hint))
	s.WriteString("\n")

	if len(lines) == 0 {
		s.WriteString(th.MutedTextStyle.Render(" (no goal recorded)"))
		s.WriteString("\n")
		return s.String()
	}
	if expanded {
		v.blockScroll = clampScroll(v.blockScroll, len(lines), maxLines)
		writeScrolledLines(&s, lines, v.blockScroll, maxLines, width, lipgloss.NewStyle())
		return s.String()
	}
	writeCappedLines(&s, lines, maxLines, width, lipgloss.NewStyle(), "line(s)")
	return s.String()
}

// requestLines is the request block's body, wrapped to the pane: the goal,
// then the review focus prefixed so the two cannot be mistaken for one
// paragraph — or the focus alone when it is the only text there is.
func (v *FlowsView) requestLines(sel FlowInfo, width int) []string {
	var paras []string
	if goal := strings.TrimSpace(sel.Goal); goal != "" {
		paras = append(paras, goal)
	}
	if focus := strings.TrimSpace(sel.ReviewGoal); focus != "" {
		paras = append(paras, "Review focus: "+focus)
	}
	if len(paras) == 0 {
		return nil
	}
	return wrapBlock(strings.Join(paras, "\n"), width)
}

// renderFlowFindingsBlock renders the most recent round's verdict — the
// review agent's decision, summary and findings — as a wrapped multiline
// block, so what the reviewer actually said is legible without expanding
// the review step in the tree below. It draws at most maxLines lines under
// its header.
//
// When the full text does not fit it falls back to one clipped line per
// item — the summary, then severity, location and detail of each finding —
// so every finding is at least visible, and the header says how to expand.
// Down to a single line it still shows text: the decision with the finding
// count. Expanded, the block takes the height the tree gave up and scrolls
// with j/k.
func (v *FlowsView) renderFlowFindingsBlock(width, maxLines int) string {
	th := theme.GetTheme()
	var s strings.Builder

	round, verdict := v.latestVerdict()
	if verdict == nil {
		s.WriteString(th.MutedTextStyle.Render(" Findings"))
		s.WriteString("\n")
		s.WriteString(th.MutedTextStyle.Render(" (no review verdict yet)"))
		s.WriteString("\n")
		return s.String()
	}

	// The decision line takes the first budgeted line; the rest is the body.
	budget := maxLines - 1
	lines := v.findingsLines(width)
	expanded := v.expanded == flowBlockFindings
	compact := !expanded && len(lines) > budget

	hint := ""
	switch {
	case expanded:
		hint = "  (f: collapse  j/k: scroll)"
	case compact:
		hint = "  (f: expand)"
	}
	s.WriteString(th.MutedTextStyle.Render(" Findings" + hint))
	s.WriteString("\n")

	decisionStyle := flowStateStyle(string(verdict.Decision), th)
	decision := fmt.Sprintf(" round %d: %s", round, decisionStyle.Render(string(verdict.Decision)))
	if budget < 1 {
		// No room for a body: the count is what the body would have said.
		if n := len(verdict.Findings); n > 0 {
			decision += th.MutedTextStyle.Render(fmt.Sprintf("  %d finding(s)", n))
		}
		s.WriteString(decision)
		s.WriteString("\n")
		return s.String()
	}
	s.WriteString(decision)
	s.WriteString("\n")

	if verdict.Summary == "" && len(verdict.Findings) == 0 {
		s.WriteString(th.MutedTextStyle.Render(" (no summary or findings)"))
		s.WriteString("\n")
		return s.String()
	}

	switch {
	case expanded:
		v.blockScroll = clampScroll(v.blockScroll, len(lines), budget)
		writeScrolledLines(&s, lines, v.blockScroll, budget, width, th.MutedTextStyle)
	case compact:
		writeCappedLines(&s, findingsItemLines(verdict, width), budget, width, th.MutedTextStyle, "finding(s)")
	default:
		writeCappedLines(&s, lines, budget, width, th.MutedTextStyle, "line(s)")
	}
	return s.String()
}

// findingsLines is the findings block's full body, wrapped to the pane: the
// summary, then one paragraph per finding.
func (v *FlowsView) findingsLines(width int) []string {
	_, verdict := v.latestVerdict()
	if verdict == nil {
		return nil
	}
	var paras []string
	if verdict.Summary != "" {
		paras = append(paras, verdict.Summary)
	}
	for _, f := range verdict.Findings {
		paras = append(paras, findingText(f))
	}
	return wrapBlock(strings.Join(paras, "\n"), width)
}

// findingsItemLines is the findings block's compact form: the summary and
// each finding on one line each, clipped to the pane rather than wrapped.
func findingsItemLines(verdict *service.FlowVerdict, width int) []string {
	var lines []string
	if verdict.Summary != "" {
		lines = append(lines, clipText(verdict.Summary, blockTextWidth(width)))
	}
	for _, f := range verdict.Findings {
		lines = append(lines, clipText(findingText(f), blockTextWidth(width)))
	}
	return lines
}

// findingText is one finding as a line: severity, file:line when it has
// one, and the detail.
func findingText(f service.FlowFinding) string {
	loc := f.File
	if f.Line > 0 {
		loc = fmt.Sprintf("%s:%d", f.File, f.Line)
	}
	if loc != "" {
		return fmt.Sprintf("[%s] %s: %s", f.Severity, loc, f.Detail)
	}
	return fmt.Sprintf("[%s] %s", f.Severity, f.Detail)
}

// latestVerdict returns the highest-numbered round that produced a verdict
// for the selected flow, or (0, nil) before any review has landed.
func (v *FlowsView) latestVerdict() (int, *service.FlowVerdict) {
	var round int
	var verdict *service.FlowVerdict
	for _, n := range v.treeNodes {
		if n.Kind == service.FlowNodeRound && n.Verdict != nil && n.Round >= round {
			round = n.Round
			verdict = n.Verdict
		}
	}
	return round, verdict
}

// blockTextWidth is the room a block line has after its one-space indent,
// with a margin so lipgloss never has to wrap what it is handed.
func blockTextWidth(width int) int { return max(width-2, 10) }

// wrapBlock word-wraps text to the pane, one paragraph per input line,
// hard-splitting any token wider than the pane so every returned line
// really is one terminal row.
func wrapBlock(text string, width int) []string {
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		if strings.TrimSpace(para) == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, wordWrap(para, blockTextWidth(width))...)
	}
	return lines
}

// clampScroll keeps a scroll offset within [0, total-visible]: the largest
// offset is the one that fills the block, not one past it.
func clampScroll(offset, total, visible int) int {
	return min(max(offset, 0), max(total-visible, 0))
}

// writeCappedLines writes lines indented by one space, at most maxLines of
// them, with a "+N more <unit>" marker in the last budgeted slot when they
// overflow. A budget of one line is still a line of text: the first line,
// clipped to leave room for the count, rather than a marker on its own.
func writeCappedLines(s *strings.Builder, lines []string, maxLines, width int, style lipgloss.Style, unit string) {
	th := theme.GetTheme()
	if maxLines < 1 {
		return
	}
	if len(lines) > maxLines && maxLines == 1 {
		more := fmt.Sprintf(" +%d", len(lines)-1)
		s.WriteString(" " + style.Render(clipText(lines[0], blockTextWidth(width)-len(more))) + th.MutedTextStyle.Render(more))
		s.WriteString("\n")
		return
	}
	shown := lines
	if len(lines) > maxLines {
		shown = lines[:maxLines-1]
	}
	for _, l := range shown {
		s.WriteString(" " + style.Render(l))
		s.WriteString("\n")
	}
	if hidden := len(lines) - len(shown); hidden > 0 {
		s.WriteString(th.MutedTextStyle.Render(fmt.Sprintf(" … +%d more %s", hidden, unit)))
		s.WriteString("\n")
	}
}

// writeScrolledLines writes maxLines lines from offset, the last of them
// replaced by a marker when more follow below. offset is expected to be
// clamped already, so the last page fills the block.
func writeScrolledLines(s *strings.Builder, lines []string, offset, maxLines, width int, style lipgloss.Style) {
	th := theme.GetTheme()
	if len(lines) <= maxLines {
		writeCappedLines(s, lines, maxLines, width, style, "line(s)")
		return
	}
	offset = clampScroll(offset, len(lines), maxLines)
	if maxLines == 1 {
		// One slot: the line at the offset, clipped to fit the count of
		// what follows, rather than a marker with nothing above it.
		more := fmt.Sprintf(" +%d", len(lines)-offset-1)
		s.WriteString(" " + style.Render(clipText(lines[offset], blockTextWidth(width)-len(more))) + th.MutedTextStyle.Render(more))
		s.WriteString("\n")
		return
	}
	end := offset + maxLines
	if end < len(lines) {
		end = offset + maxLines - 1
	}
	for i := offset; i < end; i++ {
		s.WriteString(" " + style.Render(lines[i]))
		s.WriteString("\n")
	}
	if end < len(lines) {
		s.WriteString(th.MutedTextStyle.Render(fmt.Sprintf(" … +%d more line(s) below", len(lines)-end)))
		s.WriteString("\n")
	}
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
