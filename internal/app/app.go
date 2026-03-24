package app

import (
	"fmt"
	"os"
	"path/filepath"

	"git-frontend/internal/app/components"
	"git-frontend/internal/app/views"
	"git-frontend/internal/config"
	"git-frontend/internal/engine"
	"git-frontend/internal/git"
	"git-frontend/internal/project"
	"git-frontend/internal/theme"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const version = "0.0.1"

// Model represents the application state
type Model struct {
	quitting         bool
	showQuitConfirm  bool
	quitConfirm      components.ConfirmDialog
	repoPath         string
	projectPath      string
	repoInfo         *git.RepoInfo
	proj             *project.Project
	engine           *engine.Engine
	statusMsg        string
	errorMsg         string
	router           *Router
	layout           *Layout
	wsClient         *WSClient
	wsStatus         WSConnectionStatus
	projectMode      bool
	activeRepoIdx    int // index of active repo in project (for [ / ] cycling)
	cfg              *config.Config
}

// New creates a new app model
func New() *Model {
	m := &Model{layout: NewLayout()}
	m.cfg, _ = config.LoadDefaultConfig()
	return m
}

// NewWithWS creates a new app model with WebSocket client
func NewWithWS(wsURL string) *Model {
	m := New()
	if wsURL != "" {
		m.SetWSClient(wsURL)
	}
	return m
}

// SetRepoPath sets the repository path
func (m *Model) SetRepoPath(path string) {
	m.repoPath = path
	m.projectMode = false
	m.loadRepo()
}

// SetProjectPath sets the project path for multi-repo mode
func (m *Model) SetProjectPath(path string) {
	m.projectPath = path
	m.projectMode = true
	m.loadProject()
}

// loadProject loads the project and initializes router with project views
func (m *Model) loadProject() {
	m.initProjectRouter()
}

// SetEngine sets the agent engine (for server mode)
func (m *Model) SetEngine(eng *engine.Engine) {
	m.engine = eng
	if m.router != nil {
		if agentView := m.getAgentView(); agentView != nil {
			agentView.SetEngine(eng)
		}
		if wv := m.getWorkflowsView(); wv != nil {
			wv.SetEngine(eng)
		}
	}
}

// SetWSClient configures the WebSocket client for receiving server events
func (m *Model) SetWSClient(url string) {
	if m.wsClient != nil {
		m.wsClient.Disconnect()
	}

	m.wsClient = NewWSViewUpdater(url, m.repoPath)

	// Register for connection status updates
	statusCh := make(chan WSConnectionStatus, 5)
	m.wsClient.SubscribeStatus(statusCh)

	// Goroutine to handle status updates
	go func() {
		for status := range statusCh {
			// Post to tea message queue via command
			_ = status // Status is already tracked via WSConnectionMsg in Update
		}
	}()

	// Start connection
	go func() {
		if err := m.wsClient.Connect(); err != nil {
			m.errorMsg = fmt.Sprintf("WebSocket connection failed: %v", err)
		}
	}()

	// Register handlers for WebSocket events - these are already registered in NewWSViewUpdater
}

// getProjectView returns the ProjectView from the router if it exists
func (m *Model) getProjectView() *views.ProjectView {
	if m.router == nil {
		return nil
	}
	view := m.router.GetView("Project")
	if pv, ok := view.(*views.ProjectView); ok {
		return pv
	}
	return nil
}

// getWorkflowsView returns the WorkflowsView from the router if it exists
func (m *Model) getWorkflowsView() *views.WorkflowsView {
	if m.router == nil {
		return nil
	}
	view := m.router.GetView("Workflows")
	if wv, ok := view.(*views.WorkflowsView); ok {
		return wv
	}
	return nil
}

// getAgentView returns the AgentView from the router if it exists
func (m *Model) getAgentView() *views.AgentView {
	if m.router == nil {
		return nil
	}
	view := m.router.GetView("Agents")
	if av, ok := view.(*views.AgentView); ok {
		return av
	}
	return nil
}

// loadRepo loads the repository
func (m *Model) loadRepo() {
	if m.repoPath == "" {
		// Find repo from current directory
		cwd, err := os.Getwd()
		if err != nil {
			m.errorMsg = fmt.Sprintf("Failed to get current directory: %v", err)
			return
		}
		m.repoPath = cwd
	}

	// Find repo if not already a git repo
	if _, err := os.Stat(filepath.Join(m.repoPath, ".git")); os.IsNotExist(err) {
		repoPath, err := git.FindRepo(m.repoPath)
		if err != nil {
			m.errorMsg = fmt.Sprintf("No git repository found: %v", err)
			return
		}
		m.repoPath = repoPath
	}

	// Load repo info
	repo, err := git.OpenRepo(m.repoPath)
	if err != nil {
		m.errorMsg = fmt.Sprintf("Failed to open repository: %v", err)
		return
	}

	m.repoInfo = repo
	m.statusMsg = fmt.Sprintf("Loaded repository: %s", filepath.Base(m.repoPath))

	// Initialize router with views after repo is loaded
	m.initRouter()
}

// initRouter initializes the view router with available views.
func (m *Model) initRouter() {
	// Create the overview view as the first view (landing page)
	// Top-level views get F-key shortcuts; git operations live in a "g" submenu
	overview := views.NewOverviewView(m.repoPath)
	router := NewRouter(overview, "Overview")
	router.viewKeys["Overview"] = "f1"
	router.keyToView["f1"] = "Overview"

	dashboard, err := NewBranchDashboard(m.repoPath)
	if err == nil {
		router.Register("Branches", dashboard, "f2")
	}

	commitView := views.NewCommitView(m.repoPath)
	router.Register("Commit", commitView, "f3")

	logView := views.NewLogView(m.repoPath)
	router.Register("Log", logView, "f4")

	// Register agent console view
	if m.engine == nil {
		m.engine = engine.New(10)
	}
	var contextFiles []string
	if m.proj != nil {
		contextFiles = m.proj.ContextFiles
	}
	agentView := views.NewAgentView(m.repoPath, m.engine, contextFiles)
	router.Register("Agents", agentView, "f5")
	if m.cfg != nil {
		agentView.SetJiraConfig(m.cfg.Jira)
	}

	// Config / settings view
	if m.cfg != nil {
		configView := views.NewConfigView(m.cfg)
		router.Register("Config", configView, "f6")
	}

	// Git operations submenu (accessible via "g" key)
	syncView := views.NewSyncView(m.repoPath)
	router.Register("Sync", syncView)

	branchCompareView := views.NewBranchComparisonView(m.repoPath)
	router.Register("BranchCompare", branchCompareView)

	stashView := views.NewStashView(m.repoPath)
	router.Register("Stashes", stashView)

	rebaseView := views.NewRebaseView(m.repoPath)
	router.Register("Rebase", rebaseView)

	worktreeView := views.NewWorktreeView(m.repoPath)
	worktreeView.SetEngine(m.engine)
	router.Register("Worktrees", worktreeView)

	pipelineView := views.NewPipelineView(m.repoPath)
	router.Register("Pipeline", pipelineView)

	prView := views.NewPRView(m.repoPath)
	router.Register("CreatePR", prView)

	gitItems := []components.SubmenuItem{
		{Key: "s", Label: "Sync (push/pull/fetch)", ViewName: "Sync"},
		{Key: "b", Label: "Branch Compare", ViewName: "BranchCompare"},
		{Key: "t", Label: "Stashes", ViewName: "Stashes"},
		{Key: "r", Label: "Rebase", ViewName: "Rebase"},
		{Key: "w", Label: "Worktrees", ViewName: "Worktrees"},
		{Key: "p", Label: "Pipeline", ViewName: "Pipeline"},
		{Key: "c", Label: "Create PR", ViewName: "CreatePR"},
	}
	if m.cfg != nil && m.cfg.Jira.Enabled {
		jiraView := views.NewJiraView(m.cfg.Jira)
		jiraView.SetEngine(m.engine)
		jiraView.SetRepoPath(m.repoPath)
		router.Register("Jira", jiraView)
		gitItems = append(gitItems, components.SubmenuItem{Key: "j", Label: "Jira Issues", ViewName: "Jira"})
	}

	router.RegisterSubmenu("g", "Git", gitItems)

	m.router = router

	// Wire Jira config to views that support the Jira picker
	// (must happen after router is set so getAgentView() works)
	if m.cfg != nil && m.cfg.Jira.BaseURL != "" {
		if av := m.getAgentView(); av != nil {
			av.SetJiraConfig(m.cfg.Jira)
		}
	}

	// Notify router of initial window size
	if m.layout != nil {
		vw, vh := m.layout.AvailableViewDimensions()
		m.router.NotifySize(vw, vh)
	}
}

// initProjectRouter initializes the router with project-level views for multi-repo mode.
func (m *Model) initProjectRouter() {
	// Load project from path
	if m.projectPath == "" {
		m.projectPath = project.GetDefaultConfigPath()
	}

	// Try to load project config
	loader, err := project.NewLoaderFromFile(m.projectPath)
	if err != nil {
		// No config file, try auto-discovery in current directory
		// Create a project with all subdirectories that are git repos
		m.statusMsg = "No project config found, using auto-discovery"
		proj := m.discoverProject(m.projectPath)
		if proj == nil {
			m.errorMsg = "No git repositories found"
			return
		}
		m.proj = proj
	} else {
		// Load the first project from config
		keys := loader.ListProjectKeys()
		if len(keys) == 0 {
			m.errorMsg = "No projects in config"
			return
		}
		proj, err := loader.LoadProject(keys[0])
		if err != nil {
			m.errorMsg = fmt.Sprintf("Failed to load project: %v", err)
			return
		}
		m.proj = proj
	}

	// Create engine if not already set (same pattern as initRouter)
	if m.engine == nil {
		m.engine = engine.New(10)
	}

	// Create the project overview view as the first view (landing page)
	projectView := views.NewProjectView(m.proj)

	router := NewRouter(projectView, "Project")
	// Add F1 shortcut for the project view
	router.viewKeys["Project"] = "f1"
	router.keyToView["f1"] = "Project"

	// Feature workflows view (multi-repo worktrees, push, MR, agents)
	workflowsView := views.NewWorkflowsView(m.proj)
	workflowsView.SetEngine(m.engine)
	router.Register("Workflows", workflowsView, "f2")

	// Workflow diff view (drill-down from Workflows, not a top-level tab)
	workflowDiffView := views.NewWorkflowDiffView()
	router.Register("WorkflowDiff", workflowDiffView)
	router.submenuViews["WorkflowDiff"] = true // hide from tab bar
	workflowsView.SetWorkflowDiffView(workflowDiffView)

	// Use the active repo's path as the default for single-repo views.
	// Fall back to the project directory itself.
	defaultRepoPath := m.projectPath
	if m.proj != nil && len(m.proj.Repos) > 0 {
		if m.activeRepoIdx >= len(m.proj.Repos) {
			m.activeRepoIdx = 0
		}
		defaultRepoPath = m.proj.Repos[m.activeRepoIdx].Path
	}

	// Load repo info for the status bar
	if repoInfo, err := git.OpenRepo(defaultRepoPath); err == nil {
		m.repoInfo = repoInfo
	}

	// Register single-repo views that are also useful in project mode
	dashboard, err := NewBranchDashboard(defaultRepoPath)
	if err == nil {
		router.Register("Branches", dashboard, "f3")
	}

	commitView := views.NewCommitView(defaultRepoPath)
	router.Register("Commit", commitView, "f4")

	logView := views.NewLogView(defaultRepoPath)
	router.Register("Log", logView, "f5")

	// Register agent console view (shared engine so agents spawned from
	// WorkflowsView are visible in the AgentView)
	var contextFiles []string
	if m.proj != nil {
		contextFiles = m.proj.ContextFiles
	}
	agentView := views.NewAgentView(defaultRepoPath, m.engine, contextFiles)
	router.Register("Agents", agentView, "f6")
	if m.cfg != nil {
		agentView.SetJiraConfig(m.cfg.Jira)
	}

	// Config / settings view
	if m.cfg != nil {
		configView := views.NewConfigView(m.cfg)
		router.Register("Config", configView, "f7")
	}

	// Git operations submenu (accessible via "g" key)
	projectSyncView := views.NewProjectSyncView(m.proj)
	router.Register("ProjectSync", projectSyncView)

	projectDiffView := views.NewProjectDiffView(m.proj)
	router.Register("ProjectDiff", projectDiffView)

	syncView := views.NewSyncView(defaultRepoPath)
	router.Register("Sync", syncView)

	branchCompareView := views.NewBranchComparisonView(defaultRepoPath)
	router.Register("BranchCompare", branchCompareView)

	stashView := views.NewStashView(defaultRepoPath)
	router.Register("Stashes", stashView)

	rebaseView := views.NewRebaseView(defaultRepoPath)
	router.Register("Rebase", rebaseView)

	worktreeView := views.NewWorktreeView(defaultRepoPath)
	worktreeView.SetEngine(m.engine)
	router.Register("Worktrees", worktreeView)

	pipelineView := views.NewPipelineView(defaultRepoPath)
	router.Register("Pipeline", pipelineView)

	prView := views.NewPRView(defaultRepoPath)
	router.Register("CreatePR", prView)

	projGitItems := []components.SubmenuItem{
		{Key: "a", Label: "Sync All Repos", ViewName: "ProjectSync"},
		{Key: "d", Label: "Project Diff (open changes)", ViewName: "ProjectDiff"},
		{Key: "s", Label: "Sync (push/pull/fetch)", ViewName: "Sync"},
		{Key: "b", Label: "Branch Compare", ViewName: "BranchCompare"},
		{Key: "t", Label: "Stashes", ViewName: "Stashes"},
		{Key: "r", Label: "Rebase", ViewName: "Rebase"},
		{Key: "w", Label: "Worktrees", ViewName: "Worktrees"},
		{Key: "p", Label: "Pipeline", ViewName: "Pipeline"},
		{Key: "c", Label: "Create PR", ViewName: "CreatePR"},
	}
	if m.cfg != nil && m.cfg.Jira.Enabled {
		jiraView := views.NewJiraView(m.cfg.Jira)
		jiraView.SetEngine(m.engine)
		jiraView.SetProject(m.proj)
		router.Register("Jira", jiraView)
		projGitItems = append(projGitItems, components.SubmenuItem{Key: "j", Label: "Jira Issues", ViewName: "Jira"})
	}

	router.RegisterSubmenu("g", "Git", projGitItems)

	m.router = router

	// Wire Jira config to views that support the Jira picker
	if m.cfg != nil && m.cfg.Jira.BaseURL != "" {
		if av := m.getAgentView(); av != nil {
			av.SetJiraConfig(m.cfg.Jira)
		}
		if wv := m.getWorkflowsView(); wv != nil {
			wv.SetJiraConfig(m.cfg.Jira)
		}
	}

	// Notify router of initial window size
	if m.layout != nil {
		vw, vh := m.layout.AvailableViewDimensions()
		m.router.NotifySize(vw, vh)
	}
}

// discoverProject creates a project by auto-discovering git repos in a directory
func (m *Model) discoverProject(dir string) *project.Project {
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

// switchToProjectRepo updates all single-repo views to point at the repo at the given index.
func (m *Model) switchToProjectRepo(idx int) tea.Cmd {
	repo := m.proj.Repos[idx]
	m.router.SetAllRepoPath(repo.Path)

	// Load repo info for the status bar
	repoInfo, err := git.OpenRepo(repo.Path)
	if err == nil {
		m.repoInfo = repoInfo
	}

	m.statusMsg = fmt.Sprintf("Switched to repo: %s", repo.Name)

	// Re-init the active view so it reloads data for the new repo
	return m.router.ActiveView().Init()
}

// activeRepoName returns the name of the currently active repo in project mode.
func (m *Model) activeRepoName() string {
	if m.proj != nil && m.activeRepoIdx < len(m.proj.Repos) {
		return m.proj.Repos[m.activeRepoIdx].Name
	}
	return ""
}

// Init initializes the tea program
func (m Model) Init() tea.Cmd {
	if m.router == nil {
		return nil
	}
	return m.router.Init()
}

// Update handles messages and updates the model
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle quit confirmation dialog (works with or without router)
	if m.showQuitConfirm {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			var cmd tea.Cmd
			m.quitConfirm, cmd = m.quitConfirm.Update(msg)
			return m, cmd
		case components.ConfirmResult:
			if msg.ID == "quit" && msg.Confirmed {
				m.quitting = true
				if m.wsClient != nil {
					m.wsClient.Disconnect()
				}
				return m, tea.Quit
			}
			m.showQuitConfirm = false
			return m, nil
		case tea.WindowSizeMsg:
			m.quitConfirm, _ = m.quitConfirm.Update(msg)
			if m.layout != nil {
				m.layout.SetSize(msg.Width, msg.Height)
			}
			if m.router != nil {
				vw, vh := m.layout.AvailableViewDimensions()
				m.router.NotifySize(vw, vh)
			}
			return m, nil
		}
		return m, nil
	}

	if m.router == nil {
		// Router not initialized yet, handle basic keys
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "ctrl+c", "q":
				m.showQuitConfirm = true
				m.quitConfirm = components.NewConfirmDialog("Quit", "Are you sure you want to quit?", "quit")
				return m, nil
			}
		case tea.WindowSizeMsg:
			m.layout.SetSize(msg.Width, msg.Height)
		}
		return m, nil
	}

	// Check if the active view is capturing input (e.g., text input modal)
	viewCapturesInput := m.router.ActiveViewCapturesInput()

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.showQuitConfirm = true
			m.quitConfirm = components.NewConfirmDialog("Quit", "Are you sure you want to quit?", "quit")
			m.quitConfirm.Modal.SetSize(m.layout.width, m.layout.height)
			return m, nil
		case "q":
			if !viewCapturesInput {
				m.showQuitConfirm = true
				m.quitConfirm = components.NewConfirmDialog("Quit", "Are you sure you want to quit?", "quit")
				m.quitConfirm.Modal.SetSize(m.layout.width, m.layout.height)
				return m, nil
			}
		case "R":
			if !viewCapturesInput {
				// Refresh repo (capital R — lowercase r used by views)
				m.loadRepo()
				return m, nil
			}
		case "P":
			if !viewCapturesInput && m.proj != nil && !m.projectMode {
				// Return to project overview
				m.projectMode = true
				m.loadProject()
				return m, nil
			}
		case "[":
			if !viewCapturesInput && m.projectMode && m.proj != nil && len(m.proj.Repos) > 1 {
				m.activeRepoIdx = (m.activeRepoIdx - 1 + len(m.proj.Repos)) % len(m.proj.Repos)
				return m, m.switchToProjectRepo(m.activeRepoIdx)
			}
		case "]":
			if !viewCapturesInput && m.projectMode && m.proj != nil && len(m.proj.Repos) > 1 {
				m.activeRepoIdx = (m.activeRepoIdx + 1) % len(m.proj.Repos)
				return m, m.switchToProjectRepo(m.activeRepoIdx)
			}
		case "T":
			if !viewCapturesInput {
				// Toggle theme
				theme.ToggleTheme()
				m.layout.rebuildStyles()
				m.statusMsg = "Theme toggled"
				return m, nil
			}
		}
	case tea.WindowSizeMsg:
		m.layout.SetSize(msg.Width, msg.Height)
		vw, vh := m.layout.AvailableViewDimensions()
		m.router.NotifySize(vw, vh)
		return m, nil
	case WSConnectionMsg:
		m.wsStatus = msg.Status
		// Update status message based on connection state
		if msg.Status.Connected {
			m.statusMsg = fmt.Sprintf("Connected to %s", msg.Status.URL)
		} else if msg.Status.Reconnecting {
			m.statusMsg = fmt.Sprintf("Reconnecting to %s...", msg.Status.URL)
		} else if msg.Status.Error != "" {
			m.errorMsg = fmt.Sprintf("Connection error: %s", msg.Status.Error)
		}
	case WSRepoUpdateMsg:
		// Repo was updated on server, refresh the current view
		m.repoInfo = msg.Repo
		return m, func() tea.Msg {
			return views.RefreshMsg{}
		}
	case WSBranchUpdateMsg:
		// Branch was updated, switch to Branches view and refresh
		if m.router != nil {
			m.router.SwitchTo("Branches")
		}
		return m, func() tea.Msg {
			return views.RefreshMsg{}
		}
	case WSPipelineUpdateMsg:
		// Pipeline was updated, switch to Pipeline view and refresh
		if m.router != nil {
			m.router.SwitchTo("Pipeline")
		}
		return m, func() tea.Msg {
			return views.RefreshMsg{}
		}
	case WSAgentOutputMsg:
		// Agent output received, switch to Agents view
		if m.router != nil {
			m.router.SwitchTo("Agents")
		}
		// The AgentView will handle the refresh via its Update
		return m, nil
	case WSAgentEventMsg:
		// Agent lifecycle event, switch to Agents view and refresh
		if m.router != nil {
			m.router.SwitchTo("Agents")
		}
		return m, func() tea.Msg {
			return views.RefreshMsg{}
		}
	case WSProjectUpdateMsg:
		// Project update, could show a notification or refresh overview
		m.statusMsg = fmt.Sprintf("Project updated: %s", msg.Status)
		return m, nil
	case views.OpenPRForBranchMsg:
		// Navigate to PR creation view with the worktree branch pre-selected
		if m.router != nil {
			view := m.router.GetView("CreatePR")
			if prv, ok := view.(*views.PRView); ok {
				prv.SetPendingSourceBranch(msg.Branch)
			}
			m.router.SwitchTo("CreatePR")
		}
		return m, func() tea.Msg { return views.RefreshMsg{} }
	case views.RefreshMsg:
		// Forward refresh to active view
		if m.router != nil {
			if av, ok := m.router.ActiveView().(interface{ Refresh() error }); ok {
				av.Refresh()
			}
		}
		return m, nil
	case views.ViewChangeMsg:
		// Handle view changes, possibly with a specific repo path
		if msg.RepoPath != "" {
			// Warn if leaving project mode with an active workflow
			if m.projectMode {
				if wv := m.getWorkflowsView(); wv != nil && wv.HasActiveWorkflow() {
					m.statusMsg = "Warning: leaving project mode will disconnect from active workflow"
				}
			}
			// Drill into single-repo mode for the specified repo
			m.repoPath = msg.RepoPath
			m.projectMode = false
			m.loadRepo()
		} else if msg.ViewName == "Project" && m.proj != nil {
			// Return to project overview
			m.projectMode = true
			m.loadProject()
		} else {
			// Simple view switch — switch and run the new view's Init
			if m.router != nil {
				if err := m.router.SwitchTo(msg.ViewName); err == nil {
					return m, m.router.ActiveView().Init()
				}
			}
		}
		return m, nil
	}

	// Delegate to router
	_, cmd := m.router.Update(msg)
	return m, cmd
}

