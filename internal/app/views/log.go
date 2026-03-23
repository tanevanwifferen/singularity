package views

import (
	"bufio"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"git-frontend/internal/app/components"
	"git-frontend/internal/git"
	"git-frontend/internal/theme"

	"github.com/charmbracelet/bubbletea"
)

// LogCommit represents a commit entry in the log view
type LogCommit struct {
	Hash       string
	ShortHash  string
	Subject    string
	Body       string
	Author     string
	AuthorEmail string
	Date       time.Time
	Refs       string
	FilesCount int
}

// LogView displays a scrollable commit log with filtering and detail view.
type LogView struct {
	repoPath    string
	repo        *git.RepoInfo
	commits     []LogCommit
	filter      *components.Filter[LogCommit]
	loading     bool
	loadingMore bool
	err         error
	width       int
	height      int

	// Filter state
	authorFilter string
	messageFilter string

	// Pagination
	pageSize int
	hasMore  bool

	// Detail panel state
	showDetail   bool
	detailCommit *LogCommit

	// Filter mode (author vs message)
	filterMode string // "" or "author" or "message"
}

// NewLogView creates a new log view.
func NewLogView(repoPath string) *LogView {
	v := &LogView{
		repoPath:  repoPath,
		width:     80,
		height:    24,
		pageSize:  50,
		hasMore:   true,
	}

	// Initialize the filter with commit items
	commits := []LogCommit{}
	v.filter = components.NewFilter(commits, v.renderCommitItem)
	listHeight := v.height - v.headerFooterLines()
	if listHeight < 3 {
		listHeight = 3
	}
	v.filter.SetHeight(listHeight)

	return v
}

// Init initializes the log view.
func (v *LogView) Init() tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		v.loadCommits(true)
		return RefreshDoneMsg{}
	}
}

// loadCommits loads commits from git log with pagination and filtering.
func (v *LogView) loadCommits(reset bool) {
	v.err = nil

	if reset {
		v.commits = []LogCommit{}
		v.hasMore = true
	}

	if !v.hasMore && !reset {
		return
	}

	repo, err := git.OpenRepo(v.repoPath)
	if err != nil {
		v.err = fmt.Errorf("failed to open repo: %w", err)
		v.loading = false
		return
	}
	v.repo = repo

	// Build git log arguments
	args := []string{
		"-C", v.repoPath,
		"log",
		fmt.Sprintf("-%d", v.pageSize),
		"--format=%H|%h|%s|%b|%an|%ae|%at|%D",
	}

	// Add author filter if set
	if v.authorFilter != "" {
		args = append(args, "--author="+v.authorFilter)
	}

	// Add message filter if set
	if v.messageFilter != "" {
		args = append(args, "--grep="+v.messageFilter)
	}

	// Handle pagination - skip existing commits if loading more
	if !reset && len(v.commits) > 0 {
		// Count total commits to skip
		skip := len(v.commits)
		args = append(args, fmt.Sprintf("--skip=%d", skip))
	}

	cmd := exec.Command("git", args...)
	output, err := cmd.Output()
	if err != nil {
		// Check if it's because there are no more commits
		if strings.Contains(err.Error(), "exited with status 1") && len(output) == 0 {
			v.hasMore = false
			v.loading = false
			v.loadingMore = false
			return
		}
		v.err = fmt.Errorf("failed to get commits: %w", err)
		v.loading = false
		v.loadingMore = false
		return
	}

	newCommits, err := v.parseCommits(string(output))
	if err != nil {
		v.err = err
		v.loading = false
		v.loadingMore = false
		return
	}

	if reset {
		v.commits = newCommits
	} else {
		v.commits = append(v.commits, newCommits...)
	}

	// Check if we have more commits
	if len(newCommits) < v.pageSize {
		v.hasMore = false
	}

	// Update filter with new commit list
	v.filter.SetItems(v.commits)

	v.loading = false
	v.loadingMore = false
}

// parseCommits parses git log output into LogCommit structs
func (v *LogView) parseCommits(output string) ([]LogCommit, error) {
	var commits []LogCommit
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Split on | but handle body (message) which may contain |
		parts := strings.SplitN(line, "|", 9)
		if len(parts) < 8 {
			continue
		}

		var timestamp int64
		fmt.Sscanf(parts[6], "%d", &timestamp)

		commit := LogCommit{
			Hash:       parts[0],
			ShortHash:  parts[1],
			Subject:    parts[2],
			Body:       parts[3],
			Author:     parts[4],
			AuthorEmail: parts[5],
			Date:       time.Unix(timestamp, 0),
			Refs:       parts[7],
		}

		commits = append(commits, commit)
	}

	return commits, nil
}

