package views

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"git-frontend/internal/app/components"
	"git-frontend/internal/git"
	"git-frontend/internal/theme"

	"github.com/charmbracelet/bubbletea"
)

// BranchesView displays a filterable list of branches with comparison capabilities.
type BranchesView struct {
	repoPath    string
	repo        *git.RepoInfo
	branches    []git.BranchInfo
	filter      *components.Filter[git.BranchInfo]
	loading     bool
	err         error
	width       int
	height      int

	// Comparison panel state
	showCompare   bool
	compareBranch *git.BranchInfo
	comparison    *git.BranchComparison

	// Modal state for delete confirmation
	showDeleteConfirm bool
	deleteBranch      *git.BranchInfo

	// New branch input state
	showNewBranch  bool
	newBranchName  string
	newBranchInput components.Filter[byte]
}

// NewBranchesView creates a new branches view.
func NewBranchesView(repoPath string) *BranchesView {
	v := &BranchesView{
		repoPath: repoPath,
		width:    80,
		height:   24,
	}

	// Initialize the filter with branch items
	branches := []git.BranchInfo{}
	v.filter = components.NewFilter(branches, v.renderBranchItem)
	v.filter.SetHeight(v.height)

	return v
}

// Init initializes the branches view.
func (v *BranchesView) Init() tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		v.loadData()
		return RefreshDoneMsg{}
	}
}

// loadData loads all repository data.
func (v *BranchesView) loadData() {
	v.err = nil

	repo, err := git.OpenRepo(v.repoPath)
	if err != nil {
		v.err = fmt.Errorf("failed to open repo: %w", err)
		v.loading = false
		return
	}
	v.repo = repo
	v.branches = repo.Branches

	// Update filter with new branch list
	v.filter.SetItems(v.branches)

	v.loading = false
}

// Update handles update events.
func (v *BranchesView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle modal states first
		if v.showDeleteConfirm {
			return v, v.handleDeleteConfirm(msg)
		}
		if v.showNewBranch {
			return v, v.handleNewBranchInput(msg)
		}
		if v.showCompare {
			return v, v.handleCompareKey(msg)
		}

		// Main view keys
		switch msg.String() {
		case "r":
			v.loading = true
			return v, func() tea.Msg {
				v.loadData()
				return RefreshDoneMsg{}
			}
		case "/":
			// Activate filter mode
			if v.filter != nil {
				// The filter handles '/' internally
				v.filter.Update(msg)
			}
		case "enter":
			// Open comparison panel for selected branch
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				v.compareBranch = &item
				v.showCompare = true
				v.loadComparison()
			}
		case "c":
			// Checkout selected branch
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				v.checkoutBranch(item.Name)
			}
		case "d":
			// Show delete confirmation
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				v.deleteBranch = &item
				v.showDeleteConfirm = true
			}
		case "n":
			// Show new branch input
			v.showNewBranch = true
			v.newBranchName = ""
			v.newBranchInput = *components.NewFilter([]byte{}, func(b byte, i int, s bool) string {
				return string(b)
			})
		case "esc":
			// Clear filter if active, otherwise do nothing
			if v.filter.IsActive() {
				v.filter.Update(msg)
			}
		}

		// Pass to filter for navigation
		if v.filter != nil {
			v.filter.Update(msg)
		}

	case RefreshDoneMsg:
		v.loading = false

	case tea.WindowSizeMsg:
		v.width = msg.Width
		v.height = msg.Height
		if v.filter != nil {
			v.filter.SetHeight(msg.Height)
		}
	}

	return v, nil
}

// handleDeleteConfirm handles key events during delete confirmation.
func (v *BranchesView) handleDeleteConfirm(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "enter":
		if v.deleteBranch != nil {
			v.deleteBranchCmd(v.deleteBranch.Name)
		}
		v.showDeleteConfirm = false
		v.deleteBranch = nil
	case "n", "esc":
		v.showDeleteConfirm = false
		v.deleteBranch = nil
	}
	return nil
}

