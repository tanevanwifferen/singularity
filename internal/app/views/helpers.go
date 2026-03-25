package views

import (
	"fmt"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	"github.com/charmbracelet/lipgloss"
)

// renderModal renders content inside a box-drawing border.
func renderModal(title string, lines []string, width int) string {
	if width < 20 {
		width = 20
	}
	innerW := width - 4 // account for " | " + "|"

	var s strings.Builder

	// Top border
	titlePart := fmt.Sprintf(" %s ", title)
	dashCount := innerW - len(titlePart)
	if dashCount < 0 {
		dashCount = 0
	}
	s.WriteString(fmt.Sprintf(" ╭─%s%s╮\n", titlePart, strings.Repeat("─", dashCount)))

	// Content lines
	for _, line := range lines {
		// Pad line to inner width (approximate, since line may contain ANSI)
		s.WriteString(fmt.Sprintf(" │ %-*s│\n", innerW, line))
	}

	// Bottom border
	s.WriteString(fmt.Sprintf(" ╰%s╯\n", strings.Repeat("─", innerW+2)))

	return s.String()
}

// modalWidth calculates the width for modal dialogs given a view width.
func modalWidth(viewWidth int) int {
	w := viewWidth - 2
	if w < 50 {
		w = 50
	}
	if w > 72 {
		w = 72
	}
	return w
}

// wrapText splits text into lines of at most width runes.
func wrapText(text string, width int) []string {
	if width <= 0 {
		width = 40
	}
	runes := []rune(text)
	var lines []string
	for len(runes) > 0 {
		if len(runes) <= width {
			lines = append(lines, string(runes))
			break
		}
		lines = append(lines, string(runes[:width]))
		runes = runes[width:]
	}
	return lines
}

// wordWrap wraps text to the specified width, breaking at word boundaries.
func wordWrap(text string, width int) []string {
	words := strings.Fields(text)
	var lines []string
	var currentLine strings.Builder

	for _, word := range words {
		if currentLine.Len()+len(word)+1 > width {
			if currentLine.Len() > 0 {
				lines = append(lines, currentLine.String())
				currentLine.Reset()
			}
		}
		if currentLine.Len() > 0 {
			currentLine.WriteString(" ")
		}
		currentLine.WriteString(word)
	}

	if currentLine.Len() > 0 {
		lines = append(lines, currentLine.String())
	}

	return lines
}

// wrapLine wraps a line to fit within maxWidth, preserving a prefix on continuation lines.
func wrapLine(line string, maxWidth int, contPrefix string) []string {
	if maxWidth <= 0 || len(line) <= maxWidth {
		return []string{line}
	}
	var wrapped []string
	for len(line) > 0 {
		cut := maxWidth
		if len(wrapped) > 0 {
			cut = maxWidth - len(contPrefix)
			if cut <= 0 {
				cut = maxWidth
			}
		}
		if cut >= len(line) {
			if len(wrapped) > 0 {
				line = contPrefix + line
			}
			wrapped = append(wrapped, line)
			break
		}
		chunk := line[:cut]
		if len(wrapped) > 0 {
			chunk = contPrefix + chunk
		}
		wrapped = append(wrapped, chunk)
		line = line[cut:]
	}
	return wrapped
}

