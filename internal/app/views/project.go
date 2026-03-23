package views

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"git-frontend/internal/app/components"
	"git-frontend/internal/engine"
	"git-frontend/internal/git"
	"git-frontend/internal/project"
	"git-frontend/internal/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// pushDoneMsg signals that batch push has completed.
type pushDoneMsg struct{}

// mrDoneMsg signals that batch MR creation has completed.
type mrDoneMsg struct{}

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

	// Feature workflow state
	activeWorkflow       *project.FeatureWorkflow
	showWorkflowStart    bool   // modal for entering branch name
	workflowBranchName   string // input text
	showWorkflowCleanup  bool   // confirmation modal
	workflowStatusMsg    string // flash message
	workflowBaseDir      string // default ~/.worktrees/<projectName>/
	showWorkflowStatus   bool   // toggle for status panel

	// Batch push state
	showPushConfirm bool   // confirmation for batch push
	pushResults     string // flash message with push results

	// Batch MR creation state
	showBatchMRConfirm bool   // confirmation for batch MR creation
	mrResults          string // flash message with MR results

	// Agent orchestration
	engine            *engine.Engine
	showAgentPrompt   bool   // modal for entering agent task prompt
	agentPromptText   string // the task text input
	agentSpawnResults string // flash message showing which repos got agents

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

	v.workflowBaseDir = defaultWorkflowBaseDir(proj.Name)
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

	v.workflowBaseDir = defaultWorkflowBaseDir(filepath.Base(projectPath))
	v.filter = components.NewFilter([]treeNode{}, v.renderTreeNode)
	v.filter.SetHeight(v.height)

	return v
}

// defaultWorkflowBaseDir returns ~/.worktrees/<projectName>/
func defaultWorkflowBaseDir(projectName string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	return filepath.Join(home, ".worktrees", projectName)
}

// SetProject updates the project reference.
func (v *ProjectView) SetProject(proj *project.Project) {
	v.proj = proj
	v.loadData()
}