// View renders the TUI
func (m Model) View() string {
	// If router is initialized, use layout composition
	if m.router != nil && m.layout != nil {
		var base string
		if m.proj != nil {
			opts := RenderOpts{ProjectName: m.proj.Name}
			if len(m.proj.Repos) > 1 {
				opts.RepoSel = &RepoSelector{
					ActiveIdx:  m.activeRepoIdx,
					TotalRepos: len(m.proj.Repos),
					RepoName:   m.proj.Repos[m.activeRepoIdx].Name,
				}
			}
			base = m.layout.Render(m.router, m.repoInfo, m.router.View(), opts)
		} else {
			base = m.layout.Render(m.router, m.repoInfo, m.router.View())
		}
		if m.showQuitConfirm {
			return m.quitConfirm.View(base)
		}
		return base
	}

	// Fallback to basic view if router not initialized
	th := theme.GetTheme()

	if m.quitting {
		return "Goodbye!\n"
	}

	// Build the view
	view := th.Title.Render("Git Frontend") + "\n"
	view += th.Version.Render("v" + version) + "\n\n"

	if m.errorMsg != "" {
		view += lipgloss.NewStyle().
			Foreground(th.Error).
			Render("Error: "+m.errorMsg+"\n\n")
	}

	if m.statusMsg != "" {
		view += th.InfoStyle.Render(m.statusMsg) + "\n\n"
	}

	// Show repo info
	if m.repoInfo != nil {
		view += m.renderRepoInfo()
	} else {
		view += th.Help.Render("No repository loaded") + "\n"
	}

	view += "\n" + th.Help.Render("Press q or Ctrl+C to quit, r to refresh, t to toggle theme") + "\n"

	return view
}

