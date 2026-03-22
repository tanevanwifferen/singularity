package components

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNewModal(t *testing.T) {
	m := NewModal("Test Title", "Test content")
	if m.Title != "Test Title" {
		t.Errorf("expected title 'Test Title', got %q", m.Title)
	}
	if m.Content != "Test content" {
		t.Errorf("expected content 'Test content', got %q", m.Content)
	}
	if m.Width != 50 {
		t.Errorf("expected width 50, got %d", m.Width)
	}
}

func TestModalSetSize(t *testing.T) {
	m := NewModal("T", "C")
	m.SetSize(120, 40)
	if m.termWidth != 120 || m.termHeight != 40 {
		t.Errorf("expected 120x40, got %dx%d", m.termWidth, m.termHeight)
	}
}

func TestModalRender(t *testing.T) {
	m := NewModal("Title", "Body text")
	m.SetSize(80, 24)
	bg := strings.Repeat("background line\n", 24)
	output := m.Render(bg)
	if output == "" {
		t.Error("expected non-empty render output")
	}
	if !strings.Contains(output, "Title") {
		t.Error("expected output to contain title")
	}
	if !strings.Contains(output, "Body text") {
		t.Error("expected output to contain body text")
	}
}

func TestConfirmDialogDefaults(t *testing.T) {
	d := NewConfirmDialog("Confirm?", "Are you sure?", "test-id")
	if d.ID != "test-id" {
		t.Errorf("expected ID 'test-id', got %q", d.ID)
	}
	if d.selected != 1 {
		t.Error("expected default selection to be No (1)")
	}
}

func TestConfirmDialogYKey(t *testing.T) {
	d := NewConfirmDialog("Confirm?", "msg", "id")
	d.SetSize(80, 24)

	updated, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	_ = updated
	if cmd == nil {
		t.Fatal("expected command from 'y' key")
	}
	msg := cmd()
	result, ok := msg.(ConfirmResult)
	if !ok {
		t.Fatal("expected ConfirmResult message")
	}
	if !result.Confirmed {
		t.Error("expected Confirmed=true for 'y' key")
	}
	if result.ID != "id" {
		t.Errorf("expected ID 'id', got %q", result.ID)
	}
}

func TestConfirmDialogNKey(t *testing.T) {
	d := NewConfirmDialog("Confirm?", "msg", "id")

	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd == nil {
		t.Fatal("expected command from 'n' key")
	}
	msg := cmd()
	result, ok := msg.(ConfirmResult)
	if !ok {
		t.Fatal("expected ConfirmResult message")
	}
	if result.Confirmed {
		t.Error("expected Confirmed=false for 'n' key")
	}
}

func TestConfirmDialogEsc(t *testing.T) {
	d := NewConfirmDialog("Confirm?", "msg", "id")

	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("expected command from esc key")
	}
	msg := cmd()
	result, ok := msg.(ConfirmResult)
	if !ok {
		t.Fatal("expected ConfirmResult message")
	}
	if result.Confirmed {
		t.Error("expected Confirmed=false for esc key")
	}
}

func TestConfirmDialogArrowNavAndEnter(t *testing.T) {
	d := NewConfirmDialog("Confirm?", "msg", "id")
	// Default is No (selected=1), move left to Yes
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if d.selected != 0 {
		t.Error("expected selected=0 after left arrow")
	}

	// Press enter to confirm
	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected command from enter")
	}
	msg := cmd()
	result := msg.(ConfirmResult)
	if !result.Confirmed {
		t.Error("expected Confirmed=true when Yes is selected")
	}
}

func TestConfirmDialogView(t *testing.T) {
	d := NewConfirmDialog("Delete?", "Are you sure?", "del")
	d.SetSize(80, 24)
	bg := strings.Repeat("x\n", 24)
	output := d.View(bg)
	if !strings.Contains(output, "[Y]es") {
		t.Error("expected [Y]es button in output")
	}
	if !strings.Contains(output, "[N]o") {
		t.Error("expected [N]o button in output")
	}
}

func TestInfoDialogTypes(t *testing.T) {
	tests := []struct {
		infoType InfoType
		name     string
	}{
		{InfoTypeInfo, "info"},
		{InfoTypeError, "error"},
		{InfoTypeSuccess, "success"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewInfoDialog("Title", "Message", "id", tt.infoType)
			if d.InfoType != tt.infoType {
				t.Errorf("expected InfoType %d, got %d", tt.infoType, d.InfoType)
			}
		})
	}
}

func TestInfoDialogDismissOnAnyKey(t *testing.T) {
	d := NewInfoDialog("Error", "Something failed", "err1", InfoTypeError)

	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd == nil {
		t.Fatal("expected command on key press")
	}
	msg := cmd()
	dismissed, ok := msg.(InfoDismissedMsg)
	if !ok {
		t.Fatal("expected InfoDismissedMsg")
	}
	if dismissed.ID != "err1" {
		t.Errorf("expected ID 'err1', got %q", dismissed.ID)
	}
}