// handleNewBranchInput handles key events during new branch creation.
func (v *BranchesView) handleNewBranchInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		if v.newBranchName != "" {
			v.createBranch(v.newBranchName)
		}
		v.showNewBranch = false
		v.newBranchName = ""
	case "esc":
		v.showNewBranch = false
		v.newBranchName = ""
	default:
		// Handle text input for branch name
		if len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r >= 32 && r <= 126 {
				v.newBranchName += string(r)
			}
		} else if msg.String() == "backspace" && len(v.newBranchName) > 0 {
			v.newBranchName = v.newBranchName[:len(v.newBranchName)-1]
		}
	}
	return nil
}

// handleCompareKey handles key events during comparison view.
func (v *BranchesView) handleCompareKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		v.showCompare = false
		v.compareBranch = nil
		v.comparison = nil
	}
	return nil
}

// loadComparison loads branch comparison data.
func (v *BranchesView) loadComparison() {
	if v.compareBranch == nil || v.repo == nil || v.repo.CurrentBranch == "" {
		return
	}

	comparison, err := git.CompareBranches(v.repoPath, v.repo.CurrentBranch, v.compareBranch.Name)
	if err != nil {
		v.err = err
		return
	}
	v.comparison = comparison
}

// checkoutBranch checks out the specified branch.
func (v *BranchesView) checkoutBranch(branchName string) {
	cmd := exec.Command("git", "-C", v.repoPath, "checkout", branchName)
	if err := cmd.Run(); err != nil {
		v.err = fmt.Errorf("failed to checkout branch: %w", err)
		return
	}
	// Refresh data after checkout
	v.loadData()
}

// deleteBranchCmd deletes the specified branch.
func (v *BranchesView) deleteBranchCmd(branchName string) {
	// Use -d for delete (safe, won't delete unmerged) or -D for force
	cmd := exec.Command("git", "-C", v.repoPath, "branch", "-d", branchName)
	if err := cmd.Run(); err != nil {
		// Try force delete
		cmd = exec.Command("git", "-C", v.repoPath, "branch", "-D", branchName)
		if err := cmd.Run(); err != nil {
			v.err = fmt.Errorf("failed to delete branch: %w", err)
			return
		}
	}
	// Refresh data after delete
	v.loadData()
}

// createBranch creates a new branch with the given name.
func (v *BranchesView) createBranch(branchName string) {
	// Create branch from current HEAD
	cmd := exec.Command("git", "-C", v.repoPath, "branch", branchName)
	if err := cmd.Run(); err != nil {
		v.err = fmt.Errorf("failed to create branch: %w", err)
		return
	}
	// Refresh data after create
	v.loadData()
}

// renderBranchItem renders a single branch item in the list.
func (v *BranchesView) renderBranchItem(branch git.BranchInfo, index int, selected bool) string {
	th := theme.GetTheme()

	// Branch name
	nameStyle := th.BranchStyle
	if selected {
		nameStyle = th.SelectedBranchStyle
	}
	namePrefix := "  "
	if selected {
		namePrefix = " >"
	}

	var line strings.Builder
	line.WriteString(nameStyle.Render(fmt.Sprintf("%s%s", namePrefix, branch.Name)))

	// Current branch indicator
	if v.repo != nil && v.repo.CurrentBranch == branch.Name {
		line.WriteString(th.DashboardAccentStyle.Render(" (current)"))
	}

	// Ahead/behind with color coding
	if branch.Upstream != "" {
		line.WriteString(fmt.Sprintf(" → %s", th.StatsStyle.Render(branch.Upstream)))

		if branch.Ahead > 0 {
			line.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf(" ↑%d", branch.Ahead)))
		}
		if branch.Behind > 0 {
			line.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" ↓%d", branch.Behind)))
		}
	} else if !branch.IsLocal {
		line.WriteString(th.DashboardErrorStyle.Render(" (gone)"))
	}

	return line.String()
}

