package views

import (
	"fmt"
	"strings"

	"git-frontend/internal/app/components"
	"git-frontend/internal/git"
	"git-frontend/internal/theme"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// BranchComparisonView provides a split-panel branch comparison interface.
// Left panel: list of branches to compare against
// Right panel: detailed comparison results
type BranchComparisonView struct {
	repoPath    string
	repo        *git.RepoInfo
	branches    []git.BranchInfo
	selectedIdx int
	width       int
	height      int
	loading     bool
	err         error

	// Comparison state
	compareBranch *git.BranchInfo
	comparison    *git.BranchComparison
	treeCompare   *git.TreeComparison
	fileDiff      *git.BranchDiff
}

// NewBranchComparisonView creates a new branch comparison view.
func NewBranchComparisonView(repoPath string) *BranchComparisonView {
	return &BranchComparisonView{
		repoPath:    repoPath,
		width:       120,
		height:      30,
		selectedIdx: 0,
	}
}

// SetRepoPath updates the repository path for this view.
func (v *BranchComparisonView) SetRepoPath(path string) { v.repoPath = path }

// Init initializes the view.
func (v *BranchComparisonView) Init() tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		v.loadData()
		return RefreshDoneMsg{}
	}
}

// loadData loads repository and branch data.
func (v *BranchComparisonView) loadData() {
	v.err = nil

	repo, err := git.OpenRepo(v.repoPath)
	if err != nil {
		v.err = fmt.Errorf("failed to open repo: %w", err)
		v.loading = false
		return
	}
	v.repo = repo
	v.branches = repo.Branches

	// Keep selected index in bounds
	if v.selectedIdx >= len(v.branches) {
		v.selectedIdx = len(v.branches) - 1
	}
	if v.selectedIdx < 0 {
		v.selectedIdx = 0
	}

	// Auto-select first non-current branch for comparison
	if v.compareBranch == nil && len(v.branches) > 0 {
		for i, b := range v.branches {
			if b.Name != v.repo.CurrentBranch {
				v.compareBranch = &b
				v.selectedIdx = i
				break
			}
		}
	}

	// Load comparison data if we have a compare branch
	if v.compareBranch != nil && v.repo.CurrentBranch != "" {
		v.loadComparison()
	}

	v.loading = false
}

// loadComparison loads comparison data for selected branch.
func (v *BranchComparisonView) loadComparison() {
	if v.compareBranch == nil || v.repo == nil || v.repo.CurrentBranch == "" {
		return
	}

	// Compare branches (ahead/behind)
	comparison, err := git.CompareBranches(v.repoPath, v.repo.CurrentBranch, v.compareBranch.Name)
	if err != nil {
		v.err = err
		return
	}
	v.comparison = comparison

	// Compare trees (detects squash merges)
	treeCompare, err := git.CompareBranchesByTree(v.repoPath, v.repo.CurrentBranch, v.compareBranch.Name)
	if err != nil {
		v.err = err
		return
	}
	v.treeCompare = treeCompare

	// Get file diff summary
	fileDiff, err := git.GetBranchDiff(v.repoPath, v.repo.CurrentBranch, v.compareBranch.Name)
	if err != nil {
		v.err = err
		return
	}
	v.fileDiff = fileDiff
}

// Update handles update events.
func (v *BranchComparisonView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if v.selectedIdx > 0 {
				v.selectedIdx--
				v.updateCompareFromSelection()
			}
		case "down", "j":
			if v.selectedIdx < len(v.branches)-1 {
				v.selectedIdx++
				v.updateCompareFromSelection()
			}
		case "enter":
			// Select current branch for comparison (switch to different branch)
			if v.selectedIdx < len(v.branches) {
				branch := v.branches[v.selectedIdx]
				if branch.Name != v.repo.CurrentBranch {
					v.compareBranch = &branch
					v.loadComparison()
				}
			}
		case "tab":
			// Switch to next branch for comparison without leaving view
			v.switchToNextBranch()
		case "shift+tab":
			// Switch to previous branch for comparison
			v.switchToPrevBranch()
		case "r":
			// Refresh
			v.loading = true
			return v, func() tea.Msg {
				v.loadData()
				return RefreshDoneMsg{}
			}
		case "esc":
			// Close comparison view - return nil to signal parent to switch views
			return v, func() tea.Msg {
				return ViewChangeMsg{ViewName: "Branches"}
			}
		}

	case RefreshDoneMsg:
		v.loading = false
	}

	return v, nil
}

