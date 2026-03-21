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

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			Background(lipgloss.Color("57")).
			Padding(0, 1)

	versionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Italic(true)

	infoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("75"))
)

// Model represents the application state
type Model struct {
	quitting   bool
	repoPath   string
	repoInfo   *git.RepoInfo
	statusMsg  string
	errorMsg   string
}

// New creates a new app model
func New() *Model {
	return &Model{}
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
}

// Init initializes the tea program
func (m Model) Init() tea.Cmd {
	return nil
}

// Update handles messages and updates the model
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		case "r":
			// Refresh repo
			m.loadRepo()
		}
	}
	return m, nil
}

// View renders the TUI
func (m Model) View() string {
	if m.quitting {
		return "Goodbye!\n"
	}

	// Build the view
	view := titleStyle.Render("Git Frontend") + "\n"
	view += versionStyle.Render("v" + version) + "\n\n"

	if m.errorMsg != "" {
		view += lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Render("Error: "+m.errorMsg+"\n\n")
	}

	if m.statusMsg != "" {
		view += infoStyle.Render(m.statusMsg) + "\n\n"
	}

	// Show repo info
	if m.repoInfo != nil {
		view += m.renderRepoInfo()
	} else {
		view += helpStyle.Render("No repository loaded") + "\n"
	}

	view += "\n" + helpStyle.Render("Press q or Ctrl+C to quit, r to refresh") + "\n"

	return view
}

// renderRepoInfo renders repository information
func (m Model) renderRepoInfo() string {
	repo := m.repoInfo

	var view string

	// Branch info
	if repo.CurrentBranch != "" {
		branchStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
		view += branchStyle.Render("Branch: ") + repo.CurrentBranch + "\n"
	} else {
		view += "Branch: (detached)\n"
	}

	// HEAD
	if len(repo.HEAD) >= 7 {
		view += "HEAD: " + infoStyle.Render(repo.HEAD[:7]) + "\n"
	}

	// Status
	if repo.IsDirty {
		dirtyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("226"))
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
				// Highlight current branch
				branchName = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Render(branch.Name)
			}
			
			view += fmt.Sprintf("  %s", branchName)
			
			// Ahead/behind
			if branch.Ahead > 0 || branch.Behind > 0 {
				aheadStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
				behindStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
				
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
