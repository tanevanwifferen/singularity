package views

import (
	"fmt"
	"strconv"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// DiffViewMode represents the type of diff being viewed
type DiffViewMode int

const (
	DiffModeBranch DiffViewMode = iota
	DiffModeWorkdir
)

// DiffLine represents a single line in a parsed diff
type DiffLine struct {
	Content       string
	LineType      string // "+", "-", " ", "@" (hunk header), "" (header)
	OldLineNum    int    // Line number in old file (0 if not applicable)
	NewLineNum    int    // Line number in new file (0 if not applicable)
	AlreadyInBase bool   // true if this line belongs to a hunk already in the base branch
}

// DiffView provides a split-panel diff viewer interface.
// Left panel: scrollable file list with status indicators
// Right panel: file details and diff content
type DiffView struct {
	repoPath string
	repo     *git.RepoInfo
	width    int
	height   int
	loading  bool
	err      error

	// Mode: branch diff or workdir diff
	mode DiffViewMode

	// Branch comparison mode
	branchA    string
	branchB    string
	branchDiff *git.BranchDiff

	// Workdir diff mode
	workdirDiff *git.WorkdirDiff

	// File list state
	files       []git.FileChange
	selectedIdx int
	showDiff    bool // Whether to show actual diff content

	// Focus: true = file list, false = diff panel
	focusFileList bool

	// Current file diff content
	currentDiff string

	// Parsed diff lines for viewport scrolling
	parsedDiffLines  []DiffLine
	diffScrollOffset int

	// Navigation mode: when viewing diff content, j/k navigate diff lines
	diffNavMode bool
}

// NewDiffView creates a new diff view for branch comparison.
func NewDiffView(repoPath, branchA, branchB string) *DiffView {
	return &DiffView{
		repoPath:      repoPath,
		branchA:       branchA,
		branchB:       branchB,
		mode:          DiffModeBranch,
		width:         120,
		height:        30,
		selectedIdx:   0,
		focusFileList: true,
	}
}

// SetRepoPath updates the repository path for this view.
func (v *DiffView) SetRepoPath(path string) { v.repoPath = path }

// NewWorkdirDiffView creates a new diff view for staged/unstaged changes.
func NewWorkdirDiffView(repoPath string) *DiffView {
	return &DiffView{
		repoPath:      repoPath,
		mode:          DiffModeWorkdir,
		width:         120,
		height:        30,
		selectedIdx:   0,
		focusFileList: true,
	}
}

// Init initializes the view.
func (v *DiffView) Init() tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		v.loadData()
		return RefreshDoneMsg{}
	}
}

// loadData loads diff data based on mode.
func (v *DiffView) loadData() {
	v.err = nil

	// Load repo info
	repo, err := git.OpenRepo(v.repoPath)
	if err != nil {
		v.err = fmt.Errorf("failed to open repo: %w", err)
		v.loading = false
		return
	}
	v.repo = repo

	if v.mode == DiffModeBranch {
		v.loadBranchDiff()
	} else {
		v.loadWorkdirDiff()
	}

	v.loading = false
}

// loadBranchDiff loads diff data for branch comparison.
func (v *DiffView) loadBranchDiff() {
	if v.branchA == "" || v.branchB == "" {
		return
	}

	diff, err := git.GetBranchDiff(v.repoPath, v.branchA, v.branchB)
	if err != nil {
		v.err = err
		return
	}
	v.branchDiff = diff
	v.files = diff.Files

	// Keep selected index in bounds
	if v.selectedIdx >= len(v.files) {
		v.selectedIdx = len(v.files) - 1
	}
	if v.selectedIdx < 0 {
		v.selectedIdx = 0
	}
}

// loadWorkdirDiff loads diff data for working directory changes.
func (v *DiffView) loadWorkdirDiff() {
	diff, err := git.GetWorkdirStatus(v.repoPath)
	if err != nil {
		v.err = err
		return
	}
	v.workdirDiff = diff

	// Convert WorkdirStatus to FileChange for unified handling
	v.files = make([]git.FileChange, 0, len(diff.Files))
	for _, f := range diff.Files {
		fc := git.FileChange{
			NewPath:   f.Path,
			Additions: f.UnstagedAdditions + f.StagedAdditions,
			Deletions: f.UnstagedDeletions + f.StagedDeletions,
		}
		// Prefer staged status for display
		if f.StagedStatus != "" && f.StagedStatus != "?" {
			fc.Status = f.StagedStatus
		} else if f.UnstagedStatus != "" && f.UnstagedStatus != "?" {
			fc.Status = f.UnstagedStatus
		}
		v.files = append(v.files, fc)
	}

	// Keep selected index in bounds
	if v.selectedIdx >= len(v.files) {
		v.selectedIdx = len(v.files) - 1
	}
	if v.selectedIdx < 0 {
		v.selectedIdx = 0
	}
}

