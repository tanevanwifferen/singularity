package components

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// FilterStyles defines styling options for the Filter component.
type FilterStyles struct {
	Input          lipgloss.Style
	InputText      lipgloss.Style
	InputCursor    lipgloss.Style
	Prompt         lipgloss.Style
	MatchHighlight lipgloss.Style
}

// defaultFilterStyles returns default styles for the filter input.
func defaultFilterStyles() FilterStyles {
	return FilterStyles{
		Input: lipgloss.NewStyle().
			Foreground(lipgloss.Color("250")).
			Background(lipgloss.Color("57")).
			Padding(0, 1),

		InputText: lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")),

		InputCursor: lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("240")),

		Prompt: lipgloss.NewStyle().
			Foreground(lipgloss.Color("250")).
			Bold(true),

		MatchHighlight: lipgloss.NewStyle().
			Foreground(lipgloss.Color("220")).
			Bold(true),
	}
}

// Filter is a component that wraps a list and provides inline filtering.
// It activates when the user presses '/' and allows real-time filtering
// of list items using case-insensitive substring matching.
type Filter[T any] struct {
	// Source items (unfiltered)
	sourceItems []T

	// Filtered items (after applying filter)
	filteredItems []T

	// Filter text
	filterText string

	// Cursor position in filter input
	filterCursor int

	// Whether filter mode is active
	active bool

	// Renderer for source items
	sourceRenderer ItemRenderer[T]

	// Renderer for filtered items (optional, can highlight matches)
	filteredRenderer ItemRenderer[T]

	// Styles
	Styles FilterStyles

	// Height for the wrapped list
	height int

	// The underlying list component
	list *List[T]
}

// NewFilter creates a new filter component wrapping the given items and renderer.
func NewFilter[T any](items []T, renderer ItemRenderer[T]) *Filter[T] {
	f := &Filter[T]{
		sourceItems:    items,
		filteredItems:  items,
		sourceRenderer: renderer,
		Styles:         defaultFilterStyles(),
		filterCursor:   0,
		active:         false,
		height:         20,
	}

	// Create the underlying list with filtered renderer
	f.filteredRenderer = f.defaultFilteredRenderer
	f.list = NewList[T](f.filteredItems, f.filteredRenderer)
	return f
}

// defaultFilteredRenderer renders items with potential filter match highlighting.
func (f *Filter[T]) defaultFilteredRenderer(item T, index int, selected bool) string {
	// Use the source renderer if available
	if f.sourceRenderer != nil {
		base := f.sourceRenderer(item, index, selected)
		// If we have filter text and highlighting is desired, apply it
		if f.filterText != "" {
			return f.highlightMatches(base, f.filterText)
		}
		return base
	}
	return ""
}

// highlightMatches highlights filter matches in the rendered string.
func (f *Filter[T]) highlightMatches(text, filter string) string {
	if filter == "" {
		return text
	}
	// Case-insensitive search
	lowerText := strings.ToLower(text)
	lowerFilter := strings.ToLower(filter)

	idx := strings.Index(lowerText, lowerFilter)
	if idx == -1 {
		return text
	}

	// Split into: before, match, after
	before := text[:idx]
	match := text[idx : idx+len(filter)]
	after := text[idx+len(filter):]

	return before + f.Styles.MatchHighlight.Render(match) + after
}

// SetItems updates the source items and re-applies the filter.
func (f *Filter[T]) SetItems(items []T) {
	f.sourceItems = items
	f.applyFilter()
}

// SetHeight sets the viewport height.
func (f *Filter[T]) SetHeight(height int) {
	f.height = height
	if f.list != nil {
		f.list.SetHeight(height)
	}
}

// applyFilter applies the current filter text to source items.
func (f *Filter[T]) applyFilter() {
	if f.filterText == "" {
		f.filteredItems = f.sourceItems
	} else {
		f.filteredItems = make([]T, 0)
		filterLower := strings.ToLower(f.filterText)
		for _, item := range f.sourceItems {
			if f.itemMatches(item, filterLower) {
				f.filteredItems = append(f.filteredItems, item)
			}
		}
	}
	if f.list != nil {
		f.list.SetItems(f.filteredItems)
	}
}

