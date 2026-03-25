package views

import (
	"bufio"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// LogCommit represents a commit entry in the log view
type LogCommit struct {
	Hash        string
	ShortHash   string
	Subject     string
	Body        string
	Author      string
	AuthorEmail string
	Date        time.Time
	Refs        string
	FilesCount  int
}

// LogView displays a scrollable commit log with filtering and detail view.
type LogView struct {
	viewBase
	repo        *git.RepoInfo
	commits     []LogCommit
	filter      *components.Filter[LogCommit]
	loading     bool
	loadingMore bool
	err         error

	// Filter state
	authorFilter  string
	messageFilter string

	// Pagination
	pageSize int
	hasMore  bool

	// Detail panel state
	showDetail   bool
	detailCommit *LogCommit

	// Detail sub-view: split-panel with file list + diff
	detailFiles       []git.FileChange
	detailFileIdx     int
	detailFocusFiles  bool // true = file list focused, false = diff panel focused
	detailDiffLines   []DiffLine
	detailDiffScroll  int
	detailDiffRaw     string
	detailLoadingDiff bool

	// Filter mode (author vs message)
	filterMode string // "" or "author" or "message"

	// Commit operations modal states
	cherryPickConfirm components.ConfirmPrompt
	showResetMenu     bool // shows soft/mixed/hard submenu
	resetHash         string
	resetMode         string // "soft", "mixed", "hard"
	resetConfirm      components.ConfirmPrompt
	showRewordEditor  bool
	rewordInput       components.TextInput
	operationErr      error  // transient error from last operation
	operationSuccess  string // transient success message
}

// NewLogView creates a new log view.
func NewLogView(repoPath string) *LogView {
	v := &LogView{
		viewBase: viewBase{repoPath: repoPath, width: 80, height: 24},
		pageSize: 50,
		hasMore:  true,
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
			Hash:        parts[0],
			ShortHash:   parts[1],
			Subject:     parts[2],
			Body:        parts[3],
			Author:      parts[4],
			AuthorEmail: parts[5],
			Date:        time.Unix(timestamp, 0),
			Refs:        parts[7],
		}

		commits = append(commits, commit)
	}

	return commits, nil
}

// openCommitDetail opens the split-panel detail view for a commit
func (v *LogView) openCommitDetail(commit *LogCommit) {
	v.detailCommit = commit
	v.showDetail = true
	v.detailFileIdx = 0
	v.detailFocusFiles = true
	v.detailDiffLines = nil
	v.detailDiffScroll = 0
	v.detailDiffRaw = ""
	v.detailLoadingDiff = false

	// Load file list
	files, err := git.GetCommitFiles(v.repoPath, commit.Hash)
	if err != nil {
		v.detailFiles = nil
		return
	}
	v.detailFiles = files
	commit.FilesCount = len(files)

	// Auto-load diff for first file
	if len(files) > 0 {
		v.loadDetailFileDiff(0)
	}
}

// loadDetailFileDiff loads the diff for the file at the given index
func (v *LogView) loadDetailFileDiff(idx int) {
	if idx < 0 || idx >= len(v.detailFiles) || v.detailCommit == nil {
		return
	}
	v.detailLoadingDiff = true
	file := v.detailFiles[idx]
	raw, err := git.GetCommitFileDiff(v.repoPath, v.detailCommit.Hash, file.NewPath)
	if err != nil {
		v.detailDiffRaw = ""
		v.detailDiffLines = nil
	} else {
		v.detailDiffRaw = raw
		v.detailDiffLines = v.parseDiff(raw)
	}
	v.detailDiffScroll = 0
	v.detailLoadingDiff = false
}

// closeDetail closes the detail sub-view
func (v *LogView) closeDetail() {
	v.showDetail = false
	v.detailCommit = nil
	v.detailFiles = nil
	v.detailFileIdx = 0
	v.detailDiffLines = nil
	v.detailDiffScroll = 0
	v.detailDiffRaw = ""
}

// Update handles update events.
func (v *LogView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return v.handleLogKeyMsg(msg)

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

// handleLogKeyMsg dispatches key events based on the current modal/view state.
func (v *LogView) handleLogKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle modal states first (highest priority)
	if handled, cmd := v.cherryPickConfirm.HandleKey(msg); handled {
		return v, cmd
	}
	if handled, cmd := v.resetConfirm.HandleKey(msg); handled {
		return v, cmd
	}
	if v.showResetMenu {
		return v, v.handleResetMenu(msg)
	}
	if v.showRewordEditor {
		return v, v.handleRewordEditor(msg)
	}

	// Handle detail panel
	if v.showDetail {
		return v, v.handleDetailKey(msg)
	}

	// Main list view keys
	return v.handleListKeyMsg(msg)
}