// updateCompareFromSelection updates compare branch from current selection.
func (v *BranchComparisonView) updateCompareFromSelection() {
	if v.selectedIdx < len(v.branches) {
		branch := v.branches[v.selectedIdx]
		// Only update comparison when a non-current branch is selected;
		// keep the existing compareBranch when cursor is on the current branch.
		if branch.Name != v.repo.CurrentBranch {
			v.compareBranch = &branch
			v.loadComparison()
		}
	}
}

// switchToNextBranch switches to the next branch for comparison.
func (v *BranchComparisonView) switchToNextBranch() {
	if len(v.branches) < 2 {
		return
	}

	currentCompare := ""
	if v.compareBranch != nil {
		currentCompare = v.compareBranch.Name
	}

	for i := 0; i < len(v.branches); i++ {
		idx := (v.selectedIdx + i + 1) % len(v.branches)
		branch := v.branches[idx]
		if branch.Name != v.repo.CurrentBranch && branch.Name != currentCompare {
			v.compareBranch = &branch
			v.selectedIdx = idx
			v.loadComparison()
			return
		}
	}
}

// switchToPrevBranch switches to the previous branch for comparison.
func (v *BranchComparisonView) switchToPrevBranch() {
	if len(v.branches) < 2 {
		return
	}

	currentCompare := ""
	if v.compareBranch != nil {
		currentCompare = v.compareBranch.Name
	}

	for i := 0; i < len(v.branches); i++ {
		idx := (v.selectedIdx - i - 1 + len(v.branches)) % len(v.branches)
		branch := v.branches[idx]
		if branch.Name != v.repo.CurrentBranch && branch.Name != currentCompare {
			v.compareBranch = &branch
			v.selectedIdx = idx
			v.loadComparison()
			return
		}
	}
}

// ViewChangeMsg signals a view change request.
type ViewChangeMsg struct {
	ViewName string
	RepoPath string // Optional: when set, indicates a repo-specific view change
}

// View renders the split panel view.
func (v *BranchComparisonView) View() string {
	th := theme.GetTheme()

	// Calculate panel widths
	leftWidth := 45
	rightWidth := v.width - leftWidth - 3 // 3 for divider

	var s strings.Builder

	// Header
	s.WriteString(th.DashboardTitle.Render(" Branch Comparison "))
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
	leftPanel := v.renderLeftPanel(leftWidth)
	rightPanel := v.renderRightPanel(rightWidth)

	// Combine panels side by side
	s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel))

	s.WriteString("\n")

	// Footer
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")
	s.WriteString(th.Help.Render(" ↑↓: Select branch   Enter/Tab: Switch comparison   Esc: Back to branches   r: Refresh "))

	return s.String()
}

