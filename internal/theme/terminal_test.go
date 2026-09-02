package theme

import (
	"os"
	"testing"
)

func TestDetectFromColorfgbg(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		expected ThemeType
	}{
		{"empty", "", DarkThemeType},
		{"dark bg (black)", "15;0", DarkThemeType},
		{"light bg (white)", "0;15", LightThemeType},
		{"light bg (gray)", "0;7", LightThemeType},
		{"invalid", "invalid", DarkThemeType},
		{"single value", "15", DarkThemeType},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := os.Getenv("COLORFGBG")
			defer os.Setenv("COLORFGBG", old)

			if tt.env == "" {
				os.Unsetenv("COLORFGBG")
			} else {
				os.Setenv("COLORFGBG", tt.env)
			}

			got := detectFromColorfgbg()
			if got != tt.expected {
				t.Errorf("detectFromColorfgbg() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestBuildAdaptiveTheme(t *testing.T) {
	// Test that we can build themes for both types without panicking
	dark := BuildAdaptiveTheme(DarkThemeType)
	if dark.Type != DarkThemeType {
		t.Errorf("BuildAdaptiveTheme(DarkThemeType).Type = %v, want %v", dark.Type, DarkThemeType)
	}

	light := BuildAdaptiveTheme(LightThemeType)
	if light.Type != LightThemeType {
		t.Errorf("BuildAdaptiveTheme(LightThemeType).Type = %v, want %v", light.Type, LightThemeType)
	}

	// Verify key colors are set
	if dark.Added == "" {
		t.Error("dark theme Added color is empty")
	}
	if light.Added == "" {
		t.Error("light theme Added color is empty")
	}
}

func TestSetAdaptiveMode(t *testing.T) {
	// Save original state
	origTheme := CurrentTheme
	origMode := adaptiveMode
	defer func() {
		CurrentTheme = origTheme
		adaptiveMode = origMode
	}()

	SetAdaptiveMode(true)
	if !IsAdaptiveMode() {
		t.Error("SetAdaptiveMode(true) did not enable adaptive mode")
	}

	SetAdaptiveMode(false)
	if IsAdaptiveMode() {
		t.Error("SetAdaptiveMode(false) did not disable adaptive mode")
	}
}

func TestToggleThemeInAdaptiveMode(t *testing.T) {
	// Save original state
	origTheme := CurrentTheme
	origMode := adaptiveMode
	defer func() {
		CurrentTheme = origTheme
		adaptiveMode = origMode
	}()

	// Start in adaptive dark mode
	adaptiveMode = true
	CurrentTheme = BuildAdaptiveTheme(DarkThemeType)

	// Toggle should switch to light
	ToggleTheme()
	if CurrentTheme.Type != LightThemeType {
		t.Error("ToggleTheme() in adaptive mode did not switch from dark to light")
	}

	// Toggle again should switch back to dark
	ToggleTheme()
	if CurrentTheme.Type != DarkThemeType {
		t.Error("ToggleTheme() in adaptive mode did not switch from light to dark")
	}
}