// Update handles update events.
func (v *DiffView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if v.diffNavMode {
				// Scroll up in diff view
				if v.diffScrollOffset > 0 {
					v.diffScrollOffset--
				}
			} else if v.selectedIdx > 0 {
				v.selectedIdx--
				v.diffScrollOffset = 0
				v.loadFileDiff()
				v.showDiff = true
			}
		case "down", "j":
			if v.diffNavMode {
				// Scroll down in diff view
				maxScroll := len(v.parsedDiffLines) - (v.height - 20)
				if maxScroll < 0 {
					maxScroll = 0
				}
				if v.diffScrollOffset < maxScroll {
					v.diffScrollOffset++
				}
			} else if v.selectedIdx < len(v.files)-1 {
				v.selectedIdx++
				v.diffScrollOffset = 0
				v.loadFileDiff()
				v.showDiff = true
			}
		case "g":
			// g = go to top
			if v.showDiff && v.diffNavMode {
				v.diffScrollOffset = 0
			}
		case "G":
			// G = go to bottom
			if v.showDiff && v.diffNavMode {
				maxScroll := len(v.parsedDiffLines) - (v.height - 20)
				if maxScroll < 0 {
					maxScroll = 0
				}
				v.diffScrollOffset = maxScroll
			}
		case "enter":
			// Toggle diff scroll/nav mode
			if v.selectedIdx < len(v.files) && v.showDiff {
				v.diffNavMode = !v.diffNavMode
			}
		case "tab":
			// Switch focus between file list and diff
			v.focusFileList = !v.focusFileList
			if v.showDiff {
				v.diffNavMode = !v.focusFileList
			}
		case "shift+tab":
			// Also switch focus (reverse)
			v.focusFileList = !v.focusFileList
			if v.showDiff {
				v.diffNavMode = !v.focusFileList
			}
		case "r":
			// Refresh
			v.loading = true
			return v, func() tea.Msg {
				v.loadData()
				return RefreshDoneMsg{}
			}
		case "esc":
			// Exit diff nav mode and return focus to file list
			if v.diffNavMode {
				v.diffNavMode = false
				v.focusFileList = true
			}
		case "1":
			// Switch to staged changes mode (workdir only)
			if v.mode == DiffModeWorkdir {
				// This would filter to show only staged
			}
		case "2":
			// Switch to unstaged changes mode (workdir only)
			if v.mode == DiffModeWorkdir {
				// This would filter to show only unstaged
			}
		}

	case RefreshDoneMsg:
		v.loading = false
		if len(v.files) > 0 {
			v.loadFileDiff()
			v.showDiff = true
		}
	}

	return v, nil
}

// loadFileDiff loads the actual diff content for the selected file.
func (v *DiffView) loadFileDiff() {
	if v.selectedIdx >= len(v.files) {
		return
	}

	file := v.files[v.selectedIdx]

	if v.mode == DiffModeBranch {
		diff, err := git.GetFileDiff(v.repoPath, v.branchA, v.branchB, file.NewPath)
		if err != nil {
			v.err = err
			return
		}
		v.currentDiff = diff
		v.parsedDiffLines = ParseDiffLines(diff)
	} else {
		// Workdir mode - check if file is staged or unstaged
		var diff string
		var err error

		// Try to get both staged and unstaged diffs
		stagedDiff, _ := git.GetStagedFileDiff(v.repoPath, file.NewPath)
		unstagedDiff, _ := git.GetUnstagedFileDiff(v.repoPath, file.NewPath)

		// Combine them
		var combined []string
		if stagedDiff != "" {
			combined = append(combined, "=== STAGED ===\n"+stagedDiff)
		}
		if unstagedDiff != "" {
			combined = append(combined, "=== UNSTAGED ===\n"+unstagedDiff)
		}

		if len(combined) > 0 {
			diff = strings.Join(combined, "\n")
		} else {
			diff = ""
		}

		if err != nil {
			v.err = err
			return
		}
		v.currentDiff = diff
		v.parsedDiffLines = ParseDiffLines(diff)
	}
}

