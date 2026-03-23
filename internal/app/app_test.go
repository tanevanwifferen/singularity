package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNew(t *testing.T) {
	model := New()
	if model == nil {
		t.Fatal("Expected non-nil Model")
	}
	if model.quitting {
		t.Error("Expected quitting=false initially")
	}
}

func TestModelInit(t *testing.T) {
	model := New()
	cmd := model.Init()
	if cmd != nil {
		t.Error("Expected nil command from Init")
	}
}

func TestModelUpdateQuitCtrlC(t *testing.T) {
	model := New()

	// Test ctrl+c shows confirmation instead of quitting immediately
	result, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil {
		t.Error("Expected nil command (confirmation shown, not quit)")
	}
	m := result.(Model)
	if !m.showQuitConfirm {
		t.Error("Expected showQuitConfirm=true after ctrl+c")
	}
}

func TestModelUpdateQuitQ(t *testing.T) {
	model := New()

	// Test q shows confirmation instead of quitting immediately
	result, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd != nil {
		t.Error("Expected nil command (confirmation shown, not quit)")
	}
	m := result.(Model)
	if !m.showQuitConfirm {
		t.Error("Expected showQuitConfirm=true after q")
	}
}

func TestModelUpdateQuitConfirmYes(t *testing.T) {
	model := New()

	// First trigger the confirmation
	result, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m := result.(Model)

	// Confirm with 'y' - produces a cmd that returns ConfirmResult
	result, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("Expected non-nil command from confirm dialog")
	}

	// Execute the cmd to get the ConfirmResult message, then dispatch it
	msg := cmd()
	result, cmd = result.(Model).Update(msg)
	if cmd == nil {
		t.Error("Expected tea.Quit command after confirming quit")
	}
	m = result.(Model)
	if !m.quitting {
		t.Error("Expected quitting=true after confirming quit")
	}
}

func TestModelUpdateQuitConfirmNo(t *testing.T) {
	model := New()

	// First trigger the confirmation
	result, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m := result.(Model)

	// Cancel with 'n' - produces a cmd that returns ConfirmResult
	result, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd == nil {
		t.Fatal("Expected non-nil command from confirm dialog")
	}

	// Execute the cmd to get the ConfirmResult message, then dispatch it
	msg := cmd()
	result, _ = result.(Model).Update(msg)
	m = result.(Model)
	if m.showQuitConfirm {
		t.Error("Expected showQuitConfirm=false after cancelling")
	}
	if m.quitting {
		t.Error("Expected quitting=false after cancelling")
	}
}

func TestModelUpdateOtherKey(t *testing.T) {
	model := New()
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if cmd != nil {
		t.Error("Expected nil command for other keys")
	}
}

func TestModelUpdateOtherMsg(t *testing.T) {
	model := New()
	_, cmd := model.Update("some other message")

	if cmd != nil {
		t.Error("Expected nil command for other messages")
	}
}

func TestModelViewNotQuitting(t *testing.T) {
	model := New()
	view := model.View()

	if view == "" {
		t.Error("Expected non-empty view")
	}
}

func TestModelViewQuitting(t *testing.T) {
	model := New()
	model.quitting = true
	view := model.View()

	if view != "Goodbye!\n" {
		t.Errorf("Expected 'Goodbye!\\n', got %q", view)
	}
}

func TestVersion(t *testing.T) {
	// Just verify version constant exists and is non-empty
	if version == "" {
		t.Error("Expected non-empty version")
	}
}