// handleListKeyMsg handles key events in the main commit list view.
func (v *LogView) handleListKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
			v.openCommitDetail(&item)
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

	case "y":
		// Copy commit hash to clipboard
		if item, idx := v.filter.SelectedItem(); idx >= 0 {
			v.operationErr = nil
			v.operationSuccess = ""
			if err := git.CopyToClipboard(item.Hash); err != nil {
				v.operationErr = err
			} else {
				v.operationSuccess = fmt.Sprintf("Copied %s to clipboard", item.ShortHash)
			}
		}

	case "c":
		// Cherry-pick selected commit (show confirmation)
		if item, idx := v.filter.SelectedItem(); idx >= 0 {
			v.operationErr = nil
			v.operationSuccess = ""
			hash := item.Hash
			v.cherryPickConfirm.Show("Cherry-pick Commit",
				fmt.Sprintf("Cherry-pick commit %s onto current branch?", hash[:min(8, len(hash))]),
				func() tea.Cmd {
					if err := git.CherryPick(v.repoPath, hash); err != nil {
						v.operationErr = err
					} else {
						v.operationSuccess = fmt.Sprintf("Cherry-picked %s", hash[:8])
						v.loadCommits(true)
					}
					return nil
				})
		}

	case "w":
		// Reword HEAD commit message
		if item, idx := v.filter.SelectedItem(); idx >= 0 {
			// Only allow reword on the first commit (HEAD)
			if idx == 0 {
				v.showRewordEditor = true
				v.rewordInput.Set(item.Subject)
				v.operationErr = nil
				v.operationSuccess = ""
			} else {
				v.operationErr = fmt.Errorf("can only reword HEAD commit (first in list)")
				v.operationSuccess = ""
			}
		}

	case "x":
		// Reset to commit (show submenu)
		if item, idx := v.filter.SelectedItem(); idx >= 0 {
			_ = idx // idx used only for bounds check
			v.resetHash = item.Hash
			v.showResetMenu = true
			v.resetMode = "mixed" // default selection
			v.operationErr = nil
			v.operationSuccess = ""
		}
	}

	// Handle filter mode activation via typing
	if v.filterMode != "" {
		if v.filterMode == "author" {
			// Author filter - handle character input
			if msg.Paste && len(msg.Runes) > 0 {
				v.authorFilter += string(msg.Runes)
				return v, func() tea.Msg {
					v.loadCommits(true)
					return RefreshDoneMsg{}
				}
			} else if len(msg.Runes) == 1 {
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

	return v, nil
}

// handleDetailKey handles key events in the detail split-panel view.
func (v *LogView) handleDetailKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		v.closeDetail()

	case "tab":
		// Switch focus between file list and diff panel
		v.detailFocusFiles = !v.detailFocusFiles

	case "up", "k":
		if v.detailFocusFiles {
			// Navigate file list up
			if v.detailFileIdx > 0 {
				v.detailFileIdx--
				v.loadDetailFileDiff(v.detailFileIdx)
			}
		} else {
			// Scroll diff up
			if v.detailDiffScroll > 0 {
				v.detailDiffScroll--
			}
		}

	case "down", "j":
		if v.detailFocusFiles {
			// Navigate file list down
			if v.detailFileIdx < len(v.detailFiles)-1 {
				v.detailFileIdx++
				v.loadDetailFileDiff(v.detailFileIdx)
			}
		} else {
			// Scroll diff down
			maxScroll := len(v.detailDiffLines) - v.detailDiffVisibleLines()
			if maxScroll < 0 {
				maxScroll = 0
			}
			if v.detailDiffScroll < maxScroll {
				v.detailDiffScroll++
			}
		}

	case "enter":
		// When on file list, load that file's diff and switch focus to diff
		if v.detailFocusFiles && v.detailFileIdx < len(v.detailFiles) {
			v.loadDetailFileDiff(v.detailFileIdx)
			v.detailFocusFiles = false
		}

	case "g":
		// Go to top
		if !v.detailFocusFiles {
			v.detailDiffScroll = 0
		} else if len(v.detailFiles) > 0 {
			v.detailFileIdx = 0
			v.loadDetailFileDiff(0)
		}

	case "G":
		// Go to bottom
		if !v.detailFocusFiles {
			maxScroll := len(v.detailDiffLines) - v.detailDiffVisibleLines()
			if maxScroll < 0 {
				maxScroll = 0
			}
			v.detailDiffScroll = maxScroll
		} else if len(v.detailFiles) > 0 {
			v.detailFileIdx = len(v.detailFiles) - 1
			v.loadDetailFileDiff(v.detailFileIdx)
		}
	}
	return nil
}

