package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// --- KeybindManager Tests ---

func TestNewKeybindManagerDefaults(t *testing.T) {
	km := NewKeybindManager()

	// Check that defaults are loaded
	if len(km.global) == 0 {
		t.Error("Expected global keybinds to be loaded")
	}

	// Check specific defaults
	if keys := km.global[ActionQuit]; len(keys) == 0 {
		t.Error("Expected ActionQuit to have default bindings")
	}

	if keys := km.global[ActionShowHelp]; len(keys) == 0 {
		t.Error("Expected ActionShowHelp to have default bindings")
	}
}

func TestKeybindManagerGlobalKeybinds(t *testing.T) {
	km := NewKeybindManager()

	bindings := km.GlobalKeybinds()
	if len(bindings) == 0 {
		t.Error("Expected GlobalKeybinds to return bindings")
	}

	// Find quit binding
	found := false
	for _, b := range bindings {
		if b.Key == "q" && b.Description == "Quit application" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected to find 'q' -> 'Quit application' binding")
	}
}

func TestKeybindManagerGetActionKey(t *testing.T) {
	km := NewKeybindManager()

	// Test ActionQuit returns "q" (first key)
	if key := km.GetActionKey(ActionQuit); key != "q" {
		t.Errorf("Expected 'q' for ActionQuit, got %q", key)
	}

	// Test unknown action returns empty string
	if key := km.GetActionKey(Action("unknown")); key != "" {
		t.Errorf("Expected empty string for unknown action, got %q", key)
	}
}

func TestKeybindManagerMatchesAction(t *testing.T) {
	km := NewKeybindManager()

	if !km.MatchesAction("q", ActionQuit) {
		t.Error("Expected 'q' to match ActionQuit")
	}

	if !km.MatchesAction("ctrl+c", ActionQuit) {
		t.Error("Expected 'ctrl+c' to match ActionQuit")
	}

	if km.MatchesAction("r", ActionQuit) {
		t.Error("Expected 'r' to NOT match ActionQuit")
	}
}

func TestKeybindManagerViewKeybinds(t *testing.T) {
	km := NewKeybindManager()

	// Test non-existent view returns nil
	if bindings := km.ViewKeybinds("NonExistentView"); bindings != nil {
		t.Error("Expected nil for non-existent view")
	}
}

func TestKeybindManagerResolveKeybinds(t *testing.T) {
	km := NewKeybindManager()

	// Global bindings should be present
	bindings := km.ResolveKeybinds("")
	if len(bindings) == 0 {
		t.Error("Expected ResolveKeybinds to return bindings")
	}
}

func TestKeybindManagerLoadConfig(t *testing.T) {
	// Create temp directory for config
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "keybinds.json")

	// Create a valid config
	config := `{
		"global": {
			"quit": ["Q", "ctrl+shift+c"],
			"refresh": ["R"]
		},
		"view": {
			"AgentView": {
				"navigate_up": ["w"],
				"navigate_down": ["s"]
			}
		}
	}`

	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	km := NewKeybindManager()
	if err := km.loadConfig(configPath); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Check global override was applied
	if key := km.GetActionKey(ActionQuit); key != "Q" {
		t.Errorf("Expected 'Q' for ActionQuit after config load, got %q", key)
	}

	// Check view-specific keybinds were loaded
	bindings := km.ViewKeybinds("AgentView")
	if bindings == nil {
		t.Fatal("Expected AgentView keybinds to be loaded")
	}

	// Find w binding
	found := false
	for _, b := range bindings {
		if b.Key == "w" && b.Description == "Navigate up" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected to find 'w' -> 'Navigate up' binding in AgentView")
	}
}

func TestKeybindManagerLoadConfigUnknownAction(t *testing.T) {
	// Create temp directory for config
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "keybinds.json")

	// Config with unknown action - should be silently ignored
	config := `{
		"global": {
			"unknown_action": ["x"],
			"quit": ["Q"]
		}
	}`

	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	km := NewKeybindManager()
	if err := km.loadConfig(configPath); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Unknown action should be ignored, quit should still be updated
	if key := km.GetActionKey(ActionQuit); key != "Q" {
		t.Errorf("Expected 'Q' for ActionQuit after config load, got %q", key)
	}
}

func TestKeybindManagerLoadConfigInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "keybinds.json")

	// Invalid JSON
	if err := os.WriteFile(configPath, []byte("not valid json"), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	km := NewKeybindManager()
	err := km.loadConfig(configPath)
	if err == nil {
		t.Error("Expected error loading invalid JSON config")
	}
}

func TestKeybindManagerLoadConfigMissingFile(t *testing.T) {
	km := NewKeybindManager()
	err := km.loadConfig("/nonexistent/path/keybinds.json")
	if err == nil {
		t.Error("Expected error loading missing file")
	}
}