// renderDiffWithGutter renders parsed diff lines with a line-number gutter.
// scrollOffset is the current scroll position, height is the viewport height,
// headerLines/footerLines account for chrome above/below the diff content.
// If dimAlreadyInBase is true, lines with AlreadyInBase are rendered in muted style.
func renderDiffWithGutter(lines []DiffLine, scrollOffset, width, height, headerLines, footerLines int, dimAlreadyInBase bool, scrollHint string) string {
	th := theme.GetTheme()
	var s strings.Builder

	gutterWidth := 6
	diffWidth := width - gutterWidth - 1
	if diffWidth < 10 {
		diffWidth = 10
	}

	visibleLines := height - headerLines - footerLines
	if visibleLines < 5 {
		visibleLines = 5
	}

	startIdx := scrollOffset
	endIdx := startIdx + visibleLines
	if endIdx > len(lines) {
		endIdx = len(lines)
		startIdx = endIdx - visibleLines
		if startIdx < 0 {
			startIdx = 0
		}
	}

	for i := startIdx; i < endIdx; i++ {
		line := lines[i]
		gutter := ""
		lineStyle := th.Help

		switch line.LineType {
		case "+":
			lineStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
			if line.NewLineNum > 0 {
				gutter = fmt.Sprintf(" %4d ", line.NewLineNum)
			} else {
				gutter = "      "
			}
		case "-":
			lineStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
			if line.OldLineNum > 0 {
				gutter = fmt.Sprintf(" %4d ", line.OldLineNum)
			} else {
				gutter = "      "
			}
		case "@":
			lineStyle = th.InfoStyle
			gutter = "      "
		case "H":
			lineStyle = th.Help
			gutter = "      "
		case " ":
			lineStyle = th.Help
			if line.NewLineNum > 0 {
				gutter = fmt.Sprintf(" %4d ", line.NewLineNum)
			} else if line.OldLineNum > 0 {
				gutter = fmt.Sprintf(" %4d ", line.OldLineNum)
			} else {
				gutter = "      "
			}
		default:
			lineStyle = th.Help
			gutter = "      "
		}

		if dimAlreadyInBase && line.AlreadyInBase {
			lineStyle = th.MutedTextStyle
		}

		content := line.Content
		if len(content) > diffWidth-2 {
			content = content[:diffWidth-5] + "..."
		}

		prefix := " "
		if line.LineType == "+" {
			prefix = "+"
		} else if line.LineType == "-" {
			prefix = "-"
		}

		s.WriteString(th.Help.Render(gutter))
		s.WriteString(lineStyle.Render(prefix + content))
		s.WriteString("\n")
	}

	totalLines := len(lines)
	if totalLines > visibleLines {
		scrollInfo := fmt.Sprintf(" %d-%d of %d ", startIdx+1, endIdx, totalLines)
		s.WriteString(th.Help.Render(scrollInfo))
		s.WriteString(th.Help.Render(scrollHint))
		s.WriteString("\n")
	}

	return s.String()
}

// fileStatusIndicator returns a short status character and corresponding style for a git file status.
func fileStatusIndicator(status string, th theme.Theme) (char string, style lipgloss.Style) {
	switch status {
	case "A":
		return "A", th.DashboardAccentStyle
	case "M":
		return "M", th.WarningStyle
	case "D":
		return "D", th.DashboardErrorStyle
	case "R":
		return "R", th.InfoStyle
	case "C":
		return "C", th.InfoStyle
	default:
		return " ", th.StatsStyle
	}
}

// fileStatusLabel returns a human-readable label and style for a git file status.
func fileStatusLabel(status string, th theme.Theme) (label string, style lipgloss.Style) {
	switch status {
	case "A":
		return "Added", th.DashboardAccentStyle
	case "M":
		return "Modified", th.WarningStyle
	case "D":
		return "Deleted", th.DashboardErrorStyle
	case "R":
		return "Renamed", th.InfoStyle
	case "C":
		return "Copied", th.InfoStyle
	default:
		return status, th.StatsStyle
	}
}

// calcViewport computes the visible window [start, end) for a centered-on-cursor
// list view. viewHeight is the total view height, chrome is the lines consumed by
// headers/footers, cursor is the focused index, total is len(items).
func calcViewport(viewHeight, chrome, cursor, total int) (start, end int) {
	visible := viewHeight - chrome
	if visible < 1 {
		visible = 1
	}

	start = cursor - visible/2
	if start < 0 {
		start = 0
	}
	end = start + visible
	if end > total {
		end = total
		start = end - visible
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

// truncatePath shortens a path to fit maxLen, prefixing with "..." if needed.
func truncatePath(path string, maxLen int) string {
	if maxLen < 10 {
		maxLen = 10
	}
	if len(path) > maxLen {
		return "..." + path[len(path)-maxLen+3:]
	}
	return path
}
