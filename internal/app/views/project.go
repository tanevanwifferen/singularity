package views

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git-frontend/internal/app/components"
	"git-frontend/internal/git"
	"git-frontend/internal/project"
	"git-frontend/internal/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// treeNode represents a row in the project tree (either a repo or a branch).
type treeNode struct {
	IsRepo   bool
	RepoIdx  int
	Repo     *project.RepoStatus
	Branch   *git.BranchInfo
	RepoName string // set on branch nodes for context
}

// String returns the display name for filtering purposes.
func (n treeNode) String() string {
	if n.IsRepo {
		return n.Repo.Name
	}
	return n.Branch.Name
}

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

	// Tree state
	expanded map[int]bool // repo index -> expanded

	// Branch check state
	showBranchCheck bool
	branchCheckName string
	branchExistence *project.BranchExistence

	// Filter for tree list
	filter *components.Filter[treeNode]
}

// NewProjectView creates a new project view with an already-loaded project.
func NewProjectView(proj *project.Project) *ProjectView {
	v := &ProjectView{
		proj:     proj,
		width:    80,
		height:   24,
		expanded: make(map[int]bool),
	}

	v.filter = components.NewFilter([]treeNode{}, v.renderTreeNode)
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
		expanded:    make(map[int]bool),
	}

	v.filter = components.NewFilter([]treeNode{}, v.renderTreeNode)
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
	v.rebuildTree()
	v.loading = false
}

// rebuildTree flattens repos and their branches into the filter list.
func (v *ProjectView) rebuildTree() {
	if v.status == nil {
		return
	}

	var nodes []treeNode
	for i := range v.status.Repos {
		repo := &v.status.Repos[i]
		nodes = append(nodes, treeNode{
			IsRepo:  true,
			RepoIdx: i,
			Repo:    repo,
		})
		if v.expanded[i] {
			for j := range repo.Branches {
				nodes = append(nodes, treeNode{
					IsRepo:   false,
					RepoIdx:  i,
					Repo:     repo,
					Branch:   &repo.Branches[j],
					RepoName: repo.Name,
				})
			}
		}
	}

	v.filter.SetItems(nodes)
}

// selectedNode returns the currently selected tree node.
func (v *ProjectView) selectedNode() *treeNode {
	item, idx := v.filter.SelectedItem()
	if idx < 0 {
		return nil
	}
	return &item
}

