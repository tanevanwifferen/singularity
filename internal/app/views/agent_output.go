package views

import (
	"fmt"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	"github.com/charmbracelet/lipgloss"
)

// outputBlock is one collapsible entry in the output pane: which entry it
// renders and the line (within the viewport content) its header sits on.
type outputBlock struct {
	entry int
	line  int
}

const (
	collapsedMarker = "▸"
	expandedMarker  = "▾"
)

// collapsibleSource reports whether entries of this source fold to a single
// line when they span several. Assistant text and the user's own prompts
// stay expanded: those are the conversation; tool traffic is the noise.
func collapsibleSource(source string) bool {
	switch source {
	case "tool_use", "tool_result", "system", "error", "result":
		return true
	}
	return false
}

// outputEntryLines renders one non-text entry to unstyled, wrapped lines and
// returns the style to paint them with. The indent and continuation prefix
// per source match the pre-collapse layout so expanded blocks look as before.
func (v *AgentView) outputEntryLines(source, content string, isError bool, w int) ([]string, lipgloss.Style) {
	th := theme.GetTheme()
	prefix, cont := "  ", "    "
	var style lipgloss.Style
	switch source {
	case "tool_use":
		style = lipgloss.NewStyle().Foreground(th.Info).Bold(true)
	case "tool_result":
		prefix, cont = "    ", "      "
		style = th.MutedTextStyle
		if isError {
			style = th.DashboardErrorStyle
		}
	case "system":
		style = th.MutedTextStyle
	case "error":
		style = th.DashboardErrorStyle
	case "result":
		style = lipgloss.NewStyle().Foreground(th.Info)
	case "user_input":
		prefix, cont = "  > ", "      "
		style = lipgloss.NewStyle().Foreground(th.Accent).Bold(true)
	default:
		style = lipgloss.NewStyle()
	}
	var lines []string
	for _, raw := range strings.Split(content, "\n") {
		lines = append(lines, wrapLine(prefix+raw, w, cont)...)
	}
	return lines, style
}

// collapseHeader folds a multi-line block to one line: the block's first line
// with a fold marker, followed by how many lines are hidden.
func collapseHeader(lines []string, w int) string {
	first := lines[0]
	indent := first[:len(first)-len(strings.TrimLeft(first, " "))]
	body := strings.TrimSpace(first)
	suffix := fmt.Sprintf("  (+%d lines)", len(lines)-1)
	head := indent + collapsedMarker + " "
	avail := w - len(head) - len(suffix)
	if avail < 8 {
		avail = 8
	}
	if r := []rune(body); len(r) > avail {
		body = string(r[:avail-1]) + "…"
	}
	return head + body + suffix
}

// rebuildOutputViewport rebuilds the viewport content from output entries,
// folding multi-line tool blocks unless the user expanded them.
func (v *AgentView) rebuildOutputViewport() {
	if v.width <= 0 {
		return
	}
	th := theme.GetTheme()
	cursorStyle := lipgloss.NewStyle().Foreground(th.Accent).Bold(true)
	var lines []string
	w := v.width
	v.outputBlocks = v.outputBlocks[:0]

	for i, entry := range v.outputEntries {
		if entry.Source == "text" {
			if r := v.markdownRenderer(w); r != nil {
				if rendered, err := r.Render(entry.Content); err == nil {
					rendered = strings.TrimRight(rendered, "\n")
					lines = append(lines, strings.Split(rendered, "\n")...)
					continue
				}
			}
			for _, raw := range strings.Split(entry.Content, "\n") {
				lines = append(lines, wrapLine(raw, w, "  ")...)
			}
			continue
		}

		raw, style := v.outputEntryLines(entry.Source, entry.Content, entry.IsError, w)
		if !collapsibleSource(entry.Source) || len(raw) <= 1 {
			for _, l := range raw {
				lines = append(lines, style.Render(l))
			}
			continue
		}

		blockIdx := len(v.outputBlocks)
		v.outputBlocks = append(v.outputBlocks, outputBlock{entry: i, line: len(lines)})
		headStyle := style
		if blockIdx == v.outputCursor {
			headStyle = cursorStyle
		}
		if v.blockExpanded(i) {
			first := raw[0]
			indent := first[:len(first)-len(strings.TrimLeft(first, " "))]
			lines = append(lines, headStyle.Render(indent+expandedMarker+" "+strings.TrimLeft(first, " ")))
			for _, l := range raw[1:] {
				lines = append(lines, style.Render(l))
			}
			continue
		}
		lines = append(lines, headStyle.Render(collapseHeader(raw, w)))
	}

	if v.outputCursor >= len(v.outputBlocks) {
		v.outputCursor = len(v.outputBlocks) - 1
	}

	content := strings.Join(lines, "\n")
	v.outputViewport.SetContent(content)
	v.outputLastLen = len(v.outputEntries)
	v.outputLastWidth = w

	if v.outputAutoScroll {
		v.outputViewport.GotoBottom()
	}
}

