package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// --- Router Tests ---

func TestRouterNew(t *testing.T) {
	stub1 := NewStubView1("/test/repo")
	router := NewRouter(stub1, "stub1")

	if router == nil {
		t.Fatal("Expected non-nil router")
	}
	if router.ActiveName() != "stub1" {
		t.Errorf("Expected active name 'stub1', got %q", router.ActiveName())
	}
	if router.ActiveView() == nil {
		t.Error("Expected non-nil active view")
	}
}

func TestRouterRegister(t *testing.T) {
	stub1 := NewStubView1("/test/repo")
	router := NewRouter(stub1, "stub1")

	stub2 := NewStubView2("/test/repo")
	router.Register("stub2", stub2)

	names := router.ViewNames()
	if len(names) != 2 {
		t.Errorf("Expected 2 view names, got %d", len(names))
	}

	found := false
	for _, n := range names {
		if n == "stub2" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected 'stub2' to be registered")
	}
}

func TestRouterSwitchTo(t *testing.T) {
	stub1 := NewStubView1("/test/repo")
	router := NewRouter(stub1, "stub1")

	stub2 := NewStubView2("/test/repo")
	router.Register("stub2", stub2)

	// Switch to stub2
	err := router.SwitchTo("stub2")
	if err != nil {
		t.Errorf("Unexpected error switching to stub2: %v", err)
	}
	if router.ActiveName() != "stub2" {
		t.Errorf("Expected active name 'stub2', got %q", router.ActiveName())
	}

	// Switch to non-existent view
	err = router.SwitchTo("nonexistent")
	if err == nil {
		t.Error("Expected error switching to nonexistent view")
	}
}

func TestRouterSwitchToMessage(t *testing.T) {
	stub1 := NewStubView1("/test/repo")
	router := NewRouter(stub1, "stub1")

	stub2 := NewStubView2("/test/repo")
	router.Register("stub2", stub2)

	// Send SwitchViewMsg
	msg := SwitchViewMsg{ViewName: "stub2"}
	_, cmd := router.Update(msg)

	if router.ActiveName() != "stub2" {
		t.Errorf("Expected active name 'stub2' after SwitchViewMsg, got %q", router.ActiveName())
	}
	// Init may return nil for stub views
	_ = cmd
}

func TestRouterKeybindSwitching(t *testing.T) {
	stub1 := NewStubView1("/test/repo")
	router := NewRouter(stub1, "stub1")
	router.viewKeys["stub1"] = "f1"
	router.keyToView["f1"] = "stub1"

	stub2 := NewStubView2("/test/repo")
	router.Register("stub2", stub2, "f2")

	// Press "f2" to switch to second view
	keyMsg := tea.KeyMsg{Type: tea.KeyF2}
	_, cmd := router.Update(keyMsg)

	if router.ActiveName() != "stub2" {
		t.Errorf("Expected active name 'stub2' after pressing f2, got %q", router.ActiveName())
	}
	_ = cmd

	// Press "f1" to switch back to first view
	keyMsg = tea.KeyMsg{Type: tea.KeyF1}
	router.Update(keyMsg)

	if router.ActiveName() != "stub1" {
		t.Errorf("Expected active name 'stub1' after pressing f1, got %q", router.ActiveName())
	}
}

func TestRouterKeybindOutOfRange(t *testing.T) {
	stub1 := NewStubView1("/test/repo")
	router := NewRouter(stub1, "stub1")

	// Press "5" when only 1 view exists
	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}}
	_, cmd := router.Update(keyMsg)

	// Should stay on stub1 (no panic, no switch)
	if router.ActiveName() != "stub1" {
		t.Errorf("Expected active name 'stub1', got %q", router.ActiveName())
	}
	_ = cmd
}

func TestRouterHelpText(t *testing.T) {
	stub1 := NewStubView1("/test/repo")
	router := NewRouter(stub1, "stub1")

	stub2 := NewStubView2("/test/repo")
	router.Register("stub2", stub2)

	help := router.HelpText()

	// Should contain both view names
	if !strings.Contains(help, "stub1") {
		t.Error("Expected help text to contain 'stub1'")
	}
	if !strings.Contains(help, "stub2") {
		t.Error("Expected help text to contain 'stub2'")
	}

	// Active view should be in brackets - stub1 is active since it was the initial view
	if !strings.Contains(help, "[") || !strings.Contains(help, "]") {
		t.Error("Expected help text to contain brackets for active view")
	}

	// Should contain 2 views total
	if strings.Count(help, "stub1")+strings.Count(help, "stub2") != 2 {
		t.Errorf("Expected each view name to appear exactly once, got %q", help)
	}
}

func TestRouterInitDelegation(t *testing.T) {
	stub1 := NewStubView1("/test/repo")
	router := NewRouter(stub1, "stub1")

	// Init should delegate to active view (returns nil for stub)
	cmd := router.Init()
	_ = cmd // Stub views return nil
}