func TestInfoDialogView(t *testing.T) {
	d := NewInfoDialog("Info", "Details here", "i1", InfoTypeInfo)
	d.SetSize(80, 24)
	bg := strings.Repeat(".\n", 24)
	output := d.View(bg)
	if !strings.Contains(output, "Details here") {
		t.Error("expected message in output")
	}
	if !strings.Contains(output, "Press any key to dismiss") {
		t.Error("expected dismiss hint in output")
	}
}

func TestInfoDialogAutoDismiss(t *testing.T) {
	d := NewInfoDialog("Info", "Will auto-dismiss", "auto1", InfoTypeInfo)
	d.SetAutoDismiss(true, 0) // use default 3s delay

	// Init should return a tick command
	cmd := d.Init()
	if cmd == nil {
		t.Error("expected non-nil command when auto-dismiss is enabled")
	}

	// Without auto-dismiss, Init should return nil
	d2 := NewInfoDialog("Info", "No auto-dismiss", "noauto", InfoTypeInfo)
	cmd2 := d2.Init()
	if cmd2 != nil {
		t.Error("expected nil command when auto-dismiss is disabled")
	}
}

func TestHelpOverlayCreation(t *testing.T) {
	bindings := []KeyBinding{
		{Key: "q", Description: "Quit"},
		{Key: "?", Description: "Toggle help"},
		{Key: "j/k", Description: "Navigate"},
	}
	h := NewHelpOverlay(bindings)
	if len(h.Bindings) != 3 {
		t.Errorf("expected 3 bindings, got %d", len(h.Bindings))
	}
}

func TestHelpOverlayDismiss(t *testing.T) {
	h := NewHelpOverlay([]KeyBinding{{Key: "q", Description: "Quit"}})

	// Test ? and esc to dismiss
	for _, key := range []string{"?", "esc"} {
		var cmd tea.Cmd
		if key == "?" {
			_, cmd = h.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		} else if key == "esc" {
			_, cmd = h.Update(tea.KeyMsg{Type: tea.KeyEscape})
		}
		if cmd == nil {
			t.Errorf("expected command from %q key", key)
			continue
		}
		msg := cmd()
		if _, ok := msg.(ModalClosedMsg); !ok {
			t.Errorf("expected ModalClosedMsg from %q key", key)
		}
	}
}

func TestHelpOverlayScroll(t *testing.T) {
	bindings := make([]KeyBinding, 30)
	for i := range bindings {
		bindings[i] = KeyBinding{Key: "key", Description: "desc"}
	}
	h := NewHelpOverlay(bindings)
	h.visible = 10

	// Scroll down
	h, _ = h.Update(tea.KeyMsg{Type: tea.KeyDown})
	if h.scroll != 1 {
		t.Errorf("expected scroll=1, got %d", h.scroll)
	}

	// Scroll up
	h, _ = h.Update(tea.KeyMsg{Type: tea.KeyUp})
	if h.scroll != 0 {
		t.Errorf("expected scroll=0, got %d", h.scroll)
	}

	// Can't scroll past 0
	h, _ = h.Update(tea.KeyMsg{Type: tea.KeyUp})
	if h.scroll != 0 {
		t.Errorf("expected scroll=0 (clamped), got %d", h.scroll)
	}
}

func TestHelpOverlayView(t *testing.T) {
	bindings := []KeyBinding{
		{Key: "q", Description: "Quit application"},
		{Key: "?", Description: "Toggle help overlay"},
	}
	h := NewHelpOverlay(bindings)
	h.SetSize(80, 24)
	bg := strings.Repeat(" \n", 24)
	output := h.View(bg)
	if !strings.Contains(output, "Quit application") {
		t.Error("expected binding description in output")
	}
}

func TestWindowSizeMsg(t *testing.T) {
	// ConfirmDialog handles WindowSizeMsg
	cd := NewConfirmDialog("T", "M", "id")
	cd, _ = cd.Update(tea.WindowSizeMsg{Width: 100, Height: 50})
	if cd.Modal.termWidth != 100 || cd.Modal.termHeight != 50 {
		t.Error("ConfirmDialog did not update size")
	}

	// InfoDialog handles WindowSizeMsg
	id := NewInfoDialog("T", "M", "id", InfoTypeInfo)
	id.SetSize(100, 50)
	if id.Modal.termWidth != 100 || id.Modal.termHeight != 50 {
		t.Error("InfoDialog did not update size")
	}

	// HelpOverlay handles WindowSizeMsg
	ho := NewHelpOverlay(nil)
	ho, _ = ho.Update(tea.WindowSizeMsg{Width: 100, Height: 50})
	if ho.termWidth != 100 || ho.termHeight != 50 {
		t.Error("HelpOverlay did not update size")
	}
}