// blockExpanded reports whether the entry is shown unfolded: its explicit
// toggle if the user made one, else the expand-all state.
func (v *AgentView) blockExpanded(entry int) bool {
	if expanded, set := v.outputExpanded[entry]; set {
		return expanded
	}
	return v.outputAllExpanded
}

// resetOutputFolds clears fold state when a different agent is shown.
func (v *AgentView) resetOutputFolds() {
	v.outputExpanded = map[int]bool{}
	v.outputBlocks = nil
	v.outputCursor = -1
	v.outputAllExpanded = false
}

// firstVisibleBlock returns the index of the first block whose header is at
// or below the viewport's top line, or the last block when none is.
func (v *AgentView) firstVisibleBlock() int {
	if len(v.outputBlocks) == 0 {
		return -1
	}
	top := v.outputViewport.YOffset
	for i, b := range v.outputBlocks {
		if b.line >= top {
			return i
		}
	}
	return len(v.outputBlocks) - 1
}

// moveOutputCursor steps the block cursor by delta and scrolls the header
// into view. With no cursor yet, the first visible block is picked.
func (v *AgentView) moveOutputCursor(delta int) {
	if len(v.outputBlocks) == 0 {
		return
	}
	if v.outputCursor < 0 {
		v.outputCursor = v.firstVisibleBlock()
	} else {
		v.outputCursor += delta
	}
	if v.outputCursor < 0 {
		v.outputCursor = 0
	}
	if v.outputCursor >= len(v.outputBlocks) {
		v.outputCursor = len(v.outputBlocks) - 1
	}
	v.outputAutoScroll = false
	v.rebuildOutputViewport()
	v.scrollBlockIntoView(v.outputCursor)
}

// scrollBlockIntoView adjusts the viewport so the block's header is visible.
func (v *AgentView) scrollBlockIntoView(idx int) {
	if idx < 0 || idx >= len(v.outputBlocks) {
		return
	}
	line := v.outputBlocks[idx].line
	top := v.outputViewport.YOffset
	h := v.outputViewport.Height
	if line < top {
		v.outputViewport.SetYOffset(line)
	} else if h > 0 && line >= top+h {
		v.outputViewport.SetYOffset(line - h + 1)
	}
}

// toggleOutputBlock expands or collapses the block under the cursor. With no
// cursor yet, the first visible block is toggled.
func (v *AgentView) toggleOutputBlock() {
	if len(v.outputBlocks) == 0 {
		return
	}
	if v.outputCursor < 0 {
		v.outputCursor = v.firstVisibleBlock()
	}
	if v.outputExpanded == nil {
		v.outputExpanded = map[int]bool{}
	}
	entry := v.outputBlocks[v.outputCursor].entry
	v.outputExpanded[entry] = !v.blockExpanded(entry)
	v.outputAutoScroll = false
	v.rebuildOutputViewport()
	v.scrollBlockIntoView(v.outputCursor)
}

// toggleAllOutputBlocks expands every block when any is collapsed, and
// collapses them all otherwise.
func (v *AgentView) toggleAllOutputBlocks() {
	if len(v.outputBlocks) == 0 {
		return
	}
	if v.outputExpanded == nil {
		v.outputExpanded = map[int]bool{}
	}
	anyCollapsed := false
	for _, b := range v.outputBlocks {
		if !v.blockExpanded(b.entry) {
			anyCollapsed = true
			break
		}
	}
	for _, b := range v.outputBlocks {
		v.outputExpanded[b.entry] = anyCollapsed
	}
	v.outputAllExpanded = anyCollapsed
	v.rebuildOutputViewport()
	v.scrollBlockIntoView(v.outputCursor)
}