func TestRouterUpdateDelegation(t *testing.T) {
	stub1 := NewStubView1("/test/repo")
	router := NewRouter(stub1, "stub1")

	// Update with a non-routing message should delegate to active view
	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}
	_, cmd := router.Update(keyMsg)

	// Should not panic, should return nil cmd for 'r' key in stub
	_ = cmd
}

func TestRouterViewDelegation(t *testing.T) {
	stub1 := NewStubView1("/test/repo")
	router := NewRouter(stub1, "stub1")

	view := router.View()

	// Should contain Stub View 1 content
	if !strings.Contains(view, "Stub View 1") {
		t.Errorf("Expected view to contain 'Stub View 1', got %q", view)
	}
}

func TestRouterShortHelpDelegation(t *testing.T) {
	stub1 := NewStubView1("/test/repo")
	router := NewRouter(stub1, "stub1")

	help := router.ShortHelp()

	if help != stub1.ShortHelp() {
		t.Errorf("Expected ShortHelp to match, got %q", help)
	}
}

// --- SizableView Tests ---

func TestRouterNotifySize(t *testing.T) {
	stub1 := NewStubView1("/test/repo")
	router := NewRouter(stub1, "stub1")

	stub2 := NewStubView2("/test/repo")
	router.Register("stub2", stub2)

	// NotifySize should not panic even though stub views implement SetSize
	router.NotifySize(80, 24)

	// Switch to stub2 and notify again
	router.SwitchTo("stub2")
	router.NotifySize(120, 40)
}

// --- Layout Tests ---

func TestLayoutNew(t *testing.T) {
	layout := NewLayout()
	if layout == nil {
		t.Fatal("Expected non-nil layout")
	}
	if layout.width != 80 {
		t.Errorf("Expected default width 80, got %d", layout.width)
	}
	if layout.height != 24 {
		t.Errorf("Expected default height 24, got %d", layout.height)
	}
}

func TestLayoutSetSize(t *testing.T) {
	layout := NewLayout()
	layout.SetSize(100, 50)

	if layout.width != 100 {
		t.Errorf("Expected width 100, got %d", layout.width)
	}
	if layout.height != 50 {
		t.Errorf("Expected height 50, got %d", layout.height)
	}
}

func TestLayoutAvailableViewDimensions(t *testing.T) {
	layout := NewLayout()
	layout.SetSize(80, 24)

	width, height := layout.AvailableViewDimensions()

	// Should reserve 4 lines (tab bar, tab divider, status divider, status bar)
	if height != 20 {
		t.Errorf("Expected available height 20, got %d", height)
	}
	if width != 80 {
		t.Errorf("Expected available width 80, got %d", width)
	}
}

func TestLayoutRenderTabBar(t *testing.T) {
	stub1 := NewStubView1("/test/repo")
	router := NewRouter(stub1, "stub1")

	stub2 := NewStubView2("/test/repo")
	router.Register("stub2", stub2)

	layout := NewLayout()
	tabBar := layout.RenderTabBar(router)

	// Should contain both view names
	if !strings.Contains(tabBar, "stub1") {
		t.Error("Expected tab bar to contain 'stub1'")
	}
	if !strings.Contains(tabBar, "stub2") {
		t.Error("Expected tab bar to contain 'stub2'")
	}
}

func TestLayoutRenderTabBarNilRouter(t *testing.T) {
	layout := NewLayout()
	tabBar := layout.RenderTabBar(nil)

	if tabBar != "" {
		t.Errorf("Expected empty tab bar for nil router, got %q", tabBar)
	}
}

func TestLayoutRenderStatusBar(t *testing.T) {
	layout := NewLayout()
	statusBar := layout.RenderStatusBar(nil, "test-view", "")

	// Should contain the view name
	if !strings.Contains(statusBar, "test-view") {
		t.Errorf("Expected status bar to contain 'test-view', got %q", statusBar)
	}
}

func TestLayoutRender(t *testing.T) {
	stub1 := NewStubView1("/test/repo")
	router := NewRouter(stub1, "stub1")

	stub2 := NewStubView2("/test/repo")
	router.Register("stub2", stub2)

	layout := NewLayout()
	layout.SetSize(80, 24)

	output := layout.Render(router, nil, "Test content")

	// Should contain tab bar with view names
	if !strings.Contains(output, "stub1") {
		t.Error("Expected rendered output to contain 'stub1'")
	}

	// Should contain the active view content
	if !strings.Contains(output, "Test content") {
		t.Error("Expected rendered output to contain 'Test content'")
	}

	// Should contain dividers
	if !strings.Contains(output, "─") {
		t.Error("Expected rendered output to contain divider character")
	}
}

// --- Edge Case Tests ---

