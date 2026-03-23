package views

import (
	"fmt"
	"strings"

	"git-frontend/internal/project"
	"git-frontend/internal/theme"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ProjectView displays aggregate multi-repo project status.
type ProjectView struct {
	proj *project.Project
	err  error
	width int
	height int

	// Navigation state
	selectedIdx int

	// Branch check state
	showBranchCheck   bool
	branchCheckName   string
	branchExistence   *project.BranchExistence
}

// NewProjectView creates a new project view with an already-loaded project.
func NewProjectView(proj *project.Project) *ProjectView {
	return &ProjectView{
		proj:   proj,
		width:  80,
		height: 24,
	}
}

// Init initializes the project view.
func (v *ProjectView) Init() tea.Cmd {
	return func() tea.Msg {
		return RefreshDoneMsg{}
	}
}

// RefreshDoneMsg is sent when refresh completes - declared in overview.go

// Update handles update events.
func (v *ProjectView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "r":
			// Refresh project
			if v.proj != nil {
				v.proj.Refresh()
			}
		case "up", "k":
			if v.selectedIdx > 0 {
				v.selectedIdx--
			}
		case "down", "j":
			if v.proj != nil {
				status := v.proj.Status()
				if v.selectedIdx < len(status.Repos)-1 {
					v.selectedIdx++
				}
			}
		case "enter":
			// Drill into selected repo
			if v.proj != nil {
				status := v.proj.Status()
				if v.selectedIdx < len(status.Repos) {
					// Switch to Overview view
					return v, func() tea.Msg {
						return ViewChangeMsg{ViewName: "Overview"}
					}
				}
			}
		case "b":
			// Toggle branch check mode
			v.showBranchCheck = !v.showBranchCheck
			if v.showBranchCheck {
				v.branchCheckName = ""
				v.branchExistence = nil
			}
		case "esc":
			if v.showBranchCheck {
				v.showBranchCheck = false
			}
		}

		// Handle branch check input
		if v.showBranchCheck {
			switch msg.Type {
			case tea.KeyBackspace:
				if len(v.branchCheckName) > 0 {
					v.branchCheckName = v.branchCheckName[:len(v.branchCheckName)-1]
				}
			case tea.KeyEnter:
				// Perform branch check
				if v.branchCheckName != "" && v.proj != nil {
					v.branchExistence = v.proj.BranchExistsAcross(v.branchCheckName)
				}
			case tea.KeyRunes:
				v.branchCheckName += string(msg.Runes)
			}
		}

	case RefreshDoneMsg:
		// Refresh completed
	}
	return v, nil
}