func TestKeybindManagerLoadConfigPartial(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "keybinds.json")

	// Partial config - only some actions
	config := `{
		"global": {
			"quit": ["Q"]
		}
	}`

	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	km := NewKeybindManager()
	if err := km.loadConfig(configPath); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Quit should be updated
	if key := km.GetActionKey(ActionQuit); key != "Q" {
		t.Errorf("Expected 'Q' for ActionQuit after partial config load, got %q", key)
	}

	// Other defaults should remain unchanged (refresh should still be 'r')
	if key := km.GetActionKey(ActionRefresh); key != "r" {
		t.Errorf("Expected 'r' for ActionRefresh (default), got %q", key)
	}
}

func TestKeybindManagerMatchesViewAction(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "keybinds.json")

	config := `{
		"view": {
			"TestView": {
				"navigate_up": ["w"]
			}
		}
	}`

	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	km := NewKeybindManager()
	if err := km.loadConfig(configPath); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Check view-specific action
	if !km.MatchesViewAction("w", "TestView", ActionNavigateUp) {
		t.Error("Expected 'w' to match ActionNavigateUp in TestView")
	}

	// Check that other keys don't match
	if km.MatchesViewAction("k", "TestView", ActionNavigateUp) {
		t.Error("Expected 'k' to NOT match ActionNavigateUp in TestView")
	}

	// Check that action falls back to global
	if !km.MatchesViewAction("q", "TestView", ActionQuit) {
		t.Error("Expected 'q' to match ActionQuit (global fallback) in TestView")
	}
}

func TestGetDefaultKeybindsPath(t *testing.T) {
	// The function should return a non-empty path
	path := GetDefaultKeybindsPath()
	if path == "" {
		t.Error("Expected non-empty keybinds path")
	}

	// Path should end with keybinds.json
	if filepath.Base(path) != "keybinds.json" {
		t.Errorf("Expected path to end with keybinds.json, got %s", path)
	}
}

func TestGetKeybindManagerSingleton(t *testing.T) {
	// GetKeybindManager should return the same instance
	km1 := GetKeybindManager()
	km2 := GetKeybindManager()

	if km1 != km2 {
		t.Error("Expected GetKeybindManager to return singleton")
	}
}

func TestActionDescription(t *testing.T) {
	// Test all known actions have descriptions
	actions := []Action{
		ActionQuit,
		ActionRefresh,
		ActionToggleTheme,
		ActionShowHelp,
		ActionGoBack,
		ActionSwitchView,
		ActionNavigateUp,
		ActionNavigateDown,
		ActionSelect,
		ActionCancel,
		ActionSearch,
		ActionNewItem,
		ActionDeleteItem,
		ActionClearItem,
	}

	for _, action := range actions {
		desc := actionDescription(action)
		if desc == "" {
			t.Errorf("Expected non-empty description for action %s", action)
		}
	}

	// Test unknown action returns the action string itself
	unknown := Action("unknown_action")
	if desc := actionDescription(unknown); desc != string(unknown) {
		t.Errorf("Expected unknown action to return itself, got %q", desc)
	}
}

func TestDefaultKeybindsCoverage(t *testing.T) {
	// Ensure all actions have at least one default binding
	requiredActions := []Action{
		ActionQuit,
		ActionRefresh,
		ActionToggleTheme,
		ActionShowHelp,
		ActionGoBack,
		ActionSwitchView,
		ActionNavigateUp,
		ActionNavigateDown,
		ActionSelect,
		ActionCancel,
		ActionSearch,
		ActionNewItem,
		ActionDeleteItem,
		ActionClearItem,
	}

	for _, action := range requiredActions {
		if keys, ok := DefaultKeybinds[action]; !ok || len(keys) == 0 {
			t.Errorf("Action %s has no default bindings", action)
		}
	}
}

func TestKeybindsConfigStructure(t *testing.T) {
	// Test that KeybindsConfig marshals/unmarshals correctly
	config := KeybindsConfig{
		Global: map[string][]string{
			"quit": {"Q", "ctrl+c"},
		},
		View: map[string]map[string][]string{
			"AgentView": {
				"navigate_up": {"w"},
			},
		},
	}

	// Marshal to JSON
	data, err := config.MarshalJSON()
	if err != nil {
		t.Fatalf("Failed to marshal KeybindsConfig: %v", err)
	}

	// Unmarshal back
	var parsed KeybindsConfig
	if err := parsed.UnmarshalJSON(data); err != nil {
		t.Fatalf("Failed to unmarshal KeybindsConfig: %v", err)
	}

	if len(parsed.Global) != 1 {
		t.Errorf("Expected 1 global entry, got %d", len(parsed.Global))
	}

	if len(parsed.View) != 1 {
		t.Errorf("Expected 1 view entry, got %d", len(parsed.View))
	}
}

// MarshalJSON implements json.Marshaler for KeybindsConfig
func (kc KeybindsConfig) MarshalJSON() ([]byte, error) {
	type alias KeybindsConfig
	return json.Marshal(alias(kc))
}

// UnmarshalJSON implements json.Unmarshaler for KeybindsConfig
func (kc *KeybindsConfig) UnmarshalJSON(data []byte) error {
	type alias KeybindsConfig
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*kc = KeybindsConfig(a)
	return nil
}
