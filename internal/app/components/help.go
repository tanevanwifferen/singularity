package components

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// KeyBinding represents a single keybinding with its description.
type KeyBinding struct {
	// Key is the key or key combination (e.g., "q", "Ctrl+C", "1-4")
	Key string

	// Description explains what the key does
	Description string
}

// KeyBindings is an interface for views that expose their keybindings.
// Views implement this to provide help overlay content.
type KeyBindings interface {
	// KeyBindings returns the list of keybindings for this view
	KeyBindings() []KeyBinding
}

// GlobalBindings returns the global keybindings that work in all views.
func GlobalBindings() []KeyBinding {
	return []KeyBinding{
		{Key: "q", Description: "Quit application"},
		{Key: "Ctrl+C", Description: "Quit application"},
		{Key: "r", Description: "Refresh repository data"},
		{Key: "t", Description: "Toggle light/dark theme"},
		{Key: "?", Description: "Show this help overlay"},
		{Key: "Esc", Description: "Go back / Cancel / Close overlay"},
		{Key: "1-9", Description: "Switch to view by number"},
	}
}

// HelpOverlay shows keybindings for the current view.
type HelpOverlay struct {
	// Modal for rendering
	title       string
	width       int
	borderColor lipgloss.Color
	termWidth   int
	termHeight  int

	Bindings []KeyBinding
	scroll  int
	visible int // how many bindings visible at once
}

// NewHelpOverlay creates a help overlay.
func NewHelpOverlay(bindings []KeyBinding) HelpOverlay {
	return HelpOverlay{
		title:       "Help — Keybindings",
		width:       56,
		borderColor: lipgloss.Color("75"),
		termWidth:   80,
		termHeight:  24,
		Bindings:    bindings,
		visible:     15,
	}
}

// SetSize updates the terminal dimensions for centering.
func (h *HelpOverlay) SetSize(width, height int) {
	h.termWidth = width
	h.termHeight = height
	// Adjust visible count based on terminal height
	h.visible = height - 10
	if h.visible < 5 {
		h.visible = 5
	}
}

// Init implements tea.Model.
func (h HelpOverlay) Init() tea.Cmd {
	return nil
}

// Update handles scrolling and dismiss.
func (h HelpOverlay) Update(msg tea.Msg) (HelpOverlay, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "?", "esc":
			return h, func() tea.Msg {
				return ModalClosedMsg{}
			}
		case "up", "k":
			if h.scroll > 0 {
				h.scroll--
			}
		case "down", "j":
			maxScroll := len(h.Bindings) - h.visible
			if maxScroll < 0 {
				maxScroll = 0
			}
			if h.scroll < maxScroll {
				h.scroll++
			}
		}
	case tea.WindowSizeMsg:
		h.SetSize(msg.Width, msg.Height)
	}
	return h, nil
}

// View renders the help overlay with a keybinding table.
func (h HelpOverlay) View(background string) string {
	keyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("220")).
		Bold(true).
		Width(16)

	descStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("252"))

	sepStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240"))

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("205")).
		MarginBottom(1)

	var rows []string

	// Title
	rows = append(rows, titleStyle.Render(h.title))
	rows = append(rows, sepStyle.Render(strings.Repeat("─", 50)))

	// Determine visible range
	end := h.scroll + h.visible
	if end > len(h.Bindings) {
		end = len(h.Bindings)
	}

	for _, b := range h.Bindings[h.scroll:end] {
		// Skip separator lines in scroll
		if b.Key == "---" && b.Description == "---" {
			rows = append(rows, sepStyle.Render(strings.Repeat("─", 50)))
			continue
		}
		// Handle section headers
		if strings.HasPrefix(b.Description, "--- View:") {
			rows = append(rows, "")
			rows = append(rows, descStyle.Render(b.Description))
			rows = append(rows, "")
			continue
		}
		row := keyStyle.Render(b.Key) + descStyle.Render(b.Description)
		rows = append(rows, row)
	}

	rows = append(rows, sepStyle.Render(strings.Repeat("─", 50)))

	// Scroll indicator
	if len(h.Bindings) > h.visible {
		indicator := lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Italic(true).
			Render("↑/↓ to scroll • ? or Esc to close")
		rows = append(rows, indicator)
	} else {
		hint := lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Italic(true).
			Render("? or Esc to close")
		rows = append(rows, hint)
	}

	content := strings.Join(rows, "\n")

	// Render centered overlay
	return h.renderOverlay(background, content)
}

// renderOverlay places the content centered over the background, dimming it.
func (h HelpOverlay) renderOverlay(background, content string) string {
	bgLines := strings.Split(background, "\n")
	contentLines := strings.Split(content, "\n")

	// Ensure we have enough background lines
	for len(bgLines) < h.termHeight {
		bgLines = append(bgLines, "")
	}

	contentHeight := len(contentLines)
	contentWidth := 0
	for _, line := range contentLines {
		if lipgloss.Width(line) > contentWidth {
			contentWidth = lipgloss.Width(line)
		}
	}

	// Calculate centering offsets
	startRow := (h.termHeight - contentHeight) / 2
	if startRow < 0 {
		startRow = 0
	}
	startCol := (h.termWidth - contentWidth) / 2
	if startCol < 0 {
		startCol = 0
	}

	// Dim style for background lines
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(h.borderColor).
		Padding(1, 2).
		Width(h.width)

	var result []string
	for i := 0; i < h.termHeight; i++ {
		bgLine := ""
		if i < len(bgLines) {
			bgLine = bgLines[i]
		}

		if i >= startRow && i < startRow+contentHeight {
			// This row has content
			contentLineIdx := i - startRow
			contentLine := contentLines[contentLineIdx]

			// Pad content line to be box-width
			if lipgloss.Width(contentLine) < h.width {
				contentLine += strings.Repeat(" ", h.width-lipgloss.Width(contentLine))
			}

			// Build: dimmed left padding + content line
			leftPad := strings.Repeat(" ", startCol)
			line := leftPad + boxStyle.Render(contentLine)

			// Pad to terminal width
			lineWidth := lipgloss.Width(line)
			if lineWidth < h.termWidth {
				line += strings.Repeat(" ", h.termWidth-lineWidth)
			}
			result = append(result, line)
		} else {
			// Dim the background line
			result = append(result, dimStyle.Render(bgLine))
		}
	}

	return strings.Join(result, "\n")
}
