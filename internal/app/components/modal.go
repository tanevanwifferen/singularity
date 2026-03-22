package components

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ModalClosedMsg is sent when a modal is dismissed.
type ModalClosedMsg struct{}

// Modal is a base overlay component that renders content centered
// on top of the current view with a dimmed background.
type Modal struct {
	// Title displayed at the top of the modal box.
	Title string

	// Content is the inner body rendered inside the modal box.
	Content string

	// Width of the modal box (0 = auto-size to content).
	Width int

	// BorderColor overrides the default border color.
	BorderColor lipgloss.Color

	// terminal dimensions
	termWidth  int
	termHeight int
}

// NewModal creates a new modal overlay.
func NewModal(title, content string) Modal {
	return Modal{
		Title:       title,
		Content:     content,
		Width:       50,
		BorderColor: lipgloss.Color("240"),
		termWidth:   80,
		termHeight:  24,
	}
}

// SetSize updates the terminal dimensions for centering.
func (m *Modal) SetSize(width, height int) {
	m.termWidth = width
	m.termHeight = height
}

// Render draws the modal box centered on the screen.
// The background parameter is the underlying view to dim.
func (m Modal) Render(background string) string {
	// Build the modal box
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.BorderColor).
		Padding(1, 2).
		Width(m.Width)

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("205")).
		MarginBottom(1)

	var inner string
	if m.Title != "" {
		inner = titleStyle.Render(m.Title) + "\n"
	}
	inner += m.Content

	box := boxStyle.Render(inner)

	// Center the box on screen
	return m.overlay(background, box)
}

// overlay places the box centered over the background, dimming it.
func (m Modal) overlay(background, box string) string {
	bgLines := strings.Split(background, "\n")
	boxLines := strings.Split(box, "\n")

	// Ensure we have enough background lines
	for len(bgLines) < m.termHeight {
		bgLines = append(bgLines, "")
	}

	boxHeight := len(boxLines)
	boxWidth := lipgloss.Width(box)

	// Calculate centering offsets
	startRow := (m.termHeight - boxHeight) / 2
	if startRow < 0 {
		startRow = 0
	}
	startCol := (m.termWidth - boxWidth) / 2
	if startCol < 0 {
		startCol = 0
	}

	// Dim style for background lines
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))

	var result []string
	for i := 0; i < m.termHeight; i++ {
		bgLine := ""
		if i < len(bgLines) {
			bgLine = bgLines[i]
		}

		if i >= startRow && i < startRow+boxHeight {
			// This row has modal content - compose it
			boxLineIdx := i - startRow
			boxLine := boxLines[boxLineIdx]

			// Build: dimmed left padding + box line + dimmed right
			leftPad := strings.Repeat(" ", startCol)
			line := leftPad + boxLine

			// Pad to terminal width
			lineWidth := lipgloss.Width(line)
			if lineWidth < m.termWidth {
				line += strings.Repeat(" ", m.termWidth-lineWidth)
			}
			result = append(result, line)
		} else {
			// Dim the background line
			result = append(result, dimStyle.Render(bgLine))
		}
	}

	return strings.Join(result, "\n")
}

// ConfirmResult is sent when the user responds to a confirmation dialog.
type ConfirmResult struct {
	Confirmed bool
	ID        string // optional identifier to distinguish multiple dialogs
}

// ConfirmDialog is a yes/no confirmation modal.
type ConfirmDialog struct {
	Modal
	ID       string
	selected int // 0 = yes, 1 = no
}

// NewConfirmDialog creates a confirmation dialog.
func NewConfirmDialog(title, message, id string) ConfirmDialog {
	return ConfirmDialog{
		Modal: Modal{
			Title:       title,
			Content:     message,
			Width:       50,
			BorderColor: lipgloss.Color("220"),
			termWidth:   80,
			termHeight:  24,
		},
		ID:       id,
		selected: 1, // default to "No" for safety
	}
}

// Init implements tea.Model.
func (d ConfirmDialog) Init() tea.Cmd {
	return nil
}

// Update handles key events for the confirmation dialog.
func (d ConfirmDialog) Update(msg tea.Msg) (ConfirmDialog, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y":
			return d, func() tea.Msg {
				return ConfirmResult{Confirmed: true, ID: d.ID}
			}
		case "n", "N", "esc":
			return d, func() tea.Msg {
				return ConfirmResult{Confirmed: false, ID: d.ID}
			}
		case "left", "h", "tab":
			d.selected = 0
		case "right", "l", "shift+tab":
			d.selected = 1
		case "enter":
			return d, func() tea.Msg {
				return ConfirmResult{Confirmed: d.selected == 0, ID: d.ID}
			}
		}
	case tea.WindowSizeMsg:
		d.Modal.SetSize(msg.Width, msg.Height)
	}
	return d, nil
}

