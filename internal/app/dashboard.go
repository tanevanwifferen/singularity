package app

import (
	"fmt"

	"singularity/internal/app/components"
	"singularity/internal/git"
	"singularity/internal/theme"

	"github.com/charmbracelet/bubbletea"
)

// BranchDashboard is the main branch dashboard component
type BranchDashboard struct {
	repo       *git.RepoInfo
	branches   []git.BranchInfo
	selected   int
	err        error
	width      int
	height     int
	comparing  bool
	compareIdx int
}

// NewBranchDashboard creates a new branch dashboard
func NewBranchDashboard(repoPath string) (*BranchDashboard, error) {
	repo, err := git.OpenRepo(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open repo: %w", err)
	}

	return &BranchDashboard{
		repo:     repo,
		branches: repo.Branches,
		selected: 0,
		width:    80,
		height:   24,
	}, nil
}

// SetRepoPath updates the repository path and reloads data.
func (d *BranchDashboard) SetRepoPath(path string) {
	repo, err := git.OpenRepo(path)
	if err != nil {
		d.err = err
		return
	}
	d.repo = repo
	d.branches = repo.Branches
	d.err = nil
	if d.selected >= len(d.branches) {
		d.selected = 0
	}
}

// Init initializes the dashboard
func (d *BranchDashboard) Init() tea.Cmd {
	return nil
}

// Update handles update events
func (d *BranchDashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if d.selected > 0 {
				d.selected--
			}
		case "down", "j":
			if d.selected < len(d.branches)-1 {
				d.selected++
			}
		case "enter", " ":
			// Compare selected branch with current
			if len(d.branches) > 0 && d.repo.CurrentBranch != "" {
				d.comparing = true
				d.compareIdx = d.selected
			}
		case "esc":
			d.comparing = false
		case "t":
			theme.ToggleTheme()
		case "q", "ctrl+c":
			return d, tea.Quit
		}
	case tea.WindowSizeMsg:
		d.width = msg.Width
		d.height = msg.Height
	}
	return d, nil
}

