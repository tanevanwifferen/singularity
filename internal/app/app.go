package app

import (
	"fmt"
	"os"
	"path/filepath"

	"git-frontend/internal/app/views"
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
	quitting     bool
	repoPath     string
	projectPath  string
	repoInfo     *git.RepoInfo
	proj         *project.Project
	engine       *engine.Engine
	statusMsg    string
	errorMsg     string
	router       *Router
	layout       *Layout
	wsClient     *WSClient
	wsStatus     WSConnectionStatus
	projectMode  bool
}

// New creates a new app model
func New() *Model {
	return &Model{
		layout: NewLayout(),
	}
}

// NewWithWS creates a new app model with WebSocket client
func NewWithWS(wsURL string) *Model {
	m := &Model{
		layout: NewLayout(),
	}
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
		// Update the AgentView if it exists
		if agentView := m.getAgentView(); agentView != nil {
			agentView.SetEngine(eng)
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

// getAgentView returns the AgentView from the router if it exists
func (m *Model) getAgentView() *views.AgentView {
	if m.router == nil {
		return nil
	}
	view := m.router.ActiveView()
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
	overview := views.NewOverviewView(m.repoPath)
	router := NewRouter(overview, "Overview")

	// Register branch dashboard view
	dashboard, err := NewBranchDashboard(m.repoPath)
	if err == nil {
		router.Register("Branches", dashboard)
	}

	// Register commit view
	commitView := views.NewCommitView(m.repoPath)
	router.Register("commit", commitView)

	// Register branch comparison view (split panel with diff summary)
	branchCompareView := views.NewBranchComparisonView(m.repoPath)
	router.Register("BranchCompare", branchCompareView)

	// Register stash view
	stashView := views.NewStashView(m.repoPath)
	router.Register("Stashes", stashView)

	// Register rebase planner view
	rebaseView := views.NewRebaseView(m.repoPath)
	router.Register("Rebase", rebaseView)

	// Register worktree view
	worktreeView := views.NewWorktreeView(m.repoPath)
	router.Register("Worktrees", worktreeView)

	// Register commit log view
	logView := views.NewLogView(m.repoPath)
	router.Register("Log", logView)

	// Register pipeline dashboard view
	pipelineView := views.NewPipelineView(m.repoPath)
	router.Register("Pipeline", pipelineView)

	// Register PR/MR creation view
	prView := views.NewPRView(m.repoPath)
	router.Register("CreatePR", prView)

	// Register agent console view (for server mode)
	agentView := views.NewAgentView(m.repoPath, m.engine)
	router.Register("Agents", agentView)

	// Register stub views for testing routing
	stub1 := NewStubView1(m.repoPath)
	stub2 := NewStubView2(m.repoPath)
	router.Register("stub1", stub1)
	router.Register("stub2", stub2)

	m.router = router

	// Notify router of initial window size
	if m.layout != nil {
		m.router.NotifySize(m.layout.width, m.layout.height)
	}
}

// initProjectRouter initializes the router with project-level views for multi-repo mode.
func (m *Model) initProjectRouter() {
	// Load project from path
	if m.projectPath == "" {
		m.projectPath = "."
	}

	// Try to load project config
	configPath := project.GetDefaultConfigPath()
	loader, err := project.NewLoaderFromFile(configPath)
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

	// Create the project overview view as the first view (landing page)
	projectView := views.NewProjectView(m.proj)
	router := NewRouter(projectView, "Project")

	// Register stub views for testing routing (same as single repo mode)
	stub1 := NewStubView1("")
	stub2 := NewStubView2("")
	router.Register("stub1", stub1)
	router.Register("stub2", stub2)

	m.router = router

	// Notify router of initial window size
	if m.layout != nil {
		m.router.NotifySize(m.layout.width, m.layout.height)
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

// Init initializes the tea program
func (m Model) Init() tea.Cmd {
	if m.router == nil {
		return nil
	}
	return m.router.Init()
}

// Update handles messages and updates the model
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.router == nil {
		// Router not initialized yet, handle basic keys
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "ctrl+c", "q":
				m.quitting = true
				return m, tea.Quit
			}
		case tea.WindowSizeMsg:
			m.layout.SetSize(msg.Width, msg.Height)
		}
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			if m.wsClient != nil {
				m.wsClient.Disconnect()
			}
			return m, tea.Quit
		case "r":
			// Refresh repo
			m.loadRepo()
		case "t":
			// Toggle theme
			theme.ToggleTheme()
			m.statusMsg = "Theme toggled"
		}
	case tea.WindowSizeMsg:
		m.layout.SetSize(msg.Width, msg.Height)
		m.router.NotifySize(msg.Width, msg.Height)
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
			// Drill into single-repo mode for the specified repo
			m.repoPath = msg.RepoPath
			m.projectMode = false
			m.loadRepo()
		} else {
			// Simple view switch
			if m.router != nil {
				m.router.SwitchTo(msg.ViewName)
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
		return m.layout.Render(m.router, m.repoInfo, m.router.View())
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
	// Load repo if path not set
	if m.repoPath == "" {
		m.loadRepo()
	}

	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	_, err := p.Run()
	return err
}
