package views

import (
	"fmt"
	"os"
	"path/filepath"
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

	// Feature workflow state (multi-workflow)
	workflows            []*project.FeatureWorkflow
	selectedWorkflow     int    // index into workflows slice
	showWorkflowStart    bool   // modal for entering branch name
	workflowBranchName   string // input text
	showWorkflowCleanup  bool   // confirmation modal
	workflowStatusMsg    string // flash message
	workflowBaseDir      string // default ~/.worktrees/<projectName>/

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

	// Auto-refresh tick for live agent status
	workflowTicking bool // true when we have an active tick loop

	// Cached workflow agent snapshot for rendering (updated on tick)
	workflowAgentSnap *engine.AgentSnapshot

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

// HasActiveWorkflow returns true if any feature workflows exist.
func (v *ProjectView) HasActiveWorkflow() bool {
	return len(v.workflows) > 0
}

// currentWorkflow returns the currently selected workflow, or nil if none.
func (v *ProjectView) currentWorkflow() *project.FeatureWorkflow {
	if len(v.workflows) == 0 || v.selectedWorkflow >= len(v.workflows) {
		return nil
	}
	return v.workflows[v.selectedWorkflow]
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

// spawnAgentForWorkflow spawns a single agent at the workflow's BaseDir.
// The BaseDir contains all repo worktrees as subdirectories, so the agent
// can see all repos. StartAgent returns immediately (it spawns a background
// process), so this is safe to call synchronously in the Update handler.
func (v *ProjectView) spawnAgentForWorkflow(task string) {
	wf := v.currentWorkflow()
	if wf == nil || v.engine == nil {
		return
	}

	// Gather context files from the project
	var ctxFiles []string
	if v.proj != nil {
		ctxFiles = v.proj.ContextFiles
	}

	// Check engine capacity (just need 1 slot)
	stats := v.engine.Stats()
	available := stats.MaxAgents - stats.Active
	if available < 1 {
		v.workflowStatusMsg = fmt.Sprintf("Engine capacity exceeded: no slots available (%d/%d active)",
			stats.Active, stats.MaxAgents)
		return
	}

	// Spawn a single agent at the workflow directory (contains all repo worktrees)
	id, err := v.engine.StartAgent(wf.WorkflowDir(), task, engine.AgentOptions{
		ContextFiles: ctxFiles,
		SmartRoute:   true,
	})
	if err != nil {
		v.workflowStatusMsg = fmt.Sprintf("Agent spawn failed: %v", err)
	} else {
		wf.SetWorkflowAgentID(id)
		v.workflowStatusMsg = fmt.Sprintf(" Agent spawned for '%s'\n   Next: press F5 for Agent view, or 'p' to push when ready", wf.BranchName)
	}
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

		// Inline workflow indicators (worktree, agent, push, MR)
		line.WriteString(v.repoWorkflowIndicators(node.Repo.Name))
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
					v.workflows = append(v.workflows, wf)
					v.selectedWorkflow = len(v.workflows) - 1
					v.recalcFilterHeight()
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
				if v.agentPromptText != "" && v.engine != nil && v.currentWorkflow() != nil {
					promptText := v.agentPromptText
					v.showAgentPrompt = false
					v.agentPromptText = ""
					v.spawnAgentForWorkflow(promptText)
					v.refreshWorkflowAgentSnap()
					return v, v.ensureWorkflowTick()
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
				wf := v.currentWorkflow()
				v.showWorkflowCleanup = false
				if wf == nil {
					return v, nil
				}
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
				wf := v.currentWorkflow()
				v.showPushConfirm = false
				if wf == nil {
					return v, nil
				}
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
				wf := v.currentWorkflow()
				v.showBatchMRConfirm = false
				if wf == nil {
					return v, nil
				}
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
			// Cleanup selected workflow
			wf := v.currentWorkflow()
			if wf != nil {
				v.showWorkflowCleanup = true
			}
		case "a":
			// Spawn agent for selected workflow (only when workflow has worktrees)
			wf := v.currentWorkflow()
			if wf != nil && v.engine != nil {
				// Check that at least one worktree was created
				hasWorktree := false
				for _, wr := range wf.Repos {
					if wr.WorktreeCreated {
						hasWorktree = true
						break
					}
				}
				if hasWorktree {
					v.showAgentPrompt = true
					v.agentPromptText = ""
				} else {
					v.workflowStatusMsg = "No worktrees created yet -- create worktrees first"
				}
			}
		case "p":
			// Batch push all repos in the selected workflow
			wf := v.currentWorkflow()
			if wf == nil {
				v.pushResults = "No active workflow"
			} else {
				// Check if any worktree has been created
				hasWorktree := false
				for _, wr := range wf.Repos {
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
			// Batch create MRs for all pushed repos in selected workflow
			wf := v.currentWorkflow()
			if wf == nil {
				v.mrResults = "No active workflow"
			} else {
				// Check if any repo has been pushed
				hasPushed := false
				for _, wr := range wf.Repos {
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
		case "[":
			// Switch to previous workflow
			if len(v.workflows) > 1 && v.selectedWorkflow > 0 {
				v.selectedWorkflow--
				v.refreshWorkflowAgentSnap()
			}
		case "]":
			// Switch to next workflow
			if len(v.workflows) > 1 && v.selectedWorkflow < len(v.workflows)-1 {
				v.selectedWorkflow++
				v.refreshWorkflowAgentSnap()
			}
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
		// Refresh agent snapshot when workflow is active
		v.refreshWorkflowAgentSnap()
		// Generate workflow flash messages from current workflow state
		wf := v.currentWorkflow()
		if wf != nil {
			st := wf.Status()
			switch st.State {
			case project.WorkflowActive:
				// Just finished creating worktrees
				created := 0
				for _, wr := range wf.Repos {
					if wr.WorktreeCreated {
						created++
					}
				}
				v.workflowStatusMsg = fmt.Sprintf(" Worktrees created for '%s' across %d repos\n   Next: press 'a' to spawn an agent, or start working in the worktrees", wf.BranchName, created)
			case project.WorkflowDone:
				// Cleanup finished -- remove this workflow from the slice
				v.workflowStatusMsg = fmt.Sprintf("Worktrees for '%s' removed", wf.BranchName)
				v.removeCurrentWorkflow()
			}
		}

	case pushDoneMsg:
		wf := v.currentWorkflow()
		if wf != nil {
			pushed := 0
			total := len(wf.Repos)
			for _, wr := range wf.Repos {
				if wr.Pushed {
					pushed++
				}
			}
			v.pushResults = fmt.Sprintf(" Pushed %d/%d repos\n   Next: press 'M' to create merge requests", pushed, total)
		}

	case mrDoneMsg:
		wf := v.currentWorkflow()
		if wf != nil {
			created := 0
			for _, wr := range wf.Repos {
				if wr.MRURL != "" {
					created++
				}
			}
			v.mrResults = fmt.Sprintf(" Created %d MRs\n   Next: press 'D' to cleanup worktrees when merged", created)
		}

	case WorkflowTickMsg:
		// Refresh agent snapshot without full repo refresh
		v.refreshWorkflowAgentSnap()
		if v.hasRunningAgents() {
			return v, v.workflowTickCmd()
		}
		// No more running agents, stop ticking
		v.workflowTicking = false
		return v, nil

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
		s.WriteString(renderModal("Cross-Repo Branch Check", lines, v.modalWidth()))
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
		s.WriteString(renderModal("Create Branch Across All Repos", lines, v.modalWidth()))
		s.WriteString("\n")
	}

	// Workflow start modal
	if v.showWorkflowStart {
		lines := []string{
			"",
			fmt.Sprintf("  Branch name: %s%s", th.InfoStyle.Render(v.workflowBranchName), th.MutedTextStyle.Render("_")),
			"",
			"  This creates worktrees for all repos in the",
			fmt.Sprintf("  project under %s/<branch>/", v.workflowBaseDir),
			"",
			"  Enter: Create  Esc: Cancel",
		}
		s.WriteString(renderModal("Start Feature Workflow", lines, v.modalWidth()))
		s.WriteString("\n")
	}

	// Agent prompt modal
	if v.showAgentPrompt {
		wf := v.currentWorkflow()
		wfName := ""
		wfDir := ""
		if wf != nil {
			wfName = wf.BranchName
			wfDir = wf.WorkflowDir()
		}
		lines := []string{
			"",
			fmt.Sprintf("  Workflow: %s", th.InfoStyle.Render(wfName)),
			fmt.Sprintf("  Working dir: %s", th.MutedTextStyle.Render(wfDir)),
			"",
			fmt.Sprintf("  Task: %s%s", th.InfoStyle.Render(v.agentPromptText), th.MutedTextStyle.Render("_")),
			"",
			"  The agent will work across all repo worktrees.",
			"",
			"  Enter: Spawn  Esc: Cancel",
		}
		s.WriteString(renderModal("Spawn Agent", lines, v.modalWidth()))
		s.WriteString("\n")
	}

	// Workflow cleanup confirmation modal
	if v.showWorkflowCleanup {
		wf := v.currentWorkflow()
		if wf != nil {
			lines := []string{
				"",
				fmt.Sprintf("  Branch: %s", th.InfoStyle.Render(wf.BranchName)),
				fmt.Sprintf("  This will remove all worktrees for '%s'.", wf.BranchName),
				"",
				"  y: Confirm  Esc: Cancel",
			}
			s.WriteString(renderModal("Remove Worktrees", lines, v.modalWidth()))
			s.WriteString("\n")
		}
	}

	// Batch push confirmation modal
	if v.showPushConfirm {
		wf := v.currentWorkflow()
		if wf != nil {
			lines := []string{
				"",
				fmt.Sprintf("  Branch: %s", th.InfoStyle.Render(wf.BranchName)),
				fmt.Sprintf("  Push all repos on branch '%s'.", wf.BranchName),
				"",
				"  y: Confirm  n: Cancel",
			}
			s.WriteString(renderModal("Push All Repos", lines, v.modalWidth()))
			s.WriteString("\n")
		}
	}

	// Batch MR creation confirmation modal
	if v.showBatchMRConfirm {
		wf := v.currentWorkflow()
		if wf != nil {
			st := wf.Status()
			lines := []string{
				"",
				fmt.Sprintf("  Branch: %s", th.InfoStyle.Render(wf.BranchName)),
				fmt.Sprintf("  Create MRs/PRs for %d pushed repos.", st.Pushed),
				"",
				"  y: Confirm  n: Cancel",
			}
			s.WriteString(renderModal("Create MRs/PRs", lines, v.modalWidth()))
			s.WriteString("\n")
		}
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
		s.WriteString(renderModal("Create MR/PR", lines, v.modalWidth()))
		s.WriteString("\n")
	}

	// Compact workflow summary (always shown when workflows exist)
	s.WriteString(v.renderWorkflowSummary())

	// Flash messages (workflow, push, MR results)
	flashMsg := v.workflowStatusMsg
	if flashMsg == "" {
		flashMsg = v.pushResults
	}
	if flashMsg == "" {
		flashMsg = v.mrResults
	}
	if flashMsg != "" {
		for _, line := range strings.Split(flashMsg, "\n") {
			if strings.Contains(line, "Next:") {
				s.WriteString(th.MutedTextStyle.Render(" " + line))
			} else if strings.Contains(line, "✗") || strings.Contains(line, "failed") || strings.Contains(line, "error") {
				s.WriteString(th.DashboardErrorStyle.Render(" " + line))
			} else {
				s.WriteString(th.DashboardAccentStyle.Render(" " + line))
			}
			s.WriteString("\n")
		}
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


// workflowTickCmd returns a tick command for live agent status refresh.
func (v *ProjectView) workflowTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return WorkflowTickMsg{}
	})
}

// WorkflowTickMsg is sent periodically to refresh workflow agent status.
type WorkflowTickMsg struct{}

// hasRunningAgents checks if any workflow has an agent that is currently running.
func (v *ProjectView) hasRunningAgents() bool {
	if v.engine == nil {
		return false
	}
	for _, wf := range v.workflows {
		agentID := wf.GetWorkflowAgentID()
		if agentID == "" {
			continue
		}
		agent := v.engine.GetAgent(agentID)
		if agent == nil {
			continue
		}
		snap := agent.Snapshot()
		if snap.State == engine.AgentRunning || snap.State == engine.AgentStarting || snap.State == engine.AgentRouting {
			return true
		}
	}
	return false
}

// refreshWorkflowAgentSnap updates the cached agent snapshot for the current workflow.
func (v *ProjectView) refreshWorkflowAgentSnap() {
	wf := v.currentWorkflow()
	if wf == nil || v.engine == nil {
		v.workflowAgentSnap = nil
		return
	}
	agentID := wf.GetWorkflowAgentID()
	if agentID == "" {
		v.workflowAgentSnap = nil
		return
	}
	agent := v.engine.GetAgent(agentID)
	if agent == nil {
		v.workflowAgentSnap = nil
		return
	}
	s := agent.Snapshot()
	v.workflowAgentSnap = &s
}

// removeCurrentWorkflow removes the currently selected workflow from the slice and adjusts selection.
func (v *ProjectView) removeCurrentWorkflow() {
	if len(v.workflows) == 0 {
		return
	}
	idx := v.selectedWorkflow
	v.workflows = append(v.workflows[:idx], v.workflows[idx+1:]...)
	if v.selectedWorkflow >= len(v.workflows) && v.selectedWorkflow > 0 {
		v.selectedWorkflow--
	}
	if len(v.workflows) == 0 {
		v.workflowAgentSnap = nil
	}
	v.recalcFilterHeight()
}

// ensureWorkflowTick starts the tick loop if agents are running and we aren't already ticking.
func (v *ProjectView) ensureWorkflowTick() tea.Cmd {
	if !v.workflowTicking && v.hasRunningAgents() {
		v.workflowTicking = true
		return v.workflowTickCmd()
	}
	return nil
}

// repoWorkflowIndicators returns inline status indicators for a repo row when a workflow is active.
func (v *ProjectView) repoWorkflowIndicators(repoName string) string {
	wf := v.currentWorkflow()
	if wf == nil {
		return ""
	}
	wr, ok := wf.Repos[repoName]
	if !ok {
		return ""
	}

	th := theme.GetTheme()
	var parts []string

	// Worktree indicator
	if wr.WorktreeCreated {
		parts = append(parts, th.BranchStyle.Render("W"))
	}

	// Push indicator
	if wr.Pushed {
		parts = append(parts, th.StatsStyle.Render("↑"))
	}

	// MR indicator
	if wr.MRURL != "" {
		parts = append(parts, th.DashboardAccentStyle.Render("MR"))
	}

	if len(parts) == 0 {
		return ""
	}
	return "  " + strings.Join(parts, " ")
}

// renderWorkflowSummary renders a compact one-line-per-workflow summary.
// Always shown when workflows exist, no toggle needed.
func (v *ProjectView) renderWorkflowSummary() string {
	if len(v.workflows) == 0 {
		return ""
	}

	th := theme.GetTheme()
	var s strings.Builder

	// Header
	headerW := v.width - 4
	if headerW < 40 {
		headerW = 40
	}
	headerText := " Workflows "
	dashCount := headerW - len(headerText)
	if dashCount < 0 {
		dashCount = 0
	}
	s.WriteString(th.MutedTextStyle.Render(fmt.Sprintf(" ── %s%s", headerText, strings.Repeat("─", dashCount))))
	s.WriteString("\n")

	for i, wf := range v.workflows {
		st := wf.Status()

		var line strings.Builder

		// Selection indicator
		if i == v.selectedWorkflow {
			line.WriteString(" ")
			line.WriteString(th.DashboardAccentStyle.Render("►"))
			line.WriteString(" ")
		} else {
			line.WriteString("   ")
		}

		// Branch name
		if i == v.selectedWorkflow {
			line.WriteString(th.DashboardAccentStyle.Render(st.BranchName))
		} else {
			line.WriteString(th.BranchStyle.Render(st.BranchName))
		}

		// State indicator
		switch st.State {
		case project.WorkflowActive:
			line.WriteString(th.DashboardAccentStyle.Render("  ● active"))
		case project.WorkflowDone:
			line.WriteString(th.StatsStyle.Render("  ✓ done"))
		case project.WorkflowInitializing:
			line.WriteString(th.MutedTextStyle.Render("  … init"))
		case project.WorkflowPushingAll:
			line.WriteString(th.DashboardAccentStyle.Render("  ↑ pushing"))
		case project.WorkflowCreatingMRs:
			line.WriteString(th.DashboardAccentStyle.Render("  MR creating"))
		case project.WorkflowCleaningUp:
			line.WriteString(th.MutedTextStyle.Render("  … cleanup"))
		}

		// Agent status
		agentID := wf.GetWorkflowAgentID()
		if agentID != "" && v.engine != nil {
			agent := v.engine.GetAgent(agentID)
			if agent != nil {
				snap := agent.Snapshot()
				switch snap.State {
				case engine.AgentRunning, engine.AgentStarting, engine.AgentRouting:
					line.WriteString(th.DashboardAccentStyle.Render("  agent running"))
				case engine.AgentComplete:
					line.WriteString(th.StatsStyle.Render("  agent done"))
				case engine.AgentError, engine.AgentKilled:
					line.WriteString(th.DashboardErrorStyle.Render("  agent failed"))
				}
			}
		}

		// Repo count
		line.WriteString(th.MutedTextStyle.Render(fmt.Sprintf("  %d repos", st.TotalRepos)))

		// Push indicator
		if st.Pushed > 0 {
			line.WriteString(th.StatsStyle.Render(fmt.Sprintf("  ↑ pushed %d", st.Pushed)))
		}

		// MR indicator
		if st.MRsCreated > 0 {
			line.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf("  MR:%d", st.MRsCreated)))
		}

		s.WriteString(line.String())
		s.WriteString("\n")
	}
	s.WriteString("\n")

	return s.String()
}

// workflowSummaryHeight returns the number of lines the workflow summary occupies.
func (v *ProjectView) workflowSummaryHeight() int {
	if len(v.workflows) == 0 {
		return 0
	}
	// 1 (header) + N (per-workflow line) + 1 (blank separator)
	return 2 + len(v.workflows)
}

// renderModal renders content inside a box-drawing border.
func renderModal(title string, lines []string, width int) string {
	if width < 20 {
		width = 20
	}
	innerW := width - 4 // account for " | " + "|"

	var s strings.Builder

	// Top border
	titlePart := fmt.Sprintf(" %s ", title)
	dashCount := innerW - len(titlePart)
	if dashCount < 0 {
		dashCount = 0
	}
	s.WriteString(fmt.Sprintf(" ╭─%s%s╮\n", titlePart, strings.Repeat("─", dashCount)))

	// Content lines
	for _, line := range lines {
		// Pad line to inner width (approximate, since line may contain ANSI)
		s.WriteString(fmt.Sprintf(" │ %-*s│\n", innerW, line))
	}

	// Bottom border
	s.WriteString(fmt.Sprintf(" ╰%s╯\n", strings.Repeat("─", innerW+2)))

	return s.String()
}

// modalWidth returns the width for modal dialogs.
func (v *ProjectView) modalWidth() int {
	w := v.width - 2
	if w < 50 {
		w = 50
	}
	if w > 72 {
		w = 72
	}
	return w
}

// renderFooterHelp returns contextual help text based on current state.
func (v *ProjectView) renderFooterHelp() string {
	th := theme.GetTheme()

	// During modal: show only modal keys
	if v.CapturesInput() {
		return th.Help.Render("Enter: Confirm  Esc: Cancel")
	}

	// Base navigation keys always shown
	line1 := " ↑↓ Navigate  Enter Expand  o Open  c Checkout  n Branch  m MR  b Check  / Filter  r Refresh"

	if len(v.workflows) > 0 {
		// Workflows active: show workflow-specific keys
		wf := v.currentWorkflow()
		wfLabel := ""
		if wf != nil {
			wfLabel = wf.BranchName
		}
		idx := fmt.Sprintf("[%d/%d]", v.selectedWorkflow+1, len(v.workflows))
		line2 := fmt.Sprintf(" Workflow: %s %s  a Agent  p Push  M Create MRs  D Cleanup  [/] Switch  w New", wfLabel, idx)
		return th.Help.Render(line1) + "\n" + th.Help.Render(line2)
	}

	// No workflows: show workflow start hint
	line2 := " w Start Feature Workflow"
	return th.Help.Render(line1) + "\n" + th.Help.Render(line2)
}

// ShortHelp returns a contextual short help string.
func (v *ProjectView) ShortHelp() string {
	if v.CapturesInput() {
		return "Enter: Confirm  Esc: Cancel"
	}
	if len(v.workflows) > 0 {
		wf := v.currentWorkflow()
		wfLabel := ""
		if wf != nil {
			wfLabel = wf.BranchName
		}
		return fmt.Sprintf("↑↓ Navigate  Enter Expand  o Open  / Filter  r Refresh | Workflow: %s  a Agent  p Push  M MRs  D Cleanup  w New", wfLabel)
	}
	return "↑↓ Navigate  Enter Expand  o Open  c Checkout  n Branch  m MR  b Check  / Filter  r Refresh  w Workflow"
}

// CapturesInput returns true when the view is in an input mode.
func (v *ProjectView) CapturesInput() bool {
	return v.showBranchCheck || v.showNewBranch || v.showMRConfirm || v.showWorkflowStart || v.showWorkflowCleanup || v.showAgentPrompt || v.showPushConfirm || v.showBatchMRConfirm
}

// CapturesKey returns true for keys this view handles directly.
func (v *ProjectView) CapturesKey(key string) bool {
	switch key {
	case "r", "o", "b", "c", "n", "m", "w", "a", "p", "D", "M", "[", "]", "enter", "/", "j", "k", "up", "down":
		return true
	}
	return false
}

// SetSize updates the view dimensions.
func (v *ProjectView) SetSize(width, height int) {
	v.width = width
	v.height = height
	v.recalcFilterHeight()
}

// recalcFilterHeight adjusts the filter height to account for workflow summary.
func (v *ProjectView) recalcFilterHeight() {
	if v.filter != nil {
		available := v.height - v.workflowSummaryHeight()
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
		{Key: "w", Description: "Start new feature workflow (create worktrees)"},
		{Key: "[/]", Description: "Switch between workflows"},
		{Key: "a", Description: "Spawn agent for selected workflow"},
		{Key: "p", Description: "Push all repos in selected workflow"},
		{Key: "M", Description: "Create MRs/PRs for all pushed repos"},
		{Key: "D", Description: "Cleanup selected workflow (remove worktrees)"},
		{Key: "/", Description: "Filter"},
		{Key: "r", Description: "Refresh all repos"},
	}
}
