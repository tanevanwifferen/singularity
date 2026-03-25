package components

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ItemRenderer is a callback function type for rendering list items.
// It receives the item, its index, and whether it's currently selected.
// Returns the rendered string for the item.
type ItemRenderer[T any] func(item T, index int, selected bool) string

// ListStyles defines styling options for the List component.
type ListStyles struct {
	Item          lipgloss.Style
	SelectedItem  lipgloss.Style
	Scrollbar     lipgloss.Style
	StatusBar     lipgloss.Style
	StatusBarText lipgloss.Style
}

// defaultListStyles returns default styles for a dark theme list.
func defaultListStyles() ListStyles {
	return ListStyles{
		Item: lipgloss.NewStyle().
			Foreground(lipgloss.Color("86")),

		SelectedItem: lipgloss.NewStyle().
			Foreground(lipgloss.Color("220")).
			Background(lipgloss.Color("235")).
			Bold(true),

		Scrollbar: lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")),

		StatusBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Background(lipgloss.Color("57")),

		StatusBarText: lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")),
	}
}

// List is a generic scrollable list component with viewport management.
// It supports cursor navigation, configurable item rendering, and shows
// the current selection range (e.g., "1-20 of 100").
type List[T any] struct {
	// Items holds the list items to display
	Items []T

	// Cursor tracks the currently selected index
	Cursor int

	// Offset tracks the scroll position (first visible item index)
	Offset int

	// Height is the visible viewport height (number of items to show)
	Height int

	// Renderer is the callback for rendering individual items
	Renderer ItemRenderer[T]

	// Styles for the list appearance
	Styles ListStyles

	// ShowStatusBar controls whether to show item count display
	ShowStatusBar bool

	// statusBarHeight is the number of lines for the status bar
	statusBarHeight int
}

// NewList creates a new List component with the given items and renderer.
func NewList[T any](items []T, renderer ItemRenderer[T]) *List[T] {
	return &List[T]{
		Items:           items,
		Cursor:          0,
		Offset:          0,
		Height:          20,
		Renderer:        renderer,
		Styles:          defaultListStyles(),
		ShowStatusBar:   true,
		statusBarHeight: 1,
	}
}

// SetItems updates the list items and resets cursor position.
func (l *List[T]) SetItems(items []T) {
	l.Items = items
	if l.Cursor >= len(items) {
		l.Cursor = len(items) - 1
		if l.Cursor < 0 {
			l.Cursor = 0
		}
	}
	l.ensureCursorVisible()
}

// SetHeight sets the viewport height.
func (l *List[T]) SetHeight(height int) {
	l.Height = height
	l.ensureCursorVisible()
}

// ensureCursorVisible ensures the cursor is within the visible viewport.
// If the cursor is outside the visible range, it adjusts the offset.
func (l *List[T]) ensureCursorVisible() {
	if len(l.Items) == 0 {
		l.Offset = 0
		l.Cursor = 0
		return
	}

	// Calculate visible area
	visibleHeight := l.Height - l.statusBarHeight
	if visibleHeight < 1 {
		visibleHeight = 1
	}

	// If cursor is above viewport, scroll up
	if l.Cursor < l.Offset {
		l.Offset = l.Cursor
	}

	// If cursor is below viewport, scroll down
	if l.Cursor >= l.Offset+visibleHeight {
		l.Offset = l.Cursor - visibleHeight + 1
	}

	// Clamp offset to valid range
	maxOffset := len(l.Items) - visibleHeight
	if maxOffset < 0 {
		maxOffset = 0
	}
	if l.Offset > maxOffset {
		l.Offset = maxOffset
	}
}

// pageUp moves the cursor and viewport up by one page.
func (l *List[T]) pageUp() {
	visibleHeight := l.Height - l.statusBarHeight
	if visibleHeight < 1 {
		visibleHeight = 1
	}

	// Move cursor up by page size
	l.Cursor -= visibleHeight
	if l.Cursor < 0 {
		l.Cursor = 0
	}

	// Adjust offset
	if l.Offset > l.Cursor {
		l.Offset = l.Cursor
	}

	l.ensureCursorVisible()
}