// SetEngine sets the agent engine (allows late binding, matches AgentView pattern).
func (v *ProjectView) SetEngine(eng *engine.Engine) {
	v.engine = eng
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

// spawnAgentsIntoWorktrees spawns an agent into each repo's worktree directory.
// StartAgent returns immediately (it spawns a background process), so this is safe
// to call synchronously in the Update handler.
func (v *ProjectView) spawnAgentsIntoWorktrees(task string) {
	if v.activeWorkflow == nil || v.engine == nil {
		return
	}

	// Gather context files from the project
	var ctxFiles []string
	if v.proj != nil {
		ctxFiles = v.proj.ContextFiles
	}

	// Count how many worktrees need agents
	var needed int
	for _, wr := range v.activeWorkflow.Repos {
		if wr.WorktreeCreated {
			needed++
		}
	}

	// Check engine capacity
	stats := v.engine.Stats()
	available := stats.MaxAgents - stats.Active
	if needed > available {
		v.agentSpawnResults = fmt.Sprintf("Engine capacity exceeded: need %d agents but only %d/%d slots available",
			needed, available, stats.MaxAgents)
		return
	}

	// Sort repo names for deterministic output
	names := make([]string, 0, len(v.activeWorkflow.Repos))
	for name := range v.activeWorkflow.Repos {
		names = append(names, name)
	}
	sort.Strings(names)

	var results []string
	for _, name := range names {
		wr := v.activeWorkflow.Repos[name]
		if !wr.WorktreeCreated {
			results = append(results, fmt.Sprintf("  - %s: skipped (no worktree)", name))
			continue
		}

		id, err := v.engine.StartAgent(wr.WorktreePath, task, engine.AgentOptions{
			ContextFiles: ctxFiles,
			SmartRoute:   true,
		})
		if err != nil {
			results = append(results, fmt.Sprintf("  ✗ %s: %v", name, err))
		} else {
			v.activeWorkflow.SetAgentID(name, id)
			results = append(results, fmt.Sprintf("  ✓ %s: agent %s", name, id))
		}
	}

	v.agentSpawnResults = strings.Join(results, "\n")
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
		if v.workflowStatusMsg != "" {
			v.workflowStatusMsg = ""
		}
		if v.agentSpawnResults != "" {
			v.agentSpawnResults = ""
		}
		if v.pushResults != "" {
			v.pushResults = ""
		}
		if v.mrResults != "" {
			v.mrResults = ""
		}

		// Handle workflow start modal
		if v.showWorkflowStart {
			switch msg.String() {
			case "enter":
				if v.workflowBranchName != "" && v.proj != nil {
					branchName := v.workflowBranchName
					baseDir := v.workflowBaseDir
					v.showWorkflowStart = false
					v.workflowBranchName = ""
					wf := project.NewFeatureWorkflow(v.proj, branchName, baseDir)
					v.activeWorkflow = wf
					v.showWorkflowStatus = true
					return v, func() tea.Msg {
						wf.CreateAllWorktrees()
						return RefreshDoneMsg{}
					}
				}
				v.showWorkflowStart = false
			case "esc":
				v.showWorkflowStart = false
				v.workflowBranchName = ""
			case "backspace":
				if len(v.workflowBranchName) > 0 {
					v.workflowBranchName = v.workflowBranchName[:len(v.workflowBranchName)-1]
				}
			case "ctrl+w":
				v.workflowBranchName = components.DeleteWordEnd(v.workflowBranchName)
			default:
				if len(msg.Runes) == 1 {
					r := msg.Runes[0]
					if r >= 32 && r <= 126 {
						v.workflowBranchName += string(r)
					}
				}
			}
			return v, nil
		}

		// Handle agent prompt modal
		if v.showAgentPrompt {
			switch msg.String() {
			case "enter":
				if v.agentPromptText != "" && v.engine != nil && v.activeWorkflow != nil {
					promptText := v.agentPromptText
					v.showAgentPrompt = false
					v.agentPromptText = ""
					v.spawnAgentsIntoWorktrees(promptText)
				} else {
					v.showAgentPrompt = false
				}
			case "esc":
				v.showAgentPrompt = false
				v.agentPromptText = ""
			case "backspace":
				if len(v.agentPromptText) > 0 {
					v.agentPromptText = v.agentPromptText[:len(v.agentPromptText)-1]
				}
			case "ctrl+w":
				v.agentPromptText = components.DeleteWordEnd(v.agentPromptText)
			default:
				if len(msg.Runes) == 1 {
					r := msg.Runes[0]
					if r >= 32 && r <= 126 {
						v.agentPromptText += string(r)
					}
				}
			}
			return v, nil
		}

		// Handle workflow cleanup confirmation
		if v.showWorkflowCleanup {
			switch msg.String() {
			case "y":
				wf := v.activeWorkflow
				v.showWorkflowCleanup = false
				return v, func() tea.Msg {
					wf.RemoveAllWorktrees()
					return RefreshDoneMsg{}
				}
			case "n", "esc":
				v.showWorkflowCleanup = false
			}
			return v, nil
		}

		// Handle batch push confirmation
		if v.showPushConfirm {
			switch msg.String() {
			case "y":
				wf := v.activeWorkflow
				v.showPushConfirm = false
				return v, func() tea.Msg {
					wf.PushAll()
					return pushDoneMsg{}
				}
			case "n", "esc":
				v.showPushConfirm = false
			}
			return v, nil
		}

		// Handle batch MR creation confirmation
		if v.showBatchMRConfirm {
			switch msg.String() {
			case "y":
				wf := v.activeWorkflow
				v.showBatchMRConfirm = false
				return v, func() tea.Msg {
					wf.CreateAllMRs()
					return mrDoneMsg{}
				}
			case "n", "esc":
				v.showBatchMRConfirm = false
			}
			return v, nil
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
				result, err := git.CreateMergeRequestCLI(v.mrConfirmPath, provider)
				if err != nil {
					v.mrResult = fmt.Sprintf("MR creation failed: %v", err)
				} else {
					v.mrResult = fmt.Sprintf("MR created: %s", result)
				}
				v.showMRConfirm = false
			case "n", "esc":
				v.showMRConfirm = false
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
		case "b":
			v.showBranchCheck = true
			v.branchCheckName = ""
			v.branchExistence = nil
		case "w":
			// Start new feature workflow
			v.showWorkflowStart = true
			v.workflowBranchName = ""
		case "D":
			// Cleanup feature workflow
			if v.activeWorkflow != nil {
				v.showWorkflowCleanup = true
			}
		case "a":
			// Spawn agents into worktrees (only when workflow is active with worktrees)
			if v.activeWorkflow != nil && v.engine != nil {
				// Check that at least one worktree was created
				hasWorktree := false
				for _, wr := range v.activeWorkflow.Repos {
					if wr.WorktreeCreated {
						hasWorktree = true
						break
					}
				}
				if hasWorktree {
					v.showAgentPrompt = true
					v.agentPromptText = ""
				} else {
					v.agentSpawnResults = "No worktrees created yet -- create worktrees first"
				}
			}
		case "p":
			// Batch push all repos in the workflow
			if v.activeWorkflow == nil {
				v.pushResults = "No active workflow"
			} else {
				// Check if any worktree has been created
				hasWorktree := false
				for _, wr := range v.activeWorkflow.Repos {
					if wr.WorktreeCreated {
						hasWorktree = true
						break
					}
				}
				if !hasWorktree {
					v.pushResults = "Nothing to push - no worktrees created"
				} else {
					v.showPushConfirm = true
				}
			}
		case "M":
			// Batch create MRs for all pushed repos
			if v.activeWorkflow == nil {
				v.mrResults = "No active workflow"
			} else {
				// Check if any repo has been pushed
				hasPushed := false
				for _, wr := range v.activeWorkflow.Repos {
					if wr.Pushed {
						hasPushed = true
						break
					}
				}
				if !hasPushed {
					v.mrResults = "No repos have been pushed yet"
				} else {
					v.showBatchMRConfirm = true
				}
			}
		case "W":
			// Toggle workflow status panel
			v.showWorkflowStatus = !v.showWorkflowStatus
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
		// Generate workflow flash messages from active workflow state
		if v.activeWorkflow != nil {
			st := v.activeWorkflow.Status()
			switch st.State {
			case project.WorkflowActive:
				// Just finished creating worktrees
				var lines []string
				// Sort repo names for deterministic output
				names := make([]string, 0, len(v.activeWorkflow.Repos))
				for name := range v.activeWorkflow.Repos {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					wr := v.activeWorkflow.Repos[name]
					if wr.Error != "" {
						lines = append(lines, fmt.Sprintf("  ✗ %s: %s", name, wr.Error))
					} else if wr.WorktreeCreated {
						lines = append(lines, fmt.Sprintf("  ✓ %s: worktree created", name))
					}
				}
				if len(lines) > 0 {
					v.workflowStatusMsg = strings.Join(lines, "\n")
				}
			case project.WorkflowDone:
				// Cleanup finished
				v.workflowStatusMsg = fmt.Sprintf("Worktrees for '%s' removed", v.activeWorkflow.BranchName)
				v.activeWorkflow = nil
				v.showWorkflowStatus = false
			}
		}

	case pushDoneMsg:
		if v.activeWorkflow != nil {
			var lines []string
			names := make([]string, 0, len(v.activeWorkflow.Repos))
			for name := range v.activeWorkflow.Repos {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				wr := v.activeWorkflow.Repos[name]
				if wr.Error != "" {
					lines = append(lines, fmt.Sprintf("  ✗ %s: %s", name, wr.Error))
				} else if wr.Pushed {
					lines = append(lines, fmt.Sprintf("  ✓ %s: pushed", name))
				} else {
					lines = append(lines, fmt.Sprintf("  ⊘ %s: nothing to push", name))
				}
			}
			v.pushResults = strings.Join(lines, "\n")
		}

	case mrDoneMsg:
		if v.activeWorkflow != nil {
			var lines []string
			names := make([]string, 0, len(v.activeWorkflow.Repos))
			for name := range v.activeWorkflow.Repos {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				wr := v.activeWorkflow.Repos[name]
				if !wr.Pushed {
					lines = append(lines, fmt.Sprintf("  ⊘ %s: skipped (not pushed)", name))
				} else if wr.Error != "" {
					lines = append(lines, fmt.Sprintf("  ✗ %s: %s", name, wr.Error))
				} else if wr.MRURL != "" {
					lines = append(lines, fmt.Sprintf("  ✓ %s: %s", name, wr.MRURL))
				} else {
					lines = append(lines, fmt.Sprintf("  ⊘ %s: no MR URL returned", name))
				}
			}
			v.mrResults = strings.Join(lines, "\n")
		}

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

	// New branch creation section
	if v.showNewBranch {
		s.WriteString(th.StatsStyle.Render(" Create Branch Across All Repos "))
		s.WriteString("\n")
		s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render("Branch name: "))
		s.WriteString(th.InfoStyle.Render(v.newBranchName))
		s.WriteString(th.MutedTextStyle.Render("_"))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render("Enter: Create   Esc: Cancel"))
		s.WriteString("\n\n")
	}

	// Workflow start modal
	if v.showWorkflowStart {
		s.WriteString(th.StatsStyle.Render(" Start Feature Workflow "))
		s.WriteString("\n")
		s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render("Branch name: "))
		s.WriteString(th.InfoStyle.Render(v.workflowBranchName))
		s.WriteString(th.MutedTextStyle.Render("_"))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render("Enter: Create worktrees   Esc: Cancel   Ctrl+W: Delete word"))
		s.WriteString("\n\n")
	}

	// Agent prompt modal
	if v.showAgentPrompt {
		s.WriteString(th.StatsStyle.Render(" Spawn Agents into Worktrees "))
		s.WriteString("\n")
		s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render("Task: "))
		s.WriteString(th.InfoStyle.Render(v.agentPromptText))
		s.WriteString(th.MutedTextStyle.Render("_"))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render("Enter: Spawn agents   Esc: Cancel   Ctrl+W: Delete word"))
		s.WriteString("\n\n")
	}

	// Workflow cleanup confirmation
	if v.showWorkflowCleanup && v.activeWorkflow != nil {
		s.WriteString(th.DashboardAccentStyle.Render(" Remove all worktrees? "))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render("  Branch: "))
		s.WriteString(th.InfoStyle.Render(v.activeWorkflow.BranchName))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render(fmt.Sprintf("  Remove all worktrees for '%s'? (y/n)", v.activeWorkflow.BranchName)))
		s.WriteString("\n\n")
	}

	// Batch push confirmation
	if v.showPushConfirm && v.activeWorkflow != nil {
		s.WriteString(th.DashboardAccentStyle.Render(" Push all repos? "))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render("  Branch: "))
		s.WriteString(th.InfoStyle.Render(v.activeWorkflow.BranchName))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render(fmt.Sprintf("  Push all repos on branch '%s'? [y] Yes [n] No", v.activeWorkflow.BranchName)))
		s.WriteString("\n\n")
	}

	// Batch MR creation confirmation
	if v.showBatchMRConfirm && v.activeWorkflow != nil {
		st := v.activeWorkflow.Status()
		s.WriteString(th.DashboardAccentStyle.Render(" Create MRs/PRs? "))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render("  Branch: "))
		s.WriteString(th.InfoStyle.Render(v.activeWorkflow.BranchName))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render(fmt.Sprintf("  Create MRs/PRs for %d pushed repos? [y] Yes [n] No", st.Pushed)))
		s.WriteString("\n\n")
	}

	// Workflow status panel
	if v.showWorkflowStatus && v.activeWorkflow != nil {
		st := v.activeWorkflow.Status()
		s.WriteString(th.StatsStyle.Render(" Feature Workflow "))
		s.WriteString("\n")
		s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
		s.WriteString("\n")
		s.WriteString(th.Help.Render("  Branch: "))
		s.WriteString(th.InfoStyle.Render(st.BranchName))
		s.WriteString("\n")
		s.WriteString(th.Help.Render("  State:  "))
		s.WriteString(th.InfoStyle.Render(st.State.String()))
		s.WriteString("\n")

		// Per-repo status
		names := make([]string, 0, len(v.activeWorkflow.Repos))
		for name := range v.activeWorkflow.Repos {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			wr := v.activeWorkflow.Repos[name]
			icon := "  "
			if wr.Error != "" {
				icon = th.DashboardErrorStyle.Render("✗ ")
			} else if wr.WorktreeCreated {
				icon = th.StatsStyle.Render("✓ ")
			} else {
				icon = th.MutedTextStyle.Render("- ")
			}
			s.WriteString(fmt.Sprintf("  %s%s", icon, name))
			if wr.WorktreeCreated {
				s.WriteString(th.MutedTextStyle.Render(fmt.Sprintf("  %s", wr.WorktreePath)))
			}
			if wr.Error != "" {
				s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf("  %s", wr.Error)))
			}
			// Push status
			if wr.Pushed {
				s.WriteString("  ")
				s.WriteString(th.StatsStyle.Render("pushed"))
			}
			// MR status
			if wr.MRURL != "" {
				s.WriteString("  ")
				s.WriteString(th.DashboardAccentStyle.Render(wr.MRURL))
			}
			// Agent status
			if wr.AgentID != "" && v.engine != nil {
				agentStatus := v.renderAgentStatus(wr.AgentID)
				s.WriteString("  ")
				s.WriteString(agentStatus)
			} else if wr.WorktreeCreated && wr.AgentID == "" {
				s.WriteString("  ")
				s.WriteString(th.MutedTextStyle.Render("-"))
			}
			s.WriteString("\n")
		}
		s.WriteString("\n")
	}

	// Workflow flash message
	if v.workflowStatusMsg != "" {
		for _, line := range strings.Split(v.workflowStatusMsg, "\n") {
			if strings.Contains(line, "✓") || strings.HasPrefix(line, "Worktrees for") {
				s.WriteString(th.DashboardAccentStyle.Render(" " + line))
			} else {
				s.WriteString(th.DashboardErrorStyle.Render(" " + line))
			}
			s.WriteString("\n")
		}
		s.WriteString("\n")
	}

	// Agent spawn results flash message
	if v.agentSpawnResults != "" {
		for _, line := range strings.Split(v.agentSpawnResults, "\n") {
			if strings.Contains(line, "✓") {
				s.WriteString(th.DashboardAccentStyle.Render(" " + line))
			} else {
				s.WriteString(th.DashboardErrorStyle.Render(" " + line))
			}
			s.WriteString("\n")
		}
		s.WriteString("\n")
	}

	// Push results flash message
	if v.pushResults != "" {
		for _, line := range strings.Split(v.pushResults, "\n") {
			if strings.Contains(line, "✓") {
				s.WriteString(th.DashboardAccentStyle.Render(" " + line))
			} else if strings.Contains(line, "✗") {
				s.WriteString(th.DashboardErrorStyle.Render(" " + line))
			} else {
				s.WriteString(th.MutedTextStyle.Render(" " + line))
			}
			s.WriteString("\n")
		}
		s.WriteString("\n")
	}

	// MR results flash message
	if v.mrResults != "" {
		for _, line := range strings.Split(v.mrResults, "\n") {
			if strings.Contains(line, "✓") {
				s.WriteString(th.DashboardAccentStyle.Render(" " + line))
			} else if strings.Contains(line, "✗") {
				s.WriteString(th.DashboardErrorStyle.Render(" " + line))
			} else {
				s.WriteString(th.MutedTextStyle.Render(" " + line))
			}
			s.WriteString("\n")
		}
		s.WriteString("\n")
	}

	// MR confirmation dialog
	if v.showMRConfirm {
		s.WriteString(th.DashboardAccentStyle.Render(" Create MR/PR? "))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render("  Repo:   "))
		s.WriteString(th.InfoStyle.Render(v.mrConfirmRepo))
		s.WriteString("\n")
		s.WriteString(th.Help.Render("  Branch: "))
		s.WriteString(th.InfoStyle.Render(v.mrConfirmBranch))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render("  [y] Yes  [n] No"))
		s.WriteString("\n\n")
	}

	// Flash messages
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
	s.WriteString(th.Help.Render("↑↓: Navigate  Enter: Expand/collapse  o: Open  c: Checkout  n: New branch  m: MR/PR  b: Check  w: Workflow  a: Agents  p: Push all  M: Create MRs  D: Cleanup  W: Status  /: Filter  r: Refresh"))

	return s.String()
}

