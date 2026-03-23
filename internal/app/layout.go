package app

import (
	"fmt"
	"path/filepath"

	"git-frontend/internal/theme"

	"github.com/charmbracelet/lipgloss"
	"git-frontend/internal/git"
)

// Layout manages the shared TUI layout structure.
// It composes the tab bar, active view content, and status bar.
type Layout struct {
	width  int
	height int

	// Shared layout styles
	tabBarStyle       lipgloss.Style
	statusBarStyle    lipgloss.Style
	activeTabStyle    lipgloss.Style
	inactiveTabStyle  lipgloss.Style
	dividerStyle      lipgloss.Style
	primaryTextStyle  lipgloss.Style
	mutedTextStyle    lipgloss.Style
	accent2TextStyle  lipgloss.Style
	modifiedTextStyle lipgloss.Style
}

// NewLayout creates a new layout manager.
func NewLayout() *Layout {
	l := &Layout{
		width:  80,
		height: 24,
	}
	l.rebuildStyles()
	return l
}

// rebuildStyles rebuilds all styles from the current theme.
func (l *Layout) rebuildStyles() {
	th := theme.GetTheme()
	l.tabBarStyle = lipgloss.NewStyle().
		Foreground(th.MutedText)
	l.statusBarStyle = lipgloss.NewStyle().
		Foreground(th.MutedText)
	l.activeTabStyle = lipgloss.NewStyle().
		Foreground(th.Accent).
		Bold(true)
	l.inactiveTabStyle = lipgloss.NewStyle().
		Foreground(th.MutedText)
	l.dividerStyle = lipgloss.NewStyle().
		Foreground(th.Border)
	l.primaryTextStyle = lipgloss.NewStyle().
		Foreground(th.PrimaryText)
	l.mutedTextStyle = lipgloss.NewStyle().
		Foreground(th.MutedText)
	l.accent2TextStyle = lipgloss.NewStyle().
		Foreground(th.Accent2)
	l.modifiedTextStyle = lipgloss.NewStyle().
		Foreground(th.Modified)
}

// SetSize updates the layout dimensions.
func (l *Layout) SetSize(width, height int) {
	l.width = width
	l.height = height
}

// viewCount returns a formatted view count indicator.
func (l *Layout) viewCount(names []string) string {
	return fmt.Sprintf("(%d views)", len(names))
}

// RenderTabBar renders the tab bar at the top of the screen.
func (l *Layout) RenderTabBar(router *Router) string {
	if router == nil {
		return ""
	}

	names := router.ViewNames()
	activeName := router.ActiveName()

	// Build tab bar string
	var tabBar string

	// Left side: view tabs
	for i, name := range names {
		if i > 0 {
			tabBar += l.dividerStyle.Render(" │ ")
		}

		key := router.ViewKey(name)
		if key == "" {
			key = "-"
		}
		if name == activeName {
			// Active tab
			tabBar += l.activeTabStyle.Render(fmt.Sprintf("[%s] %s", key, name))
		} else {
			// Inactive tab
			tabBar += l.inactiveTabStyle.Render(fmt.Sprintf("%s: %s", key, name))
		}
	}

	// Right side: view count
	tabBar += "  "
	tabBar += l.tabBarStyle.Render(l.viewCount(names))

	return tabBar
}

// RenderStatusBar renders the status bar at the bottom of the screen.
func (l *Layout) RenderStatusBar(repoInfo *git.RepoInfo, viewName string) string {
	// Build status bar content
	var status string

	// Left side: repo info
	if repoInfo != nil {
		repoName := filepath.Base(repoInfo.Path)
		status += l.primaryTextStyle.Render(repoName)

		// Branch info
		if repoInfo.CurrentBranch != "" {
			status += l.mutedTextStyle.Render(" · ")
			status += l.accent2TextStyle.Render(repoInfo.CurrentBranch)
		}

		// Dirty indicator
		if repoInfo.IsDirty {
			status += " "
			status += l.modifiedTextStyle.Render("●")
		}
	} else {
		status += l.mutedTextStyle.Render("No repository")
	}

	// Right side: view name
	if viewName != "" {
		// Pad to align view name to the right
		viewLabel := fmt.Sprintf("│ %s", viewName)
		plainLen := len(stripAnsi(status)) + len(stripAnsi(viewLabel))
		status += l.statusBarStyle.Width(l.width - plainLen).Render(viewLabel)
	}

	return status
}

// AvailableViewDimensions calculates the available dimensions for the active view.
// It accounts for the tab bar (1 line + 1 divider) and status bar (1 line).
func (l *Layout) AvailableViewDimensions() (width, height int) {
	// Reserve 1 line for tab bar and 1 for divider, 1 for status bar
	height = l.height - 3
	if height < 1 {
		height = 1
	}
	// Width is full terminal width
	width = l.width
	if width < 1 {
		width = 1
	}
	return width, height
}

// Render renders the complete layout with the active view content.
func (l *Layout) Render(router *Router, repoInfo *git.RepoInfo, activeViewContent string) string {
	th := theme.GetTheme()
	l.rebuildStyles()

	var output string

	// Tab bar
	output += l.RenderTabBar(router)
	output += "\n"

	// Tab bar divider
	divider := ""
	for i := 0; i < l.width; i++ {
		divider += "─"
	}
	output += th.BorderStyle.Render(divider)
	output += "\n"

	// Active view content
	output += activeViewContent
	if len(activeViewContent) > 0 && activeViewContent[len(activeViewContent)-1] != '\n' {
		output += "\n"
	}

	// Status bar divider
	output += th.BorderStyle.Render(divider)
	output += "\n"

	// Status bar
	statusBar := l.RenderStatusBar(repoInfo, "")
	if router != nil {
		statusBar = l.RenderStatusBar(repoInfo, router.ActiveName())
	}
	output += statusBar
	output += "\n"

	return output
}

// stripAnsi strips ANSI escape codes from a string.
// This is used to calculate actual display width.
func stripAnsi(s string) string {
	result := ""
	inEscape := false
	for _, c := range s {
		if c == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if c == 'm' {
				inEscape = false
			}
			continue
		}
		result += string(c)
	}
	return result
}
