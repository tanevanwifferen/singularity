package app

import (
	"fmt"
	"path/filepath"
	"strings"

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

	topLevel := router.TopLevelViewNames()
	activeName := router.ActiveName()
	activeIsSubmenu := router.IsSubmenuView(activeName)

	// Build tab bar string
	var tabBar string

	// Left side: top-level view tabs
	for i, name := range topLevel {
		if i > 0 {
			tabBar += l.dividerStyle.Render(" │ ")
		}

		key := router.ViewKey(name)
		if key == "" {
			key = "-"
		}
		displayKey := strings.ToUpper(key)
		if name == activeName {
			tabBar += l.activeTabStyle.Render(fmt.Sprintf("[%s] %s", displayKey, name))
		} else {
			tabBar += l.inactiveTabStyle.Render(fmt.Sprintf("%s: %s", displayKey, name))
		}
	}

	// Submenu indicators (e.g. "G: Git ▸")
	for _, triggerKey := range router.SubmenuKeys() {
		tabBar += l.dividerStyle.Render(" │ ")
		title := router.SubmenuTitle(triggerKey)
		displayKey := strings.ToUpper(triggerKey)
		tabBar += l.inactiveTabStyle.Render(fmt.Sprintf("%s: %s ▸", displayKey, title))
	}

	// If active view is in a submenu, show it highlighted after the submenu indicators
	if activeIsSubmenu {
		tabBar += l.dividerStyle.Render(" │ ")
		tabBar += l.activeTabStyle.Render(fmt.Sprintf("[%s]", activeName))
	}

	return tabBar
}

// RenderStatusBar renders the status bar at the bottom of the screen.
// projectName is an optional fallback displayed when repoInfo is nil (e.g. project mode).
func (l *Layout) RenderStatusBar(repoInfo *git.RepoInfo, viewName string, projectName string) string {
	// Build status bar content
	var status string

	// Left side: repo info or project name
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
	} else if projectName != "" {
		status += l.primaryTextStyle.Render(projectName)
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
// It accounts for the tab bar (1 line), tab divider (1 line), status divider (1 line), status bar (1 line).
func (l *Layout) AvailableViewDimensions() (width, height int) {
	// Reserve 4 lines: tab bar, tab divider, status divider, status bar
	height = l.height - 4
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
// projectName is displayed in the status bar when repoInfo is nil (project mode).
func (l *Layout) Render(router *Router, repoInfo *git.RepoInfo, activeViewContent string, projectName ...string) string {
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

	// Active view content — constrain to exactly the available height so the
	// status bar stays pinned to the bottom of the terminal.
	_, viewHeight := l.AvailableViewDimensions()
	viewPane := lipgloss.NewStyle().
		Width(l.width).
		Height(viewHeight).
		MaxHeight(viewHeight).
		Render(activeViewContent)
	output += viewPane
	output += "\n"

	// Status bar divider
	output += th.BorderStyle.Render(divider)
	output += "\n"

	// Status bar (no trailing newline — avoids scrolling the tab bar off screen)
	pName := ""
	if len(projectName) > 0 {
		pName = projectName[0]
	}
	statusBar := l.RenderStatusBar(repoInfo, "", pName)
	if router != nil {
		statusBar = l.RenderStatusBar(repoInfo, router.ActiveName(), pName)
	}
	output += statusBar

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