// ParseDiffLines parses raw diff output into structured DiffLine slices with line numbers.
// This is a package-level function so it can be reused by other diff views.
func ParseDiffLines(rawDiff string) []DiffLine {
	var lines []DiffLine
	var oldLineNum, newLineNum int

	for _, line := range strings.Split(rawDiff, "\n") {
		diffLine := DiffLine{Content: line}

		// Detect line type and track line numbers
		if strings.HasPrefix(line, "@@") {
			// Hunk header: @@ -old,new +old,new @@
			diffLine.LineType = "@"
			// Parse line numbers from hunk header
			parts := strings.Fields(line)
			for i, p := range parts {
				if strings.HasPrefix(p, "-") && !strings.HasPrefix(p, "--") {
					// Old line number
					numStr := strings.TrimPrefix(p, "-")
					if idx := strings.Index(numStr, ","); idx > 0 {
						numStr = numStr[:idx]
					}
					if n, err := strconv.Atoi(numStr); err == nil {
						oldLineNum = n
					}
				} else if strings.HasPrefix(p, "+") && !strings.HasPrefix(p, "++") {
					// New line number
					numStr := strings.TrimPrefix(p, "+")
					if idx := strings.Index(numStr, ","); idx > 0 {
						numStr = numStr[:idx]
					}
					if n, err := strconv.Atoi(numStr); err == nil {
						newLineNum = n
					}
				}
				_ = i // suppress unused variable warning
			}
		} else if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") {
			// File header
			diffLine.LineType = "H"
			oldLineNum = 0
			newLineNum = 0
		} else if strings.HasPrefix(line, "+") {
			diffLine.LineType = "+"
			diffLine.NewLineNum = newLineNum
			newLineNum++
		} else if strings.HasPrefix(line, "-") {
			diffLine.LineType = "-"
			diffLine.OldLineNum = oldLineNum
			oldLineNum++
		} else if strings.HasPrefix(line, " ") {
			diffLine.LineType = " "
			diffLine.OldLineNum = oldLineNum
			diffLine.NewLineNum = newLineNum
			oldLineNum++
			newLineNum++
		} else {
			diffLine.LineType = ""
		}

		lines = append(lines, diffLine)
	}

	return lines
}

// View renders the diff view.
func (v *DiffView) View() string {
	th := theme.GetTheme()

	// Calculate panel widths
	leftWidth := 50
	rightWidth := v.width - leftWidth - 3 // 3 for divider

	var s strings.Builder

	// Header
	headerTitle := " Diff Viewer "
	if v.mode == DiffModeBranch {
		headerTitle = fmt.Sprintf(" Branch Diff: %s ←→ %s ", v.branchA, v.branchB)
	} else {
		headerTitle = " Working Directory Changes "
	}
	s.WriteString(th.DashboardTitle.Render(headerTitle))
	s.WriteString("\n\n")

	// Repository info line
	if v.repo != nil {
		s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" Repository: %s ", v.repo.Path)))
		if v.repo.IsDirty {
			s.WriteString(th.DashboardErrorStyle.Render("● dirty"))
		}
		s.WriteString("\n")
	}

	// Create split panels
	leftPanel := v.renderFileList(leftWidth)
	rightPanel := v.renderDetailPanel(rightWidth)

	// Combine panels side by side
	s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel))

	s.WriteString("\n")

	// Footer
	helpText := " ↑↓: Navigate   Enter: Toggle diff   Tab: Switch panel   Esc: Back   r: Refresh "
	if v.mode == DiffModeWorkdir {
		helpText = " ↑↓: Navigate   Enter: Toggle diff   Tab: Switch panel   Esc: Back   r: Refresh "
	}
	s.WriteString(th.Help.Render(helpText))

	return s.String()
}