// handleResetMenu handles the soft/mixed/hard submenu for reset.
func (v *LogView) handleResetMenu(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "s":
		v.showResetMenu = false
		v.showResetConfirmForMode("soft")
	case "m":
		v.showResetMenu = false
		v.showResetConfirmForMode("mixed")
	case "h":
		v.showResetMenu = false
		v.showResetConfirmForMode("hard")
	case "esc":
		v.showResetMenu = false
		v.resetHash = ""
		v.resetMode = ""
	}
	return nil
}

// showResetConfirmForMode configures and shows the reset confirmation dialog.
func (v *LogView) showResetConfirmForMode(mode string) {
	hash := v.resetHash
	v.resetMode = mode
	v.resetConfirm.ShowWithCancel("Confirm Reset",
		fmt.Sprintf("Reset --%s to %s?\nThis may modify your working tree.", mode, hash[:min(8, len(hash))]),
		func() tea.Cmd {
			v.resetHash = ""
			v.resetMode = ""
			if err := git.ResetToCommit(v.repoPath, hash, mode); err != nil {
				v.operationErr = err
			} else {
				v.operationSuccess = fmt.Sprintf("Reset --%s to %s", mode, hash[:8])
				v.loadCommits(true)
			}
			return nil
		},
		func() {
			v.resetHash = ""
			v.resetMode = ""
		})
}

// handleRewordEditor handles the text editor for rewording HEAD commit.
func (v *LogView) handleRewordEditor(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		// Confirm reword
		if v.rewordInput.Value != "" {
			newMsg := v.rewordInput.Value
			v.showRewordEditor = false
			v.rewordInput.Clear()
			if err := git.AmendCommitMessage(v.repoPath, newMsg); err != nil {
				v.operationErr = err
			} else {
				v.operationSuccess = "Commit message amended"
				v.loadCommits(true)
			}
		}
	case "esc":
		v.showRewordEditor = false
		v.rewordInput.Clear()
	default:
		v.rewordInput.HandleKey(msg)
	}
	return nil
}

// detailDiffVisibleLines returns the number of diff lines visible in the panel
func (v *LogView) detailDiffVisibleLines() int {
	// Account for header (commit info ~6 lines + panel chrome ~4 lines)
	visible := v.height - 10
	if visible < 5 {
		visible = 5
	}
	return visible
}

