package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sync"
)

// Action represents a keybinding action identifier
type Action string

// Keybinding actions
const (
	ActionQuit           Action = "quit"
	ActionRefresh        Action = "refresh"
	ActionToggleTheme    Action = "toggle_theme"
	ActionShowHelp       Action = "show_help"
	ActionGoBack         Action = "go_back"
	ActionSwitchView     Action = "switch_view"
	ActionNavigateUp     Action = "navigate_up"
	ActionNavigateDown   Action = "navigate_down"
	ActionNavigateLeft   Action = "navigate_left"
	ActionNavigateRight  Action = "navigate_right"
	ActionSelect         Action = "select"
	ActionCancel         Action = "cancel"
	ActionSearch         Action = "search"
	ActionNewItem        Action = "new_item"
	ActionDeleteItem     Action = "delete_item"
	ActionClearItem      Action = "clear_item"
)

// DefaultKeybinds defines the default keybindings for the application.
// These are used when no config file exists or on invalid config.
var DefaultKeybinds = map[Action][]string{
	ActionQuit:          {"q", "ctrl+c"},
	ActionRefresh:       {"r"},
	ActionToggleTheme:   {"t"},
	ActionShowHelp:      {"?"},
	ActionGoBack:        {"esc"},
	ActionSwitchView:    {"1", "2", "3", "4", "5", "6", "7", "8", "9"},
	ActionNavigateUp:    {"up", "k"},
	ActionNavigateDown:  {"down", "j"},
	ActionNavigateLeft:  {"left", "h"},
	ActionNavigateRight: {"right", "l"},
	ActionSelect:        {"enter"},
	ActionCancel:        {"esc"},
	ActionSearch:        {"/"},
	ActionNewItem:       {"n"},
	ActionDeleteItem:    {"d", "k"},
	ActionClearItem:     {"c"},
}

// KeybindsConfig is the JSON structure for the keybinds config file
type KeybindsConfig struct {
	// Global overrides global keybindings
	Global map[string][]string `json:"global,omitempty"`
	// View overrides view-specific keybindings
	// Key is view name (e.g., "AgentView"), value is map of action to keys
	View map[string]map[string][]string `json:"view,omitempty"`
}

// KeybindManager manages keybindings with config file support
type KeybindManager struct {
	mu          sync.RWMutex
	global      map[Action][]string  // resolved global keybinds
	viewKeybinds map[string]map[Action][]string  // resolved view keybinds by view name
}

// GlobalKeybinds returns the resolved global keybindings
func (km *KeybindManager) GlobalKeybinds() []KeyBinding {
	km.mu.RLock()
	defer km.mu.RUnlock()

	var bindings []KeyBinding
	for action, keys := range km.global {
		for _, key := range keys {
			bindings = append(bindings, KeyBinding{
				Key:         key,
				Description: actionDescription(action),
			})
		}
	}
	return bindings
}

// ViewKeybinds returns the resolved keybindings for a specific view
func (km *KeybindManager) ViewKeybinds(viewName string) []KeyBinding {
	km.mu.RLock()
	defer km.mu.RUnlock()

	if viewKB, ok := km.viewKeybinds[viewName]; ok {
		var bindings []KeyBinding
		for action, keys := range viewKB {
			for _, key := range keys {
				bindings = append(bindings, KeyBinding{
					Key:         key,
					Description: actionDescription(action),
				})
			}
		}
		return bindings
	}
	return nil
}

// ResolveKeybinds returns the resolved global and view keybindings as components.KeyBinding slice
// This is used by the help overlay to show actual (resolved) keybinds
func (km *KeybindManager) ResolveKeybinds(viewName string) []KeyBinding {
	bindings := km.GlobalKeybinds()
	if viewName != "" {
		if viewKB := km.ViewKeybinds(viewName); viewKB != nil {
			bindings = append(bindings, KeyBinding{Key: "---", Description: "--- View: " + viewName + " ---"})
			bindings = append(bindings, viewKB...)
		}
	}
	return bindings
}

// GetActionKey returns the primary key for an action, or empty string if not bound
func (km *KeybindManager) GetActionKey(action Action) string {
	km.mu.RLock()
	defer km.mu.RUnlock()

	if keys, ok := km.global[action]; ok && len(keys) > 0 {
		return keys[0]
	}
	return ""
}