// renderFileList renders the scrollable file list panel.
func (v *DiffView) renderFileList(width int) string {
	th := theme.GetTheme()
	var s strings.Builder

	// Panel header
	focusIndicator := ""
	if v.focusFileList {
		focusIndicator = " [FOCUS]"
	}
	s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" Files %s ", focusIndicator)))
	s.WriteString("\n")

	dividerLen := width - 2
	if dividerLen < 0 {
		dividerLen = 0
	}
	s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %s ", strings.Repeat("─", dividerLen))))
	s.WriteString("\n")

	if v.loading {
		s.WriteString(th.StatsStyle.Render(" Loading..."))
		return s.String()
	}

	// Summary line
	if v.mode == DiffModeBranch && v.branchDiff != nil {
		s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %d files changed ", v.branchDiff.FilesChanged)))
		if v.branchDiff.TotalAdditions > 0 {
			s.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf(" +%d ", v.branchDiff.TotalAdditions)))
		}
		if v.branchDiff.TotalDeletions > 0 {
			s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" -%d ", v.branchDiff.TotalDeletions)))
		}
		s.WriteString("\n")
	} else if v.mode == DiffModeWorkdir && v.workdirDiff != nil {
		totalChanged := len(v.workdirDiff.Files)
		s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %d files changed ", totalChanged)))
		s.WriteString("\n")
	}

	if len(v.files) == 0 {
		s.WriteString(th.Help.Render(" No files changed"))
		return s.String()
	}

	// Calculate visible range
	visibleLines := v.height - 10 // Account for header, footer, summary
	if visibleLines < 1 {
		visibleLines = 1
	}

	startIdx := v.selectedIdx - visibleLines/2
	if startIdx < 0 {
		startIdx = 0
	}
	endIdx := startIdx + visibleLines
	if endIdx > len(v.files) {
		endIdx = len(v.files)
		startIdx = endIdx - visibleLines
		if startIdx < 0 {
			startIdx = 0
		}
	}

	// Render files
	for i := startIdx; i < endIdx && i < len(v.files); i++ {
		file := v.files[i]
		prefix := "  "
		style := th.BranchStyle

		if i == v.selectedIdx {
			prefix = " >"
			style = th.SelectedBranchStyle
			if v.focusFileList {
				style = th.DashboardAccentStyle
			}
		}

		// Status indicator
		statusChar, statusStyle := fileStatusIndicator(file.Status, th)

		// Truncate long paths
		path := truncatePath(file.NewPath, width-20)

		// Additions/deletions
		additions := ""
		deletions := ""
		if file.Additions > 0 {
			additions = th.DashboardAccentStyle.Render(fmt.Sprintf("+%d", file.Additions))
		}
		if file.Deletions > 0 {
			deletions = th.DashboardErrorStyle.Render(fmt.Sprintf("-%d", file.Deletions))
		}

		s.WriteString(fmt.Sprintf("%s%s %s %s%s%s\n",
			prefix,
			statusStyle.Render(statusChar),
			style.Render(path),
			th.Help.Render(" "),
			additions,
			deletions))
	}

	// Show scroll indicator if needed
	if len(v.files) > visibleLines {
		scrollInfo := fmt.Sprintf(" %d-%d of %d ", startIdx+1, endIdx, len(v.files))
		s.WriteString(th.Help.Render(scrollInfo))
	}

	return s.String()
}