// renderAgentStatus returns a styled status string for an agent.
func (v *ProjectView) renderAgentStatus(agentID string) string {
	th := theme.GetTheme()
	if v.engine == nil {
		return th.MutedTextStyle.Render("-")
	}

	agent := v.engine.GetAgent(agentID)
	if agent == nil {
		return th.MutedTextStyle.Render("- (not found)")
	}

	snap := agent.Snapshot()
	switch snap.State {
	case engine.AgentRunning, engine.AgentStarting, engine.AgentRouting:
		elapsed := time.Since(snap.CreatedAt).Truncate(time.Second)
		return th.DashboardAccentStyle.Render(fmt.Sprintf("● %s (%s)", snap.State.String(), elapsed))
	case engine.AgentComplete:
		return th.StatsStyle.Render("✓ done")
	case engine.AgentError:
		return th.DashboardErrorStyle.Render("✗ error")
	case engine.AgentKilled:
		return th.DashboardErrorStyle.Render("✗ killed")
	default:
		return th.MutedTextStyle.Render(snap.State.String())
	}
}

// ShortHelp returns a short help string.
func (v *ProjectView) ShortHelp() string {
	return "↑↓: Navigate  Enter: Expand/collapse  o: Open  c: Checkout  n: New branch  m: MR/PR  b: Check  w: Workflow  a: Agents  p: Push all  M: Create MRs  D: Cleanup  W: Status  /: Filter  r: Refresh"
}

