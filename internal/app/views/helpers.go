package views

import (
	"fmt"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
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

// DeleteWordEnd is re-exported from components for convenience.
var DeleteWordEnd = components.DeleteWordEnd