// renderDetailPanel renders the file detail and diff content panel.
func (v *DiffView) renderDetailPanel(width int) string {
	th := theme.GetTheme()
	var s strings.Builder

	// Panel header
	focusIndicator := ""
	if !v.focusFileList {
		focusIndicator = " [FOCUS]"
	}
	navIndicator := ""
	if v.showDiff && v.diffNavMode {
		navIndicator = " [j/k scroll]"
	}
	s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" Details %s %s ", focusIndicator, navIndicator)))
	s.WriteString("\n")

	dividerLen := width - 2
	if dividerLen < 0 {
		dividerLen = 0
	}
	s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %s ", strings.Repeat("─", dividerLen))))
	s.WriteString("\n")

	if v.loading {
		s.WriteString(th.StatsStyle.Render(" Loading..."))
		return s.String()
	}

	if len(v.files) == 0 {
		s.WriteString(th.Help.Render(" Select a file to view details"))
		return s.String()
	}

	// Show selected file info
	file := v.files[v.selectedIdx]

	// File path
	s.WriteString(th.StatsStyle.Render(" Path: "))
	s.WriteString(th.BranchStyle.Render(file.NewPath))
	s.WriteString("\n")

	// Status
	s.WriteString(th.StatsStyle.Render(" Status: "))
	statusStr, statusStyle := fileStatusLabel(file.Status, th)
	s.WriteString(statusStyle.Render(statusStr))
	s.WriteString("\n")

	// Change counts
	s.WriteString(th.StatsStyle.Render(" Changes: "))
	if file.Additions > 0 {
		s.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf("+%d", file.Additions)))
		s.WriteString(th.StatsStyle.Render(" / "))
	}
	if file.Deletions > 0 {
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf("-%d", file.Deletions)))
	}
	if file.Additions == 0 && file.Deletions == 0 {
		s.WriteString(th.Help.Render("No line changes"))
	}
	s.WriteString("\n")

	// Workdir-specific info
	if v.mode == DiffModeWorkdir && v.workdirDiff != nil && v.selectedIdx < len(v.workdirDiff.Files) {
		ws := v.workdirDiff.Files[v.selectedIdx]

		if ws.StagedStatus != "" && ws.StagedStatus != "?" {
			s.WriteString(th.StatsStyle.Render(" Staged: "))
			s.WriteString(th.DashboardAccentStyle.Render(ws.StagedStatus))
			if ws.StagedAdditions > 0 || ws.StagedDeletions > 0 {
				s.WriteString(th.Help.Render(fmt.Sprintf(" (+%d/-%d)", ws.StagedAdditions, ws.StagedDeletions)))
			}
			s.WriteString("\n")
		}

		if ws.UnstagedStatus != "" && ws.UnstagedStatus != "?" {
			s.WriteString(th.StatsStyle.Render(" Unstaged: "))
			s.WriteString(th.WarningStyle.Render(ws.UnstagedStatus))
			if ws.UnstagedAdditions > 0 || ws.UnstagedDeletions > 0 {
				s.WriteString(th.Help.Render(fmt.Sprintf(" (+%d/-%d)", ws.UnstagedAdditions, ws.UnstagedDeletions)))
			}
			s.WriteString("\n")
		}
	}

	s.WriteString("\n")

	// Diff content
	s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %s ", strings.Repeat("─", dividerLen-2))))
	s.WriteString("\n")

	if v.showDiff && len(v.parsedDiffLines) > 0 {
		// Render scrollable diff with line numbers
		s.WriteString(v.renderDiffWithGutter(width))
	} else if v.showDiff && v.currentDiff != "" {
		// Fallback to old-style rendering if no parsed lines
		diffLines := strings.Split(v.currentDiff, "\n")
		maxLines := v.height - 20
		if maxLines < 5 {
			maxLines = 5
		}

		for i := 0; i < len(diffLines) && i < maxLines; i++ {
			line := diffLines[i]
			lineStyle := th.Help

			// Color diff lines
			if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
				lineStyle = th.DashboardAccentStyle
			} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
				lineStyle = th.DashboardErrorStyle
			} else if strings.HasPrefix(line, "@@") {
				lineStyle = th.InfoStyle
			}

			// Truncate long lines
			if len(line) > width-4 {
				line = line[:width-7] + "..."
			}

			s.WriteString(lineStyle.Render(" " + line))
			s.WriteString("\n")
		}

		if len(diffLines) > maxLines {
			s.WriteString(th.Help.Render(fmt.Sprintf(" ... and %d more lines", len(diffLines)-maxLines)))
			s.WriteString("\n")
		}
	} else if v.showDiff && v.currentDiff == "" {
		s.WriteString(th.Help.Render(" No diff content available"))
		s.WriteString("\n")
	} else {
		s.WriteString(th.Help.Render(" Enter: View diff | g/G: Top/Bottom | Esc: Back to list"))
		s.WriteString("\n")
	}

	return s.String()
}

// renderDiffWithGutter renders the diff content with line numbers in a gutter.
func (v *DiffView) renderDiffWithGutter(width int) string {
	scrollHint := "[Enter to navigate]"
	if v.diffNavMode {
		scrollHint = "[j/k scroll, g/G top/bottom]"
	}
	return renderDiffWithGutter(v.parsedDiffLines, v.diffScrollOffset, width, v.height, 12, 2, false, scrollHint)
}

// ShortHelp returns a short help string.
func (v *DiffView) ShortHelp() string {
	return "↑↓: Navigate files  Enter: View diff  j/k: Scroll diff  g/G: Top/Bottom  Esc: Back"
}

// SetSize updates the view dimensions.
func (v *DiffView) SetSize(width, height int) {
	v.width = width
	v.height = height
}

// GetRepoPath returns the repository path.
func (v *DiffView) GetRepoPath() string {
	return v.repoPath
}

// Refresh reloads diff data.
func (v *DiffView) Refresh() error {
	v.loadData()
	return v.err
}

// KeyBindings returns the keybindings for this view.
func (v *DiffView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "↑/k", Description: "Navigate up in file list"},
		{Key: "↓/j", Description: "Navigate down in file list"},
		{Key: "g", Description: "Go to top (when viewing diff)"},
		{Key: "G", Description: "Go to bottom (when viewing diff)"},
		{Key: "Enter", Description: "Toggle diff content view"},
		{Key: "Tab", Description: "Switch focus between panels"},
		{Key: "Shift+Tab", Description: "Switch focus (reverse)"},
		{Key: "r", Description: "Refresh diff data"},
		{Key: "Esc", Description: "Close diff content / Go back"},
		{Key: "1", Description: "Switch to staged changes (workdir mode)"},
		{Key: "2", Description: "Switch to unstaged changes (workdir mode)"},
	}
}