// renderRepoInfo renders repository information
func (m Model) renderRepoInfo() string {
	th := theme.GetTheme()
	repo := m.repoInfo

	var view string

	// Branch info
	if repo.CurrentBranch != "" {
		branchStyle := lipgloss.NewStyle().Foreground(th.Accent2).Bold(true)
		view += branchStyle.Render("Branch: ") + repo.CurrentBranch + "\n"
	} else {
		view += "Branch: (detached)\n"
	}

	// HEAD
	if len(repo.HEAD) >= 7 {
		view += "HEAD: " + th.InfoStyle.Render(repo.HEAD[:7]) + "\n"
	}

	// Status
	if repo.IsDirty {
		dirtyStyle := lipgloss.NewStyle().Foreground(th.Modified)
		view += dirtyStyle.Render("Status: dirty (+uncommitted changes)") + "\n"
	} else {
		view += "Status: clean\n"
	}

	// Remotes
	if len(repo.Remotes) > 0 {
		view += "\nRemotes:\n"
		for _, remote := range repo.Remotes {
			view += fmt.Sprintf("  %s: %s\n", remote.Name, remote.URL)
		}
	}

	// Branches
	if len(repo.Branches) > 0 {
		view += fmt.Sprintf("\nBranches (%d):\n", len(repo.Branches))
		for _, branch := range repo.Branches {
			branchName := branch.Name
			isCurrent := m.repoInfo != nil && m.repoInfo.CurrentBranch == branch.Name
			if isCurrent {
				// Highlight current branch using theme accent
				branchName = lipgloss.NewStyle().Foreground(th.Accent2).Render(branch.Name)
			}

			view += fmt.Sprintf("  %s", branchName)

			// Ahead/behind
			if branch.Ahead > 0 || branch.Behind > 0 {
				aheadStyle := lipgloss.NewStyle().Foreground(th.Added)
				behindStyle := lipgloss.NewStyle().Foreground(th.Removed)

				if branch.Ahead > 0 {
					view += aheadStyle.Render(fmt.Sprintf(" +%d", branch.Ahead))
				}
				if branch.Behind > 0 {
					view += behindStyle.Render(fmt.Sprintf(" -%d", branch.Behind))
				}
			}

			if branch.Upstream != "" {
				view += fmt.Sprintf(" (%s)", branch.Upstream)
			}
			view += "\n"
		}
	}

	return view
}

// Run starts the application
func (m *Model) Run() error {
	// Load repo if path not set and not in project mode
	if m.repoPath == "" && !m.projectMode {
		m.loadRepo()
	}

	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	_, err := p.Run()
	return err
}
