package views

import (
	"fmt"
	"strings"

	"git-frontend/internal/app/components"
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

// wrapText splits text into lines of at most width characters.
func wrapText(text string, width int) []string {
	if width <= 0 {
		width = 40
	}
	var lines []string
	for len(text) > 0 {
		if len(text) <= width {
			lines = append(lines, text)
			break
		}
		lines = append(lines, text[:width])
		text = text[width:]
	}
	return lines
}

// DeleteWordEnd is re-exported from components for convenience.
var DeleteWordEnd = components.DeleteWordEnd