// itemMatches returns true if the item contains the filter text (case-insensitive).
// For string types, it checks if the string representation contains the filter.
// This method can be overridden for custom item types.
func (f *Filter[T]) itemMatches(item T, filterLower string) bool {
	// Default implementation: convert item to string and check substring
	itemStr := f.itemToString(item)
	return strings.Contains(strings.ToLower(itemStr), filterLower)
}

// itemToString converts an item to string for filtering.
func (f *Filter[T]) itemToString(item T) string {
	// Try to handle common types
	switch v := any(item).(type) {
	case string:
		return v
	case fmtStringer:
		return v.String()
	case error:
		return v.Error()
	default:
		// Fall back to fmt.Sprintf for other types
		return strings.TrimSpace(fmt.Sprintf("%v", item))
	}
}

// fmtStringer is an interface for types that implement fmt.Stringer.
type fmtStringer interface {
	String() string
}

// activateFilterMode activates the filter input mode.
func (f *Filter[T]) activateFilterMode() {
	f.active = true
	f.filterCursor = len(f.filterText)
}

// clearFilter clears the filter and exits filter mode.
func (f *Filter[T]) clearFilter() {
	f.filterText = ""
	f.filterCursor = 0
	f.active = false
	f.applyFilter()
}

// confirmFilter confirms the current filter and stays in filter mode.
func (f *Filter[T]) confirmFilter() {
	f.filterCursor = len(f.filterText)
	// Stay in filter mode
}

// deleteChar removes the character before the cursor.
func (f *Filter[T]) deleteChar() {
	if f.filterCursor > 0 && len(f.filterText) > 0 {
		f.filterText = f.filterText[:f.filterCursor-1] + f.filterText[f.filterCursor:]
		f.filterCursor--
		f.applyFilter()
	}
}

// moveCursorLeft moves the filter cursor left.
func (f *Filter[T]) moveCursorLeft() {
	if f.filterCursor > 0 {
		f.filterCursor--
	}
}

// moveCursorRight moves the filter cursor right.
func (f *Filter[T]) moveCursorRight() {
	if f.filterCursor < len(f.filterText) {
		f.filterCursor++
	}
}