// View renders the confirmation dialog with yes/no buttons.
func (d ConfirmDialog) View(background string) string {
	yesStyle := lipgloss.NewStyle().Padding(0, 2)
	noStyle := lipgloss.NewStyle().Padding(0, 2)

	if d.selected == 0 {
		yesStyle = yesStyle.Bold(true).
			Foreground(lipgloss.Color("82")).
			Background(lipgloss.Color("235"))
	} else {
		noStyle = noStyle.Bold(true).
			Foreground(lipgloss.Color("196")).
			Background(lipgloss.Color("235"))
	}

	buttons := "\n" + yesStyle.Render("[Y]es") + "  " + noStyle.Render("[N]o")
	hint := "\n\n" + lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		Italic(true).
		Render("y/n or ←/→ + Enter • Esc to cancel")

	d.Modal.Content = d.Modal.Content + buttons + hint
	return d.Modal.Render(background)
}

// InfoType distinguishes info dialogs by severity.
type InfoType int

const (
	InfoTypeInfo InfoType = iota
	InfoTypeError
	InfoTypeSuccess
)

// InfoDismissedMsg is sent when an info dialog is dismissed.
type InfoDismissedMsg struct {
	ID string
}

// InfoDialog shows an informational or error message.
type InfoDialog struct {
	Modal
	ID               string
	InfoType         InfoType
	AutoDismiss      bool
	AutoDismissDelay time.Duration
	autoDismissCmd   tea.Cmd
}

// NewInfoDialog creates an info/error dialog.
func NewInfoDialog(title, message, id string, infoType InfoType) *InfoDialog {
	borderColor := lipgloss.Color("75") // blue for info
	switch infoType {
	case InfoTypeError:
		borderColor = lipgloss.Color("196") // red
	case InfoTypeSuccess:
		borderColor = lipgloss.Color("82") // green
	}

	return &InfoDialog{
		Modal: Modal{
			Title:       title,
			Content:     message,
			Width:       50,
			BorderColor: borderColor,
			termWidth:   80,
			termHeight:  24,
		},
		ID:               id,
		InfoType:         infoType,
		AutoDismiss:      false,
		AutoDismissDelay: 3 * time.Second,
	}
}

// SetAutoDismiss enables auto-dismiss with the specified delay.
// If delay is 0, defaults to 3 seconds.
func (d *InfoDialog) SetAutoDismiss(enabled bool, delay time.Duration) {
	d.AutoDismiss = enabled
	if enabled && delay == 0 {
		d.AutoDismissDelay = 3 * time.Second
	} else {
		d.AutoDismissDelay = delay
	}
}

// Init implements tea.Model.
func (d InfoDialog) Init() tea.Cmd {
	if d.AutoDismiss && d.AutoDismissDelay > 0 {
		return tea.Tick(d.AutoDismissDelay, func(t time.Time) tea.Msg {
			return InfoDismissedMsg{ID: d.ID}
		})
	}
	return nil
}

// Update handles key events - any key dismisses.
// Also handles auto-dismiss via InfoDismissedMsg from timer.
func (d InfoDialog) Update(msg tea.Msg) (InfoDialog, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return d, func() tea.Msg {
			return InfoDismissedMsg{ID: d.ID}
		}
	case tea.WindowSizeMsg:
		d.Modal.SetSize(msg.Width, msg.Height)
	}
	return d, nil
}

// View renders the info dialog.
func (d InfoDialog) View(background string) string {
	colorMap := map[InfoType]lipgloss.Color{
		InfoTypeInfo:    lipgloss.Color("75"),
		InfoTypeError:   lipgloss.Color("196"),
		InfoTypeSuccess: lipgloss.Color("82"),
	}

	titleColor := colorMap[d.InfoType]
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(titleColor).
		MarginBottom(1)

	hint := "\n\n" + lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		Italic(true).
		Render("Press any key to dismiss")

	// Override title rendering with colored title
	d.Modal.Title = ""
	d.Modal.Content = titleStyle.Render(d.Title) + "\n" + d.Modal.Content + hint
	return d.Modal.Render(background)
}
