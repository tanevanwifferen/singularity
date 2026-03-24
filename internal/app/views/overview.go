package views

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"singularity/internal/app/components"
	"singularity/internal/git"
	"singularity/internal/theme"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// OverviewView displays repository health at a glance.
type OverviewView struct {
	repoPath    string
	repo        *git.RepoInfo
	commits     []CommitInfo
	stashCount  int
	worktreeCnt int
	width       int
	height      int
	loading     bool
	err         error
}

// CommitInfo holds information about a recent commit.
type CommitInfo struct {
	Hash    string
	Subject string
	Author  string
	Date    time.Time
}

// NewOverviewView creates a new overview view.
func NewOverviewView(repoPath string) *OverviewView {
	return &OverviewView{
		repoPath: repoPath,
		width:    80,
		height:   24,
	}
}

// SetRepoPath updates the repository path for this view.
func (v *OverviewView) SetRepoPath(path string) { v.repoPath = path }

// Init initializes the overview view.
func (v *OverviewView) Init() tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		v.loadData()
		return RefreshDoneMsg{}
	}
}

// loadData loads all repository data.
func (v *OverviewView) loadData() {
	v.err = nil

	// Load repo info
	repo, err := git.OpenRepo(v.repoPath)
	if err != nil {
		v.err = fmt.Errorf("failed to open repo: %w", err)
		v.loading = false
		return
	}
	v.repo = repo

	// Load commits
	v.commits, err = v.getRecentCommits(v.repoPath, 8)
	if err != nil {
		// Non-fatal - just show empty commits
		v.commits = nil
	}

	// Load stash count
	stashes, err := git.GetStashList(v.repoPath)
	if err != nil {
		v.stashCount = 0
	} else {
		v.stashCount = len(stashes)
	}

	// Load worktree count
	worktrees, err := git.GetWorktrees(v.repoPath)
	if err != nil {
		v.worktreeCnt = 0
	} else {
		v.worktreeCnt = len(worktrees)
	}

	v.loading = false
}

// getRecentCommits returns the most recent commits.
func (v *OverviewView) getRecentCommits(repoPath string, count int) ([]CommitInfo, error) {
	cmd := exec.Command("git", "-C", repoPath, "log",
		fmt.Sprintf("-%d", count),
		"--format=%h|%s|%an|%at",
		"--no-merges")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var commits []CommitInfo
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 4 {
			continue
		}

		var timestamp int64
		fmt.Sscanf(parts[3], "%d", &timestamp)

		commits = append(commits, CommitInfo{
			Hash:    parts[0],
			Subject: parts[1],
			Author:  parts[2],
			Date:    time.Unix(timestamp, 0),
		})
	}

	return commits, nil
}

// RefreshDoneMsg is sent when refresh completes.
type RefreshDoneMsg struct{}

// Update handles update events.
func (v *OverviewView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "r":
			v.loading = true
			return v, func() tea.Msg {
				v.loadData()
				return RefreshDoneMsg{}
			}
		}
	case RefreshDoneMsg:
		v.loading = false
	}
	return v, nil
}

