package components

import (
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TextInput is a reusable single-line text input with cursor support.
type TextInput struct {
	Value  string
	Cursor int
}

// NewTextInput creates a new empty TextInput.
func NewTextInput() TextInput {
	return TextInput{}
}

// Set replaces the value and moves the cursor to the end.
func (t *TextInput) Set(value string) {
	t.Value = value
	t.Cursor = len(value)
}

// Clear resets the input to empty.
func (t *TextInput) Clear() {
	t.Value = ""
	t.Cursor = 0
}

// Insert inserts a string at the cursor position.
func (t *TextInput) Insert(s string) {
	t.Value = t.Value[:t.Cursor] + s + t.Value[t.Cursor:]
	t.Cursor += len(s)
}

// Backspace deletes the character before the cursor.
func (t *TextInput) Backspace() {
	if t.Cursor > 0 {
		t.Value = t.Value[:t.Cursor-1] + t.Value[t.Cursor:]
		t.Cursor--
	}
}

// Delete deletes the character at the cursor.
func (t *TextInput) Delete() {
	if t.Cursor < len(t.Value) {
		t.Value = t.Value[:t.Cursor] + t.Value[t.Cursor+1:]
	}
}

// MoveCursorLeft moves the cursor one position left.
func (t *TextInput) MoveCursorLeft() {
	if t.Cursor > 0 {
		t.Cursor--
	}
}

// MoveCursorRight moves the cursor one position right.
func (t *TextInput) MoveCursorRight() {
	if t.Cursor < len(t.Value) {
		t.Cursor++
	}
}

// Home moves the cursor to the beginning.
func (t *TextInput) Home() {
	t.Cursor = 0
}

// End moves the cursor to the end.
func (t *TextInput) End() {
	t.Cursor = len(t.Value)
}

// DeleteWord removes the word before the cursor (ctrl+w behavior).
func (t *TextInput) DeleteWord() {
	t.Value, t.Cursor = DeleteWord(t.Value, t.Cursor)
}

// HandleKey processes a key message and returns true if it was handled.
func (t *TextInput) HandleKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "left", "ctrl+b":
		t.MoveCursorLeft()
	case "right", "ctrl+f":
		t.MoveCursorRight()
	case "home", "ctrl+a":
		t.Home()
	case "end", "ctrl+e":
		t.End()
	case "backspace":
		t.Backspace()
	case "delete":
		t.Delete()
	case "ctrl+w":
		t.DeleteWord()
	case "ctrl+enter":
		t.Insert("\n")
	default:
		if msg.Paste && len(msg.Runes) > 0 {
			t.Insert(string(msg.Runes))
		} else if len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r >= 32 && unicode.IsPrint(r) {
				t.Insert(string(r))
			}
		} else {
			return false
		}
	}
	return true
}

// Render renders the input value with a cursor indicator using the given style.
func (t *TextInput) Render(style lipgloss.Style) string {
	if t.Cursor >= len(t.Value) {
		return style.Render(t.Value) + "█"
	}
	return style.Render(t.Value[:t.Cursor]) + "█" + style.Render(t.Value[t.Cursor:])
}

// RenderPlain renders the input value with a block cursor, no styling.
func (t *TextInput) RenderPlain() string {
	if t.Cursor >= len(t.Value) {
		return t.Value + "█"
	}
	return t.Value[:t.Cursor] + "█" + t.Value[t.Cursor:]
}
