package app

import (
	"fmt"
	"strconv"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"git-frontend/internal/git"
)

// DashboardStyle defines the visual style for the dashboard
var (
	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("205")).
			Bold(true)

	branchStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("86"))

	selectedBranchStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("220")).
				Background(lipgloss.Color("235")).
				Bold(true)

	statsStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	commitStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))
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
	if d.err != nil {
		return errorStyle.Render(fmt.Sprintf("Error: %v", d.err))
	}

	var s string

	// Header
	s += titleStyle.Render(" Git Frontend - Branch Dashboard ")
	s += "\n\n"

	// Repo info
	s += statsStyle.Render(fmt.Sprintf(" Repository: %s ", d.repo.Path))
	s += "\n"
	s += statsStyle.Render(fmt.Sprintf(" Branch: %s ", d.repo.CurrentBranch))
	if d.repo.IsDirty {
		s += errorStyle.Render(" ●")
	}
	s += "\n\n"

	// Branches header
	s += statsStyle.Render(" Branches ")
	s += "\n"
	s += statsStyle.Render(" ─────────────────────────────────────────────── ")
	s += "\n"

	// List branches
	for i, branch := range d.branches {
		prefix := "  "
		if i == d.selected {
			prefix = selectedBranchStyle.Render(" >")
		}

		branchStr := fmt.Sprintf("%s %s", prefix, branch.Name)

		if i == d.selected {
			branchStr = selectedBranchStyle.Render(branchStr)
		} else {
			branchStr = branchStyle.Render(branchStr)
		}

		s += branchStr

		// Add ahead/behind if available
		if branch.Upstream != "" {
			behindAhead := fmt.Sprintf(" ↑%d ↓%d (%s)",
				branch.Ahead, branch.Behind, branch.Upstream)
			if branch.Ahead > 0 || branch.Behind > 0 {
				s += statsStyle.Render(behindAhead)
			} else {
				s += statsStyle.Render(fmt.Sprintf(" (%s)", branch.Upstream))
			}
		}

		s += "\n"
	}

	// Comparison view
	if d.comparing && d.compareIdx < len(d.branches) {
		s += "\n"
		s += statsStyle.Render(" ─────────────────────────────────────────────── ")
		s += "\n"
		s += titleStyle.Render(" Branch Comparison ")
		s += "\n\n"

		branch := d.branches[d.compareIdx]
		comparison, err := git.CompareBranches(d.repo.Path, d.repo.CurrentBranch, branch.Name)
		if err != nil {
			s += errorStyle.Render(fmt.Sprintf(" Error: %v", err))
		} else {
			s += statsStyle.Render(fmt.Sprintf(" %s...%s:", d.repo.CurrentBranch, branch.Name))
			s += "\n"
			if comparison.Diverged {
				s += statsStyle.Render(fmt.Sprintf("   Diverged: %d ahead, %d behind",
					comparison.Ahead, comparison.Behind))
			} else if comparison.Ahead > 0 {
				s += statsStyle.Render(fmt.Sprintf("   Ahead by %d commits", comparison.Ahead))
			} else if comparison.Behind > 0 {
				s += statsStyle.Render(fmt.Sprintf("   Behind by %d commits", comparison.Behind))
			} else {
				s += statsStyle.Render("   Identical")
			}

			// Also show tree comparison
			s += "\n\n"
			treeComp, treeErr := git.CompareBranchesByTree(d.repo.Path, d.repo.CurrentBranch, branch.Name)
			if treeErr != nil {
				s += errorStyle.Render(fmt.Sprintf("   Tree comparison error: %v", treeErr))
			} else {
				s += statsStyle.Render(" Tree Status: ")
				if treeComp.SquashDetected {
					s += lipgloss.Color("220").Render(" Squash merge detected!")
				} else if treeComp.TreeDiverged {
					s += statsStyle.Render(" Trees differ")
				} else {
					s += statsStyle.Render(" Trees identical")
				}
			}
		}
		s += "\n\n"
		s += statsStyle.Render(" Press ESC to close comparison ")
	}

	// Footer
	s += "\n"
	s += statsStyle.Render(" ─────────────────────────────────────────────── ")
	s += "\n"
	s += statsStyle.Render(" ↑/k: Select   Enter: Compare   q: Quit ")

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
