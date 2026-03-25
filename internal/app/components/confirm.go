package components

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ConfirmPrompt is a reusable y/n confirmation dialog with callbacks.
// It replaces the duplicated switch-on-key confirmation patterns across views.
type ConfirmPrompt struct {
	Title     string
	Message   string
	Visible   bool
	OnConfirm func() tea.Cmd
	OnCancel  func()
}

// Show configures and displays the confirmation dialog.
func (c *ConfirmPrompt) Show(title, message string, onConfirm func() tea.Cmd) {
	c.Title = title
	c.Message = message
	c.Visible = true
	c.OnConfirm = onConfirm
	c.OnCancel = nil
}

// ShowWithCancel configures and displays the dialog with a custom cancel callback.
func (c *ConfirmPrompt) ShowWithCancel(title, message string, onConfirm func() tea.Cmd, onCancel func()) {
	c.Title = title
	c.Message = message
	c.Visible = true
	c.OnConfirm = onConfirm
	c.OnCancel = onCancel
}

// Hide dismisses the dialog and resets state.
func (c *ConfirmPrompt) Hide() {
	c.Visible = false
	c.Title = ""
	c.Message = ""
	c.OnConfirm = nil
	c.OnCancel = nil
}

// HandleKey processes a key event. Returns (handled, cmd) where handled is true
// if the dialog consumed the key event.
func (c *ConfirmPrompt) HandleKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if !c.Visible {
		return false, nil
	}
	switch msg.String() {
	case "y", "Y", "enter":
		onConfirm := c.OnConfirm
		c.Hide()
		if onConfirm != nil {
			return true, onConfirm()
		}
		return true, nil
	case "n", "N", "esc":
		onCancel := c.OnCancel
		c.Hide()
		if onCancel != nil {
			onCancel()
		}
		return true, nil
	}
	return true, nil // consume all keys while visible
}

// Render returns the dialog as a styled modal string using box-drawing characters.
func (c *ConfirmPrompt) Render(width int) string {
	if !c.Visible {
		return ""
	}
	if width < 20 {
		width = 20
	}
	innerW := width - 4

	var s strings.Builder

	// Top border
	titlePart := fmt.Sprintf(" %s ", c.Title)
	dashCount := innerW - len(titlePart)
	if dashCount < 0 {
		dashCount = 0
	}
	s.WriteString(fmt.Sprintf(" ╭─%s%s╮\n", titlePart, strings.Repeat("─", dashCount)))

	// Content lines
	for _, line := range strings.Split(c.Message, "\n") {
		s.WriteString(fmt.Sprintf(" │ %-*s│\n", innerW, line))
	}

	// Hint line
	hint := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("y: Confirm  n/Esc: Cancel")
	s.WriteString(fmt.Sprintf(" │ %-*s│\n", innerW, hint))

	// Bottom border
	s.WriteString(fmt.Sprintf(" ╰%s╯\n", strings.Repeat("─", innerW+2)))

	return s.String()
}