// parseDiff parses raw diff output into structured DiffLine slices with line numbers.
// Reuses the same logic as DiffView.parseDiff.
func (v *LogView) parseDiff(rawDiff string) []DiffLine {
	var lines []DiffLine
	var oldLineNum, newLineNum int

	for _, line := range strings.Split(rawDiff, "\n") {
		diffLine := DiffLine{Content: line}

		if strings.HasPrefix(line, "@@") {
			diffLine.LineType = "@"
			parts := strings.Fields(line)
			for _, p := range parts {
				if strings.HasPrefix(p, "-") && !strings.HasPrefix(p, "--") {
					numStr := strings.TrimPrefix(p, "-")
					if idx := strings.Index(numStr, ","); idx > 0 {
						numStr = numStr[:idx]
					}
					if n, err := strconv.Atoi(numStr); err == nil {
						oldLineNum = n
					}
				} else if strings.HasPrefix(p, "+") && !strings.HasPrefix(p, "++") {
					numStr := strings.TrimPrefix(p, "+")
					if idx := strings.Index(numStr, ","); idx > 0 {
						numStr = numStr[:idx]
					}
					if n, err := strconv.Atoi(numStr); err == nil {
						newLineNum = n
					}
				}
			}
		} else if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") {
			diffLine.LineType = "H"
			oldLineNum = 0
			newLineNum = 0
		} else if strings.HasPrefix(line, "+") {
			diffLine.LineType = "+"
			diffLine.NewLineNum = newLineNum
			newLineNum++
		} else if strings.HasPrefix(line, "-") {
			diffLine.LineType = "-"
			diffLine.OldLineNum = oldLineNum
			oldLineNum++
		} else if strings.HasPrefix(line, " ") {
			diffLine.LineType = " "
			diffLine.OldLineNum = oldLineNum
			diffLine.NewLineNum = newLineNum
			oldLineNum++
			newLineNum++
		} else {
			diffLine.LineType = ""
		}

		lines = append(lines, diffLine)
	}

	return lines
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

	// If detail view is active, render split-panel detail instead
	if v.showDetail && v.detailCommit != nil {
		return v.renderDetailView()
	}

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
			helpText := " a: Author   s: Search   Enter: Detail   y: Copy   c: Cherry-pick   w: Reword   x: Reset   g: More"
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

	// Error display
	if v.err != nil {
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" Error: %v", v.err)))
	}

	// Operation status display
	if v.operationErr != nil {
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" Operation error: %v", v.operationErr)))
	}
	if v.operationSuccess != "" {
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf(" %s", v.operationSuccess)))
	}

	// Footer
	s.WriteString("\n")
	s.WriteString(renderSeparator())
	footerText := " r: Refresh   a: Author   s: Search   Enter: Detail   y: Copy hash   c: Cherry-pick   w: Reword   x: Reset"
	if v.hasMore {
		footerText += "   g: More"
	}
	s.WriteString(th.Help.Render(footerText))

	// Render modal overlays on top
	base := s.String()

	if v.cherryPickConfirm.Visible {
		return base + "\n\n" + v.cherryPickConfirm.Render(modalWidth(v.width))
	}

	if v.showResetMenu {
		shortHash := v.resetHash
		if len(shortHash) > 8 {
			shortHash = shortHash[:8]
		}
		m := components.NewModal(
			"Reset to "+shortHash,
			"Choose reset mode:\n\n"+
				"  [s] Soft  - keep changes staged\n"+
				"  [m] Mixed - keep changes unstaged (default)\n"+
				"  [h] Hard  - discard all changes\n\n"+
				"  Esc to cancel",
		)
		m.SetSize(v.width, v.height)
		return m.Render(base)
	}

	if v.resetConfirm.Visible {
		return base + "\n\n" + v.resetConfirm.Render(modalWidth(v.width))
	}

	if v.showRewordEditor {
		// Render inline text editor modal
		content := fmt.Sprintf("Edit commit message for HEAD:\n\n> %s\n\nEnter: Confirm   Esc: Cancel", v.rewordInput.RenderPlain())
		m := components.NewModal("Reword Commit", content)
		m.SetSize(v.width, v.height)
		return m.Render(base)
	}

	return base
}

// renderDetailView renders the full split-panel commit detail view
func (v *LogView) renderDetailView() string {
	th := theme.GetTheme()
	var s strings.Builder

	// Header with commit info
	s.WriteString(th.DashboardTitle.Render(" Commit Detail "))
	s.WriteString("\n")

	// Commit metadata (compact)
	s.WriteString(fmt.Sprintf(" %s %s",
		th.BranchStyle.Render("Hash:"),
		th.InfoStyle.Render(v.detailCommit.ShortHash)))
	if v.detailCommit.Refs != "" {
		s.WriteString(fmt.Sprintf("  %s",
			th.DashboardAccentStyle.Render(v.detailCommit.Refs)))
	}
	s.WriteString("\n")

	s.WriteString(fmt.Sprintf(" %s %s <%s>  %s %s\n",
		th.BranchStyle.Render("Author:"),
		th.StatsStyle.Render(v.detailCommit.Author),
		th.MutedTextStyle.Render(v.detailCommit.AuthorEmail),
		th.BranchStyle.Render("Date:"),
		th.MutedTextStyle.Render(v.detailCommit.Date.Format(time.RFC822))))

	s.WriteString(fmt.Sprintf(" %s %s\n",
		th.BranchStyle.Render("Subject:"),
		th.StatsStyle.Render(v.detailCommit.Subject)))

	if v.detailCommit.Body != "" {
		bodyPreview := v.detailCommit.Body
		if len(bodyPreview) > 80 {
			bodyPreview = bodyPreview[:77] + "..."
		}
		s.WriteString(fmt.Sprintf(" %s\n", th.MutedTextStyle.Render(bodyPreview)))
	}

	s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %s\n", strings.Repeat("─", v.width-2))))

	// Split panels
	leftWidth := v.width * 2 / 5
	if leftWidth < 30 {
		leftWidth = 30
	}
	if leftWidth > 60 {
		leftWidth = 60
	}
	rightWidth := v.width - leftWidth - 3 // 3 for divider

	leftPanel := v.renderDetailFileList(leftWidth)
	rightPanel := v.renderDetailDiffPanel(rightWidth)

	s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, " | ", rightPanel))

	s.WriteString("\n")

	// Footer
	helpText := " j/k: Navigate"
	if v.detailFocusFiles {
		helpText += " files"
	} else {
		helpText += " diff"
	}
	helpText += "   Tab: Switch panel   Enter: View file diff   g/G: Top/Bottom   Esc: Close"
	s.WriteString(th.Help.Render(helpText))

	return s.String()
}