// loadCommitFiles loads the list of files changed in a commit
func (v *LogView) loadCommitFiles(hash string) {
	cmd := exec.Command("git", "-C", v.repoPath, "diff-tree", "--no-commit-id", "--numstat", "-r", hash+"^.."+hash)
	output, err := cmd.Output()
	if err != nil {
		// Try alternate approach for the commit
		cmd = exec.Command("git", "-C", v.repoPath, "show", "--numstat", "--format=", hash)
		output, err = cmd.Output()
		if err != nil {
			return
		}
	}

	count := 0
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		// Count lines that look like numstat output (additions\tdeletions\tfilename)
		parts := strings.Split(line, "\t")
		if len(parts) >= 3 {
			count++
		}
	}

	if v.detailCommit != nil && v.detailCommit.Hash == hash {
		v.detailCommit.FilesCount = count
	}
}

// Update handles update events.
func (v *LogView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle detail panel first
		if v.showDetail {
			return v, v.handleDetailKey(msg)
		}

		// Main view keys
		switch msg.String() {
		case "r":
			v.loading = true
			return v, func() tea.Msg {
				v.loadCommits(true)
				return RefreshDoneMsg{}
			}

		case "/":
			// Activate filter mode - show filter type prompt first
			v.filterMode = ""

		case "a":
			// Set author filter mode
			if v.authorFilter != "" {
				v.authorFilter = ""
			} else {
				v.filterMode = "author"
			}

		case "s":
			// Set message filter mode
			if v.messageFilter != "" {
				v.messageFilter = ""
			} else {
				v.filterMode = "message"
			}

		case "enter":
			// Show commit detail
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				v.detailCommit = &item
				v.loadCommitFiles(item.Hash)
				v.showDetail = true
			}

		case "esc":
			// Clear filter if active, or clear filter mode
			if v.filterMode != "" {
				v.filterMode = ""
				return v, nil
			}
			if v.authorFilter != "" {
				v.authorFilter = ""
				v.loadCommits(true)
				return v, func() tea.Msg { return RefreshDoneMsg{} }
			}
			if v.messageFilter != "" {
				v.messageFilter = ""
				v.loadCommits(true)
				return v, func() tea.Msg { return RefreshDoneMsg{} }
			}
			if v.filter.IsActive() {
				v.filter.Update(msg)
			}

		case "g":
			// Go to bottom - load more
			if v.hasMore && !v.loadingMore {
				v.loadingMore = true
				return v, func() tea.Msg {
					v.loadCommits(false)
					return RefreshDoneMsg{}
				}
			}

		case "f":
			// Filter - if in filter mode, activate filter input
			if v.filterMode != "" {
				v.filter.Update(msg)
			}
		}

		// Handle filter mode activation via typing
		if v.filterMode != "" {
			if v.filterMode == "author" {
				// Author filter - handle character input
				if len(msg.Runes) == 1 {
					r := msg.Runes[0]
					if r >= 32 && r <= 126 {
						v.authorFilter += string(r)
						return v, func() tea.Msg {
							v.loadCommits(true)
							return RefreshDoneMsg{}
						}
					}
				} else if msg.String() == "backspace" && len(v.authorFilter) > 0 {
					v.authorFilter = v.authorFilter[:len(v.authorFilter)-1]
					return v, func() tea.Msg {
						v.loadCommits(true)
						return RefreshDoneMsg{}
					}
				} else if msg.String() == "enter" {
					v.filterMode = ""
				} else if msg.String() == "esc" {
					v.authorFilter = ""
					v.filterMode = ""
				}
			} else if v.filterMode == "message" {
				// Message filter - use the filter component
				v.filter.Update(msg)
				// Sync filter text to message filter
				v.messageFilter = v.filter.FilterText()
			}
		}

		// Pass to filter for navigation
		if v.filter != nil {
			v.filter.Update(msg)
		}

		// Check if we scrolled to bottom for auto-load-more
		// (handled by filter's internal state)

	case RefreshDoneMsg:
		v.loading = false
		v.loadingMore = false

		// Check if we should auto-load more
		if v.hasMore && v.filter != nil {
			_, idx := v.filter.SelectedItem()
			if idx >= len(v.commits)-5 {
				// Near the bottom, load more
				v.loadingMore = true
				return v, func() tea.Msg {
					v.loadCommits(false)
					return RefreshDoneMsg{}
				}
			}
		}

	case tea.WindowSizeMsg:
		v.width = msg.Width
		v.height = msg.Height
		if v.filter != nil {
			listHeight := msg.Height - v.headerFooterLines()
			if listHeight < 3 {
				listHeight = 3
			}
			v.filter.SetHeight(listHeight)
		}

	case tea.MouseMsg:
		// Handle mouse events for the filter/list
		if v.filter != nil {
			if v.filter.HandleMouse(msg) {
				return v, nil
			}
		}
	}

	return v, nil
}

