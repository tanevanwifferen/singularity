package app

import (
	"fmt"

	"git-frontend/internal/app/components"
	"git-frontend/internal/app/views"

	tea "github.com/charmbracelet/bubbletea"
)

// View is the interface that all views must implement.
// This allows the router to manage multiple views and switch between them.
type View interface {
	// Init initializes the view, returning a command to run after startup.
	Init() tea.Cmd

	// Update handles incoming messages and updates the view.
	// It returns the updated view and an optional command.
	Update(msg tea.Msg) (tea.Model, tea.Cmd)

	// View renders the view to a string.
	View() string

	// ShortHelp returns a short help string for the view.
	ShortHelp() string
}

// SwitchViewMsg is a message to switch to a different view.
type SwitchViewMsg struct {
	ViewName string
}

// Router manages multiple views and handles switching between them.
type Router struct {
	views      map[string]View
	activeName string
	active     View

	// Help overlay state
	showHelp   bool
	helpOverlay components.HelpOverlay
}

// NewRouter creates a new router with the given initial view.
func NewRouter(initial View, name string) *Router {
	views := make(map[string]View)
	views[name] = initial
	return &Router{
		views:      views,
		activeName: name,
		active:     initial,
	}
}

// Register adds a view to the router under the given name.
func (r *Router) Register(name string, view View) {
	r.views[name] = view
}

// ActiveView returns the currently active view.
func (r *Router) ActiveView() View {
	return r.active
}

// ActiveName returns the name of the currently active view.
func (r *Router) ActiveName() string {
	return r.activeName
}

// SwitchTo switches to the view with the given name.
// Returns an error if the view doesn't exist.
func (r *Router) SwitchTo(name string) error {
	view, ok := r.views[name]
	if !ok {
		return fmt.Errorf("view %q not found", name)
	}
	r.activeName = name
	r.active = view
	return nil
}

// ViewNames returns a list of registered view names.
func (r *Router) ViewNames() []string {
	names := make([]string, 0, len(r.views))
	for name := range r.views {
		names = append(names, name)
	}
	return names
}

// Init delegates to the active view's Init.
func (r *Router) Init() tea.Cmd {
	return r.active.Init()
}

// Update handles messages and delegates to the active view.
// It handles view switching messages.
func (r *Router) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle help overlay first if visible
	if r.showHelp {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "?", "esc":
				r.showHelp = false
				return r, nil
			}
			// Pass other keys to help overlay
			updated := components.HelpOverlay{}
			updated, _ = r.helpOverlay.Update(msg)
			r.helpOverlay = updated
			return r, nil
		case tea.WindowSizeMsg:
			r.helpOverlay.SetSize(msg.Width, msg.Height)
			return r, nil
		}
	}

	// Handle SwitchViewMsg (defined in this package)
	if swMsg, ok := msg.(SwitchViewMsg); ok {
		if err := r.SwitchTo(swMsg.ViewName); err != nil {
			// Could log error here
			return r, nil
		}
		// Re-init the new view
		return r, r.active.Init()
	}

	// Handle views.ViewChangeMsg by importing views package
	// This avoids circular dependencies while allowing view-to-view communication
	// Note: We can't use the ViewChanger interface here because views.ViewChangeMsg
	// can't implement an interface from app package without circular imports.
	// Instead, we check for the concrete type via type switch.
	switch v := msg.(type) {
	case views.ViewChangeMsg:
		if err := r.SwitchTo(v.ViewName); err != nil {
			return r, nil
		}
		return r, r.active.Init()
	}

	// Handle number key switching (1-9)
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "?":
			// Show help overlay with combined global and view-specific bindings
			r.showHelp = true
			r.buildHelpOverlay()
			return r, nil
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			names := r.ViewNames()
			idx := int(keyMsg.String()[0] - '1')
			if idx < len(names) {
				if err := r.SwitchTo(names[idx]); err != nil {
					return r, nil
				}
				return r, r.active.Init()
			}
		}
	}

	// Delegate to active view
	_, cmd := r.active.Update(msg)
	return r, cmd
}

// buildHelpOverlay constructs the help overlay from global and view-specific bindings.
func (r *Router) buildHelpOverlay() {
	// Get global bindings
	bindings := components.GlobalBindings()

	// Add view-specific bindings if the active view implements KeyBindings
	if kb, ok := r.active.(components.KeyBindings); ok {
		bindings = append(bindings, KeyBinding{Key: "---", Description: "--- View: " + r.activeName + " ---"})
		bindings = append(bindings, kb.KeyBindings()...)
	}

	r.helpOverlay = components.NewHelpOverlay(bindings)
}

// KeyBinding represents a single keybinding for help display.
type KeyBinding = components.KeyBinding

// View delegates to the active view's View.
func (r *Router) View() string {
	if r.showHelp {
		// Render help overlay with the active view as background
		background := r.active.View()
		return r.helpOverlay.View(background)
	}
	return r.active.View()
}

// ShortHelp returns the active view's ShortHelp.
func (r *Router) ShortHelp() string {
	return r.active.ShortHelp()
}

// HelpText returns a formatted help string showing available views.
func (r *Router) HelpText() string {
	names := r.ViewNames()
	var help string
	for i, name := range names {
		if i > 0 {
			help += "  "
		}
		key := fmt.Sprintf("%d", i+1)
		if name == r.activeName {
			help += fmt.Sprintf("[%s: %s]", key, name)
		} else {
			help += fmt.Sprintf("%s: %s", key, name)
		}
	}
	return help
}

// NotifySize informs the router and active view of the window size.
func (r *Router) NotifySize(width, height int) {
	// Forward to active view if it supports sizing
	if sized, ok := r.active.(SizableView); ok {
		sized.SetSize(width, height)
	}
	// Also update help overlay size if visible
	if r.showHelp {
		r.helpOverlay.SetSize(width, height)
	}
}

// SizableView is an optional interface for views that need to know about window size.
type SizableView interface {
	SetSize(width, height int)
}
