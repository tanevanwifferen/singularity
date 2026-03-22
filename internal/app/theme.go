package app

import (
	"github.com/charmbracelet/lipgloss"
)

// ThemeType represents the type of theme
type ThemeType int

const (
	DarkThemeType ThemeType = iota
	LightThemeType
)

// Theme colors for git-frontend TUI
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
	Accent   lipgloss.Color
	Accent2  lipgloss.Color

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
	DashboardTitle         lipgloss.Style
	BranchStyle            lipgloss.Style
	SelectedBranchStyle    lipgloss.Style
	StatsStyle             lipgloss.Style
	CommitStyle            lipgloss.Style
	DashboardErrorStyle    lipgloss.Style
	DashboardAccentStyle   lipgloss.Style

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
	Type:        DarkThemeType,
	Added:       lipgloss.Color("82"),    // Green
	Removed:     lipgloss.Color("196"),   // Red
	Modified:    lipgloss.Color("226"),   // Yellow
	Info:        lipgloss.Color("75"),    // Blue
	Warning:     lipgloss.Color("226"),   // Yellow
	Error:       lipgloss.Color("196"),   // Red
	PrimaryText:   lipgloss.Color("205"), // Magenta/pink
	SecondaryText: lipgloss.Color("86"),  // Green
	MutedText:     lipgloss.Color("241"), // Gray
	Background: lipgloss.Color("57"),     // Dark blue
	Surface:    lipgloss.Color("235"),     // Dark gray
	Accent:     lipgloss.Color("220"),     // Orange/yellow
	Accent2:    lipgloss.Color("82"),      // Green
	Border:     lipgloss.Color("240"),     // Gray

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

// lightTheme is a light theme (structure ready, not fully styled)
var lightTheme = Theme{
	Type:        LightThemeType,
	Added:       lipgloss.Color("34"),    // Green
	Removed:     lipgloss.Color("196"),   // Red
	Modified:    lipgloss.Color("220"),   // Yellow/orange
	Info:        lipgloss.Color("33"),    // Blue
	Warning:     lipgloss.Color("220"),   // Yellow/orange
	Error:       lipgloss.Color("196"),   // Red
	PrimaryText:   lipgloss.Color("129"), // Magenta
	SecondaryText: lipgloss.Color("28"),  // Green
	MutedText:     lipgloss.Color("245"), // Gray
	Background: lipgloss.Color("255"),    // White
	Surface:    lipgloss.Color("252"),      // Light gray
	Accent:     lipgloss.Color("214"),     // Orange
	Accent2:    lipgloss.Color("34"),     // Green
	Border:     lipgloss.Color("250"),     // Light gray

	Title: lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("129")).
		Background(lipgloss.Color("252")).
		Padding(0, 1),

	Version: lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")),

	Help: lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")).
		Italic(true),

	InfoStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("33")),

	WarningStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("220")),

	ErrorStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("196")),

	DashboardTitle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("129")).
			Bold(true),

	BranchStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("28")),

	SelectedBranchStyle: lipgloss.NewStyle().
				Foreground(lipgloss.Color("214")).
				Background(lipgloss.Color("252")).
				Bold(true),

	StatsStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("237")),

	CommitStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("239")),

	DashboardErrorStyle: lipgloss.NewStyle().
				Foreground(lipgloss.Color("196")),

	DashboardAccentStyle: lipgloss.NewStyle().
				Foreground(lipgloss.Color("214")),

	HeaderStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("129")).
			Bold(true).
			Padding(0, 1),

	BodyStyle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("237")),

	FooterStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Italic(true),

	BorderStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("250")),

	ModalBorderColor:   lipgloss.Color("250"),
	ModalInfoBorder:    lipgloss.Color("33"),
	ModalErrorBorder:   lipgloss.Color("196"),
	ModalSuccessBorder: lipgloss.Color("34"),
	ModalConfirmBorder: lipgloss.Color("214"),
}

// CurrentTheme holds the currently active theme
var CurrentTheme = darkTheme

// GetTheme returns the current theme
func GetTheme() Theme {
	return CurrentTheme
}

// SetTheme sets the current theme
func SetTheme(t ThemeType) {
	switch t {
	case DarkThemeType:
		CurrentTheme = darkTheme
	case LightThemeType:
		CurrentTheme = lightTheme
	}
}

// ToggleTheme toggles between dark and light themes
func ToggleTheme() {
	if CurrentTheme.Type == DarkThemeType {
		CurrentTheme = lightTheme
	} else {
		CurrentTheme = darkTheme
	}
}