// Update handles key events for the filter component.
func (f *Filter[T]) Update(msg tea.Msg) *Filter[T] {
	if f.list != nil {
		f.list.Update(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if f.active {
			switch msg.String() {
			case "esc":
				f.clearFilter()
				return f
			case "enter":
				f.confirmFilter()
				return f
			case "backspace":
				f.deleteChar()
				return f
			case "left", "ctrl+b":
				f.moveCursorLeft()
				return f
			case "right", "ctrl+f":
				f.moveCursorRight()
				return f
			case "ctrl+u":
				// Clear all text before cursor
				f.filterText = f.filterText[f.filterCursor:]
				f.filterCursor = 0
				f.applyFilter()
				return f
			case "ctrl+k":
				// Clear all text after cursor
				f.filterText = f.filterText[:f.filterCursor]
				f.applyFilter()
				return f
			case "ctrl+w":
				f.filterText, f.filterCursor = DeleteWord(f.filterText, f.filterCursor)
				f.applyFilter()
				return f
			case "ctrl+a":
				f.filterCursor = 0
				return f
			case "ctrl+e":
				f.filterCursor = len(f.filterText)
				return f
			}

			// Handle character input (single key or bracketed paste)
			if msg.Paste && len(msg.Runes) > 0 {
				pasted := string(msg.Runes)
				if f.filterCursor == len(f.filterText) {
					f.filterText += pasted
				} else {
					f.filterText = f.filterText[:f.filterCursor] + pasted + f.filterText[f.filterCursor:]
				}
				f.filterCursor += len([]rune(pasted))
				f.applyFilter()
			} else if len(msg.Runes) == 1 {
				r := msg.Runes[0]
				// Only accept printable characters
				if r >= 32 && r <= 126 {
					c := string(r)
					// Insert at cursor position
					if f.filterCursor == len(f.filterText) {
						f.filterText += c
					} else {
						f.filterText = f.filterText[:f.filterCursor] + c + f.filterText[f.filterCursor:]
					}
					f.filterCursor++
					f.applyFilter()
				}
			}
		} else {
			// Not in filter mode - check for '/' to activate
			if msg.String() == "/" {
				f.activateFilterMode()
			}
		}
	case tea.WindowSizeMsg:
		f.height = msg.Height
		if f.list != nil {
			f.list.SetHeight(msg.Height)
		}
	}
	return f
}

// View renders the filter component.
func (f *Filter[T]) View() string {
	var view strings.Builder

	// Render filter input if active
	if f.active {
		view.WriteString(f.renderFilterInput())
		view.WriteString("\n")
	}

	// Render the list
	if f.list != nil {
		view.WriteString(f.list.View())
	}

	return view.String()
}

// renderFilterInput renders the filter input line.
func (f *Filter[T]) renderFilterInput() string {
	var input strings.Builder

	// Prompt
	input.WriteString(f.Styles.Prompt.Render("/ "))

	// Input background
	input.WriteString(f.Styles.Input.Render(""))

	// Build the filter text with cursor
	if f.filterText != "" {
		if f.filterCursor > 0 {
			input.WriteString(f.Styles.InputText.Render(f.filterText[:f.filterCursor]))
		}

		// Cursor (rendered as a block character)
		if f.filterCursor < len(f.filterText) {
			// Show char before cursor in text style, char at cursor as cursor
			input.WriteString(f.Styles.InputCursor.Render(string(f.filterText[f.filterCursor])))
			input.WriteString(f.Styles.InputText.Render(f.filterText[f.filterCursor+1:]))
		} else {
			// Cursor at end - show blinking cursor block
			input.WriteString(f.Styles.InputCursor.Render(" "))
		}
	} else {
		// Empty input - just show cursor
		input.WriteString(f.Styles.InputCursor.Render(" "))
	}

	// Add padding to fill the input area
	padding := f.height - 2 // Account for the input line and status bar
	if padding < 0 {
		padding = 0
	}
	for i := 0; i < padding; i++ {
		input.WriteString("\n")
	}

	// Hint text
	hint := " Type to filter • Esc: clear • Enter: confirm "
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		Italic(true)
	input.WriteString("\n" + hintStyle.Render(hint))

	return input.String()
}

// List returns the underlying list component.
func (f *Filter[T]) List() *List[T] {
	return f.list
}

// FilteredItems returns the currently filtered items.
func (f *Filter[T]) FilteredItems() []T {
	return f.filteredItems
}

// SourceItems returns the original unfiltered items.
func (f *Filter[T]) SourceItems() []T {
	return f.sourceItems
}

// IsActive returns whether the filter mode is currently active.
func (f *Filter[T]) IsActive() bool {
	return f.active
}

// FilterText returns the current filter text.
func (f *Filter[T]) FilterText() string {
	return f.filterText
}

// SelectedItem returns the currently selected item from the filtered list.
func (f *Filter[T]) SelectedItem() (T, int) {
	if f.list != nil {
		return f.list.SelectedItem()
	}
	var zero T
	return zero, -1
}

// CursorUp moves the selection up.
func (f *Filter[T]) CursorUp() {
	if f.list != nil {
		f.list.CursorUp()
	}
}

// CursorDown moves the selection down.
func (f *Filter[T]) CursorDown() {
	if f.list != nil {
		f.list.CursorDown()
	}
}

// HandleMouse processes mouse events for the filter component.
// Returns true if the mouse event was handled.
func (f *Filter[T]) HandleMouse(msg tea.MouseMsg) bool {
	if f.list != nil {
		return f.list.HandleMouse(msg)
	}
	return false
}

// HandleScroll handles scroll wheel events for the filter component.
// delta > 0 means scroll down, delta < 0 means scroll up.
func (f *Filter[T]) HandleScroll(delta int) {
	if f.list != nil {
		f.list.HandleScroll(delta)
	}
}
