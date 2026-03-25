package views

import (
	"fmt"
	"strings"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	"github.com/charmbracelet/lipgloss"
)

// logEntry represents a single line in the sync output log.
type logEntry struct {
	timestamp time.Time
	op        SyncOperation
	kind      string // "info", "success", "error", "output"
	message   string
}

// syncLogHelper manages the output log, scrolling, and rendering shared by
// SyncView and ProjectSyncView.
type syncLogHelper struct {
	outputLog    []logEntry
	scrollOffset int
}

// addLog splits multiline messages and appends log entries, then scrolls to bottom.
func (h *syncLogHelper) addLog(op SyncOperation, kind, message string, maxLines int) {
	lines := strings.Split(message, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		h.outputLog = append(h.outputLog, logEntry{
			timestamp: time.Now(),
			op:        op,
			kind:      kind,
			message:   line,
		})
	}
	h.scrollToBottom(maxLines)
}

// scrollDown scrolls the log down by one line within the given maxLines constraint.
func (h *syncLogHelper) scrollDown(maxLines int) {
	if maxLines < 5 {
		maxLines = 5
	}
	if h.scrollOffset < len(h.outputLog)-maxLines {
		h.scrollOffset++
	}
}

// scrollUp scrolls the log up by one line.
func (h *syncLogHelper) scrollUp() {
	if h.scrollOffset > 0 {
		h.scrollOffset--
	}
}

// scrollToBottom scrolls to the end of the log within the given maxLines constraint.
func (h *syncLogHelper) scrollToBottom(maxLines int) {
	if maxLines < 5 {
		maxLines = 5
	}
	if len(h.outputLog) > maxLines {
		h.scrollOffset = len(h.outputLog) - maxLines
	} else {
		h.scrollOffset = 0
	}
}

// renderSyncLog renders the output log section.
func (h *syncLogHelper) renderSyncLog(s *strings.Builder, th theme.Theme, maxLines int) {
	if len(h.outputLog) == 0 {
		return
	}

	s.WriteString(th.StatsStyle.Render(" Output Log "))
	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")

	if maxLines < 5 {
		maxLines = 5
	}

	start := h.scrollOffset
	if start > len(h.outputLog) {
		start = len(h.outputLog)
	}
	end := start + maxLines
	if end > len(h.outputLog) {
		end = len(h.outputLog)
	}

	for _, entry := range h.outputLog[start:end] {
		ts := entry.timestamp.Format("15:04:05")
		var style lipgloss.Style
		switch entry.kind {
		case "success":
			style = th.DashboardAccentStyle
		case "error":
			style = th.DashboardErrorStyle
		default:
			style = th.StatsStyle
		}

		prefix := ""
		switch entry.kind {
		case "success":
			prefix = "✓"
		case "error":
			prefix = "✗"
		case "info":
			prefix = "→"
		case "output":
			prefix = " "
		}

		line := fmt.Sprintf(" %s %s %s",
			lipgloss.NewStyle().Foreground(th.Info).Render(ts),
			prefix,
			style.Render(entry.message))
		s.WriteString(line)
		s.WriteString("\n")
	}

	// Scroll indicator
	if len(h.outputLog) > maxLines {
		s.WriteString(th.Help.Render(fmt.Sprintf(" [%d-%d of %d] j/k to scroll ", start+1, end, len(h.outputLog))))
		s.WriteString("\n")
	}
}

// renderSyncKeybindings renders the keybinding help section.
func renderSyncKeybindings(s *strings.Builder, th theme.Theme, bindings []components.KeyBinding) {
	keyStyle := lipgloss.NewStyle().Bold(true).Foreground(th.Accent)
	descStyle := lipgloss.NewStyle().Foreground(th.SecondaryText)
	sepStyle := lipgloss.NewStyle().Foreground(th.Border)

	s.WriteString(th.StatsStyle.Render(" Keybindings "))
	s.WriteString("\n")
	s.WriteString(sepStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")

	for _, kb := range bindings {
		s.WriteString(fmt.Sprintf(" %s  %s\n",
			keyStyle.Width(6).Render(kb.Key),
			descStyle.Render(kb.Description)))
	}
}

// opDoneLabel returns a human-readable completion label for a sync operation.
func opDoneLabel(op SyncOperation) string {
	switch op {
	case SyncOpFetch:
		return "Fetch complete"
	case SyncOpPull:
		return "Pull complete"
	case SyncOpPush:
		return "Push complete"
	case SyncOpForcePush:
		return "Force push complete"
	case SyncOpRebase:
		return "Rebase complete"
	case SyncOpSync:
		return "Sync complete (fetch + rebase + push)"
	case SyncOpSetUpstream:
		return "Upstream set and pushed"
	default:
		return "Done"
	}
}

// handleSyncConfirm handles y/n/esc confirmation input, returning the
// confirmed operation or SyncOpNone. The returned booleans indicate whether
// the user confirmed (yes) or dismissed (no/esc) the dialog.
func handleSyncConfirm(key string, confirmOp SyncOperation) (op SyncOperation, confirmed bool, dismissed bool) {
	switch key {
	case "y", "Y":
		return confirmOp, true, false
	case "n", "N", "esc":
		return SyncOpNone, false, true
	}
	return SyncOpNone, false, false
}