// View renders the overview.
func (v *OverviewView) View() string {
	theme := theme.GetTheme()

	if v.err != nil && v.repo == nil {
		return theme.DashboardErrorStyle.Render(fmt.Sprintf("Error: %v", v.err))
	}

	var s strings.Builder

	// Header
	s.WriteString(theme.DashboardTitle.Render(" Repository Overview "))
	s.WriteString("\n\n")

	if v.loading {
		s.WriteString(theme.StatsStyle.Render(" Loading..."))
		s.WriteString("\n")
		return s.String()
	}

	// Repository status section
	s.WriteString(theme.StatsStyle.Render(" Repository Status "))
	s.WriteString("\n")
	s.WriteString(theme.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")

	if v.repo != nil {
		// Branch
		branchDisplay := v.repo.CurrentBranch
		if branchDisplay == "" {
			branchDisplay = "(detached)"
		}
		s.WriteString(fmt.Sprintf(" %s %s", theme.BranchStyle.Render("Branch:"), branchDisplay))

		// Dirty indicator
		if v.repo.IsDirty {
			s.WriteString(theme.DashboardErrorStyle.Render(" ● dirty"))
		}
		s.WriteString("\n")

		// HEAD
		headShort := v.repo.HEAD
		if len(headShort) > 7 {
			headShort = headShort[:7]
		}
		s.WriteString(fmt.Sprintf(" %s %s\n", theme.BranchStyle.Render("HEAD:"), theme.InfoStyle.Render(headShort)))

		// Upstream sync status
		if v.repo.CurrentBranch != "" {
			branch := v.findCurrentBranch()
			if branch != nil && branch.Upstream != "" {
				s.WriteString(fmt.Sprintf(" %s %s → %s",
					theme.BranchStyle.Render("Sync:"),
					theme.StatsStyle.Render(v.repo.CurrentBranch),
					theme.StatsStyle.Render(branch.Upstream)))

				if branch.Ahead > 0 || branch.Behind > 0 {
					if branch.Ahead > 0 {
						s.WriteString(theme.DashboardAccentStyle.Render(fmt.Sprintf(" ↑%d", branch.Ahead)))
					}
					if branch.Behind > 0 {
						s.WriteString(theme.DashboardErrorStyle.Render(fmt.Sprintf(" ↓%d", branch.Behind)))
					}
				} else {
					s.WriteString(theme.StatsStyle.Render(" (up to date)"))
				}
				s.WriteString("\n")
			}
		}

		// Stash and worktree counts
		s.WriteString(fmt.Sprintf(" %s %s   %s %s\n",
			theme.BranchStyle.Render("Stashes:"),
			theme.StatsStyle.Render(fmt.Sprintf("%d", v.stashCount)),
			theme.BranchStyle.Render("Worktrees:"),
			theme.StatsStyle.Render(fmt.Sprintf("%d", v.worktreeCnt))))
	}

	s.WriteString("\n")

	// Recent commits section
	s.WriteString(theme.StatsStyle.Render(" Recent Commits "))
	s.WriteString("\n")
	s.WriteString(theme.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")

	if len(v.commits) == 0 {
		s.WriteString(theme.StatsStyle.Render(" No commits yet"))
		s.WriteString("\n")
	} else {
		for _, commit := range v.commits {
			// Truncate long subjects
			subject := commit.Subject
			if len(subject) > 50 {
				subject = subject[:47] + "..."
			}
			hashStyle := lipgloss.NewStyle().Foreground(theme.Info)
			s.WriteString(fmt.Sprintf(" %s %s\n",
				hashStyle.Render(commit.Hash),
				theme.StatsStyle.Render(subject)))
		}
	}

	return s.String()
}

// findCurrentBranch returns the current branch info.
func (v *OverviewView) findCurrentBranch() *git.BranchInfo {
	if v.repo == nil || v.repo.CurrentBranch == "" {
		return nil
	}
	for i := range v.repo.Branches {
		if v.repo.Branches[i].Name == v.repo.CurrentBranch {
			return &v.repo.Branches[i]
		}
	}
	return nil
}

// ShortHelp returns a short help string.
func (v *OverviewView) ShortHelp() string {
	return "r: Refresh"
}

// SetSize updates the view dimensions.
func (v *OverviewView) SetSize(width, height int) {
	v.width = width
	v.height = height
}

// GetRepoPath returns the repository path.
func (v *OverviewView) GetRepoPath() string {
	return v.repoPath
}

// Refresh reloads repository data.
func (v *OverviewView) Refresh() error {
	v.loadData()
	return v.err
}

// KeyBindings returns the keybindings for this view.
func (v *OverviewView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "r", Description: "Refresh repository data"},
	}
}
