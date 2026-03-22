package app

import (
	"fmt"

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
		if keyMsg.Type == tea.KeyRunes {
			switch keyMsg.String() {
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
	}

	// Delegate to active view
	_, cmd := r.active.Update(msg)
	return r, cmd
}

// View delegates to the active view's View.
func (r *Router) View() string {
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
}

// SizableView is an optional interface for views that need to know about window size.
type SizableView interface {
	SetSize(width, height int)
}
