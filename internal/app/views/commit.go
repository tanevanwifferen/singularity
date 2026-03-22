package views

import (
	"bufio"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"git-frontend/internal/theme"

	"github.com/charmbracelet/bubbletea"
)

// StagedFile represents a file in the staging area
type StagedFile struct {
	Path      string
	Additions int
	Deletions int
	IsNew     bool
	IsDeleted bool
	IsModified bool
}

// CommitView displays the staging area and allows creating commits.
type CommitView struct {
	repoPath    string
	width       int
	height      int
	loading     bool
	err         error

	// Staged and unstaged files
	stagedFiles   []StagedFile
	unstagedFiles []StagedFile

	// Navigation state
	activeSection  int // 0 = staged, 1 = unstaged
	selectedIndex  int
}

// NewCommitView creates a new commit view.
func NewCommitView(repoPath string) *CommitView {
	return &CommitView{
		repoPath:       repoPath,
		width:          80,
		height:         24,
		activeSection:  1, // Start on unstaged (more common to stage from)
		selectedIndex:  0,
	}
}

// Init initializes the commit view.
func (v *CommitView) Init() tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		v.loadFiles()
		return RefreshDoneMsg{}
	}
}

// loadFiles loads staged and unstaged files from git.
func (v *CommitView) loadFiles() {
	v.err = nil

	// Load staged files using git diff --cached --name-only --numstat
	staged, err := v.getStagedFiles()
	if err != nil {
		v.err = fmt.Errorf("failed to get staged files: %w", err)
	} else {
		v.stagedFiles = staged
	}

	// Load unstaged files using git diff --name-only --numstat
	unstaged, err := v.getUnstagedFiles()
	if err != nil {
		v.err = fmt.Errorf("failed to get unstaged files: %w", err)
	} else {
		v.unstagedFiles = unstaged
	}

	v.loading = false
}

// getStagedFiles returns files that are staged for commit.
func (v *CommitView) getStagedFiles() ([]StagedFile, error) {
	var files []StagedFile

	// Get staged files with status using git diff --cached --name-status
	cmd := exec.Command("git", "-C", v.repoPath, "diff", "--cached", "--name-status")
	output, err := cmd.Output()
	if err != nil {
		// No staged files is not an error
		if strings.Contains(err.Error(), "exit status 1") && len(output) == 0 {
			return files, nil
		}
		return nil, err
	}

	statusMap := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) < 2 {
			continue
		}
		statusMap[parts[1]] = parts[0]
	}

	// Get numstat for additions/deletions
	numstatCmd := exec.Command("git", "-C", v.repoPath, "diff", "--cached", "--numstat")
	numstatOutput, err := numstatCmd.Output()
	if err != nil {
		return nil, err
	}

	numstatMap := make(map[string]struct{ additions, deletions int })
	scanner = bufio.NewScanner(strings.NewReader(string(numstatOutput)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		path := parts[2]

		additions := 0
		deletions := 0
		if parts[0] != "-" {
			additions, _ = strconv.Atoi(parts[0])
		}
		if parts[1] != "-" {
			deletions, _ = strconv.Atoi(parts[1])
		}
		numstatMap[path] = struct{ additions, deletions int }{additions, deletions}
	}

	// Build file list
	for path, status := range statusMap {
		sf := StagedFile{Path: path}
		if nums, ok := numstatMap[path]; ok {
			sf.Additions = nums.additions
			sf.Deletions = nums.deletions
		}
		switch status {
		case "A":
			sf.IsNew = true
		case "D":
			sf.IsDeleted = true
		case "M", "R", "C":
			sf.IsModified = true
		}
		files = append(files, sf)
	}

	return files, nil
}

// getUnstagedFiles returns files that have unstaged changes.
func (v *CommitView) getUnstagedFiles() ([]StagedFile, error) {
	var files []StagedFile

	// Get unstaged files with status
	cmd := exec.Command("git", "-C", v.repoPath, "diff", "--name-status")
	output, err := cmd.Output()
	if err != nil {
		// No unstaged files
		if strings.Contains(err.Error(), "exit status 1") && len(output) == 0 {
			return files, nil
		}
		return nil, err
	}

	statusMap := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) < 2 {
			continue
		}
		statusMap[parts[1]] = parts[0]
	}

	// Get numstat for additions/deletions
	numstatCmd := exec.Command("git", "-C", v.repoPath, "diff", "--numstat")
	numstatOutput, err := numstatCmd.Output()
	if err != nil {
		return nil, err
	}

	numstatMap := make(map[string]struct{ additions, deletions int })
	scanner = bufio.NewScanner(strings.NewReader(string(numstatOutput)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		path := parts[2]

		additions := 0
		deletions := 0
		if parts[0] != "-" {
			additions, _ = strconv.Atoi(parts[0])
		}
		if parts[1] != "-" {
			deletions, _ = strconv.Atoi(parts[1])
		}
		numstatMap[path] = struct{ additions, deletions int }{additions, deletions}
	}

	// Build file list
	for path, status := range statusMap {
		sf := StagedFile{Path: path}
		if nums, ok := numstatMap[path]; ok {
			sf.Additions = nums.additions
			sf.Deletions = nums.deletions
		}
		switch status {
		case "A":
			sf.IsNew = true
		case "D":
			sf.IsDeleted = true
		case "M", "R", "C":
			sf.IsModified = true
		}
		files = append(files, sf)
	}

	return files, nil
}