// handleDetailKey handles key events in the detail panel.
func (v *LogView) handleDetailKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		v.showDetail = false
		v.detailCommit = nil
	}
	return nil
}

// renderCommitItem renders a single commit item in the list.
func (v *LogView) renderCommitItem(commit LogCommit, index int, selected bool) string {
	th := theme.GetTheme()

	namePrefix := "  "
	if selected {
		namePrefix = " >"
	}

	// Short date format
	dateStr := commit.Date.Format("Jan 02 15:04")

	var line strings.Builder

	// Hash
	hashStyle := th.InfoStyle
	if selected {
		hashStyle = th.DashboardAccentStyle
	}
	line.WriteString(hashStyle.Render(fmt.Sprintf("%s%s", namePrefix, commit.ShortHash)))

	// Subject
	line.WriteString(" " + th.StatsStyle.Render(truncate(commit.Subject, 50)))

	// Date and author on the same line for compactness
	if selected {
		line.WriteString(fmt.Sprintf("\n   %s • %s",
			th.MutedTextStyle.Render(commit.Author),
			th.MutedTextStyle.Render(dateStr)))
	}

	return line.String()
}

// truncate truncates a string to the given length
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// View renders the log view.
func (v *LogView) View() string {
	th := theme.GetTheme()

	// Loading state
	if v.loading {
		return th.StatsStyle.Render(" Loading commits...")
	}

	var s strings.Builder

	// Header
	s.WriteString(th.DashboardTitle.Render(" Commit Log "))
	s.WriteString("\n\n")

	// Repo info line
	if v.repo != nil {
		s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" Repository: %s ", filepath.Base(v.repoPath))))
		if v.repo.IsDirty {
			s.WriteString(th.DashboardErrorStyle.Render("● dirty"))
		}
		s.WriteString("\n")
	}

	// Filter status
	if v.authorFilter != "" {
		s.WriteString(fmt.Sprintf(" %s %s %s\n",
			th.DashboardAccentStyle.Render("Author filter:"),
			th.StatsStyle.Render(v.authorFilter),
			th.Help.Render("(a to clear)")))
	}
	if v.messageFilter != "" {
		s.WriteString(fmt.Sprintf(" %s %s\n",
			th.DashboardAccentStyle.Render("Message filter:"),
			th.StatsStyle.Render(v.messageFilter)))
	}

	// Filter mode prompt
	if v.filterMode == "author" {
		s.WriteString(th.DashboardAccentStyle.Render(" Author: " + v.authorFilter + "█"))
		s.WriteString(" (Enter to confirm, Esc to cancel)\n\n")
	} else if v.filterMode == "message" {
		s.WriteString(th.DashboardAccentStyle.Render(" Search: " + v.messageFilter + "█"))
		s.WriteString(" (Enter to confirm, Esc to cancel)\n\n")
	} else {
		s.WriteString("\n")
	}

	// Help hints
	if v.filterMode == "" {
		if v.filter.IsActive() {
			s.WriteString(v.filter.View())
		} else {
			helpText := " a: Filter by author   s: Search message   Enter: View detail   g: Load more   /: Quick search"
			if v.hasMore {
				helpText += "   ↑↓: Navigate"
			}
			s.WriteString(th.Help.Render(helpText))
			s.WriteString("\n\n")
			s.WriteString(v.filter.View())
		}
	}

	// Loading more indicator
	if v.loadingMore {
		s.WriteString("\n")
		s.WriteString(th.Help.Render(" Loading more commits..."))
	}

	// No more commits indicator
	if !v.hasMore && len(v.commits) > 0 {
		s.WriteString("\n")
		s.WriteString(th.MutedTextStyle.Render(" End of commit history "))
	}

	// Detail panel
	if v.showDetail && v.detailCommit != nil {
		s.WriteString("\n")
		s.WriteString(th.StatsStyle.Render(" ════════════════════════════════════════════════════════ "))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(" Commit Detail "))
		s.WriteString("\n\n")

		// Hash
		s.WriteString(fmt.Sprintf(" %s %s\n",
			th.BranchStyle.Render("Hash:"),
			th.InfoStyle.Render(v.detailCommit.Hash)))

		// Refs (branches, tags)
		if v.detailCommit.Refs != "" {
			s.WriteString(fmt.Sprintf(" %s %s\n",
				th.BranchStyle.Render("Refs:"),
				th.DashboardAccentStyle.Render(v.detailCommit.Refs)))
		}

		// Author
		s.WriteString(fmt.Sprintf(" %s %s <%s>\n",
			th.BranchStyle.Render("Author:"),
			th.StatsStyle.Render(v.detailCommit.Author),
			th.MutedTextStyle.Render(v.detailCommit.AuthorEmail)))

		// Date
		s.WriteString(fmt.Sprintf(" %s %s\n",
			th.BranchStyle.Render("Date:"),
			th.StatsStyle.Render(v.detailCommit.Date.Format(time.RFC822))))

		// Files count
		if v.detailCommit.FilesCount > 0 {
			s.WriteString(fmt.Sprintf(" %s %d file(s)\n",
				th.BranchStyle.Render("Files:"),
				v.detailCommit.FilesCount))
		}

		s.WriteString("\n")

		// Subject
		s.WriteString(th.DashboardTitle.Render(" Subject "))
		s.WriteString("\n")
		s.WriteString(th.StatsStyle.Render(v.detailCommit.Subject))

		// Body (commit message body)
		if v.detailCommit.Body != "" {
			s.WriteString("\n\n")
			s.WriteString(th.DashboardTitle.Render(" Body "))
			s.WriteString("\n")
			// Word wrap body
			bodyLines := wordWrap(v.detailCommit.Body, 70)
			for _, line := range bodyLines {
				s.WriteString(th.MutedTextStyle.Render(line) + "\n")
			}
		}

		s.WriteString("\n\n")
		s.WriteString(th.Help.Render(" ESC: Close detail "))
	}

	// Error display
	if v.err != nil {
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" Error: %v", v.err)))
	}

	// Footer
	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")
	footerText := " r: Refresh   a: Filter author   s: Search message   Enter: View detail   ESC: Clear filters"
	if v.hasMore {
		footerText += "   g: Load more"
	}
	s.WriteString(th.Help.Render(footerText))

	return s.String()
}