// pageDown moves the cursor and viewport down by one page.
func (l *List[T]) pageDown() {
	visibleHeight := l.Height - l.statusBarHeight
	if visibleHeight < 1 {
		visibleHeight = 1
	}

	// Move cursor down by page size
	l.Cursor += visibleHeight
	if l.Cursor >= len(l.Items) {
		l.Cursor = len(l.Items) - 1
		if l.Cursor < 0 {
			l.Cursor = 0
		}
	}

	l.ensureCursorVisible()
}

// goToTop moves cursor to the first item.
func (l *List[T]) goToTop() {
	l.Cursor = 0
	l.Offset = 0
}

// goToBottom moves cursor to the last item.
func (l *List[T]) goToBottom() {
	if len(l.Items) == 0 {
		return
	}
	l.Cursor = len(l.Items) - 1
	l.ensureCursorVisible()
}

// Update handles key events for the list navigation.
func (l *List[T]) Update(msg tea.Msg) *List[T] {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if l.Cursor > 0 {
				l.Cursor--
				l.ensureCursorVisible()
			}
		case "down", "j":
			if l.Cursor < len(l.Items)-1 {
				l.Cursor++
				l.ensureCursorVisible()
			}
		case "pgup", "ctrl+u":
			l.pageUp()
		case "pgdown", "ctrl+d":
			l.pageDown()
		case "home", "g":
			l.goToTop()
		case "end", "G":
			l.goToBottom()
		}
	case tea.WindowSizeMsg:
		l.Height = msg.Height
		l.ensureCursorVisible()
	}
	return l
}

// View renders the list component.
func (l *List[T]) View() string {
	if len(l.Items) == 0 {
		return l.Styles.StatusBarText.Render("No items")
	}

	visibleHeight := l.Height - l.statusBarHeight
	if visibleHeight < 1 {
		visibleHeight = 1
	}

	var lines []string

	// Calculate visible range
	start := l.Offset
	end := l.Offset + visibleHeight
	if end > len(l.Items) {
		end = len(l.Items)
	}

	// Render visible items
	for i := start; i < end; i++ {
		item := l.Items[i]
		isSelected := i == l.Cursor

		if l.Renderer != nil {
			rendered := l.Renderer(item, i, isSelected)
			lines = append(lines, rendered)
		} else {
			// Default rendering using fmt.Sprintf
			defaultRender := fmt.Sprintf("%v", item)
			if isSelected {
				lines = append(lines, l.Styles.SelectedItem.Render(defaultRender))
			} else {
				lines = append(lines, l.Styles.Item.Render(defaultRender))
			}
		}
	}

	// Build result with padding to fill viewport
	result := strings.Join(lines, "\n")

	// Add padding lines if we have fewer items than viewport height
	remaining := visibleHeight - len(lines)
	for i := 0; i < remaining; i++ {
		result += "\n"
	}

	// Add status bar
	if l.ShowStatusBar {
		result += "\n" + l.renderStatusBar()
	}

	return result
}

// renderStatusBar creates the item count display (e.g., "1-20 of 100").
func (l *List[T]) renderStatusBar() string {
	if len(l.Items) == 0 {
		return l.Styles.StatusBarText.Render("0 items")
	}

	visibleHeight := l.Height - l.statusBarHeight
	if visibleHeight < 1 {
		visibleHeight = 1
	}

	// Calculate visible range text
	startItem := l.Offset + 1
	endItem := l.Offset + visibleHeight
	if endItem > len(l.Items) {
		endItem = len(l.Items)
	}

	statusText := fmt.Sprintf("%d-%d of %d", startItem, endItem, len(l.Items))

	// Add scrollbar indicator if there are more items
	if len(l.Items) > visibleHeight {
		scrollPercent := float64(l.Offset) / float64(len(l.Items)-visibleHeight)
		if scrollPercent > 1 {
			scrollPercent = 1
		}
		scrollBar := l.renderScrollbar(int(scrollPercent * 100))
		statusText += " " + scrollBar
	}

	return l.Styles.StatusBar.Render(statusText)
}