// renderDetailFileList renders the file list panel in the detail view
func (v *LogView) renderDetailFileList(width int) string {
	th := theme.GetTheme()
	var s strings.Builder

	// Panel header
	focusIndicator := ""
	if v.detailFocusFiles {
		focusIndicator = " [FOCUS]"
	}
	filesCount := len(v.detailFiles)
	s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" Files (%d)%s ", filesCount, focusIndicator)))
	s.WriteString("\n")

	dividerLen := width - 2
	if dividerLen < 0 {
		dividerLen = 0
	}
	s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %s ", strings.Repeat("─", dividerLen))))
	s.WriteString("\n")

	if len(v.detailFiles) == 0 {
		s.WriteString(th.Help.Render(" No files changed"))
		return s.String()
	}

	// Calculate visible range for scrolling
	startIdx, endIdx := calcViewport(v.height, 12, v.detailFileIdx, len(v.detailFiles))

	// Render files
	for i := startIdx; i < endIdx && i < len(v.detailFiles); i++ {
		file := v.detailFiles[i]
		prefix := "  "
		style := th.BranchStyle

		if i == v.detailFileIdx {
			prefix = " >"
			style = th.SelectedBranchStyle
			if v.detailFocusFiles {
				style = th.DashboardAccentStyle
			}
		}

		// Status indicator
		statusChar := " "
		statusStyle := th.StatsStyle

		switch file.Status {
		case "A":
			statusStyle = th.DashboardAccentStyle
			statusChar = "A"
		case "M":
			statusStyle = th.WarningStyle
			statusChar = "M"
		case "D":
			statusStyle = th.DashboardErrorStyle
			statusChar = "D"
		case "R":
			statusStyle = th.InfoStyle
			statusChar = "R"
		case "C":
			statusStyle = th.InfoStyle
			statusChar = "C"
		}

		// Truncate long paths
		path := file.NewPath
		maxPathLen := width - 18
		if maxPathLen < 10 {
			maxPathLen = 10
		}
		if len(path) > maxPathLen {
			path = "..." + path[len(path)-maxPathLen+3:]
		}

		// Additions/deletions
		counts := ""
		if file.Additions > 0 || file.Deletions > 0 {
			addStr := ""
			delStr := ""
			if file.Additions > 0 {
				addStr = th.DashboardAccentStyle.Render(fmt.Sprintf("+%d", file.Additions))
			}
			if file.Deletions > 0 {
				delStr = th.DashboardErrorStyle.Render(fmt.Sprintf("-%d", file.Deletions))
			}
			counts = " " + addStr + delStr
		}

		s.WriteString(fmt.Sprintf("%s%s %s%s\n",
			prefix,
			statusStyle.Render(statusChar),
			style.Render(path),
			counts))
	}

	// Scroll indicator
	if endIdx-startIdx < len(v.detailFiles) {
		scrollInfo := fmt.Sprintf(" %d-%d of %d ", startIdx+1, endIdx, len(v.detailFiles))
		s.WriteString(th.Help.Render(scrollInfo))
	}

	return s.String()
}

