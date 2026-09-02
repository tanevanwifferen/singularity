package theme

import (
	"github.com/charmbracelet/lipgloss"
)

// ThemeType represents the type of theme
type ThemeType int

const (
	DarkThemeType ThemeType = iota
	LightThemeType
)

// Theme colors for singularity TUI
type Theme struct {
	Type ThemeType

	// Semantic git status colors
	Added    lipgloss.Color
	Removed  lipgloss.Color
	Modified lipgloss.Color

	// UI semantic colors
	Info    lipgloss.Color
	Warning lipgloss.Color
	Error   lipgloss.Color

	// Text colors
	PrimaryText   lipgloss.Color
	SecondaryText lipgloss.Color
	MutedText     lipgloss.Color

	// Background colors
	Background lipgloss.Color
	Surface    lipgloss.Color

	// Accent colors
	Accent  lipgloss.Color
	Accent2 lipgloss.Color

	// Border colors
	Border lipgloss.Color

	// Styles built from colors
	Title        lipgloss.Style
	Version      lipgloss.Style
	Help         lipgloss.Style
	InfoStyle    lipgloss.Style
	WarningStyle lipgloss.Style
	ErrorStyle   lipgloss.Style

	// Dashboard styles
	DashboardTitle       lipgloss.Style
	BranchStyle          lipgloss.Style
	SelectedBranchStyle  lipgloss.Style
	StatsStyle           lipgloss.Style
	CommitStyle          lipgloss.Style
	DashboardErrorStyle  lipgloss.Style
	DashboardAccentStyle lipgloss.Style

	// Stash styles
	StashStyle         lipgloss.Style
	SelectedStashStyle lipgloss.Style
	MutedTextStyle     lipgloss.Style

	// Layout styles
	HeaderStyle lipgloss.Style
	BodyStyle   lipgloss.Style
	FooterStyle lipgloss.Style
	BorderStyle lipgloss.Style

	// Modal styles
	ModalBorderColor   lipgloss.Color
	ModalInfoBorder    lipgloss.Color
	ModalErrorBorder   lipgloss.Color
	ModalSuccessBorder lipgloss.Color
	ModalConfirmBorder lipgloss.Color
}

// darkTheme is the default dark theme
var darkTheme = Theme{
	Type:          DarkThemeType,
	Added:         lipgloss.Color("82"),  // Green
	Removed:       lipgloss.Color("196"), // Red
	Modified:      lipgloss.Color("226"), // Yellow
	Info:          lipgloss.Color("75"),  // Blue
	Warning:       lipgloss.Color("226"), // Yellow
	Error:         lipgloss.Color("196"), // Red
	PrimaryText:   lipgloss.Color("205"), // Magenta/pink
	SecondaryText: lipgloss.Color("86"),  // Green
	MutedText:     lipgloss.Color("241"), // Gray
	Background:    lipgloss.Color("57"),  // Dark blue
	Surface:       lipgloss.Color("235"), // Dark gray
	Accent:        lipgloss.Color("220"), // Orange/yellow
	Accent2:       lipgloss.Color("82"),  // Green
	Border:        lipgloss.Color("240"), // Gray

	Title: lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("205")).
		Background(lipgloss.Color("57")).
		Padding(0, 1),

	Version: lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")),

	Help: lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		Italic(true),

	InfoStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("75")),

	WarningStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("226")),

	ErrorStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("196")),

	DashboardTitle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("205")).
		Bold(true),

	BranchStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("86")),

	SelectedBranchStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("220")).
		Background(lipgloss.Color("235")).
		Bold(true),

	StatsStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("244")),

	CommitStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")),

	DashboardErrorStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("196")),

	DashboardAccentStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("220")),

	StashStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("227")),

	SelectedStashStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("227")).
		Background(lipgloss.Color("235")).
		Bold(true),

	MutedTextStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")),

	HeaderStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("205")).
		Bold(true).
		Padding(0, 1),

	BodyStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("244")),

	FooterStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		Italic(true),

	BorderStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")),

	ModalBorderColor:   lipgloss.Color("240"),
	ModalInfoBorder:    lipgloss.Color("75"),
	ModalErrorBorder:   lipgloss.Color("196"),
	ModalSuccessBorder: lipgloss.Color("82"),
	ModalConfirmBorder: lipgloss.Color("220"),
}

