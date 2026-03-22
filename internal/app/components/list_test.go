package components

import (
	"strings"
	"testing"
)

func TestListScrolling(t *testing.T) {
	// Create 100 test items
	items := make([]string, 100)
	for i := 0; i < 100; i++ {
		items[i] = string(rune('A'+i%26)) + " item " + string(rune('0'+i/10)) + string(rune('0'+i%10))
	}

	// Create list with 20-line viewport (21 with status bar)
	renderer := func(item string, index int, selected bool) string {
		prefix := "  "
		if selected {
			prefix = "> "
		}
		return prefix + item
	}
	list := NewList(items, renderer)
	list.SetHeight(20)

	// Test initial state
	if list.Offset != 0 {
		t.Errorf("Expected initial offset 0, got %d", list.Offset)
	}
	if list.Cursor != 0 {
		t.Errorf("Expected initial cursor 0, got %d", list.Cursor)
	}

	// Test scrolling down
	view := list.View()
	lines := strings.Split(view, "\n")
	// Should show items 1-19 (0-18) + status bar = 20 lines
	if len(lines) < 20 {
		t.Errorf("Expected at least 20 lines in view, got %d", len(lines))
	}

	// Verify first visible item is "A item 00"
	if !strings.Contains(lines[0], "A item 00") {
		t.Errorf("First item should be 'A item 00', got: %s", lines[0])
	}

	// Test cursor down navigation
	list.CursorDown()
	if list.Cursor != 1 {
		t.Errorf("After cursor down, cursor should be 1, got %d", list.Cursor)
	}

	// Test page down - move cursor down by page size
	visibleHeight := 20 - 1 // minus status bar
	for i := 0; i < visibleHeight; i++ {
		list.CursorDown()
	}
	expectedCursorAfterPageDown := 1 + visibleHeight
	if list.Cursor != expectedCursorAfterPageDown {
		t.Errorf("After page down, cursor should be %d, got %d", expectedCursorAfterPageDown, list.Cursor)
	}

	// Test go to bottom
	list.goToBottom()
	if list.Cursor != 99 {
		t.Errorf("After goToBottom, cursor should be 99, got %d", list.Cursor)
	}

	// Test go to top
	list.goToTop()
	if list.Cursor != 0 {
		t.Errorf("After goToTop, cursor should be 0, got %d", list.Cursor)
	}

	// Test page up from top
	for i := 0; i < visibleHeight; i++ {
		list.CursorUp()
	}
	expectedCursorAfterPageUp := 0
	if list.Cursor != expectedCursorAfterPageUp {
		t.Errorf("After page up, cursor should be %d, got %d", expectedCursorAfterPageUp, list.Cursor)
	}
}

func TestListSelectedItem(t *testing.T) {
	items := []string{"apple", "banana", "cherry"}
	renderer := func(item string, index int, selected bool) string {
		return item
	}
	list := NewList(items, renderer)

	item, idx := list.SelectedItem()
	if item != "apple" || idx != 0 {
		t.Errorf("Expected ('apple', 0), got (%s, %d)", item, idx)
	}

	// Move cursor down
	list.CursorDown()
	item, idx = list.SelectedItem()
	if item != "banana" || idx != 1 {
		t.Errorf("Expected ('banana', 1), got (%s, %d)", item, idx)
	}
}

func TestListStatusBar(t *testing.T) {
	items := make([]string, 100)
	for i := 0; i < 100; i++ {
		items[i] = "item"
	}
	renderer := func(item string, index int, selected bool) string {
		return item
	}
	list := NewList(items, renderer)
	list.SetHeight(20)

	view := list.View()

	// Check status bar shows "1-19 of 100" (19 items visible = 20 - 1 status bar)
	if !strings.Contains(view, "1-19 of 100") {
		t.Errorf("Status bar should contain '1-19 of 100', got: %s", view)
	}
}

func TestListEmpty(t *testing.T) {
	items := []string{}
	renderer := func(item string, index int, selected bool) string {
		return item
	}
	list := NewList(items, renderer)
	list.SetHeight(20)

	view := list.View()
	if !strings.Contains(view, "No items") {
		t.Errorf("Empty list should show 'No items', got: %s", view)
	}
}