// renderScrollbar creates a visual scrollbar indicator.
func (l *List[T]) renderScrollbar(position int) string {
	const totalBars = 10
	bar := position / (100 / totalBars)
	if bar > totalBars-1 {
		bar = totalBars - 1
	}

	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < totalBars; i++ {
		if i == bar {
			sb.WriteString(l.Styles.Scrollbar.Render("█"))
		} else {
			sb.WriteString(l.Styles.Scrollbar.Render("░"))
		}
	}
	sb.WriteString("]")
	return sb.String()
}

// SelectedItem returns the currently selected item and its index.
// Returns zero value and -1 if no items or invalid cursor.
func (l *List[T]) SelectedItem() (T, int) {
	var zero T
	if len(l.Items) == 0 || l.Cursor < 0 || l.Cursor >= len(l.Items) {
		return zero, -1
	}
	return l.Items[l.Cursor], l.Cursor
}

// SelectedIndex returns the currently selected index.
func (l *List[T]) SelectedIndex() int {
	return l.Cursor
}

// Len returns the number of items in the list.
func (l *List[T]) Len() int {
	return len(l.Items)
}

// CursorUp moves the cursor up by one position.
func (l *List[T]) CursorUp() {
	if l.Cursor > 0 {
		l.Cursor--
		l.ensureCursorVisible()
	}
}

// CursorDown moves the cursor down by one position.
func (l *List[T]) CursorDown() {
	if l.Cursor < len(l.Items)-1 {
		l.Cursor++
		l.ensureCursorVisible()
	}
}

// HandleMouse processes mouse events for the list.
// Returns true if the mouse event was handled.
func (l *List[T]) HandleMouse(msg tea.MouseMsg) bool {
	visibleHeight := l.Height - l.statusBarHeight
	if visibleHeight < 1 {
		visibleHeight = 1
	}

	switch msg.Type {
	case tea.MouseLeft:
		// Check if click is within the list area (not in status bar)
		if msg.Y >= 0 && msg.Y < visibleHeight {
			// Calculate which item was clicked
			clickedIdx := l.Offset + int(msg.Y)
			if clickedIdx >= 0 && clickedIdx < len(l.Items) {
				l.Cursor = clickedIdx
				l.ensureCursorVisible()
				return true
			}
		}
	case tea.MouseWheelUp, tea.MouseWheelDown, tea.MouseWheelLeft, tea.MouseWheelRight:
		// Handle scroll wheel
		// Note: msg.Type for wheel events uses the deprecated constants
		// but Button can also be checked via IsWheel()
		switch msg.Type {
		case tea.MouseWheelUp:
			l.HandleScroll(-3)
		case tea.MouseWheelDown:
			l.HandleScroll(3)
		case tea.MouseWheelLeft:
			l.HandleScroll(-3)
		case tea.MouseWheelRight:
			l.HandleScroll(3)
		}
		return true
	}
	return false
}

// HandleScroll handles scroll wheel events.
// delta > 0 means scroll down, delta < 0 means scroll up.
func (l *List[T]) HandleScroll(delta int) {
	if len(l.Items) == 0 {
		return
	}

	// Move cursor in scroll direction
	if delta > 0 {
		// Scroll down
		if l.Cursor < len(l.Items)-1 {
			l.Cursor++
			l.ensureCursorVisible()
		}
	} else if delta < 0 {
		// Scroll up
		if l.Cursor > 0 {
			l.Cursor--
			l.ensureCursorVisible()
		}
	}
}
