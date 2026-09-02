package theme

import (
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// DetectTerminalTheme queries the terminal for its background color and
// returns whether the terminal appears to be using a dark or light theme.
// Falls back to dark theme if detection fails.
func DetectTerminalTheme() ThemeType {
	// Try to detect from terminal background color
	output := termenv.NewOutput(os.Stdout)
	bg := output.BackgroundColor()

	if bg == nil {
		// Detection failed, check COLORFGBG env var as fallback
		return detectFromColorfgbg()
	}

	// Convert to RGB using termenv's helper
	rgb := termenv.ConvertToRGB(bg)

	// Calculate relative luminance using sRGB formula
	// colorful.Color has R,G,B in 0-1 range
	luminance := 0.299*rgb.R + 0.587*rgb.G + 0.114*rgb.B

	if luminance > 0.5 {
		return LightThemeType
	}
	return DarkThemeType
}

// detectFromColorfgbg tries to detect theme from COLORFGBG environment variable
// Format is typically "fg;bg" where values are color indices (0=black, 15=white)
func detectFromColorfgbg() ThemeType {
	colorfgbg := os.Getenv("COLORFGBG")
	if colorfgbg == "" {
		return DarkThemeType // default to dark
	}

	parts := strings.Split(colorfgbg, ";")
	if len(parts) < 2 {
		return DarkThemeType
	}

	// Parse background color index
	bgIdx, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return DarkThemeType
	}

	// Standard ANSI: 0=black, 7=white, 15=bright white
	// Light backgrounds typically use 7, 15, or values > 7
	if bgIdx == 7 || bgIdx == 15 || (bgIdx > 7 && bgIdx < 16) {
		return LightThemeType
	}

	return DarkThemeType
}

// InitFromTerminal initializes the theme based on terminal color detection.
// Returns true if detection was successful.
func InitFromTerminal() bool {
	themeType := DetectTerminalTheme()
	SetTheme(themeType)
	return true
}

// BuildAdaptiveTheme creates a theme that uses ANSI color references where possible,
// letting the terminal's color scheme determine the actual colors displayed.
// This makes the app automatically adapt to any terminal theme.
func BuildAdaptiveTheme(themeType ThemeType) Theme {
	// Use ANSI color codes that terminals map to their palette
	// These adapt automatically when the terminal theme changes
	var (
		// Standard ANSI colors (0-7) and bright variants (8-15)
		// These are remapped by the terminal's color scheme
		ansiRed     = lipgloss.Color("1") // ANSI red
		ansiGreen   = lipgloss.Color("2") // ANSI green
		ansiYellow  = lipgloss.Color("3") // ANSI yellow
		ansiBlue    = lipgloss.Color("4") // ANSI blue
		ansiMagenta = lipgloss.Color("5") // ANSI magenta
		ansiCyan    = lipgloss.Color("6") // ANSI cyan

		ansiBrightRed    = lipgloss.Color("9")  // ANSI bright red
		ansiBrightGreen  = lipgloss.Color("10") // ANSI bright green
		ansiBrightYellow = lipgloss.Color("11") // ANSI bright yellow
		ansiBrightBlue   = lipgloss.Color("12") // ANSI bright blue
		ansiBrightCyan   = lipgloss.Color("14") // ANSI bright cyan

		// Grayscale from 256-color palette for UI chrome
		// These are more stable across themes
		gray1 = lipgloss.Color("240") // dark gray
		gray2 = lipgloss.Color("244") // medium gray
		gray3 = lipgloss.Color("248") // light gray
	)

	// Adjust grays based on theme type for better contrast
	if themeType == LightThemeType {
		gray1 = lipgloss.Color("246") // lighter for dark-on-light
		gray2 = lipgloss.Color("242")
		gray3 = lipgloss.Color("238")
	}

	theme := Theme{
		Type: themeType,

		// Git status - use semantic ANSI colors
		Added:    ansiGreen,
		Removed:  ansiRed,
		Modified: ansiYellow,

		// UI semantic colors
		Info:    ansiBlue,
		Warning: ansiYellow,
		Error:   ansiRed,

		// Text colors - use bright variants for better visibility
		PrimaryText:   ansiBrightCyan,
		SecondaryText: ansiCyan,
		MutedText:     gray2,

		// Background/surface - let terminal handle these
		Background: lipgloss.Color(""), // use terminal default
		Surface:    gray1,

		// Accent colors
		Accent:  ansiBrightYellow,
		Accent2: ansiBrightGreen,

		// Border
		Border: gray1,

		// Modal borders
		ModalBorderColor:   gray1,
		ModalInfoBorder:    ansiBrightBlue,
		ModalErrorBorder:   ansiBrightRed,
		ModalSuccessBorder: ansiBrightGreen,
		ModalConfirmBorder: ansiBrightYellow,
	}

	// Build styles
	theme.Title = lipgloss.NewStyle().
		Bold(true).
		Foreground(ansiMagenta)

	theme.Version = lipgloss.NewStyle().
		Foreground(gray2)

	theme.Help = lipgloss.NewStyle().
		Foreground(gray2).
		Italic(true)

	theme.InfoStyle = lipgloss.NewStyle().
		Foreground(ansiBlue)

	theme.WarningStyle = lipgloss.NewStyle().
		Foreground(ansiYellow)

	theme.ErrorStyle = lipgloss.NewStyle().
		Foreground(ansiRed)

	theme.DashboardTitle = lipgloss.NewStyle().
		Foreground(ansiMagenta).
		Bold(true)

	theme.BranchStyle = lipgloss.NewStyle().
		Foreground(ansiCyan)

	theme.SelectedBranchStyle = lipgloss.NewStyle().
		Foreground(ansiBrightYellow).
		Bold(true)

	theme.StatsStyle = lipgloss.NewStyle().
		Foreground(gray2)

	theme.CommitStyle = lipgloss.NewStyle().
		Foreground(gray1)

	theme.DashboardErrorStyle = lipgloss.NewStyle().
		Foreground(ansiRed)

	theme.DashboardAccentStyle = lipgloss.NewStyle().
		Foreground(ansiBrightYellow)

	theme.StashStyle = lipgloss.NewStyle().
		Foreground(ansiYellow)

	theme.SelectedStashStyle = lipgloss.NewStyle().
		Foreground(ansiBrightYellow).
		Bold(true)

	theme.MutedTextStyle = lipgloss.NewStyle().
		Foreground(gray2)

	theme.HeaderStyle = lipgloss.NewStyle().
		Foreground(ansiMagenta).
		Bold(true).
		Padding(0, 1)

	theme.BodyStyle = lipgloss.NewStyle().
		Foreground(gray3)

	theme.FooterStyle = lipgloss.NewStyle().
		Foreground(gray2).
		Italic(true)

	theme.BorderStyle = lipgloss.NewStyle().
		Foreground(gray1)

	return theme
}

// UseAdaptiveTheme switches to a theme that uses ANSI colors,
// automatically adapting to the terminal's color scheme.
func UseAdaptiveTheme() {
	themeType := DetectTerminalTheme()
	CurrentTheme = BuildAdaptiveTheme(themeType)
}