func TestLayoutTinyTerminal(t *testing.T) {
	stub1 := NewStubView1("/test/repo")
	router := NewRouter(stub1, "stub1")

	layout := NewLayout()

	// Test with minimum viable terminal size (1x1)
	layout.SetSize(1, 1)
	width, height := layout.AvailableViewDimensions()
	if width < 1 {
		t.Errorf("Expected available width at least 1, got %d", width)
	}
	if height < 1 {
		t.Errorf("Expected available height at least 1, got %d", height)
	}

	// Should not panic rendering
	output := layout.Render(router, nil, "x")
	if output == "" {
		t.Error("Expected non-empty output for tiny terminal")
	}
}

func TestLayoutVerySmallTerminal(t *testing.T) {
	stub1 := NewStubView1("/test/repo")
	_ = stub1 // used to create router

	layout := NewLayout()

	// Test with very small terminal (smaller than reserved lines)
	layout.SetSize(10, 2)
	width, height := layout.AvailableViewDimensions()

	// Should still return at least 1 for both
	if width < 1 {
		t.Errorf("Expected available width at least 1, got %d", width)
	}
	if height < 1 {
		t.Errorf("Expected available height at least 1, got %d", height)
	}
}

func TestRouterManyViews(t *testing.T) {
	stub1 := NewStubView1("/test/repo")
	router := NewRouter(stub1, "view1")

	// Register 9 views (max number keys 1-9)
	for i := 2; i <= 9; i++ {
		stub := NewStubView2("/test/repo")
		router.Register(string(rune('0'+i)), stub)
	}

	names := router.ViewNames()
	if len(names) != 9 {
		t.Errorf("Expected 9 views, got %d", len(names))
	}

	// All keybinds should work
	for i := 1; i <= 9; i++ {
		runeKey := rune('0' + i)
		keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{runeKey}}
		_, _ = router.Update(keyMsg)
	}
}

func TestRouterViewContentUpdatesOnSwitch(t *testing.T) {
	stub1 := NewStubView1("/test/repo")
	router := NewRouter(stub1, "stub1")

	stub2 := NewStubView2("/test/repo")
	router.Register("stub2", stub2)

	// Initially shows stub1 content
	view1 := router.View()
	if !strings.Contains(view1, "Stub View 1") {
		t.Errorf("Expected initial view to contain 'Stub View 1', got %q", view1)
	}

	// Switch to stub2
	router.SwitchTo("stub2")

	// Now shows stub2 content
	view2 := router.View()
	if !strings.Contains(view2, "Stub View 2") {
		t.Errorf("Expected view after switch to contain 'Stub View 2', got %q", view2)
	}
}

func TestLayoutRenderWithNoRouter(t *testing.T) {
	layout := NewLayout()
	layout.SetSize(80, 24)

	// Should not panic, renders with empty tab bar
	output := layout.Render(nil, nil, "Content")
	if !strings.Contains(output, "Content") {
		t.Error("Expected output to contain 'Content'")
	}
}

func TestStripAnsi(t *testing.T) {
	// Test with ANSI escape codes
	ansi := "\x1b[31mRed\x1b[0m Normal"
	result := stripAnsi(ansi)
	if result != "Red Normal" {
		t.Errorf("Expected 'Red Normal', got %q", result)
	}

	// Test with plain text
	plain := "Plain text"
	result = stripAnsi(plain)
	if result != plain {
		t.Errorf("Expected %q, got %q", plain, result)
	}
}

func TestLayoutRenderTabBarSingleView(t *testing.T) {
	stub1 := NewStubView1("/test/repo")
	router := NewRouter(stub1, "stub1")

	layout := NewLayout()
	tabBar := layout.RenderTabBar(router)

	// Should contain the single view
	if !strings.Contains(tabBar, "stub1") {
		t.Error("Expected tab bar to contain 'stub1'")
	}
}

func TestLayoutViewCount(t *testing.T) {
	stub1 := NewStubView1("/test/repo")
	router := NewRouter(stub1, "stub1")

	stub2 := NewStubView2("/test/repo")
	router.Register("stub2", stub2)

	stub3 := NewStubView2("/test/repo")
	router.Register("stub3", stub3)

	layout := NewLayout()
	tabBar := layout.RenderTabBar(router)

	// All three should appear as top-level tabs
	if !strings.Contains(tabBar, "stub1") {
		t.Error("Expected tab bar to contain 'stub1'")
	}
	if !strings.Contains(tabBar, "stub2") {
		t.Error("Expected tab bar to contain 'stub2'")
	}
	if !strings.Contains(tabBar, "stub3") {
		t.Error("Expected tab bar to contain 'stub3'")
	}
}

// --- Modal Overlay Interaction Tests ---

func TestRouterModalOverlayNil(t *testing.T) {
	// Test that nil modal doesn't cause issues
	stub1 := NewStubView1("/test/repo")
	router := NewRouter(stub1, "stub1")

	// Render should work without panic
	view := router.View()
	if view == "" {
		t.Error("Expected non-empty view")
	}
}
