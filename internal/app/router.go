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

// InputCapturer is an optional interface views can implement to signal
// that they are in an input mode (e.g., text input, confirmation dialog)
// and global/navigation keybindings should not intercept keystrokes.
type InputCapturer interface {
	CapturesInput() bool
}

// KeyCapturer is an optional interface views can implement to claim
// specific keys that would otherwise be handled by the router.
// This is more granular than CapturesInput — it lets a view claim
// individual keys (e.g. "tab") without blocking all global navigation.
type KeyCapturer interface {
	CapturesKey(key string) bool
}


// SwitchViewMsg is a message to switch to a different view.
type SwitchViewMsg struct {
	ViewName string
}

// Router manages multiple views and handles switching between them.
type Router struct {
	views      map[string]View
	viewOrder  []string // deterministic ordering of view names
	viewKeys   map[string]string // view name → shortcut key (e.g. "Overview" → "o")
	keyToView  map[string]string // shortcut key → view name (e.g. "o" → "Overview")
	activeName string
	active     View

	// Submenu groups: key press (e.g. "g") → submenu overlay
	submenus    map[string]*components.Submenu
	submenuKeys []string // ordered list of submenu trigger keys
	// Set of view names that live inside a submenu (not shown in main tab bar)
	submenuViews map[string]bool

	// Cached available dimensions for views
	viewWidth  int
	viewHeight int

	// Help overlay state
	showHelp   bool
	helpOverlay components.HelpOverlay

	// Submenu overlay state
	showSubmenu    bool
	activeSubmenu  *components.Submenu
}

// NewRouter creates a new router with the given initial view.
func NewRouter(initial View, name string) *Router {
	views := make(map[string]View)
	views[name] = initial
	return &Router{
		views:        views,
		viewOrder:    []string{name},
		viewKeys:     make(map[string]string),
		keyToView:    make(map[string]string),
		submenus:     make(map[string]*components.Submenu),
		submenuViews: make(map[string]bool),
		activeName:   name,
		active:       initial,
	}
}

// Register adds a view to the router under the given name with an optional shortcut key.
func (r *Router) Register(name string, view View, keys ...string) {
	if _, exists := r.views[name]; !exists {
		r.viewOrder = append(r.viewOrder, name)
	}
	r.views[name] = view
	// Assign shortcut key if provided (e.g. "f1", "f2", etc.)
	if len(keys) > 0 && keys[0] != "" {
		r.viewKeys[name] = keys[0]
		r.keyToView[keys[0]] = name
	}
	// Apply current dimensions to newly registered views
	if r.viewWidth > 0 && r.viewHeight > 0 {
		if sized, ok := view.(SizableView); ok {
			sized.SetSize(r.viewWidth, r.viewHeight)
		}
	}
}

// RegisterSubmenu registers a submenu triggered by the given key (e.g. "g").
// Items reference views that must already be registered via Register.
// Those views are removed from the main tab bar.
func (r *Router) RegisterSubmenu(triggerKey string, title string, items []components.SubmenuItem) {
	sm := components.NewSubmenu(title, items)
	r.submenus[triggerKey] = &sm
	r.submenuKeys = append(r.submenuKeys, triggerKey)
	for _, item := range items {
		r.submenuViews[item.ViewName] = true
	}
}

// TopLevelViewNames returns view names that are NOT inside a submenu.
func (r *Router) TopLevelViewNames() []string {
	var names []string
	for _, name := range r.viewOrder {
		if !r.submenuViews[name] {
			names = append(names, name)
		}
	}
	return names
}

// IsSubmenuView returns true if the named view belongs to a submenu.
func (r *Router) IsSubmenuView(name string) bool {
	return r.submenuViews[name]
}

// SubmenuKeys returns the ordered list of submenu trigger keys.
func (r *Router) SubmenuKeys() []string {
	return r.submenuKeys
}

// SubmenuTitle returns the title of the submenu triggered by the given key.
func (r *Router) SubmenuTitle(triggerKey string) string {
	if sm, ok := r.submenus[triggerKey]; ok {
		return sm.Title
	}
	return ""
}