// stageFile stages a single file.
func (v *CommitView) stageFile(path string) error {
	cmd := exec.Command("git", "-C", v.repoPath, "add", path)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stage file: %w", err)
	}
	return nil
}

// unstageFile unstages a single file.
func (v *CommitView) unstageFile(path string) error {
	cmd := exec.Command("git", "-C", v.repoPath, "restore", "--staged", path)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to unstage file: %w", err)
	}
	return nil
}

// stageAll stages all unstaged files.
func (v *CommitView) stageAll() error {
	cmd := exec.Command("git", "-C", v.repoPath, "add", "-A")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stage all files: %w", err)
	}
	return nil
}

// unstageAll unstages all staged files.
func (v *CommitView) unstageAll() error {
	cmd := exec.Command("git", "-C", v.repoPath, "restore", "--staged", ".")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to unstage all files: %w", err)
	}
	return nil
}

// Update handles update events.
func (v *CommitView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "r":
			v.loading = true
			return v, func() tea.Msg {
				v.loadFiles()
				return RefreshDoneMsg{}
			}

		case "tab":
			// Switch between staged and unstaged sections
			if v.activeSection == 0 {
				v.activeSection = 1
			} else {
				v.activeSection = 0
			}
			v.selectedIndex = 0

		case "up", "k":
			if v.selectedIndex > 0 {
				v.selectedIndex--
			}

		case "down", "j":
			currentLen := len(v.unstagedFiles)
			if v.activeSection == 0 {
				currentLen = len(v.stagedFiles)
			}
			if v.selectedIndex < currentLen-1 {
				v.selectedIndex++
			}

		case " ":
			// Toggle stage/unstage selected file
			if v.activeSection == 0 && v.selectedIndex < len(v.stagedFiles) {
				// Unstage selected
				path := v.stagedFiles[v.selectedIndex].Path
				if err := v.unstageFile(path); err != nil {
					v.err = err
				}
				v.loadFiles()
				// Adjust selection if needed
				if v.selectedIndex >= len(v.stagedFiles) && v.selectedIndex > 0 {
					v.selectedIndex = len(v.stagedFiles) - 1
				}
			} else if v.activeSection == 1 && v.selectedIndex < len(v.unstagedFiles) {
				// Stage selected
				path := v.unstagedFiles[v.selectedIndex].Path
				if err := v.stageFile(path); err != nil {
					v.err = err
				}
				v.loadFiles()
				// Adjust selection if needed
				if v.selectedIndex >= len(v.unstagedFiles) && v.selectedIndex > 0 {
					v.selectedIndex = len(v.unstagedFiles) - 1
				}
			}

		case "a":
			// Stage all unstaged files
			if err := v.stageAll(); err != nil {
				v.err = err
			}
			v.loadFiles()
			v.activeSection = 0
			v.selectedIndex = 0

		case "u":
			// Unstage all staged files
			if err := v.unstageAll(); err != nil {
				v.err = err
			}
			v.loadFiles()
			v.activeSection = 1
			v.selectedIndex = 0
		}

	case RefreshDoneMsg:
		v.loading = false
	}

	return v, nil
}