// wordWrap wraps text to the specified width
func wordWrap(text string, width int) []string {
	words := strings.Fields(text)
	var lines []string
	var currentLine strings.Builder

	for _, word := range words {
		if currentLine.Len()+len(word)+1 > width {
			if currentLine.Len() > 0 {
				lines = append(lines, currentLine.String())
				currentLine.Reset()
			}
		}
		if currentLine.Len() > 0 {
			currentLine.WriteString(" ")
		}
		currentLine.WriteString(word)
	}

	if currentLine.Len() > 0 {
		lines = append(lines, currentLine.String())
	}

	return lines
}

// ShortHelp returns a short help string.
func (v *LogView) ShortHelp() string {
	return "a: Author filter  s: Message search  Enter: View detail  ↑↓: Navigate  g: Load more  r: Refresh"
}

// headerFooterLines returns the number of lines used by the view chrome
// (header, repo info, filter prompts, help bar, footer) so the list gets
// the remaining space.
func (v *LogView) headerFooterLines() int {
	lines := 0
	// Title + blank line
	lines += 2
	// Repo info line
	if v.repo != nil {
		lines++
	}
	// Filter status lines
	if v.authorFilter != "" {
		lines++
	}
	if v.messageFilter != "" {
		lines++
	}
	// Filter mode prompt or blank line
	if v.filterMode != "" {
		lines += 2 // prompt + blank line
	} else {
		lines++ // blank line
	}
	// Help text + blank line (when not in filter mode and filter not active)
	if v.filterMode == "" && (v.filter == nil || !v.filter.IsActive()) {
		lines += 2
	}
	// Footer (separator + help line + blank)
	lines += 3
	return lines
}

// SetSize updates the view dimensions.
func (v *LogView) SetSize(width, height int) {
	v.width = width
	v.height = height
	if v.filter != nil {
		listHeight := height - v.headerFooterLines()
		if listHeight < 3 {
			listHeight = 3
		}
		v.filter.SetHeight(listHeight)
	}
}

// GetRepoPath returns the repository path.
func (v *LogView) GetRepoPath() string {
	return v.repoPath
}

// Refresh reloads commit data.
func (v *LogView) Refresh() error {
	v.loadCommits(true)
	return v.err
}

// KeyBindings returns the keybindings for this view.
func (v *LogView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "r", Description: "Refresh commit log"},
		{Key: "a", Description: "Filter by author"},
		{Key: "s", Description: "Search commit messages"},
		{Key: "/", Description: "Quick search"},
		{Key: "↑/k", Description: "Navigate up"},
		{Key: "↓/j", Description: "Navigate down"},
		{Key: "Enter", Description: "View commit detail"},
		{Key: "g", Description: "Load more commits"},
		{Key: "Esc", Description: "Clear filters / Close detail"},
		{Key: "1", Description: "Switch to Overview"},
		{Key: "2", Description: "Switch to Branches"},
		{Key: "3", Description: "Switch to Stashes"},
		{Key: "4", Description: "Switch to Worktrees"},
	}
}
