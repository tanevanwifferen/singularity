package app

import (
	"fmt"

	"git-frontend/internal/git"
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
			ToggleTheme()
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
	theme := GetTheme()

	if d.err != nil {
		return theme.DashboardErrorStyle.Render(fmt.Sprintf("Error: %v", d.err))
	}

	var s string

	// Header
	s += theme.DashboardTitle.Render(" Git Frontend - Branch Dashboard ")
	s += "\n\n"

	// Repo info
	s += theme.StatsStyle.Render(fmt.Sprintf(" Repository: %s ", d.repo.Path))
	s += "\n"
	s += theme.StatsStyle.Render(fmt.Sprintf(" Branch: %s ", d.repo.CurrentBranch))
	if d.repo.IsDirty {
		s += theme.DashboardErrorStyle.Render(" ●")
	}
	s += "\n\n"

	// Branches header
	s += theme.StatsStyle.Render(" Branches ")
	s += "\n"
	s += theme.StatsStyle.Render(" ─────────────────────────────────────────────── ")
	s += "\n"

	// List branches
	for i, branch := range d.branches {
		prefix := "  "
		if i == d.selected {
			prefix = theme.SelectedBranchStyle.Render(" >")
		}

		branchStr := fmt.Sprintf("%s %s", prefix, branch.Name)

		if i == d.selected {
			branchStr = theme.SelectedBranchStyle.Render(branchStr)
		} else {
			branchStr = theme.BranchStyle.Render(branchStr)
		}

		s += branchStr

		// Add ahead/behind if available
		if branch.Upstream != "" {
			behindAhead := fmt.Sprintf(" ↑%d ↓%d (%s)",
				branch.Ahead, branch.Behind, branch.Upstream)
			if branch.Ahead > 0 || branch.Behind > 0 {
				s += theme.StatsStyle.Render(behindAhead)
			} else {
				s += theme.StatsStyle.Render(fmt.Sprintf(" (%s)", branch.Upstream))
			}
		}

		s += "\n"
	}

	// Comparison view
	if d.comparing && d.compareIdx < len(d.branches) {
		s += "\n"
		s += theme.StatsStyle.Render(" ─────────────────────────────────────────────── ")
		s += "\n"
		s += theme.DashboardTitle.Render(" Branch Comparison ")
		s += "\n\n"

		branch := d.branches[d.compareIdx]
		comparison, err := git.CompareBranches(d.repo.Path, d.repo.CurrentBranch, branch.Name)
		if err != nil {
			s += theme.DashboardErrorStyle.Render(fmt.Sprintf(" Error: %v", err))
		} else {
			s += theme.StatsStyle.Render(fmt.Sprintf(" %s...%s:", d.repo.CurrentBranch, branch.Name))
			s += "\n"
			if comparison.Diverged {
				s += theme.StatsStyle.Render(fmt.Sprintf("   Diverged: %d ahead, %d behind",
					comparison.Ahead, comparison.Behind))
			} else if comparison.Ahead > 0 {
				s += theme.StatsStyle.Render(fmt.Sprintf("   Ahead by %d commits", comparison.Ahead))
			} else if comparison.Behind > 0 {
				s += theme.StatsStyle.Render(fmt.Sprintf("   Behind by %d commits", comparison.Behind))
			} else {
				s += theme.StatsStyle.Render("   Identical")
			}

			// Also show tree comparison
			s += "\n\n"
			treeComp, treeErr := git.CompareBranchesByTree(d.repo.Path, d.repo.CurrentBranch, branch.Name)
			if treeErr != nil {
				s += theme.DashboardErrorStyle.Render(fmt.Sprintf("   Tree comparison error: %v", treeErr))
			} else {
				s += theme.StatsStyle.Render(" Tree Status: ")
				if treeComp.SquashDetected {
					s += theme.DashboardAccentStyle.Render(" Squash merge detected!")
				} else if treeComp.TreeDiverged {
					s += theme.StatsStyle.Render(" Trees differ")
				} else {
					s += theme.StatsStyle.Render(" Trees identical")
				}
			}
		}
		s += "\n\n"
		s += theme.StatsStyle.Render(" Press ESC to close comparison ")
	}

	// Footer
	s += "\n"
	s += theme.StatsStyle.Render(" ─────────────────────────────────────────────── ")
	s += "\n"
	s += theme.StatsStyle.Render(" ↑/k: Select   Enter: Compare   t: Toggle Theme   q: Quit ")

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
