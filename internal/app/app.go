package app

import (
	"fmt"
	"os"
	"path/filepath"

	"git-frontend/internal/git"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const version = "0.0.1"

// Model represents the application state
type Model struct {
	quitting   bool
	repoPath   string
	repoInfo   *git.RepoInfo
	statusMsg  string
	errorMsg   string
	router     *Router
	layout     *Layout
}

// New creates a new app model
func New() *Model {
	return &Model{
		layout: NewLayout(),
	}
}

// SetRepoPath sets the repository path
func (m *Model) SetRepoPath(path string) {
	m.repoPath = path
	m.loadRepo()
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
	overview := NewOverviewView(m.repoPath)
	router := NewRouter(overview, "Overview")

	// Register branch dashboard view
	dashboard, err := NewBranchDashboard(m.repoPath)
	if err == nil {
		router.Register("Branches", dashboard)
	}

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
			return m, tea.Quit
		case "r":
			// Refresh repo
			m.loadRepo()
		case "t":
			// Toggle theme
			ToggleTheme()
			m.statusMsg = "Theme toggled"
		}
	case tea.WindowSizeMsg:
		m.layout.SetSize(msg.Width, msg.Height)
		m.router.NotifySize(msg.Width, msg.Height)
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
	theme := GetTheme()

	if m.quitting {
		return "Goodbye!\n"
	}

	// Build the view
	view := theme.Title.Render("Git Frontend") + "\n"
	view += theme.Version.Render("v" + version) + "\n\n"

	if m.errorMsg != "" {
		view += lipgloss.NewStyle().
			Foreground(theme.Error).
			Render("Error: "+m.errorMsg+"\n\n")
	}

	if m.statusMsg != "" {
		view += theme.InfoStyle.Render(m.statusMsg) + "\n\n"
	}

	// Show repo info
	if m.repoInfo != nil {
		view += m.renderRepoInfo()
	} else {
		view += theme.Help.Render("No repository loaded") + "\n"
	}

	view += "\n" + theme.Help.Render("Press q or Ctrl+C to quit, r to refresh, t to toggle theme") + "\n"

	return view
}

// renderRepoInfo renders repository information
func (m Model) renderRepoInfo() string {
	theme := GetTheme()
	repo := m.repoInfo

	var view string

	// Branch info
	if repo.CurrentBranch != "" {
		branchStyle := lipgloss.NewStyle().Foreground(theme.Accent2).Bold(true)
		view += branchStyle.Render("Branch: ") + repo.CurrentBranch + "\n"
	} else {
		view += "Branch: (detached)\n"
	}

	// HEAD
	if len(repo.HEAD) >= 7 {
		view += "HEAD: " + theme.InfoStyle.Render(repo.HEAD[:7]) + "\n"
	}

	// Status
	if repo.IsDirty {
		dirtyStyle := lipgloss.NewStyle().Foreground(theme.Modified)
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
				branchName = lipgloss.NewStyle().Foreground(theme.Accent2).Render(branch.Name)
			}

			view += fmt.Sprintf("  %s", branchName)

			// Ahead/behind
			if branch.Ahead > 0 || branch.Behind > 0 {
				aheadStyle := lipgloss.NewStyle().Foreground(theme.Added)
				behindStyle := lipgloss.NewStyle().Foreground(theme.Removed)

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

	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