// renderDetailDiffPanel renders the diff content panel in the detail view
func (v *LogView) renderDetailDiffPanel(width int) string {
	th := theme.GetTheme()
	var s strings.Builder

	// Panel header
	focusIndicator := ""
	if !v.detailFocusFiles {
		focusIndicator = " [FOCUS]"
	}

	fileName := ""
	if v.detailFileIdx < len(v.detailFiles) {
		fileName = v.detailFiles[v.detailFileIdx].NewPath
		if len(fileName) > width-20 {
			fileName = "..." + fileName[len(fileName)-width+23:]
		}
	}

	s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" Diff%s ", focusIndicator)))
	if fileName != "" {
		s.WriteString(th.MutedTextStyle.Render(fmt.Sprintf(" %s", fileName)))
	}
	s.WriteString("\n")

	dividerLen := width - 2
	if dividerLen < 0 {
		dividerLen = 0
	}
	s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %s ", strings.Repeat("─", dividerLen))))
	s.WriteString("\n")

	if v.detailLoadingDiff {
		s.WriteString(th.StatsStyle.Render(" Loading diff..."))
		return s.String()
	}

	if len(v.detailFiles) == 0 {
		s.WriteString(th.Help.Render(" No files to display"))
		return s.String()
	}

	if len(v.detailDiffLines) == 0 {
		s.WriteString(th.Help.Render(" No diff content available"))
		return s.String()
	}

	// Render scrollable diff with gutter
	gutterWidth := 6
	diffWidth := width - gutterWidth - 1
	if diffWidth < 10 {
		diffWidth = 10
	}

	visibleLines := v.detailDiffVisibleLines()

	startIdx := v.detailDiffScroll
	endIdx := startIdx + visibleLines
	if endIdx > len(v.detailDiffLines) {
		endIdx = len(v.detailDiffLines)
		startIdx = endIdx - visibleLines
		if startIdx < 0 {
			startIdx = 0
		}
	}

	for i := startIdx; i < endIdx; i++ {
		line := v.detailDiffLines[i]
		gutter := ""
		lineStyle := th.Help

		switch line.LineType {
		case "+":
			lineStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
			if line.NewLineNum > 0 {
				gutter = fmt.Sprintf(" %4d ", line.NewLineNum)
			} else {
				gutter = "      "
			}
		case "-":
			lineStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
			if line.OldLineNum > 0 {
				gutter = fmt.Sprintf(" %4d ", line.OldLineNum)
			} else {
				gutter = "      "
			}
		case "@":
			lineStyle = th.InfoStyle
			gutter = "      "
		case "H":
			lineStyle = th.Help
			gutter = "      "
		case " ":
			lineStyle = th.Help
			if line.NewLineNum > 0 {
				gutter = fmt.Sprintf(" %4d ", line.NewLineNum)
			} else if line.OldLineNum > 0 {
				gutter = fmt.Sprintf(" %4d ", line.OldLineNum)
			} else {
				gutter = "      "
			}
		default:
			lineStyle = th.Help
			gutter = "      "
		}

		content := line.Content
		if len(content) > diffWidth-2 {
			content = content[:diffWidth-5] + "..."
		}

		prefix := " "
		if line.LineType == "+" {
			prefix = "+"
		} else if line.LineType == "-" {
			prefix = "-"
		}

		s.WriteString(th.Help.Render(gutter))
		s.WriteString(lineStyle.Render(prefix + content))
		s.WriteString("\n")
	}

	// Scroll indicator
	totalLines := len(v.detailDiffLines)
	if totalLines > visibleLines {
		scrollInfo := fmt.Sprintf(" %d-%d of %d lines ", startIdx+1, endIdx, totalLines)
		if !v.detailFocusFiles {
			s.WriteString(th.Help.Render(scrollInfo + "[j/k scroll, g/G top/bottom]"))
		} else {
			s.WriteString(th.Help.Render(scrollInfo + "[Tab to navigate]"))
		}
		s.WriteString("\n")
	}

	return s.String()
}

// ShortHelp returns a short help string.
func (v *LogView) ShortHelp() string {
	if v.showDetail {
		return "j/k: Navigate  Tab: Switch panel  Enter: View diff  g/G: Top/Bottom  Esc: Close"
	}
	return "a: Author  s: Search  Enter: Detail  y: Copy hash  c: Cherry-pick  w: Reword  x: Reset  r: Refresh"
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

// SetSize updates the view dimensions and resizes the filter.
func (v *LogView) SetSize(width, height int) {
	v.viewBase.SetSize(width, height)
	if v.filter != nil {
		listHeight := height - v.headerFooterLines()
		if listHeight < 3 {
			listHeight = 3
		}
		v.filter.SetHeight(listHeight)
	}
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
		{Key: "y", Description: "Copy commit hash to clipboard"},
		{Key: "c", Description: "Cherry-pick selected commit"},
		{Key: "w", Description: "Reword HEAD commit message"},
		{Key: "x", Description: "Reset to selected commit"},
		{Key: "Tab", Description: "Switch panel (in detail view)"},
		{Key: "g/G", Description: "Top/Bottom (diff or load more)"},
		{Key: "Esc", Description: "Close detail / Clear filters"},
		{Key: "1", Description: "Switch to Overview"},
		{Key: "2", Description: "Switch to Branches"},
		{Key: "3", Description: "Switch to Stashes"},
		{Key: "4", Description: "Switch to Worktrees"},
	}
}
