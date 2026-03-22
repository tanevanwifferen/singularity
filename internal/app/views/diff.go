package views

import (
	"fmt"
	"strings"

	"git-frontend/internal/git"
	"git-frontend/internal/theme"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// DiffViewMode represents the type of diff being viewed
type DiffViewMode int

const (
	DiffModeBranch DiffViewMode = iota
	DiffModeWorkdir
)

// DiffView provides a split-panel diff viewer interface.
// Left panel: scrollable file list with status indicators
// Right panel: file details and diff content
type DiffView struct {
	repoPath    string
	repo        *git.RepoInfo
	width       int
	height      int
	loading     bool
	err         error

	// Mode: branch diff or workdir diff
	mode DiffViewMode

	// Branch comparison mode
	branchA string
	branchB string
	branchDiff *git.BranchDiff

	// Workdir diff mode
	workdirDiff *git.WorkdirDiff

	// File list state
	files      []git.FileChange
	selectedIdx int
	showDiff   bool // Whether to show actual diff content

	// Focus: true = file list, false = diff panel
	focusFileList bool

	// Current file diff content
	currentDiff string
}

// NewDiffView creates a new diff view for branch comparison.
func NewDiffView(repoPath, branchA, branchB string) *DiffView {
	return &DiffView{
		repoPath:    repoPath,
		branchA:     branchA,
		branchB:     branchB,
		mode:        DiffModeBranch,
		width:       120,
		height:      30,
		selectedIdx: 0,
		focusFileList: true,
	}
}

// NewWorkdirDiffView creates a new diff view for staged/unstaged changes.
func NewWorkdirDiffView(repoPath string) *DiffView {
	return &DiffView{
		repoPath:    repoPath,
		mode:        DiffModeWorkdir,
		width:       120,
		height:      30,
		selectedIdx: 0,
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
			if v.selectedIdx > 0 {
				v.selectedIdx--
				v.showDiff = false
				v.currentDiff = ""
			}
		case "down", "j":
			if v.selectedIdx < len(v.files)-1 {
				v.selectedIdx++
				v.showDiff = false
				v.currentDiff = ""
			}
		case "enter":
			// Toggle diff content view
			if v.selectedIdx < len(v.files) {
				if v.showDiff {
					v.showDiff = false
					v.currentDiff = ""
				} else {
					v.loadFileDiff()
					v.showDiff = true
				}
			}
		case "tab":
			// Switch focus between file list and diff
			v.focusFileList = !v.focusFileList
		case "shift+tab":
			// Also switch focus (reverse)
			v.focusFileList = !v.focusFileList
		case "r":
			// Refresh
			v.loading = true
			return v, func() tea.Msg {
				v.loadData()
				return RefreshDoneMsg{}
			}
		case "esc":
			// Close diff view - signal parent to switch view
			return v, func() tea.Msg {
				return ViewChangeMsg{ViewName: "Overview"}
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
	}
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
		statusChar := " "
		statusStyle := th.StatsStyle

		switch file.Status {
		case "A":
			statusStyle = th.DashboardAccentStyle
			statusChar = "A"
		case "M":
			statusStyle = th.WarningStyle
			statusChar = "M"
		case "D":
			statusStyle = th.DashboardErrorStyle
			statusChar = "D"
		case "R":
			statusStyle = th.InfoStyle
			statusChar = "R"
		case "C":
			statusStyle = th.InfoStyle
			statusChar = "C"
		}

		// Truncate long paths
		path := file.NewPath
		maxPathLen := width - 20
		if maxPathLen < 10 {
			maxPathLen = 10
		}
		if len(path) > maxPathLen {
			path = "..." + path[len(path)-maxPathLen+3:]
		}

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
	s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" Details %s ", focusIndicator)))
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
	statusStr := file.Status
	statusStyle := th.StatsStyle
	switch file.Status {
	case "A":
		statusStr = "Added"
		statusStyle = th.DashboardAccentStyle
	case "M":
		statusStr = "Modified"
		statusStyle = th.WarningStyle
	case "D":
		statusStr = "Deleted"
		statusStyle = th.DashboardErrorStyle
	case "R":
		statusStr = "Renamed"
		statusStyle = th.InfoStyle
	case "C":
		statusStr = "Copied"
		statusStyle = th.InfoStyle
	}
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

	if v.showDiff && v.currentDiff != "" {
		// Show diff content
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
			} else if strings.HasPrefix(line, "@") {
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
		s.WriteString(th.Help.Render(" Press Enter to view diff content"))
		s.WriteString("\n")
	}

	return s.String()
}

// ShortHelp returns a short help string.
func (v *DiffView) ShortHelp() string {
	return "↑↓: Navigate  Enter: Toggle diff  Tab: Switch panel  Esc: Back  r: Refresh"
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
