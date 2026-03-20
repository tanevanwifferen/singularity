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

	// Test ctrl+c
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Error("Expected non-nil command (tea.Quit) after ctrl+c")
	}
}

func TestModelUpdateQuitQ(t *testing.T) {
	model := New()

	// Test q
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Error("Expected non-nil command (tea.Quit) after q")
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
