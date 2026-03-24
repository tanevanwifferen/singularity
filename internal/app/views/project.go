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

	// New branch creation state
	showNewBranch   bool
	newBranchName   string
	newBranchResult string // flash message for results

	// MR creation state
	showMRConfirm   bool
	mrConfirmRepo   string // repo name for confirmation
	mrConfirmPath   string // repo path
	mrConfirmBranch string // branch name
	mrResult        string // flash message for MR result

	// Reset-to-main confirmation state
	showResetAllConfirm bool
	resetAllResult      string

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

		// Ahead branch count
		aheadCount := 0
		for _, b := range node.Repo.Branches {
			if b.Ahead > 0 {
				aheadCount++
			}
		}
		if aheadCount > 0 {
			line.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf("  %d ahead", aheadCount)))
		}
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
			line.WriteString(th.DashboardAccentStyle.Render("⚡"))
		} else {
			line.WriteString("  ")
		}

		// Branch name with color based on status
		if selected {
			line.WriteString(th.SelectedBranchStyle.Render(node.Branch.Name))
		} else if isCurrent {
			line.WriteString(th.DashboardAccentStyle.Render(node.Branch.Name))
		} else if node.Branch.Ahead > 0 {
			line.WriteString(th.StatsStyle.Render(node.Branch.Name))
		} else if node.Branch.Behind > 0 {
			line.WriteString(th.DashboardErrorStyle.Render(node.Branch.Name))
		} else {
			line.WriteString(th.BranchStyle.Render(node.Branch.Name))
		}

		// Status icons after branch name
		hasUpstream := node.Branch.Upstream != ""
		if node.Branch.Ahead > 0 && node.Branch.Behind > 0 {
			line.WriteString("  ")
			line.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf("↑%d↓%d", node.Branch.Ahead, node.Branch.Behind)))
		} else if node.Branch.Ahead > 0 {
			line.WriteString("  ")
			line.WriteString(th.StatsStyle.Render(fmt.Sprintf("↑%d", node.Branch.Ahead)))
		} else if node.Branch.Behind > 0 {
			line.WriteString("  ")
			line.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf("↓%d", node.Branch.Behind)))
		} else if hasUpstream {
			line.WriteString("  ")
			line.WriteString(th.StatsStyle.Render("✓"))
		} else {
			line.WriteString("  ")
			line.WriteString(th.MutedTextStyle.Render("⊘"))
		}
	}

	return line.String()
}