// ViewKey returns the shortcut key for a view, or "" if none assigned.
func (r *Router) ViewKey(name string) string {
	return r.viewKeys[name]
}

// GetView returns a view by name, or nil if not found.
func (r *Router) GetView(name string) View {
	return r.views[name]
}

// ActiveView returns the currently active view.
func (r *Router) ActiveView() View {
	return r.active
}

// ActiveViewCapturesInput returns true if the active view is in an input mode
// that should prevent global and navigation keybindings from intercepting keys.
func (r *Router) ActiveViewCapturesInput() bool {
	if ic, ok := r.active.(InputCapturer); ok {
		return ic.CapturesInput()
	}
	return false
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
	// Ensure the view has current dimensions
	if r.viewWidth > 0 && r.viewHeight > 0 {
		if sized, ok := view.(SizableView); ok {
			sized.SetSize(r.viewWidth, r.viewHeight)
		}
	}
	return nil
}

// ViewNames returns a list of registered view names in registration order.
func (r *Router) ViewNames() []string {
	return r.viewOrder
}

// Init delegates to the active view's Init.
func (r *Router) Init() tea.Cmd {
	return r.active.Init()
}

// Update handles messages and delegates to the active view.
// It handles view switching messages.
func (r *Router) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle submenu overlay first if visible
	if r.showSubmenu && r.activeSubmenu != nil {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			key := msg.String()
			if key == "esc" {
				r.showSubmenu = false
				r.activeSubmenu = nil
				return r, nil
			}
			// Check if key matches a submenu item
			if viewName := r.activeSubmenu.Match(key); viewName != "" {
				r.showSubmenu = false
				r.activeSubmenu = nil
				if err := r.SwitchTo(viewName); err != nil {
					return r, nil
				}
				return r, r.active.Init()
			}
			// Any other key closes the submenu
			r.showSubmenu = false
			r.activeSubmenu = nil
			return r, nil
		case tea.WindowSizeMsg:
			r.activeSubmenu.SetSize(msg.Width, msg.Height)
			return r, nil
		}
	}

	// Handle help overlay if visible
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

	// Handle key-based navigation (skip when active view is capturing input)
	if keyMsg, ok := msg.(tea.KeyMsg); ok && !r.ActiveViewCapturesInput() {
		key := keyMsg.String()

		// Check submenu trigger keys first (e.g. "g" for Git Operations)
		// These take priority over view key claims.
		if sm, ok := r.submenus[key]; ok {
			r.showSubmenu = true
			r.activeSubmenu = sm
			r.activeSubmenu.SetSize(r.viewWidth, r.viewHeight)
			return r, nil
		}

		// Let views claim specific keys before router handles them.
		// If a view implements KeyCapturer and claims this key, delegate directly.
		// If a view does NOT implement KeyCapturer, assume it uses all plain
		// single-letter keys (safe default) — only F-keys and special keys bypass this.
		if kc, ok := r.active.(KeyCapturer); ok {
			if kc.CapturesKey(key) {
				_, cmd := r.active.Update(msg)
				return r, cmd
			}
		} else if len(key) == 1 && key >= "a" && key <= "z" {
			// View doesn't declare its keys — don't intercept plain letters
			_, cmd := r.active.Update(msg)
			return r, cmd
		}

		switch key {
		case "?":
			// Show help overlay with combined global and view-specific bindings
			r.showHelp = true
			r.buildHelpOverlay()
			return r, nil
		case "tab":
			// Cycle to next top-level view
			names := r.TopLevelViewNames()
			for i, name := range names {
				if name == r.activeName {
					next := (i + 1) % len(names)
					if err := r.SwitchTo(names[next]); err != nil {
						return r, nil
					}
					return r, r.active.Init()
				}
			}
			// If active view is in a submenu, jump to first top-level view
			if r.IsSubmenuView(r.activeName) && len(names) > 0 {
				if err := r.SwitchTo(names[0]); err != nil {
					return r, nil
				}
				return r, r.active.Init()
			}
		case "shift+tab":
			// Cycle to previous top-level view
			names := r.TopLevelViewNames()
			for i, name := range names {
				if name == r.activeName {
					prev := (i - 1 + len(names)) % len(names)
					if err := r.SwitchTo(names[prev]); err != nil {
						return r, nil
					}
					return r, r.active.Init()
				}
			}
			// If active view is in a submenu, jump to last top-level view
			if r.IsSubmenuView(r.activeName) && len(names) > 0 {
				if err := r.SwitchTo(names[len(names)-1]); err != nil {
					return r, nil
				}
				return r, r.active.Init()
			}
		default:
			// View switching via F-key (when view allows it)
			if viewName, ok := r.keyToView[key]; ok {
				if err := r.SwitchTo(viewName); err != nil {
					return r, nil
				}
				return r, r.active.Init()
			}
		}
	}

	// Handle mouse clicks on tab bar
	if mouseMsg, ok := msg.(tea.MouseMsg); ok {
		if mouseMsg.Type == tea.MouseLeft && mouseMsg.Y == 0 {
			if name := r.tabAtX(mouseMsg.X); name != "" {
				if err := r.SwitchTo(name); err != nil {
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
// Uses the KeybindManager to provide resolved keybindings (with config overrides).
func (r *Router) buildHelpOverlay() {
	// Get global bindings from KeybindManager (with config overrides)
	bindings := GetKeybindManager().GlobalKeybinds()

	// Try to get view-specific bindings from KeybindManager first
	// If not configured, fall back to the view's KeyBindings() method
	viewKB := GetKeybindManager().ViewKeybinds(r.activeName)
	if viewKB != nil && len(viewKB) > 0 {
		bindings = append(bindings, KeyBinding{Key: "---", Description: "--- View: " + r.activeName + " ---"})
		bindings = append(bindings, viewKB...)
	} else if kb, ok := r.active.(components.KeyBindings); ok {
		// Fall back to view's hardcoded bindings for backward compatibility
		bindings = append(bindings, KeyBinding{Key: "---", Description: "--- View: " + r.activeName + " ---"})
		bindings = append(bindings, kb.KeyBindings()...)
	}

	r.helpOverlay = components.NewHelpOverlay(bindings)
}

// KeyBinding represents a single keybinding for help display.
type KeyBinding = components.KeyBinding

// View delegates to the active view's View.
func (r *Router) View() string {
	if r.showSubmenu && r.activeSubmenu != nil {
		background := r.active.View()
		return r.activeSubmenu.View(background)
	}
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
		key := r.viewKeys[name]
		if key == "" {
			key = "-"
		}
		if name == r.activeName {
			help += fmt.Sprintf("[%s: %s]", key, name)
		} else {
			help += fmt.Sprintf("%s: %s", key, name)
		}
	}
	return help
}

// tabAtX returns the view name at the given x position in the tab bar, or "" if none.
func (r *Router) tabAtX(x int) string {
	names := r.ViewNames()
	pos := 0
	for i, name := range names {
		if i > 0 {
			pos += 3 // separator " │ "
		}
		key := r.viewKeys[name]
		if key == "" {
			key = "-"
		}
		var tabWidth int
		if name == r.activeName {
			// Active: "[K] Name"
			tabWidth = 3 + len(key) + len(name)
		} else {
			// Inactive: "K: Name"
			tabWidth = 2 + len(key) + len(name)
		}
		if x >= pos && x < pos+tabWidth {
			return name
		}
		pos += tabWidth
	}
	return ""
}

// NotifySize informs the router and all views of the available view dimensions.
// width and height should be the available dimensions for views (after chrome).
func (r *Router) NotifySize(width, height int) {
	r.viewWidth = width
	r.viewHeight = height

	// Forward to ALL views so they stay in sync when switched to
	for _, view := range r.views {
		if sized, ok := view.(SizableView); ok {
			sized.SetSize(width, height)
		}
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