// renderTreeNode renders a single tree item for the filter.
func (v *ProjectView) renderTreeNode(node treeNode, index int, selected bool) string {
	th := theme.GetTheme()
	var line strings.Builder

	if node.IsRepo {
		// Repo row: [▼/▶] [status] name (current-branch)
		prefix := "  "
		if selected {
			prefix = "> "
		}
		line.WriteString(prefix)

		// Expand indicator
		if v.expanded[node.RepoIdx] {
			line.WriteString("▼ ")
		} else {
			line.WriteString("▶ ")
		}

		// Status icon
		if node.Repo.Error != "" {
			line.WriteString(th.DashboardErrorStyle.Render("✗ "))
		} else if node.Repo.IsDirty {
			line.WriteString(th.DashboardAccentStyle.Render("● "))
		} else {
			line.WriteString(th.StatsStyle.Render("✓ "))
		}

		// Repo name
		if selected {
			line.WriteString(th.SelectedBranchStyle.Render(node.Repo.Name))
		} else {
			line.WriteString(th.BranchStyle.Render(node.Repo.Name))
		}

		// Current branch
		if node.Repo.CurrentBranch != "" {
			line.WriteString(th.MutedTextStyle.Render(fmt.Sprintf("  %s", node.Repo.CurrentBranch)))
		}

		// Branch count
		line.WriteString(th.MutedTextStyle.Render(fmt.Sprintf("  (%d branches)", node.Repo.BranchCount)))
	} else {
		// Branch row: indented under repo
		prefix := "     "
		if selected {
			prefix = "   > "
		}
		line.WriteString(prefix)

		// Current branch marker
		isCurrent := node.Repo.CurrentBranch == node.Branch.Name
		if isCurrent {
			line.WriteString(th.DashboardAccentStyle.Render("* "))
		} else {
			line.WriteString("  ")
		}

		// Branch name
		if selected {
			line.WriteString(th.SelectedBranchStyle.Render(node.Branch.Name))
		} else if isCurrent {
			line.WriteString(th.DashboardAccentStyle.Render(node.Branch.Name))
		} else {
			line.WriteString(th.BranchStyle.Render(node.Branch.Name))
		}

		// Ahead/behind indicators
		if node.Branch.Ahead > 0 || node.Branch.Behind > 0 {
			var ab []string
			if node.Branch.Ahead > 0 {
				ab = append(ab, fmt.Sprintf("↑%d", node.Branch.Ahead))
			}
			if node.Branch.Behind > 0 {
				ab = append(ab, fmt.Sprintf("↓%d", node.Branch.Behind))
			}
			line.WriteString(th.MutedTextStyle.Render(fmt.Sprintf("  %s", strings.Join(ab, " "))))
		}
	}

	return line.String()
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

		// If filter is active, let it handle keys
		if v.filter != nil && v.filter.IsActive() {
			v.filter.Update(msg)
			if msg.String() == "esc" {
				// filter deactivated
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
		case "enter":
			node := v.selectedNode()
			if node == nil {
				break
			}
			if node.IsRepo {
				// Toggle expand/collapse
				v.expanded[node.RepoIdx] = !v.expanded[node.RepoIdx]
				v.rebuildTree()
			} else {
				// Drill into repo overview
				return v, func() tea.Msg {
					return ViewChangeMsg{ViewName: "Overview", RepoPath: node.Repo.Path}
				}
			}
		case "o":
			// Open repo regardless of node type
			node := v.selectedNode()
			if node != nil {
				return v, func() tea.Msg {
					return ViewChangeMsg{ViewName: "Overview", RepoPath: node.Repo.Path}
				}
			}
		case "b":
			v.showBranchCheck = true
			v.branchCheckName = ""
			v.branchExistence = nil
		case "/":
			if v.filter != nil {
				v.filter.Update(msg)
			}
		}

		// Pass navigation keys to filter
		if v.filter != nil {
			v.filter.Update(msg)
		}

	case RefreshDoneMsg:
		v.loading = false

	case tea.MouseMsg:
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
	cleanCount := v.status.RepoCount - v.status.DirtyCount - v.status.ErrorCount
	s.WriteString(fmt.Sprintf(" %s %d clean   %s %d dirty   %s %d errors\n\n",
		th.StatsStyle.Render("✓"), cleanCount,
		th.DashboardAccentStyle.Render("●"), v.status.DirtyCount,
		th.DashboardErrorStyle.Render("✗"), v.status.ErrorCount))

	// Branch check section
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

	// Tree list
	if v.status != nil && len(v.status.Repos) > 0 {
		s.WriteString(v.filter.View())
	} else {
		s.WriteString(th.StatsStyle.Render(" No repositories in project"))
	}

	s.WriteString("\n")

	// Footer
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")
	s.WriteString(th.Help.Render("↑↓: Navigate  Enter: Expand/collapse  o: Open repo  b: Check branch  /: Filter  r: Refresh"))

	return s.String()
}

// ShortHelp returns a short help string.
func (v *ProjectView) ShortHelp() string {
	return "↑↓: Navigate  Enter: Expand/collapse  o: Open repo  b: Check branch  /: Filter  r: Refresh"
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
	node := v.selectedNode()
	if node != nil {
		return node.Repo.Path
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
		{Key: "↑/k", Description: "Select previous item"},
		{Key: "↓/j", Description: "Select next item"},
		{Key: "enter", Description: "Expand/collapse repo"},
		{Key: "o", Description: "Open selected repository"},
		{Key: "b", Description: "Check if branch exists in all repos"},
		{Key: "/", Description: "Filter"},
		{Key: "r", Description: "Refresh all repos"},
	}
}