// MatchesAction checks if a key matches the given action
func (km *KeybindManager) MatchesAction(key string, action Action) bool {
	km.mu.RLock()
	defer km.mu.RUnlock()

	if keys, ok := km.global[action]; ok {
		for _, k := range keys {
			if k == key {
				return true
			}
		}
	}
	return false
}

// MatchesViewAction checks if a key matches an action for a specific view
func (km *KeybindManager) MatchesViewAction(key string, viewName string, action Action) bool {
	km.mu.RLock()
	defer km.mu.RUnlock()

	// Check view-specific keybinds first
	if viewKB, ok := km.viewKeybinds[viewName]; ok {
		if keys, ok := viewKB[action]; ok {
			for _, k := range keys {
				if k == key {
					return true
				}
			}
		}
	}

	// Fall back to global keybinds
	if keys, ok := km.global[action]; ok {
		for _, k := range keys {
			if k == key {
				return true
			}
		}
	}
	return false
}

// actionDescription returns a human-readable description for an action
func actionDescription(action Action) string {
	descriptions := map[Action]string{
		ActionQuit:          "Quit application",
		ActionRefresh:       "Refresh repository data",
		ActionToggleTheme:   "Toggle light/dark theme",
		ActionShowHelp:      "Show this help overlay",
		ActionGoBack:        "Go back / Cancel / Close overlay",
		ActionSwitchView:    "Switch to view by number",
		ActionNavigateUp:    "Navigate up",
		ActionNavigateDown:  "Navigate down",
		ActionNavigateLeft:  "Previous tab / Navigate left",
		ActionNavigateRight: "Next tab / Navigate right",
		ActionSelect:        "Select / Confirm",
		ActionCancel:        "Cancel / Clear",
		ActionSearch:        "Search",
		ActionNewItem:       "Create new item",
		ActionDeleteItem:    "Delete selected item",
		ActionClearItem:     "Clear stopped items",
	}
	if desc, ok := descriptions[action]; ok {
		return desc
	}
	return string(action)
}

var (
	globalKeybindManager *KeybindManager
	keybindManagerOnce   sync.Once
)

// GetKeybindManager returns the global keybind manager instance
func GetKeybindManager() *KeybindManager {
	keybindManagerOnce.Do(func() {
		globalKeybindManager = NewKeybindManager()
	})
	return globalKeybindManager
}

// NewKeybindManager creates a new keybind manager with defaults and config overrides
func NewKeybindManager() *KeybindManager {
	km := &KeybindManager{
		global:       make(map[Action][]string),
		viewKeybinds: make(map[string]map[Action][]string),
	}

	// Initialize with defaults
	for action, keys := range DefaultKeybinds {
		km.global[action] = keys
	}

	// Try to load config file, ignore errors (fall back to defaults)
	configPath := GetDefaultKeybindsPath()
	if configPath != "" {
		if err := km.loadConfig(configPath); err != nil {
			// Log error but continue with defaults - invalid config falls back to defaults
			// We don't print because this runs at startup and might be annoying
			_ = err
		}
	}

	return km
}

// GetDefaultKeybindsPath returns the default keybinds config path
func GetDefaultKeybindsPath() string {
	usr, err := user.Current()
	if err != nil {
		return ""
	}

	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "git-frontend", "keybinds.json")
	}

	return filepath.Join(usr.HomeDir, ".config", "git-frontend", "keybinds.json")
}

// loadConfig loads keybindings from a JSON config file
func (km *KeybindManager) loadConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	var config KeybindsConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse config file: %w", err)
	}

	km.mu.Lock()
	defer km.mu.Unlock()

	// Apply global overrides
	if config.Global != nil {
		for actionStr, keys := range config.Global {
			action := Action(actionStr)
			if _, exists := DefaultKeybinds[action]; exists {
				km.global[action] = keys
			}
			// Silently ignore unknown actions to allow forward compatibility
		}
	}

	// Apply view-specific overrides
	if config.View != nil {
		for viewName, viewConfig := range config.View {
			if km.viewKeybinds[viewName] == nil {
				km.viewKeybinds[viewName] = make(map[Action][]string)
			}
			for actionStr, keys := range viewConfig {
				action := Action(actionStr)
				km.viewKeybinds[viewName][action] = keys
			}
		}
	}

	return nil
}

// GlobalBindings returns the global keybindings as components.KeyBinding
// This is used by the help overlay
func GlobalBindings() []KeyBinding {
	return GetKeybindManager().GlobalKeybinds()
}
