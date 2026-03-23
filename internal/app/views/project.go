package views

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git-frontend/internal/app/components"
	"git-frontend/internal/project"
	"git-frontend/internal/theme"

	"github.com/charmbracelet/bubbletea"
)

// ProjectView displays aggregate multi-repo project status.
type ProjectView struct {
	proj *project.Project
	// Fallback path if proj is nil
	projectPath string
	status      *project.ProjectStatus
	loading     bool
	err         error
	width       int
	height      int

	// Navigation state
	selectedIdx int

	// Branch check state
	showBranchCheck   bool
	branchCheckName   string
	branchExistence   *project.BranchExistence

	// Filter for repo list
	filter *components.Filter[project.RepoStatus]
}

// NewProjectView creates a new project view with an already-loaded project.
func NewProjectView(proj *project.Project) *ProjectView {
	v := &ProjectView{
		proj:      proj,
		width:     80,
		height:    24,
	}

	// Initialize filter
	statuses := []project.RepoStatus{}
	v.filter = components.NewFilter(statuses, v.renderRepoItem)
	v.filter.SetHeight(v.height)

	return v
}

// NewProjectViewWithPath creates a new project view with a project path.
// The project will be loaded lazily via auto-discovery.
func NewProjectViewWithPath(projectPath string) *ProjectView {
	v := &ProjectView{
		projectPath: projectPath,
		width:       80,
		height:      24,
	}

	// Initialize filter
	statuses := []project.RepoStatus{}
	v.filter = components.NewFilter(statuses, v.renderRepoItem)
	v.filter.SetHeight(v.height)

	return v
}

// SetProject updates the project reference.
func (v *ProjectView) SetProject(proj *project.Project) {
	v.proj = proj
	v.loadData()
}

// discoverProject creates a project by auto-discovering git repos in a directory
func discoverProject(dir string) *project.Project {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var repos []project.RepoDef
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		repoPath := filepath.Join(dir, entry.Name(), ".git")
		if _, err := os.Stat(repoPath); err == nil {
			// It's a git repo
			repos = append(repos, project.RepoDef{
				Name:          entry.Name(),
				Path:          filepath.Join(dir, entry.Name()),
				DefaultBranch: "main",
			})
		}
	}

	if len(repos) == 0 {
		return nil
	}

	proj := project.NewProject(project.ProjectDef{
		Name:  filepath.Base(dir),
		Repos: repos,
	})
	proj.Refresh()
	return proj
}

// Init initializes the project view.
func (v *ProjectView) Init() tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		v.loadData()
		return RefreshDoneMsg{}
	}
}

// loadData loads project data.
func (v *ProjectView) loadData() {
	v.err = nil

	// If we don't have a project yet, try to load from path
	if v.proj == nil && v.projectPath != "" {
		proj := discoverProject(v.projectPath)
		if proj == nil {
			v.err = fmt.Errorf("no git repositories found in %s", v.projectPath)
			v.loading = false
			return
		}
		v.proj = proj
	}

	if v.proj == nil {
		v.err = fmt.Errorf("no project configured")
		v.loading = false
		return
	}

	v.proj.Refresh()
	v.status = v.proj.Status()

	// Update filter with repos
	if v.status != nil {
		v.filter.SetItems(v.status.Repos)
	}

	v.loading = false
}

// renderRepoItem renders a single repo item for the filter
func (v *ProjectView) renderRepoItem(repo project.RepoStatus, index int, selected bool) string {
	var statusIcon string

	if repo.Error != "" {
		statusIcon = "✗"
	} else if repo.IsDirty {
		statusIcon = "●"
	} else {
		statusIcon = "✓"
	}

	// Build the line
	line := fmt.Sprintf(" %s %s", statusIcon, repo.Name)

	// Add branch info
	if repo.Error == "" && repo.CurrentBranch != "" {
		line += fmt.Sprintf(" %s", repo.CurrentBranch)
	}

	if repo.IsDirty {
		line += " dirty"
	}

	// Apply selection highlighting
	if selected {
		line = ">" + line
	}

	return line
}

// Update handles update events.
func (v *ProjectView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle branch check mode
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
				v.showBranchCheck = false
			case tea.KeyEsc:
				v.showBranchCheck = false
				v.branchCheckName = ""
			case tea.KeyRunes:
				v.branchCheckName += string(msg.Runes)
			}
			return v, nil
		}

		switch msg.String() {
		case "r":
			v.loading = true
			return v, func() tea.Msg {
				v.loadData()
				return RefreshDoneMsg{}
			}
		case "up", "k":
			if v.selectedIdx > 0 {
				v.selectedIdx--
				v.filter.CursorUp()
			}
		case "down", "j":
			if v.status != nil && v.selectedIdx < len(v.status.Repos)-1 {
				v.selectedIdx++
				v.filter.CursorDown()
			}
		case "enter":
			// Drill into selected repo
			if v.status != nil && v.selectedIdx < len(v.status.Repos) {
				selectedRepo := v.status.Repos[v.selectedIdx]
				// Switch to Overview view with this repo's path
				return v, func() tea.Msg {
					return ViewChangeMsg{ViewName: "Overview", RepoPath: selectedRepo.Path}
				}
			}
		case "b":
			// Toggle branch check mode
			v.showBranchCheck = true
			v.branchCheckName = ""
			v.branchExistence = nil
		case "/":
			// Activate filter
			if v.filter != nil {
				v.filter.Update(msg)
			}
		case "esc":
			// Deactivate filter if active
			if v.filter != nil && v.filter.IsActive() {
				v.filter.Update(msg)
			}
		}

		// Also pass to filter for filtering
		if v.filter != nil {
			v.filter.Update(msg)
		}

	case RefreshDoneMsg:
		v.loading = false

	case tea.MouseMsg:
		// Handle mouse events for the filter/list
		if v.filter != nil {
			if v.filter.HandleMouse(msg) {
				return v, nil
			}
		}
	}
	return v, nil
}