// View renders the commit view.
func (v *CommitView) View() string {
	th := theme.GetTheme()

	var s strings.Builder

	// Header
	s.WriteString(th.DashboardTitle.Render(" Commit Composer "))
	s.WriteString("\n\n")

	if v.loading {
		s.WriteString(th.StatsStyle.Render(" Loading..."))
		s.WriteString("\n")
		return s.String()
	}

	// Error display
	if v.err != nil {
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" Error: %v", v.err)))
		s.WriteString("\n\n")
	}

	// Calculate summary stats
	stagedCount := len(v.stagedFiles)
	unstagedCount := len(v.unstagedFiles)
	var stagedAdditions, stagedDeletions, unstagedAdditions, unstagedDeletions int

	for _, f := range v.stagedFiles {
		stagedAdditions += f.Additions
		stagedDeletions += f.Deletions
	}
	for _, f := range v.unstagedFiles {
		unstagedAdditions += f.Additions
		unstagedDeletions += f.Deletions
	}

	// Summary line
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")

	if stagedCount > 0 {
		s.WriteString(fmt.Sprintf(" %s %s files  %s +%s  %s -%s\n",
			th.DashboardAccentStyle.Render("Staged:"),
			th.StatsStyle.Render(fmt.Sprintf("%d", stagedCount)),
			th.DashboardAccentStyle.Render("additions:"),
			th.StatsStyle.Render(fmt.Sprintf("%d", stagedAdditions)),
			th.DashboardErrorStyle.Render("deletions:"),
			th.StatsStyle.Render(fmt.Sprintf("%d", stagedDeletions))))
	} else {
		s.WriteString(fmt.Sprintf(" %s %s files\n",
			th.DashboardAccentStyle.Render("Staged:"),
			th.StatsStyle.Render("0")))
	}

	if unstagedCount > 0 {
		s.WriteString(fmt.Sprintf(" %s %s files  %s +%s  %s -%s\n",
			th.DashboardErrorStyle.Render("Unstaged:"),
			th.StatsStyle.Render(fmt.Sprintf("%d", unstagedCount)),
			th.DashboardAccentStyle.Render("additions:"),
			th.StatsStyle.Render(fmt.Sprintf("%d", unstagedAdditions)),
			th.DashboardErrorStyle.Render("deletions:"),
			th.StatsStyle.Render(fmt.Sprintf("%d", unstagedDeletions))))
	} else {
		s.WriteString(fmt.Sprintf(" %s %s files\n",
			th.DashboardErrorStyle.Render("Unstaged:"),
			th.StatsStyle.Render("0")))
	}

	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n\n")

	// Staged section
	s.WriteString(th.StatsStyle.Render(" Staged Changes "))
	s.WriteString(" (press u to unstage all)\n")
	if stagedCount == 0 {
		s.WriteString(th.Help.Render(" No staged changes - use Space to stage files"))
	} else {
		for i, f := range v.stagedFiles {
			selected := v.activeSection == 0 && i == v.selectedIndex
			prefix := "  "
			if selected {
				prefix = " >"
			}

			statusStr := "M"
			if f.IsNew {
				statusStr = "A"
			} else if f.IsDeleted {
				statusStr = "D"
			}

			statusStyle := th.DashboardAccentStyle
			if f.IsDeleted {
				statusStyle = th.DashboardErrorStyle
			}

			line := fmt.Sprintf("%s[%s] %s", prefix, statusStyle.Render(statusStr), th.StatsStyle.Render(f.Path))

			// Show additions/deletions for modified files
			if f.Additions > 0 || f.Deletions > 0 {
				line += fmt.Sprintf(" %s+%d %s-%d",
					th.DashboardAccentStyle.Render(""),
					f.Additions,
					th.DashboardErrorStyle.Render(""),
					f.Deletions)
			}

			s.WriteString(line + "\n")
		}
	}

	s.WriteString("\n")

	// Unstaged section
	s.WriteString(th.DashboardErrorStyle.Render(" Unstaged Changes "))
	s.WriteString(" (press a to stage all)\n")
	if unstagedCount == 0 {
		s.WriteString(th.Help.Render(" No unstaged changes - working tree is clean"))
	} else {
		for i, f := range v.unstagedFiles {
			selected := v.activeSection == 1 && i == v.selectedIndex
			prefix := "  "
			if selected {
				prefix = " >"
			}

			statusStr := "M"
			if f.IsNew {
				statusStr = "?"
			} else if f.IsDeleted {
				statusStr = "D"
			}

			statusStyle := th.DashboardErrorStyle
			if f.IsNew {
				statusStyle = th.DashboardAccentStyle
			}

			line := fmt.Sprintf("%s[%s] %s", prefix, statusStyle.Render(statusStr), th.StatsStyle.Render(f.Path))

			// Show additions/deletions for modified files
			if f.Additions > 0 || f.Deletions > 0 {
				line += fmt.Sprintf(" %s+%d %s-%d",
					th.DashboardAccentStyle.Render(""),
					f.Additions,
					th.DashboardErrorStyle.Render(""),
					f.Deletions)
			}

			s.WriteString(line + "\n")
		}
	}

	s.WriteString("\n")

	// Help footer
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")
	s.WriteString(th.Help.Render(" Space: Stage/Unstage   a: Stage all   u: Unstage all   Tab: Switch section   ↑↓: Navigate   r: Refresh "))

	return s.String()
}

// ShortHelp returns a short help string.
func (v *CommitView) ShortHelp() string {
	return "Space: Toggle  a/u: Stage all/Unstage all  Tab: Switch section"
}

// SetSize updates the view dimensions.
func (v *CommitView) SetSize(width, height int) {
	v.width = width
	v.height = height
}

// GetRepoPath returns the repository path.
func (v *CommitView) GetRepoPath() string {
	return v.repoPath
}

// Refresh reloads file data.
func (v *CommitView) Refresh() error {
	v.loadFiles()
	return v.err
}