// View renders the dashboard
func (d *BranchDashboard) View() string {
	th := theme.GetTheme()

	if d.err != nil {
		return th.DashboardErrorStyle.Render(fmt.Sprintf("Error: %v", d.err))
	}

	var s string

	// Header
	s += th.DashboardTitle.Render(" Git Frontend - Branch Dashboard ")
	s += "\n\n"

	// Repo info
	s += th.StatsStyle.Render(fmt.Sprintf(" Repository: %s ", d.repo.Path))
	s += "\n"
	s += th.StatsStyle.Render(fmt.Sprintf(" Branch: %s ", d.repo.CurrentBranch))
	if d.repo.IsDirty {
		s += th.DashboardErrorStyle.Render(" ●")
	}
	s += "\n\n"

	// Branches header
	s += th.StatsStyle.Render(" Branches ")
	s += "\n"
	s += th.StatsStyle.Render(" ─────────────────────────────────────────────── ")
	s += "\n"

	// List branches
	for i, branch := range d.branches {
		prefix := "  "
		if i == d.selected {
			prefix = th.SelectedBranchStyle.Render(" >")
		}

		branchStr := fmt.Sprintf("%s %s", prefix, branch.Name)

		if i == d.selected {
			branchStr = th.SelectedBranchStyle.Render(branchStr)
		} else {
			branchStr = th.BranchStyle.Render(branchStr)
		}

		s += branchStr

		// Add ahead/behind if available
		if branch.Upstream != "" {
			behindAhead := fmt.Sprintf(" ↑%d ↓%d (%s)",
				branch.Ahead, branch.Behind, branch.Upstream)
			if branch.Ahead > 0 || branch.Behind > 0 {
				s += th.StatsStyle.Render(behindAhead)
			} else {
				s += th.StatsStyle.Render(fmt.Sprintf(" (%s)", branch.Upstream))
			}
		}

		s += "\n"
	}

	// Comparison view
	if d.comparing && d.compareIdx < len(d.branches) {
		s += "\n"
		s += th.StatsStyle.Render(" ─────────────────────────────────────────────── ")
		s += "\n"
		s += th.DashboardTitle.Render(" Branch Comparison ")
		s += "\n\n"

		branch := d.branches[d.compareIdx]
		comparison, err := git.CompareBranches(d.repo.Path, d.repo.CurrentBranch, branch.Name)
		if err != nil {
			s += th.DashboardErrorStyle.Render(fmt.Sprintf(" Error: %v", err))
		} else {
			s += th.StatsStyle.Render(fmt.Sprintf(" %s...%s:", d.repo.CurrentBranch, branch.Name))
			s += "\n"
			if comparison.Diverged {
				s += th.StatsStyle.Render(fmt.Sprintf("   Diverged: %d ahead, %d behind",
					comparison.Ahead, comparison.Behind))
			} else if comparison.Ahead > 0 {
				s += th.StatsStyle.Render(fmt.Sprintf("   Ahead by %d commits", comparison.Ahead))
			} else if comparison.Behind > 0 {
				s += th.StatsStyle.Render(fmt.Sprintf("   Behind by %d commits", comparison.Behind))
			} else {
				s += th.StatsStyle.Render("   Identical")
			}

			// Also show tree comparison
			s += "\n\n"
			treeComp, treeErr := git.CompareBranchesByTree(d.repo.Path, d.repo.CurrentBranch, branch.Name)
			if treeErr != nil {
				s += th.DashboardErrorStyle.Render(fmt.Sprintf("   Tree comparison error: %v", treeErr))
			} else {
				s += th.StatsStyle.Render(" Tree Status: ")
				if treeComp.SquashDetected {
					s += th.DashboardAccentStyle.Render(" Squash merge detected!")
				} else if treeComp.TreeDiverged {
					s += th.StatsStyle.Render(" Trees differ")
				} else {
					s += th.StatsStyle.Render(" Trees identical")
				}
			}
		}
		s += "\n\n"
		s += th.StatsStyle.Render(" Press ESC to close comparison ")
	}

	// Footer
	s += "\n"
	s += th.StatsStyle.Render(" ─────────────────────────────────────────────── ")
	s += "\n"
	s += th.StatsStyle.Render(" ↑/k: Select   Enter: Compare   t: Toggle Theme   q: Quit ")

	return s
}

// SelectBranch returns the currently selected branch
func (d *BranchDashboard) SelectBranch() *git.BranchInfo {
	if d.selected < len(d.branches) {
		return &d.branches[d.selected]
	}
	return nil
}

// GetRepo returns the repository info
func (d *BranchDashboard) GetRepo() *git.RepoInfo {
	return d.repo
}

// ShortHelp returns a short help string for the view.
func (d *BranchDashboard) ShortHelp() string {
	return "↑/k: Select  Enter: Compare  t: Theme  q: Quit"
}

// Refresh reloads repository data
func (d *BranchDashboard) Refresh() error {
	repo, err := git.OpenRepo(d.repo.Path)
	if err != nil {
		return err
	}
	d.repo = repo
	d.branches = repo.Branches
	if d.selected >= len(d.branches) {
		d.selected = len(d.branches) - 1
	}
	return nil
}

// SetSize updates the dashboard dimensions.
func (d *BranchDashboard) SetSize(width, height int) {
	d.width = width
	d.height = height
}

// KeyBindings returns the keybindings for this view.
func (d *BranchDashboard) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "↑/k", Description: "Select previous branch"},
		{Key: "↓/j", Description: "Select next branch"},
		{Key: "Enter/Space", Description: "Compare selected branch with current"},
		{Key: "Esc", Description: "Close comparison panel"},
		{Key: "t", Description: "Toggle light/dark theme"},
		{Key: "r", Description: "Refresh branch data"},
		{Key: "1", Description: "Switch to Overview"},
		{Key: "2", Description: "Switch to Branches"},
		{Key: "3", Description: "Switch to Stashes"},
		{Key: "4", Description: "Switch to Worktrees"},
	}
}