// View renders the project overview.
func (v *ProjectView) View() string {
	th := theme.GetTheme()

	var s strings.Builder

	// Get status
	var status *project.ProjectStatus
	if v.proj != nil {
		status = v.proj.Status()
	}

	// Header
	s.WriteString(th.DashboardTitle.Render(" Project Overview "))
	s.WriteString("\n\n")

	if v.err != nil {
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf("Error: %v", v.err)))
		s.WriteString("\n")
		return s.String()
	}

	if status == nil {
		s.WriteString(th.StatsStyle.Render(" No project loaded"))
		s.WriteString("\n")
		s.WriteString(th.Help.Render("Press r to refresh"))
		return s.String()
	}

	// Project summary
	s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" Project: %s ", status.Name)))
	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %d repos", status.RepoCount)))

	// Status indicators
	statusParts := []string{}
	if status.DirtyCount > 0 {
		statusParts = append(statusParts, th.DashboardErrorStyle.Render(
			fmt.Sprintf("%d dirty", status.DirtyCount)))
	}
	if status.ErrorCount > 0 {
		statusParts = append(statusParts, th.DashboardErrorStyle.Render(
			fmt.Sprintf("%d error", status.ErrorCount)))
	}
	if status.DirtyCount == 0 && status.ErrorCount == 0 {
		statusParts = append(statusParts, th.DashboardAccentStyle.Render("all clean"))
	}

	if len(statusParts) > 0 {
		s.WriteString(" • ")
		for i, p := range statusParts {
			if i > 0 {
				s.WriteString(", ")
			}
			s.WriteString(p)
		}
	}
	s.WriteString("\n\n")

	// Repos header
	s.WriteString(th.StatsStyle.Render(" Repositories "))
	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")

	// Keep selected index in bounds
	if v.selectedIdx >= len(status.Repos) {
		v.selectedIdx = len(status.Repos) - 1
	}
	if v.selectedIdx < 0 && len(status.Repos) > 0 {
		v.selectedIdx = 0
	}

	// List repos
	for i, repo := range status.Repos {
		prefix := "  "
		if i == v.selectedIdx {
			prefix = th.SelectedBranchStyle.Render(" >")
		}

		// Repo name
		repoStr := fmt.Sprintf("%s%s", prefix, repo.Name)

		if i == v.selectedIdx {
			repoStr = th.SelectedBranchStyle.Render(repoStr)
		} else {
			repoStr = th.BranchStyle.Render(repoStr)
		}

		s.WriteString(repoStr)

		// Status indicator
		if repo.Error != "" {
			s.WriteString(th.DashboardErrorStyle.Render(" ✗ ERROR"))
		} else if repo.IsDirty {
			s.WriteString(th.DashboardErrorStyle.Render(" ● dirty"))
		} else {
			s.WriteString(th.DashboardAccentStyle.Render(" ✓ clean"))
		}

		s.WriteString("\n")

		// Repo details (only for selected)
		if i == v.selectedIdx {
			indent := "     "

			// Branch info
			if repo.Error == "" {
				branchStyle := lipgloss.NewStyle().Foreground(th.Accent2)
				s.WriteString(fmt.Sprintf("%s%s %s", indent, th.BranchStyle.Render("Branch:"), branchStyle.Render(repo.CurrentBranch)))

				// Dirty indicator inline
				if repo.IsDirty {
					s.WriteString(th.DashboardErrorStyle.Render(" ●"))
				}
				s.WriteString("\n")

				// HEAD
				head := repo.HEAD
				if len(head) > 7 {
					head = head[:7]
				}
				s.WriteString(fmt.Sprintf("%s%s %s\n", indent, th.BranchStyle.Render("HEAD:"), th.InfoStyle.Render(head)))

				// Path
				s.WriteString(fmt.Sprintf("%s%s %s\n", indent, th.BranchStyle.Render("Path:"), th.StatsStyle.Render(repo.Path)))

				// Arrow hint
				s.WriteString(fmt.Sprintf("%s%s\n", indent, th.Help.Render("Press Enter to open repo")))
			} else {
				// Error message
				s.WriteString(fmt.Sprintf("%s%s %s\n", indent, th.DashboardErrorStyle.Render("Error:"), repo.Error))
			}

			s.WriteString("\n")
		}
	}

	// Branch check section
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")

	if v.showBranchCheck {
		s.WriteString(th.StatsStyle.Render(" Cross-Repo Branch Check "))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render("Enter branch name: "))
		s.WriteString(th.StatsStyle.Render(v.branchCheckName))
		s.WriteString("_")
		s.WriteString("\n")
		s.WriteString(th.Help.Render("Press Enter to check, Esc to cancel"))
		s.WriteString("\n")

		// Show branch check results if we have a project and a branch name
		if v.proj != nil && v.branchCheckName != "" && v.branchExistence != nil {
			s.WriteString("\n")
			s.WriteString(th.StatsStyle.Render(fmt.Sprintf("Branch %q exists in:", v.branchCheckName)))
			s.WriteString("\n")

			for name, exists := range v.branchExistence.Repos {
				if exists {
					s.WriteString(fmt.Sprintf("  %s %s\n", th.DashboardAccentStyle.Render("✓"), th.StatsStyle.Render(name)))
				} else {
					s.WriteString(fmt.Sprintf("  %s %s\n", th.DashboardErrorStyle.Render("✗"), th.StatsStyle.Render(name)))
				}
			}
		}
	} else {
		s.WriteString(th.Help.Render("b: Check branch across repos  "))
		s.WriteString(th.Help.Render("Enter: Open repo  "))
		s.WriteString(th.Help.Render("r: Refresh"))
	}

	return s.String()
}

// ShortHelp returns a short help string.
func (v *ProjectView) ShortHelp() string {
	return "↑/k: Select  ↓/j: Next  Enter: Open repo  b: Check branch  r: Refresh"
}

// SetSize updates the view dimensions.
func (v *ProjectView) SetSize(width, height int) {
	v.width = width
	v.height = height
}

// GetProjectPath returns the project path.
func (v *ProjectView) GetProjectPath() string {
	if v.proj != nil {
		paths := v.proj.RepoPaths()
		if len(paths) > 0 {
			return paths[0]
		}
	}
	return ""
}

// Refresh reloads project data.
func (v *ProjectView) Refresh() error {
	if v.proj != nil {
		v.proj.Refresh()
	}
	return nil
}