// renderLeftPanel renders the branch list panel.
func (v *BranchComparisonView) renderLeftPanel(width int) string {
	th := theme.GetTheme()
	var s strings.Builder

	// Panel header
	s.WriteString(th.DashboardTitle.Render(" Branches "))
	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(" ────────────────────────────────────────────── "))
	s.WriteString("\n")

	if v.loading {
		s.WriteString(th.StatsStyle.Render(" Loading..."))
		return s.String()
	}

	if len(v.branches) == 0 {
		s.WriteString(th.StatsStyle.Render(" No branches found"))
		return s.String()
	}

	// Calculate visible range
	visibleLines := v.height - 6 // Account for header, footer
	startIdx := 0
	endIdx := len(v.branches)
	if endIdx > visibleLines {
		// Scroll selection into view
		if v.selectedIdx >= endIdx {
			startIdx = endIdx - visibleLines
		} else if v.selectedIdx < startIdx {
			startIdx = v.selectedIdx
		}
		endIdx = startIdx + visibleLines
		if endIdx > len(v.branches) {
			endIdx = len(v.branches)
			startIdx = endIdx - visibleLines
		}
	}

	// Render branches
	for i := startIdx; i < endIdx && i < len(v.branches); i++ {
		branch := v.branches[i]
		prefix := "  "
		style := th.BranchStyle

		if i == v.selectedIdx {
			prefix = " >"
			style = th.SelectedBranchStyle
		}

		// Highlight compare branch
		if v.compareBranch != nil && branch.Name == v.compareBranch.Name {
			style = th.DashboardAccentStyle
		}

		// Branch name
		s.WriteString(style.Render(fmt.Sprintf("%s%s", prefix, branch.Name)))

		// Current branch indicator
		if v.repo != nil && v.repo.CurrentBranch == branch.Name {
			s.WriteString(th.DashboardErrorStyle.Render(" (current)"))
		}

		s.WriteString("\n")
	}

	// Show count if not all visible
	if len(v.branches) > visibleLines {
		s.WriteString(th.Help.Render(fmt.Sprintf(" Showing %d of %d", endIdx-startIdx, len(v.branches))))
	}

	return s.String()
}

// renderRightPanel renders the comparison results panel.
func (v *BranchComparisonView) renderRightPanel(width int) string {
	th := theme.GetTheme()
	var s strings.Builder

	// Panel header
	s.WriteString(th.DashboardTitle.Render(" Comparison "))
	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(" ────────────────────────────────────────────── "))
	s.WriteString("\n\n")

	if v.loading {
		s.WriteString(th.StatsStyle.Render(" Loading..."))
		return s.String()
	}

	if v.repo == nil || v.repo.CurrentBranch == "" {
		s.WriteString(th.DashboardErrorStyle.Render(" No repository or branch loaded"))
		return s.String()
	}

	// Show current vs compare branch
	s.WriteString(th.BranchStyle.Render(" Comparing: "))
	s.WriteString(th.StatsStyle.Render(v.repo.CurrentBranch))
	s.WriteString(th.DashboardAccentStyle.Render(" ←→ "))
	if v.compareBranch != nil {
		s.WriteString(th.StatsStyle.Render(v.compareBranch.Name))
	} else {
		s.WriteString(th.Help.Render(" (select a branch)"))
	}
	s.WriteString("\n\n")

	if v.compareBranch == nil {
		s.WriteString(th.Help.Render(" Select a branch from the list to compare"))
		s.WriteString("\n")
		return s.String()
	}

	// Divergence status
	s.WriteString(th.StatsStyle.Render(" ─── Commit Divergence ───"))
	s.WriteString("\n")

	if v.comparison != nil {
		if v.comparison.Diverged {
			s.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf("  Diverged: %d ahead, %d behind",
				v.comparison.Ahead, v.comparison.Behind)))
		} else if v.comparison.Ahead > 0 {
			s.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf("  Ahead by %d commits", v.comparison.Ahead)))
		} else if v.comparison.Behind > 0 {
			s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf("  Behind by %d commits", v.comparison.Behind)))
		} else {
			s.WriteString(th.StatsStyle.Render("  Branches are identical"))
		}
	}
	s.WriteString("\n\n")

	// Tree comparison (squash detection)
	s.WriteString(th.StatsStyle.Render(" ─── Tree Hash Comparison ───"))
	s.WriteString("\n")

	if v.treeCompare != nil {
		if v.treeCompare.SquashDetected {
			s.WriteString(th.DashboardAccentStyle.Render("  ⚠ Squash merge detected!"))
			s.WriteString("\n")
			s.WriteString(th.Help.Render("  (commits differ but tree is identical)"))
		} else if v.treeCompare.TreeDiverged {
			s.WriteString(th.DashboardErrorStyle.Render("  Trees differ"))
		} else {
			s.WriteString(th.StatsStyle.Render("  Trees are identical"))
		}
	}
	s.WriteString("\n\n")

	// File diff summary
	s.WriteString(th.StatsStyle.Render(" ─── File Diff Summary ───"))
	s.WriteString("\n")

	if v.fileDiff != nil {
		s.WriteString(fmt.Sprintf("  Files changed: %s\n",
			th.StatsStyle.Render(fmt.Sprintf("%d", v.fileDiff.FilesChanged))))

		if v.fileDiff.TotalAdditions > 0 {
			s.WriteString(fmt.Sprintf("  %s: %s\n",
				th.DashboardAccentStyle.Render("Additions"),
				th.StatsStyle.Render(fmt.Sprintf("+%d", v.fileDiff.TotalAdditions))))
		}

		if v.fileDiff.TotalDeletions > 0 {
			s.WriteString(fmt.Sprintf("  %s: %s\n",
				th.DashboardErrorStyle.Render("Deletions"),
				th.StatsStyle.Render(fmt.Sprintf("-%d", v.fileDiff.TotalDeletions))))
		}

		// Show first few changed files
		if len(v.fileDiff.Files) > 0 {
			s.WriteString("\n")
			s.WriteString(th.Help.Render("  Changed files:"))
			s.WriteString("\n")

			maxFiles := 8
			if len(v.fileDiff.Files) < maxFiles {
				maxFiles = len(v.fileDiff.Files)
			}

			for i := 0; i < maxFiles; i++ {
				file := v.fileDiff.Files[i]
				statusChar := file.Status
				statusStyle := th.StatsStyle

				switch file.Status {
				case "A":
					statusStyle = th.DashboardAccentStyle
					statusChar = "+"
				case "D":
					statusStyle = th.DashboardErrorStyle
					statusChar = "-"
				case "M":
					statusStyle = th.WarningStyle
					statusChar = "~"
				case "R":
					statusStyle = th.InfoStyle
					statusChar = ">"
				}

				// Truncate long paths
				path := file.NewPath
				if len(path) > 35 {
					path = path[:32] + "..."
				}

				s.WriteString(fmt.Sprintf("  %s %s %s\n",
					statusStyle.Render(statusChar),
					th.StatsStyle.Render(fmt.Sprintf("+%d/-%d", file.Additions, file.Deletions)),
					th.Help.Render(path)))
			}

			if len(v.fileDiff.Files) > maxFiles {
				s.WriteString(th.Help.Render(fmt.Sprintf("  ... and %d more files", len(v.fileDiff.Files)-maxFiles)))
				s.WriteString("\n")
			}
		}
	} else if v.err != nil {
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf("  Error: %v", v.err)))
	}

	return s.String()
}