// lightTheme is a light theme optimized for light backgrounds
var lightTheme = Theme{
	Type:          LightThemeType,
	Added:         lipgloss.Color("28"),  // Dark green
	Removed:       lipgloss.Color("160"), // Dark red
	Modified:      lipgloss.Color("172"), // Dark orange (readable on white)
	Info:          lipgloss.Color("25"),  // Dark blue
	Warning:       lipgloss.Color("172"), // Dark orange
	Error:         lipgloss.Color("160"), // Dark red
	PrimaryText:   lipgloss.Color("54"),  // Dark magenta
	SecondaryText: lipgloss.Color("22"),  // Dark green
	MutedText:     lipgloss.Color("240"), // Medium gray (visible on white)
	Background:    lipgloss.Color("231"), // White
	Surface:       lipgloss.Color("254"), // Very light gray
	Accent:        lipgloss.Color("166"), // Dark orange (readable on light bg)
	Accent2:       lipgloss.Color("28"),  // Dark green
	Border:        lipgloss.Color("244"), // Medium gray (visible on white)

	Title: lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("54")).
		Background(lipgloss.Color("254")).
		Padding(0, 1),

	Version: lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")),

	Help: lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Italic(true),

	InfoStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("25")),

	WarningStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("172")),

	ErrorStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("160")),

	DashboardTitle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("54")).
		Bold(true),

	BranchStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("22")),

	SelectedBranchStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("166")).
		Background(lipgloss.Color("254")).
		Bold(true),

	StatsStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("236")),

	CommitStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("238")),

	DashboardErrorStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("160")),

	DashboardAccentStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("166")),

	StashStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("130")),

	SelectedStashStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("130")).
		Background(lipgloss.Color("254")).
		Bold(true),

	MutedTextStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")),

	HeaderStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("54")).
		Bold(true).
		Padding(0, 1),

	BodyStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("236")),

	FooterStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Italic(true),

	BorderStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("244")),

	ModalBorderColor:   lipgloss.Color("244"),
	ModalInfoBorder:    lipgloss.Color("25"),
	ModalErrorBorder:   lipgloss.Color("160"),
	ModalSuccessBorder: lipgloss.Color("28"),
	ModalConfirmBorder: lipgloss.Color("166"),
}

// CurrentTheme holds the currently active theme
var CurrentTheme = darkTheme

// adaptiveMode tracks whether we're using adaptive terminal colors
var adaptiveMode = false

// GetTheme returns the current theme
func GetTheme() Theme {
	return CurrentTheme
}

// SetTheme sets the current theme to a static dark or light theme
func SetTheme(t ThemeType) {
	adaptiveMode = false
	switch t {
	case DarkThemeType:
		CurrentTheme = darkTheme
	case LightThemeType:
		CurrentTheme = lightTheme
	}
}

// ToggleTheme toggles between dark and light themes.
// In adaptive mode, it rebuilds the adaptive theme for the opposite type.
// In static mode, it switches between the hardcoded themes.
func ToggleTheme() {
	if adaptiveMode {
		if CurrentTheme.Type == DarkThemeType {
			CurrentTheme = BuildAdaptiveTheme(LightThemeType)
		} else {
			CurrentTheme = BuildAdaptiveTheme(DarkThemeType)
		}
	} else {
		if CurrentTheme.Type == DarkThemeType {
			CurrentTheme = lightTheme
		} else {
			CurrentTheme = darkTheme
		}
	}
}

// IsAdaptiveMode returns true if using adaptive terminal colors
func IsAdaptiveMode() bool {
	return adaptiveMode
}

// SetAdaptiveMode enables or disables adaptive terminal color mode
func SetAdaptiveMode(enabled bool) {
	adaptiveMode = enabled
	if enabled {
		UseAdaptiveTheme()
	}
}