// View renders the branches view.
func (v *BranchesView) View() string {
	th := theme.GetTheme()

	// Loading state
	if v.loading {
		return th.StatsStyle.Render(" Loading branches...")
	}

	// Error state
	if v.err != nil && v.repo == nil {
		return th.DashboardErrorStyle.Render(fmt.Sprintf(" Error: %v", v.err))
	}

	var s strings.Builder

	// Header
	s.WriteString(th.DashboardTitle.Render(" Branch Explorer "))
	s.WriteString("\n\n")

	// Repo info line
	if v.repo != nil {
		s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" Repository: %s ", filepath.Base(v.repoPath))))
		if v.repo.IsDirty {
			s.WriteString(th.DashboardErrorStyle.Render("● dirty"))
		}
		s.WriteString("\n")

		// Current branch indicator
		if v.repo.CurrentBranch != "" {
			s.WriteString(fmt.Sprintf(" %s %s\n",
				th.BranchStyle.Render("Current:"),
				th.DashboardAccentStyle.Render(v.repo.CurrentBranch)))
		}
	}
	s.WriteString("\n")

	// Filter hint
	if v.filter.IsActive() {
		// Filter is active, just render the filter view
		s.WriteString(v.filter.View())
	} else {
		// Show filter hint first line
		s.WriteString(th.Help.Render(" Press / to search • ↑/k: Select • Enter: Compare • c: Checkout • d: Delete • n: New "))
		s.WriteString("\n\n")
		s.WriteString(v.filter.View())
	}

	// Comparison panel
	if v.showCompare && v.compareBranch != nil {
		s.WriteString("\n")
		s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(" Branch Comparison "))
		s.WriteString("\n\n")

		if v.repo != nil && v.repo.CurrentBranch != "" {
			s.WriteString(fmt.Sprintf(" %s %s...%s\n",
				th.BranchStyle.Render("Comparing:"),
				th.StatsStyle.Render(v.repo.CurrentBranch),
				th.StatsStyle.Render(v.compareBranch.Name)))
		}

		if v.comparison != nil {
			s.WriteString("\n")
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
		} else if v.err != nil {
			s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf("  Error: %v", v.err)))
		}

		s.WriteString("\n\n")
		s.WriteString(th.Help.Render(" ESC: Close comparison "))
	}

	// Delete confirmation modal
	if v.showDeleteConfirm && v.deleteBranch != nil {
		s.WriteString("\n\n")
		s.WriteString(th.DashboardErrorStyle.Render(" ┌─────────────────────────────────────────────┐"))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" │ Delete branch '%s'?  (y/n)               │", v.deleteBranch.Name)))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(" └─────────────────────────────────────────────┘"))
	}

	// New branch input
	if v.showNewBranch {
		s.WriteString("\n\n")
		s.WriteString(th.DashboardTitle.Render(" ┌─────────────────────────────────────────────┐"))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" │ Create new branch: %s", v.newBranchName)))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(" │ (press Enter to create, Esc to cancel)    │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(" └─────────────────────────────────────────────┘"))
	}

	// Footer
	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")
	s.WriteString(th.Help.Render(" r: Refresh   /: Search   ↑↓: Navigate   Enter: Compare   c: Checkout   d: Delete   n: Create "))

	return s.String()
}

// ShortHelp returns a short help string.
func (v *BranchesView) ShortHelp() string {
	return "/: Search  ↑↓: Navigate  Enter: Compare  c: Checkout  d: Delete  n: Create"
}

// SetSize updates the view dimensions.
func (v *BranchesView) SetSize(width, height int) {
	v.width = width
	v.height = height
	if v.filter != nil {
		v.filter.SetHeight(height)
	}
}

// GetRepoPath returns the repository path.
func (v *BranchesView) GetRepoPath() string {
	return v.repoPath
}

// Refresh reloads repository data.
func (v *BranchesView) Refresh() error {
	v.loadData()
	return v.err
}