// CapturesInput returns true when the view is in an input mode.
func (v *ProjectView) CapturesInput() bool {
	return v.showBranchCheck || v.showNewBranch || v.showMRConfirm || v.showWorkflowStart || v.showWorkflowCleanup || v.showAgentPrompt || v.showPushConfirm || v.showBatchMRConfirm
}

// CapturesKey returns true for keys this view handles directly.
func (v *ProjectView) CapturesKey(key string) bool {
	switch key {
	case "r", "o", "b", "c", "n", "m", "w", "a", "p", "D", "M", "W", "enter", "/", "j", "k", "up", "down":
		return true
	}
	return false
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
		{Key: "c", Description: "Checkout selected branch"},
		{Key: "n", Description: "Create new branch across all repos"},
		{Key: "m", Description: "Create MR/PR for selected branch"},
		{Key: "b", Description: "Check if branch exists in all repos"},
		{Key: "w", Description: "Start feature workflow (create worktrees)"},
		{Key: "a", Description: "Spawn agents into worktrees"},
		{Key: "p", Description: "Push all repos in workflow"},
		{Key: "M", Description: "Create MRs/PRs for all pushed repos"},
		{Key: "D", Description: "Cleanup feature workflow (remove worktrees)"},
		{Key: "W", Description: "Toggle workflow status panel"},
		{Key: "/", Description: "Filter"},
		{Key: "r", Description: "Refresh all repos"},
	}
}
