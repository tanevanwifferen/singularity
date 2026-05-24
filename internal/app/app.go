package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
	"gitlab.com/tanevanwifferen1/singularity/internal/app/views"
	"gitlab.com/tanevanwifferen1/singularity/internal/config"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const version = "0.0.1"

// Model represents the application state
type Model struct {
	quitting        bool
	showQuitConfirm bool
	quitConfirm     components.ConfirmDialog
	repoPath        string
	projectPath     string
	repoInfo        *service.RepoInfo
	proj            *service.Project
	services        *service.Services
	statusMsg       string
	errorMsg        string
	router          *Router
	layout          *Layout
	connStatus      ConnectionStatus
	projectMode     bool
	activeRepoIdx   int // index of active repo in project (for [ / ] cycling)
	cfg             *config.Config

	// agentCancel cancels the goroutine subscribed to AgentService events.
	agentCancel context.CancelFunc
}

// New creates a new app model. Services may be nil for tests that don't
// exercise the service layer; production callers always pass non-nil from
// cmd/singularity/main.go (local.New or remote.New).
func New(services ...*service.Services) *Model {
	m := &Model{layout: NewLayout()}
	if len(services) > 0 {
		m.services = services[0]
	}
	cfg, err := config.LoadDefaultConfig()
	if err != nil {
		m.errorMsg = fmt.Sprintf("config load: %v", err)
	}
	m.cfg = cfg
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

// SetProject installs a pre-loaded project (set by main.go in local mode
// where the daemon-side loader has already produced it).
func (m *Model) SetProject(proj *service.Project) {
	m.proj = proj
	m.projectMode = proj != nil
	if m.projectMode {
		m.initProjectRouter()
	}
}

// loadProject loads the project and initializes router with project views
func (m *Model) loadProject() {
	m.initProjectRouter()
}

// SetServices wires the Services container after construction.
func (m *Model) SetServices(s *service.Services) {
	m.services = s
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

	if m.services == nil {
		m.errorMsg = "services not configured"
		return
	}

	// Find repo if not already a git repo
	if _, err := os.Stat(filepath.Join(m.repoPath, ".git")); os.IsNotExist(err) {
		repoPath, err := m.services.Repo.Find(context.TODO(), m.repoPath)
		if err != nil {
			m.errorMsg = fmt.Sprintf("No git repository found: %v", err)
			return
		}
		m.repoPath = repoPath
	}

	// Load repo info
	repo, err := m.services.Repo.Open(context.TODO(), m.repoPath)
	if err != nil {
		m.errorMsg = fmt.Sprintf("Failed to open repository: %v", err)
		return
	}

	m.repoInfo = repo
	m.statusMsg = fmt.Sprintf("Loaded repository: %s", filepath.Base(m.repoPath))

	// Initialize router with views after repo is loaded
	m.initRouter()
}

// registerCommonViews registers views shared between single-repo and project modes.
// It registers: Branches, Commit, Log, Agents, Config, and the git operations submenu
// views (Sync, BranchCompare, Stashes, Rebase, Worktrees, Pipeline, CreatePR).
// Returns the AgentView for post-init wiring.
func (m *Model) registerCommonViews(router *Router, repoPath string, startFKey int) *views.AgentView {
	fkey := func(n int) string { return fmt.Sprintf("f%d", n) }

	branchesView := views.NewBranchesView(repoPath)
	router.Register("Branches", branchesView, fkey(startFKey))

	// Build repo list for branch diff view.
	// In project mode, include all project repos; in repo mode, just the single repo.
	var diffRepos []views.BranchDiffRepoEntry
	if m.proj != nil {
		for _, r := range m.proj.Repos {
			diffRepos = append(diffRepos, views.BranchDiffRepoEntry{
				Name:          r.Name,
				Path:          r.Path,
				DefaultBranch: r.DefaultBranch,
			})
		}
	} else {
		diffRepos = []views.BranchDiffRepoEntry{{
			Name: filepath.Base(repoPath),
			Path: repoPath,
		}}
	}
	branchDiffView := views.NewBranchDiffView()
	router.Register("BranchDiff", branchDiffView)
	router.submenuViews["BranchDiff"] = true
	branchesView.SetBranchDiffView(branchDiffView, diffRepos)

	commitView := views.NewCommitView(repoPath)
	router.Register("Commit", commitView, fkey(startFKey+1))

	logView := views.NewLogView(repoPath)
	router.Register("Log", logView, fkey(startFKey+2))

	// Register agent console view
	var contextFiles []string
	if m.proj != nil {
		contextFiles = m.proj.ContextFiles
	}
	agentView := views.NewAgentView(repoPath, contextFiles)
	router.Register("Agents", agentView, fkey(startFKey+3))
	if m.cfg != nil {
		agentView.SetJiraConfig(m.cfg.Jira)
	}

	// Config / settings view
	if m.cfg != nil {
		configView := views.NewConfigView(m.cfg)
		router.Register("Config", configView, fkey(startFKey+4))
	}

	// Git operations submenu views (no F-key shortcut)
	syncView := views.NewSyncView(repoPath)
	router.Register("Sync", syncView)

	branchCompareView := views.NewBranchComparisonView(repoPath)
	router.Register("BranchCompare", branchCompareView)

	stashView := views.NewStashView(repoPath)
	router.Register("Stashes", stashView)

	rebaseView := views.NewRebaseView(repoPath)
	router.Register("Rebase", rebaseView)

	worktreeView := views.NewWorktreeView(repoPath)
	router.Register("Worktrees", worktreeView)

	pipelineView := views.NewPipelineView(repoPath)
	router.Register("Pipeline", pipelineView)

	prView := views.NewPRView(repoPath)
	router.Register("CreatePR", prView)

	return agentView
}

// initRouter initializes the view router with available views.
func (m *Model) initRouter() {
	// Create the overview view as the first view (landing page)
	overview := views.NewOverviewView(m.repoPath)
	router := NewRouter(overview, "Overview")
	router.viewKeys["Overview"] = "f1"
	router.keyToView["f1"] = "Overview"

	m.registerCommonViews(router, m.repoPath, 2)

	// Build git submenu items
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
		jiraView.SetRepoPath(m.repoPath)
		router.Register("Jira", jiraView)
		gitItems = append(gitItems, components.SubmenuItem{Key: "j", Label: "Jira Issues", ViewName: "Jira"})
	}

	router.RegisterSubmenu("g", "Git", gitItems)

	m.router = router

	// Wire services into every view that supports it.
	m.router.SetAllServices(m.services)

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
	if m.proj == nil {
		m.errorMsg = "no project loaded — main.go did not pass one"
		return
	}

	// Create the project overview view as the first view (landing page)
	projectView := views.NewProjectView(m.proj)

	router := NewRouter(projectView, "Project")
	router.viewKeys["Project"] = "f1"
	router.keyToView["f1"] = "Project"

	// Feature workflows view (multi-repo worktrees, push, MR, agents)
	workflowsView := views.NewWorkflowsView(m.proj)
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
	if m.services != nil {
		if repoInfo, err := m.services.Repo.Open(context.TODO(), defaultRepoPath); err == nil {
			m.repoInfo = repoInfo
		}
	}

	// Register shared single-repo views (F3-F7, after Workflows at F2)
	m.registerCommonViews(router, defaultRepoPath, 3)

	// Project-specific submenu views
	projectSyncView := views.NewProjectSyncView(m.proj)
	router.Register("ProjectSync", projectSyncView)

	projectDiffView := views.NewProjectDiffView(m.proj)
	router.Register("ProjectDiff", projectDiffView)

	projectStashView := views.NewProjectStashView(m.proj)
	router.Register("ProjectStash", projectStashView)

	// Build git submenu items (project-specific items first, then shared)
	projGitItems := []components.SubmenuItem{
		{Key: "a", Label: "Sync All Repos", ViewName: "ProjectSync"},
		{Key: "d", Label: "Project Diff (open changes)", ViewName: "ProjectDiff"},
		{Key: "s", Label: "Sync (push/pull/fetch)", ViewName: "Sync"},
		{Key: "b", Label: "Branch Compare", ViewName: "BranchCompare"},
		{Key: "t", Label: "Project Stashes", ViewName: "ProjectStash"},
		{Key: "r", Label: "Rebase", ViewName: "Rebase"},
		{Key: "w", Label: "Worktrees", ViewName: "Worktrees"},
		{Key: "p", Label: "Pipeline", ViewName: "Pipeline"},
		{Key: "c", Label: "Create PR", ViewName: "CreatePR"},
	}
	if m.cfg != nil && m.cfg.Jira.Enabled {
		jiraView := views.NewJiraView(m.cfg.Jira)
		jiraView.SetProject(m.proj)
		router.Register("Jira", jiraView)
		projGitItems = append(projGitItems, components.SubmenuItem{Key: "j", Label: "Jira Issues", ViewName: "Jira"})
	}

	router.RegisterSubmenu("g", "Git", projGitItems)

	m.router = router

	// Wire services into every view that supports it.
	m.router.SetAllServices(m.services)

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

// switchToProjectRepo updates all single-repo views to point at the repo at the given index.
func (m *Model) switchToProjectRepo(idx int) tea.Cmd {
	repo := m.proj.Repos[idx]
	m.router.SetAllRepoPath(repo.Path)

	// Load repo info for the status bar
	if m.services != nil {
		repoInfo, err := m.services.Repo.Open(context.TODO(), repo.Path)
		if err == nil {
			m.repoInfo = repoInfo
		}
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
	cmds := []tea.Cmd{m.router.Init()}
	// Start the agent-status tick at the app level so it survives view switches.
	if av := m.getAgentView(); av != nil {
		cmds = append(cmds, av.AgentTickStart())
	}
	return tea.Batch(cmds...)
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
				if m.agentCancel != nil {
					m.agentCancel()
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

	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleAppKeyMsg(msg)
	case tea.WindowSizeMsg:
		m.layout.SetSize(msg.Width, msg.Height)
		vw, vh := m.layout.AvailableViewDimensions()
		m.router.NotifySize(vw, vh)
		return m, nil
	case views.StreamTickMsg:
		// Handle at the app level so the tick chain survives view switches.
		if av := m.getAgentView(); av != nil {
			return m, av.AgentTickCmd()
		}
		return m, nil
	case views.AgentUpdateMsg:
		// Agent service notified us of an agent state/output change.
		if av := m.getAgentView(); av != nil {
			return m, func() tea.Msg {
				av.LoadAgents()
				return views.RefreshDoneMsg{}
			}
		}
		return m, nil
	case ConnectionStatusMsg:
		m.connStatus = msg.Status
		if msg.Status.Connected {
			m.statusMsg = fmt.Sprintf("Connected to %s", msg.Status.URL)
		} else if msg.Status.Reconnecting {
			m.statusMsg = fmt.Sprintf("Reconnecting to %s...", msg.Status.URL)
		} else if msg.Status.Error != "" {
			m.errorMsg = fmt.Sprintf("Connection error: %s", msg.Status.Error)
		}
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

// handleAppKeyMsg handles all key events for the top-level app model.
func (m Model) handleAppKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	viewCapturesInput := m.router.ActiveViewCapturesInput()

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

	// Delegate unhandled keys to router
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
	view += th.Version.Render("v"+version) + "\n\n"

	if m.errorMsg != "" {
		view += lipgloss.NewStyle().
			Foreground(th.Error).
			Render("Error: " + m.errorMsg + "\n\n")
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

// Run starts the application. It also wires the AgentService subscription
// goroutine that pumps AgentEvent messages into the bubbletea event loop
// (replacing the old engine.OnAgentUpdate callback hook).
func (m *Model) Run() error {
	// Load repo if path not set and not in project mode
	if m.repoPath == "" && !m.projectMode {
		m.loadRepo()
	}

	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	// Subscribe to agent events and forward them as AgentUpdateMsg's to the
	// bubbletea program. Cancel on shutdown.
	if m.services != nil && m.services.Agent != nil {
		ctx, cancel := context.WithCancel(context.Background())
		m.agentCancel = cancel
		ch, sub, err := m.services.Agent.SubscribeAll(ctx)
		if err == nil {
			go func() {
				defer sub()
				for ev := range ch {
					p.Send(views.AgentUpdateMsg{AgentID: ev.AgentID})
				}
			}()
		}
	}

	_, err := p.Run()
	if m.agentCancel != nil {
		m.agentCancel()
	}
	return err
}
