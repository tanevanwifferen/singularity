package components

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// SubmenuItem represents a single item in a submenu.
type SubmenuItem struct {
	// Key is the single-character shortcut to select this item (e.g., "s", "r")
	Key string

	// Label is the display name of the item
	Label string

	// ViewName is the router view name to switch to
	ViewName string
}

// Submenu renders a popup overlay listing items with single-key shortcuts.
type Submenu struct {
	Title string
	Items []SubmenuItem

	// terminal dimensions for centering
	termWidth  int
	termHeight int
}

// NewSubmenu creates a new submenu overlay.
func NewSubmenu(title string, items []SubmenuItem) Submenu {
	return Submenu{
		Title:      title,
		Items:      items,
		termWidth:  80,
		termHeight: 24,
	}
}

// SetSize updates the terminal dimensions for centering.
func (s *Submenu) SetSize(width, height int) {
	s.termWidth = width
	s.termHeight = height
}

// Match returns the ViewName for the given key, or "" if no match.
func (s *Submenu) Match(key string) string {
	for _, item := range s.Items {
		if item.Key == key {
			return item.ViewName
		}
	}
	return ""
}

// View renders the submenu as a centered overlay on top of the background.
func (s Submenu) View(background string) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("205")).
		MarginBottom(1)

	keyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("220")).
		Bold(true).
		Width(4)

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("252"))

	sepStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240"))

	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		Italic(true)

	// Build content
	var rows []string
	rows = append(rows, titleStyle.Render(s.Title))
	rows = append(rows, sepStyle.Render(strings.Repeat("─", 30)))

	for _, item := range s.Items {
		row := keyStyle.Render(item.Key) + labelStyle.Render(item.Label)
		rows = append(rows, row)
	}

	rows = append(rows, sepStyle.Render(strings.Repeat("─", 30)))
	rows = append(rows, hintStyle.Render("Press key to select • Esc to close"))

	content := strings.Join(rows, "\n")

	// Render in a box
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("75")).
		Padding(1, 2).
		Width(38)

	box := boxStyle.Render(content)

	return s.overlay(background, box)
}

// overlay places the box centered over the background, dimming it.
func (s Submenu) overlay(background, box string) string {
	bgLines := strings.Split(background, "\n")
	boxLines := strings.Split(box, "\n")

	// Ensure we have enough background lines
	for len(bgLines) < s.termHeight {
		bgLines = append(bgLines, "")
	}

	boxHeight := len(boxLines)
	boxWidth := lipgloss.Width(box)

	// Calculate centering offsets
	startRow := (s.termHeight - boxHeight) / 2
	if startRow < 0 {
		startRow = 0
	}
	startCol := (s.termWidth - boxWidth) / 2
	if startCol < 0 {
		startCol = 0
	}

	// Dim style for background lines
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))

	var result []string
	for i := 0; i < s.termHeight; i++ {
		bgLine := ""
		if i < len(bgLines) {
			bgLine = bgLines[i]
		}

		if i >= startRow && i < startRow+boxHeight {
			boxLineIdx := i - startRow
			boxLine := boxLines[boxLineIdx]

			leftPad := strings.Repeat(" ", startCol)
			line := leftPad + boxLine

			lineWidth := lipgloss.Width(line)
			if lineWidth < s.termWidth {
				line += strings.Repeat(" ", s.termWidth-lineWidth)
			}
			result = append(result, line)
		} else {
			result = append(result, dimStyle.Render(bgLine))
		}
	}

	return strings.Join(result, "\n")
}