func TestListSetItems(t *testing.T) {
	renderer := func(item string, index int, selected bool) string {
		return item
	}
	list := NewList([]string{"a", "b", "c"}, renderer)

	// Set fewer items
	list.SetItems([]string{"x"})
	if list.Len() != 1 {
		t.Errorf("Expected 1 item, got %d", list.Len())
	}
	if list.Cursor != 0 {
		t.Errorf("Expected cursor 0, got %d", list.Cursor)
	}

	// Set items with cursor beyond bounds
	list.SetItems([]string{"a", "b"})
	list.Cursor = 5 // beyond bounds
	list.SetItems([]string{"a", "b", "c", "d", "e"})
	if list.Cursor != 4 { // should be clamped to len-1
		t.Errorf("Expected cursor clamped to 4, got %d", list.Cursor)
	}
}

func TestListLen(t *testing.T) {
	renderer := func(item string, index int, selected bool) string {
		return item
	}
	list := NewList([]string{"a", "b", "c"}, renderer)
	if list.Len() != 3 {
		t.Errorf("Expected len 3, got %d", list.Len())
	}
}

func TestListHighlighting(t *testing.T) {
	renderer := func(item string, index int, selected bool) string {
		if selected {
			return "> " + item
		}
		return "  " + item
	}
	list := NewList([]string{"first", "second", "third"}, renderer)
	list.SetHeight(10)

	view := list.View()
	lines := strings.Split(view, "\n")

	// First item should be selected
	if !strings.HasPrefix(lines[0], "> ") {
		t.Errorf("First item should be selected (>), got: %s", lines[0])
	}
	if !strings.Contains(lines[0], "first") {
		t.Errorf("First item should be 'first', got: %s", lines[0])
	}

	// Move cursor and check highlighting changes
	list.CursorDown()
	view = list.View()
	lines = strings.Split(view, "\n")

	if !strings.HasPrefix(lines[1], "> ") {
		t.Errorf("Second item should be selected after cursor down, got: %s", lines[1])
	}
	if !strings.Contains(lines[1], "second") {
		t.Errorf("Second item should be 'second', got: %s", lines[1])
	}
}

func TestListStyles(t *testing.T) {
	items := []string{"item1", "item2"}
	renderer := func(item string, index int, selected bool) string {
		return item
	}
	list := NewList(items, renderer)

	// Verify styles are set - just check they don't panic
	normalRender := list.Styles.Item.Render("test")
	selectedRender := list.Styles.SelectedItem.Render("test")

	// Both should produce some output (not panic)
	if normalRender == "" || selectedRender == "" {
		t.Error("Styles should render non-empty strings")
	}

	// Styles should be different objects (checking pointer isn't possible)
	// Just verify they don't cause panics
	list.Styles.Scrollbar.Render("test")
	list.Styles.StatusBar.Render("test")
	list.Styles.StatusBarText.Render("test")
}

func TestListScrollbar(t *testing.T) {
	items := make([]string, 100)
	for i := 0; i < 100; i++ {
		items[i] = "item"
	}
	renderer := func(item string, index int, selected bool) string {
		return item
	}
	list := NewList(items, renderer)
	list.SetHeight(20)

	// Go to middle
	list.goToBottom()
	view := list.View()

	// Should contain scrollbar indicator
	if !strings.Contains(view, "[") || !strings.Contains(view, "]") {
		t.Errorf("View should contain scrollbar brackets, got: %s", view)
	}
}

func TestListHeightChange(t *testing.T) {
	items := make([]string, 100)
	for i := 0; i < 100; i++ {
		items[i] = "item"
	}
	renderer := func(item string, index int, selected bool) string {
		return item
	}
	list := NewList(items, renderer)
	list.SetHeight(20)

	// Change height
	list.SetHeight(10)

	// Status bar should now show 1-9 of 100 (9 items visible = 10 - 1 status bar)
	view := list.View()
	if !strings.Contains(view, "1-9 of 100") {
		t.Errorf("Status bar should contain '1-9 of 100', got: %s", view)
	}
}