// ShortHelp returns a short help string.
func (v *BranchComparisonView) ShortHelp() string {
	return "↑↓: Select  Enter/Tab: Switch  Esc: Back  r: Refresh"
}

// SetSize updates the view dimensions.
func (v *BranchComparisonView) SetSize(width, height int) {
	v.width = width
	v.height = height
}

// GetRepoPath returns the repository path.
func (v *BranchComparisonView) GetRepoPath() string {
	return v.repoPath
}

// Refresh reloads repository data.
func (v *BranchComparisonView) Refresh() error {
	v.loadData()
	return v.err
}

// KeyBindings returns the keybindings for this view.
func (v *BranchComparisonView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "↑/k", Description: "Select previous branch"},
		{Key: "↓/j", Description: "Select next branch"},
		{Key: "Enter", Description: "Compare with selected branch"},
		{Key: "Tab", Description: "Switch to next branch for comparison"},
		{Key: "Shift+Tab", Description: "Switch to previous branch for comparison"},
		{Key: "r", Description: "Refresh comparison data"},
		{Key: "Esc", Description: "Back to Branches view"},
		{Key: "1", Description: "Switch to Overview"},
		{Key: "2", Description: "Switch to Branches"},
		{Key: "3", Description: "Switch to Stashes"},
		{Key: "4", Description: "Switch to Worktrees"},
	}
}