// View renders the project overview.
func (v *ProjectView) View() string {
	th := theme.GetTheme()

	var s strings.Builder

	// Header
	headerTitle := "Project Overview"
	if v.status != nil {
		headerTitle = fmt.Sprintf("Project: %s", v.status.Name)
	}
	s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" %s ", headerTitle)))
	s.WriteString("\n\n")

	if v.err != nil {
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf("Error: %v", v.err)))
		s.WriteString("\n")
		s.WriteString(th.Help.Render("Press r to retry"))
		return s.String()
	}

	if v.loading {
		s.WriteString(th.StatsStyle.Render(" Loading..."))
		s.WriteString("\n")
		return s.String()
	}

	if v.status == nil {
		s.WriteString(th.StatsStyle.Render(" No project loaded"))
		s.WriteString("\n")
		return s.String()
	}

	// Project summary
	s.WriteString(th.StatsStyle.Render(" Aggregate Status "))
	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")

	// Status indicators
	cleanCount := v.status.RepoCount - v.status.DirtyCount - v.status.ErrorCount
	s.WriteString(fmt.Sprintf(" %s %d clean   %s %d dirty   %s %d errors\n\n",
		th.StatsStyle.Render("✓"), cleanCount,
		th.DashboardAccentStyle.Render("●"), v.status.DirtyCount,
		th.DashboardErrorStyle.Render("✗"), v.status.ErrorCount))

	// Branch check section (shown at top when active)
	if v.showBranchCheck {
		s.WriteString(th.StatsStyle.Render(" Cross-Repo Branch Check "))
		s.WriteString("\n")
		s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render("Branch name: "))
		s.WriteString(th.InfoStyle.Render(v.branchCheckName))
		s.WriteString(th.MutedTextStyle.Render("_"))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render("Enter: Check   Esc: Cancel"))
		s.WriteString("\n\n")

		// Show branch check results
		if v.proj != nil && v.branchCheckName != "" && v.branchExistence != nil {
			s.WriteString(th.StatsStyle.Render(" Results "))
			s.WriteString("\n")

			allHave := true
			for name, exists := range v.branchExistence.Repos {
				if !exists {
					allHave = false
				}
				if exists {
					s.WriteString(fmt.Sprintf("  %s %s\n", th.DashboardAccentStyle.Render("✓"), name))
				} else {
					s.WriteString(fmt.Sprintf("  %s %s\n", th.DashboardErrorStyle.Render("✗"), name))
				}
			}

			// Summary
			s.WriteString("\n")
			if allHave {
				s.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf(" Branch '%s' exists in ALL repos ✓",
					v.branchCheckName)))
			} else {
				missingCount := 0
				for _, exists := range v.branchExistence.Repos {
					if !exists {
						missingCount++
					}
				}
				s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" Branch '%s' missing in %d/%d repos",
					v.branchCheckName, missingCount, len(v.branchExistence.Repos))))
			}
			s.WriteString("\n\n")
		}
	}

	// Repos header
	s.WriteString(th.StatsStyle.Render(" Repositories "))
	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")

	// List repos using filter view
	if v.status != nil && len(v.status.Repos) > 0 {
		s.WriteString(v.filter.View())
	} else {
		s.WriteString(th.StatsStyle.Render(" No repositories in project"))
	}

	s.WriteString("\n")

	// Footer
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")
	s.WriteString(th.Help.Render("↑↓: Select  Enter: Open repo  b: Check branch  /: Filter  r: Refresh"))

	return s.String()
}

// ShortHelp returns a short help string.
func (v *ProjectView) ShortHelp() string {
	return "↑↓: Select  Enter: Open repo  b: Check branch  /: Filter  r: Refresh"
}

// SetSize updates the view dimensions.
func (v *ProjectView) SetSize(width, height int) {
	v.width = width
	v.height = height
	if v.filter != nil {
		v.filter.SetHeight(height)
	}
}

// GetSelectedRepoPath returns the path of the currently selected repo.
func (v *ProjectView) GetSelectedRepoPath() string {
	if v.status != nil && v.selectedIdx < len(v.status.Repos) {
		return v.status.Repos[v.selectedIdx].Path
	}
	return ""
}

// Refresh reloads project data.
func (v *ProjectView) Refresh() error {
	v.loadData()
	return v.err
}

// KeyBindings returns the keybindings for this view.
func (v *ProjectView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "↑/k", Description: "Select previous repository"},
		{Key: "↓/j", Description: "Select next repository"},
		{Key: "enter", Description: "Open selected repository"},
		{Key: "b", Description: "Check if branch exists in all repos"},
		{Key: "/", Description: "Filter repositories"},
		{Key: "r", Description: "Refresh all repos"},
	}
}