// Update handles update events.
func (v *ProjectView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Clear flash messages on any key press
		if v.newBranchResult != "" {
			v.newBranchResult = ""
		}
		if v.mrResult != "" {
			v.mrResult = ""
		}
		if v.resetAllResult != "" {
			v.resetAllResult = ""
		}

		// Handle new branch creation mode
		if v.showNewBranch {
			switch msg.Type {
			case tea.KeyBackspace:
				if len(v.newBranchName) > 0 {
					v.newBranchName = v.newBranchName[:len(v.newBranchName)-1]
				}
			case tea.KeyEnter:
				if v.newBranchName != "" && v.proj != nil {
					var results []string
					for _, repo := range v.proj.Repos {
						err := git.CreateBranch(repo.Path, v.newBranchName, repo.DefaultBranch)
						if err != nil {
							results = append(results, fmt.Sprintf("✗ %s: %v", repo.Name, err))
						} else {
							results = append(results, fmt.Sprintf("✓ %s: created '%s'", repo.Name, v.newBranchName))
						}
					}
					v.newBranchResult = strings.Join(results, "\n")
					v.loadData()
				}
				v.showNewBranch = false
			case tea.KeyEsc:
				v.showNewBranch = false
				v.newBranchName = ""
			case tea.KeyRunes:
				v.newBranchName += string(msg.Runes)
			}
			return v, nil
		}

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

		// Handle MR confirmation mode
		if v.showMRConfirm {
			switch msg.String() {
			case "y", "enter":
				provider := git.DetectRemoteProvider(v.mrConfirmPath)
				baseBranch := "main"
				if v.proj != nil {
					for _, r := range v.proj.Repos {
						if r.Path == v.mrConfirmPath && r.DefaultBranch != "" {
							baseBranch = r.DefaultBranch
							break
						}
					}
				}
				result, err := git.CreateMergeRequestCLI(v.mrConfirmPath, provider, baseBranch)
				if err != nil {
					v.mrResult = fmt.Sprintf("MR creation failed: %v", err)
				} else {
					v.mrResult = fmt.Sprintf("MR created: %s", result.URL)
				}
				v.showMRConfirm = false
			case "n", "esc":
				v.showMRConfirm = false
			}
			return v, nil
		}

		// Handle reset-all confirmation mode
		if v.showResetAllConfirm {
			switch msg.String() {
			case "y", "enter":
				var results []string
				if v.proj != nil {
					for _, repo := range v.proj.Repos {
						err := git.ResetRepoToMain(repo.Path, repo.DefaultBranch)
						if err != nil {
							results = append(results, fmt.Sprintf("✗ %s: %v", repo.Name, err))
						} else {
							results = append(results, fmt.Sprintf("✓ %s: reset to origin/%s", repo.Name, repo.DefaultBranch))
						}
					}
				}
				v.resetAllResult = strings.Join(results, "\n")
				v.showResetAllConfirm = false
				v.loadData()
			case "n", "esc":
				v.showResetAllConfirm = false
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
		case "c":
			// Checkout the selected branch
			node := v.selectedNode()
			if node != nil && !node.IsRepo && node.Branch != nil {
				err := git.Checkout(node.Repo.Path, node.Branch.Name)
				if err != nil {
					v.newBranchResult = fmt.Sprintf("✗ %s: %v", node.Repo.Name, err)
				} else {
					v.newBranchResult = fmt.Sprintf("✓ %s: checked out '%s'", node.Repo.Name, node.Branch.Name)
				}
				v.loadData()
			}
		case "n":
			// Enter new branch creation mode
			v.showNewBranch = true
			v.newBranchName = ""
		case "m":
			// Create MR/PR for the selected branch
			node := v.selectedNode()
			if node != nil && !node.IsRepo && node.Branch != nil {
				defaultBranch := node.Repo.DefaultBranch
				if node.Branch.Name == defaultBranch {
					v.mrResult = fmt.Sprintf("Cannot create MR from default branch '%s'", defaultBranch)
				} else {
					v.showMRConfirm = true
					v.mrConfirmRepo = node.Repo.Name
					v.mrConfirmPath = node.Repo.Path
					v.mrConfirmBranch = node.Branch.Name
				}
			}
		case "X":
			// Prompt to reset all repos to their default branch
			v.showResetAllConfirm = true
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

	// Branch check modal
	if v.showBranchCheck {
		var lines []string
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("  Branch name: %s%s", th.InfoStyle.Render(v.branchCheckName), th.MutedTextStyle.Render("_")))
		lines = append(lines, "")

		if v.proj != nil && v.branchCheckName != "" && v.branchExistence != nil {
			allHave := true
			for name, exists := range v.branchExistence.Repos {
				if !exists {
					allHave = false
				}
				if exists {
					lines = append(lines, fmt.Sprintf("  %s %s", th.DashboardAccentStyle.Render("✓"), name))
				} else {
					lines = append(lines, fmt.Sprintf("  %s %s", th.DashboardErrorStyle.Render("✗"), name))
				}
			}
			lines = append(lines, "")
			if allHave {
				lines = append(lines, th.DashboardAccentStyle.Render(fmt.Sprintf("  Branch '%s' exists in ALL repos", v.branchCheckName)))
			} else {
				missingCount := 0
				for _, exists := range v.branchExistence.Repos {
					if !exists {
						missingCount++
					}
				}
				lines = append(lines, th.DashboardErrorStyle.Render(fmt.Sprintf("  Branch '%s' missing in %d/%d repos",
					v.branchCheckName, missingCount, len(v.branchExistence.Repos))))
			}
		}
		lines = append(lines, "")
		lines = append(lines, "  Enter: Check  Esc: Cancel")
		s.WriteString(renderModal("Cross-Repo Branch Check", lines, modalWidth(v.width)))
		s.WriteString("\n")
	}

	// New branch creation modal
	if v.showNewBranch {
		lines := []string{
			"",
			fmt.Sprintf("  Branch name: %s%s", th.InfoStyle.Render(v.newBranchName), th.MutedTextStyle.Render("_")),
			"",
			"  Creates the branch in all project repos",
			fmt.Sprintf("  from each repo's default branch."),
			"",
			"  Enter: Create  Esc: Cancel",
		}
		s.WriteString(renderModal("Create Branch Across All Repos", lines, modalWidth(v.width)))
		s.WriteString("\n")
	}

	// MR confirmation modal (single repo)
	if v.showMRConfirm {
		lines := []string{
			"",
			fmt.Sprintf("  Repo:   %s", th.InfoStyle.Render(v.mrConfirmRepo)),
			fmt.Sprintf("  Branch: %s", th.InfoStyle.Render(v.mrConfirmBranch)),
			"",
			"  y: Yes  n: No",
		}
		s.WriteString(renderModal("Create MR/PR", lines, modalWidth(v.width)))
		s.WriteString("\n")
	}

	// Reset-all confirmation modal
	if v.showResetAllConfirm {
		repoCount := 0
		if v.proj != nil {
			repoCount = len(v.proj.Repos)
		}
		lines := []string{
			"",
			th.DashboardErrorStyle.Render("  WARNING: This will discard ALL local changes,"),
			th.DashboardErrorStyle.Render("  commits, and untracked files in every repo."),
			"",
			fmt.Sprintf("  Repos affected: %d", repoCount),
			"",
			"  Each repo will be:  fetch + reset --hard + clean -fd",
			"",
			"  y: Yes, reset everything  n/Esc: Cancel",
		}
		s.WriteString(renderModal("Reset ALL Repos to Main", lines, modalWidth(v.width)))
		s.WriteString("\n")
	}

	// Flash messages for branch/MR results
	if v.newBranchResult != "" {
		for _, line := range strings.Split(v.newBranchResult, "\n") {
			if strings.HasPrefix(line, "✓") {
				s.WriteString(th.DashboardAccentStyle.Render(" " + line))
			} else {
				s.WriteString(th.DashboardErrorStyle.Render(" " + line))
			}
			s.WriteString("\n")
		}
		s.WriteString("\n")
	}
	if v.mrResult != "" {
		if strings.Contains(v.mrResult, "failed") || strings.Contains(v.mrResult, "Cannot") {
			s.WriteString(th.DashboardErrorStyle.Render(v.mrResult))
		} else {
			s.WriteString(th.DashboardAccentStyle.Render(v.mrResult))
		}
		s.WriteString("\n\n")
	}
	if v.resetAllResult != "" {
		for _, line := range strings.Split(v.resetAllResult, "\n") {
			if strings.HasPrefix(line, "✓") {
				s.WriteString(th.DashboardAccentStyle.Render(" " + line))
			} else {
				s.WriteString(th.DashboardErrorStyle.Render(" " + line))
			}
			s.WriteString("\n")
		}
		s.WriteString("\n")
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
	s.WriteString(th.Help.Render("⚡current  ✓synced  ↑ahead  ↓behind  ⊘no remote  ●dirty  ✗error"))
	s.WriteString("\n")
	s.WriteString(v.renderFooterHelp())

	return s.String()
}

// renderFooterHelp returns contextual help text based on current state.
func (v *ProjectView) renderFooterHelp() string {
	th := theme.GetTheme()

	if v.CapturesInput() {
		return th.Help.Render("Enter: Confirm  Esc: Cancel")
	}

	return th.Help.Render(" ↑↓ Navigate  Enter Expand  o Open  c Checkout  n Branch  m MR  b Check  / Filter  r Refresh  X Reset All")
}

// ShortHelp returns a contextual short help string.
func (v *ProjectView) ShortHelp() string {
	if v.CapturesInput() {
		return "Enter: Confirm  Esc: Cancel"
	}
	return "↑↓ Navigate  Enter Expand  o Open  c Checkout  n Branch  m MR  b Check  / Filter  r Refresh  X Reset All"
}

// CapturesInput returns true when the view is in an input mode.
func (v *ProjectView) CapturesInput() bool {
	return v.showBranchCheck || v.showNewBranch || v.showMRConfirm || v.showResetAllConfirm
}

// CapturesKey returns true for keys this view handles directly.
func (v *ProjectView) CapturesKey(key string) bool {
	switch key {
	case "r", "o", "b", "c", "n", "m", "X", "enter", "/", "j", "k", "up", "down":
		return true
	}
	return false
}

// SetSize updates the view dimensions.
func (v *ProjectView) SetSize(width, height int) {
	v.width = width
	v.height = height
	if v.filter != nil {
		// Fixed overhead: title(1) + blank(1) + summary(1) + blank(1) + repos header(2) + footer(3)
		available := height - 10
		if available < 4 {
			available = 4
		}
		v.filter.SetHeight(available)
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
		{Key: "c", Description: "Checkout selected branch"},
		{Key: "n", Description: "Create new branch across all repos"},
		{Key: "m", Description: "Create MR/PR for selected branch"},
		{Key: "b", Description: "Check if branch exists in all repos"},
		{Key: "/", Description: "Filter"},
		{Key: "r", Description: "Refresh all repos"},
		{Key: "X", Description: "Reset ALL repos to main (destructive)"},
	}
}
