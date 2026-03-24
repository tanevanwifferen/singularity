package components

import (
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// SpinnerStyles defines styling options for the Spinner component.
type SpinnerStyles struct {
	Container lipgloss.Style
	Message   lipgloss.Style
}

// DefaultSpinnerStyles returns default styles matching the singularity theme.
func DefaultSpinnerStyles() SpinnerStyles {
	return SpinnerStyles{
		Container: lipgloss.NewStyle().
			Foreground(lipgloss.Color("75")),

		Message: lipgloss.NewStyle().
			Foreground(lipgloss.Color("250")),
	}
}

// Spinner is a component that displays a loading indicator with a message.
// It wraps the Bubbles spinner and provides consistent styling.
type Spinner struct {
	spinner  spinner.Model
	message  string
	visible  bool
	Styles   SpinnerStyles
}

// NewSpinner creates a new Spinner component.
func NewSpinner() *Spinner {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().
		Foreground(lipgloss.Color("75"))

	return &Spinner{
		spinner: s,
		message: "Loading...",
		visible: false,
		Styles:  DefaultSpinnerStyles(),
	}
}

// SetMessage sets the message displayed next to the spinner.
func (s *Spinner) SetMessage(msg string) *Spinner {
	s.message = msg
	return s
}

// SetVisible shows or hides the spinner.
func (s *Spinner) SetVisible(visible bool) *Spinner {
	s.visible = visible
	return s
}

// Start begins the spinner animation.
func (s *Spinner) Start() *Spinner {
	s.visible = true
	return s
}

// Stop halts the spinner animation.
func (s *Spinner) Stop() *Spinner {
	s.visible = false
	return s
}

// IsVisible returns whether the spinner is currently visible.
func (s *Spinner) IsVisible() bool {
	return s.visible
}

// View renders the spinner component with the message.
func (s *Spinner) View() string {
	if !s.visible {
		return ""
	}

	var builder strings.Builder
	builder.WriteString(s.Styles.Container.Render(s.spinner.View()))
	builder.WriteString(" ")
	builder.WriteString(s.Styles.Message.Render(s.message))

	return builder.String()
}

// ViewWithOverlay renders the spinner as an overlay on top of existing content.
// This centers the spinner and adds a semi-transparent background.
func (s *Spinner) ViewWithOverlay(content string, width, height int) string {
	if !s.visible {
		return content
	}

	spinnerView := s.View()

	// Calculate center position
	contentLines := strings.Split(content, "\n")
	contentWidth := 0
	for _, line := range contentLines {
		if len(line) > contentWidth {
			contentWidth = len(line)
		}
	}

	// Ensure minimum width
	if contentWidth < 20 {
		contentWidth = 20
	}

	// Create overlay centered on the content
	overlayLines := []string{
		strings.Repeat(" ", contentWidth/2-len(spinnerView)/2) + spinnerView,
	}

	// Find middle of content
	midPoint := height / 2
	if midPoint > len(contentLines) {
		midPoint = len(contentLines) / 2
	}

	// Reconstruct content with spinner overlay
	var result strings.Builder
	for i, line := range contentLines {
		if i == midPoint {
			// Insert spinner line before this line
			for _, overlayLine := range overlayLines {
				result.WriteString(overlayLine)
				result.WriteString("\n")
			}
		}
		result.WriteString(line)
		result.WriteString("\n")
	}

	return result.String()
}

// Init initializes the spinner component.
func (s *Spinner) Init() tea.Cmd {
	if s.visible {
		return s.spinner.Tick
	}
	return nil
}

// Update handles update events for the spinner.
func (s *Spinner) Update(msg tea.Msg) (*Spinner, tea.Cmd) {
	if !s.visible {
		return s, nil
	}

	newSpinner, cmd := s.spinner.Update(msg)
	s.spinner = newSpinner
	return s, cmd
}
