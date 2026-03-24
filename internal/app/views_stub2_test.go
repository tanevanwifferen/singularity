package app

import (
	"fmt"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	"github.com/charmbracelet/bubbletea"
)

// StubView2 is a simple stub view for testing routing.
type StubView2 struct {
	repoPath string
}

// NewStubView2 creates a new stub view 2.
func NewStubView2(repoPath string) *StubView2 {
	return &StubView2{repoPath: repoPath}
}

// Init initializes the view.
func (v *StubView2) Init() tea.Cmd {
	return nil
}

// Update handles update events.
func (v *StubView2) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "r":
			// Refresh
			return v, nil
		}
	}
	return v, nil
}

// View renders the view.
func (v *StubView2) View() string {
	th := theme.GetTheme()
	s := th.DashboardTitle.Render(" Stub View 2 ")
	s += "\n\n"
	s += th.StatsStyle.Render(fmt.Sprintf(" Repository: %s", v.repoPath))
	s += "\n\n"
	s += th.InfoStyle.Render(" This is Stub View 2 - used for testing the router.")
	s += "\n\n"
	s += th.Help.Render(" Press 1 to switch to Stub View 1")
	s += "\n"
	s += th.Help.Render(" Press 2 to stay on Stub View 2")
	return s
}

// ShortHelp returns a short help string.
func (v *StubView2) ShortHelp() string {
	return "1: View1  2: View2"
}

// SetSize updates the view dimensions (stub implementation).
func (v *StubView2) SetSize(width, height int) {
	// Stub views don't need to track dimensions
}

// KeyBindings returns the keybindings for this view.
func (v *StubView2) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "1", Description: "Switch to Stub View 1"},
		{Key: "2", Description: "Switch to Stub View 2"},
		{Key: "r", Description: "Refresh"},
	}
}
