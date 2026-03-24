package views

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// RebaseView provides visual interactive rebase planning and execution.
type RebaseView struct {
	repoPath string
	repo     *git.RepoInfo
	loading  bool
	err      error
	width    int
	height   int

	// Branch selection state
	showBranchSelect bool
	branches         []git.BranchInfo
	branchCursor     int

	// Rebase planning state
	commits    []git.RebaseCommit
	cursor     int
	baseBranch string

	// Modal states
	showExecConfirm     bool
	showAbortConfirm    bool
	showConflictModal   bool
	showContinueConfirm bool

	// Execution state
	executing     bool
	execOutput    string
	rebaseStep    int
	totalSteps    int
	statusMessage string

	// Conflict state
	hasConflict     bool
	conflictFiles   []string
	conflictMessage string
	currentCommit   string
}

// NewRebaseView creates a new rebase view.
func NewRebaseView(repoPath string) *RebaseView {
	v := &RebaseView{
		repoPath:     repoPath,
		width:        80,
		height:       24,
		cursor:       0,
		branchCursor: 0,
	}

	return v
}

// SetRepoPath updates the repository path for this view.
func (v *RebaseView) SetRepoPath(path string) { v.repoPath = path }

// Init initializes the rebase view.
func (v *RebaseView) Init() tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		v.loadData()
		return RefreshDoneMsg{}
	}
}

// loadData loads all repository data.
func (v *RebaseView) loadData() {
	v.err = nil

	repo, err := git.OpenRepo(v.repoPath)
	if err != nil {
		v.err = fmt.Errorf("failed to open repo: %w", err)
		v.loading = false
		return
	}
	v.repo = repo
	v.branches = repo.Branches

	// If we have a base branch, load commits
	if v.baseBranch != "" && v.repo.CurrentBranch != "" {
		commits, err := git.GetRebasePlan(v.repoPath, v.baseBranch, v.repo.CurrentBranch)
		if err != nil {
			v.err = err
			v.loading = false
			return
		}
		v.commits = commits
	}

	v.loading = false
}

// Update handles update events.
func (v *RebaseView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle modal states first
		if v.showBranchSelect {
			return v, v.handleBranchSelect(msg)
		}
		if v.showExecConfirm {
			return v, v.handleExecConfirm(msg)
		}
		if v.showAbortConfirm {
			return v, v.handleAbortConfirm(msg)
		}
		if v.showConflictModal {
			return v, v.handleConflictModal(msg)
		}
		if v.showContinueConfirm {
			return v, v.handleContinueConfirm(msg)
		}

		// Main view keys
		switch msg.String() {
		case "r":
			v.loading = true
			return v, func() tea.Msg {
				v.loadData()
				return RefreshDoneMsg{}
			}

		case "b":
			// Show branch selector for base branch
			v.showBranchSelect = true
			v.branchCursor = 0
			// Filter to exclude current branch
			v.filterBranches()

		case "o":
			// Cycle operation type for selected commit
			v.cycleOperation()

		case "J":
			// Move commit down (Shift+J)
			v.moveCommitDown()

		case "K":
			// Move commit up (Shift+K)
			v.moveCommitUp()

		case "enter":
			// Show exec confirmation
			if len(v.commits) > 0 && !v.executing {
				v.showExecConfirm = true
			}

		case "x":
			// Abort any in-progress rebase
			if v.isRebaseInProgress() {
				v.showAbortConfirm = true
			}

		case "c":
			// Continue rebase after conflict resolution
			if v.hasConflict {
				v.showContinueConfirm = true
			}

		case "s":
			// Skip current commit during conflict
			if v.hasConflict {
				return v, v.skipRebase()
			}

		case "p":
			// Show todo list preview (already shown in side panel)
			// This is for toggle/refresh
		}

		// Cursor navigation
		switch msg.String() {
		case "k", "up":
			if v.cursor > 0 {
				v.cursor--
			}
		case "j", "down":
			if v.cursor < len(v.commits)-1 {
				v.cursor++
			}
		case "g":
			// Go to top
			v.cursor = 0
		case "G":
			// Go to bottom
			if len(v.commits) > 0 {
				v.cursor = len(v.commits) - 1
			}
		}

	case RefreshDoneMsg:
		v.loading = false

	case RebaseOutputMsg:
		v.execOutput += msg.Output + "\n"

	case RebaseProgressMsg:
		v.rebaseStep = msg.Step
		v.totalSteps = msg.Total
		v.statusMessage = msg.Message

	case RebaseConflictMsg:
		v.hasConflict = true
		v.conflictFiles = msg.Files
		v.conflictMessage = msg.Message
		v.showConflictModal = true
		v.executing = false

	case RebaseCompleteMsg:
		v.executing = false
		v.statusMessage = ""
		if msg.Success {
			v.execOutput = "✓ Rebase completed successfully!\n"
		} else {
			v.err = msg.Error
		}
		// Refresh data after rebase
		go func() {
			v.loadData()
		}()

	case tea.WindowSizeMsg:
		v.width = msg.Width
		v.height = msg.Height
	}

	return v, nil
}

// handleBranchSelect handles key events during branch selection.
func (v *RebaseView) handleBranchSelect(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		if v.branchCursor < len(v.branches) {
			v.baseBranch = v.branches[v.branchCursor].Name
			v.cursor = 0
			v.loadData()
		}
		v.showBranchSelect = false
	case "esc":
		v.showBranchSelect = false
	case "k", "up":
		if v.branchCursor > 0 {
			v.branchCursor--
		}
	case "j", "down":
		if v.branchCursor < len(v.branches)-1 {
			v.branchCursor++
		}
	}
	return nil
}

// handleExecConfirm handles key events during execution confirmation.
func (v *RebaseView) handleExecConfirm(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "enter":
		v.showExecConfirm = false
		return v.executeRebase()
	case "n", "esc":
		v.showExecConfirm = false
	}
	return nil
}

// handleAbortConfirm handles key events during abort confirmation.
func (v *RebaseView) handleAbortConfirm(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "enter":
		v.showAbortConfirm = false
		return v.abortRebase()
	case "n", "esc":
		v.showAbortConfirm = false
	}
	return nil
}

// handleConflictModal handles key events during conflict resolution.
func (v *RebaseView) handleConflictModal(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "c":
		// Continue - user resolved conflict
		v.showConflictModal = false
		return v.continueRebase()
	case "s":
		// Skip this commit
		v.showConflictModal = false
		return v.skipRebase()
	case "x", "a":
		// Abort rebase
		v.showConflictModal = false
		v.hasConflict = false
		return v.abortRebase()
	case "esc", "n":
		v.showConflictModal = false
	}
	return nil
}

// handleContinueConfirm handles key events for continue confirmation.
func (v *RebaseView) handleContinueConfirm(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "enter":
		v.showContinueConfirm = false
		return v.continueRebase()
	case "n", "esc":
		v.showContinueConfirm = false
	}
	return nil
}

// filterBranches filters out the current branch from the selection list.
func (v *RebaseView) filterBranches() {
	filtered := make([]git.BranchInfo, 0)
	for _, b := range v.branches {
		if b.Name != v.repo.CurrentBranch {
			filtered = append(filtered, b)
		}
	}
	v.branches = filtered
}

// cycleOperation cycles the operation type for the selected commit.
func (v *RebaseView) cycleOperation() {
	if v.cursor < 0 || v.cursor >= len(v.commits) {
		return
	}

	// Cycle: pick -> reword -> edit -> squash -> fixup -> drop -> pick
	current := v.commits[v.cursor].Operation
	var next git.RebaseOperation
	switch current {
	case git.RebasePick:
		next = git.RebaseReword
	case git.RebaseReword:
		next = git.RebaseEdit
	case git.RebaseEdit:
		next = git.RebaseSquash
	case git.RebaseSquash:
		next = git.RebaseFixup
	case git.RebaseFixup:
		next = git.RebaseDrop
	case git.RebaseDrop:
		next = git.RebasePick
	default:
		next = git.RebasePick
	}
	v.commits[v.cursor].Operation = next
}

// moveCommitUp moves the selected commit up in the list (K key).
func (v *RebaseView) moveCommitUp() {
	if v.cursor <= 0 || v.cursor >= len(v.commits) {
		return
	}

	// Swap with previous
	v.commits[v.cursor], v.commits[v.cursor-1] = v.commits[v.cursor-1], v.commits[v.cursor]
	v.cursor--
}

// moveCommitDown moves the selected commit down in the list (J key, Shift+J).
func (v *RebaseView) moveCommitDown() {
	if v.cursor < 0 || v.cursor >= len(v.commits)-1 {
		return
	}

	// Swap with next
	v.commits[v.cursor], v.commits[v.cursor+1] = v.commits[v.cursor+1], v.commits[v.cursor]
	v.cursor++
}

// isRebaseInProgress checks if a rebase is currently in progress.
func (v *RebaseView) isRebaseInProgress() bool {
	inProgress, _, err := git.GetRebaseStatus(v.repoPath)
	if err != nil {
		return false
	}
	return inProgress
}

// executeRebase starts the rebase execution with progress streaming.
func (v *RebaseView) executeRebase() tea.Cmd {
	v.executing = true
	v.execOutput = ""
	v.rebaseStep = 0
	v.totalSteps = len(v.commits)
	v.hasConflict = false
	v.statusMessage = "Starting rebase..."

	return func() tea.Msg {
		// First, send progress message
		tea.Println(RebaseProgressMsg{Step: 0, Total: v.totalSteps, Message: "Starting rebase..."})

		// Execute rebase using git rebase -i with piped input
		todo := git.GenerateTodoList(v.commits)

		// Use git rebase -i with --no-autosquash
		cmd := exec.Command("git", "-C", v.repoPath, "rebase", "-i", "--no-autosquash", v.baseBranch)
		cmd.Stdin = strings.NewReader(todo)

		output, err := cmd.CombinedOutput()
		outputStr := string(output)

		// Send output
		tea.Println(RebaseOutputMsg{Output: outputStr})

		if err != nil {
			// Check if conflict occurred
			if v.detectConflict(outputStr) {
				tea.Println(RebaseConflictMsg{
					Message: "Conflict detected during rebase!",
					Files:   v.getConflictFiles(),
				})
				return RebaseCompleteMsg{Success: false, Error: fmt.Errorf("conflict detected")}
			}

			// Check if rebase is already in progress
			if v.isRebaseInProgress() {
				// There's already a rebase in progress, maybe from a previous session
				tea.Println(RebaseOutputMsg{Output: "A rebase is already in progress..."})
				return RebaseCompleteMsg{Success: false, Error: fmt.Errorf("rebase already in progress")}
			}

			tea.Println(RebaseOutputMsg{Output: fmt.Sprintf("Rebase failed: %v", err)})
			return RebaseCompleteMsg{Success: false, Error: err}
		}

		// Rebase completed successfully
		tea.Println(RebaseOutputMsg{Output: "✓ Rebase completed successfully!"})
		return RebaseCompleteMsg{Success: true}
	}
}

// detectConflict checks if the output indicates a conflict.
func (v *RebaseView) detectConflict(output string) bool {
	// Check for common conflict indicators
	conflictIndicators := []string{
		"CONFLICT",
		"conflict",
		"merge failed",
		"could not apply",
		"pick in progress",
	}

	lowerOutput := strings.ToLower(output)
	for _, indicator := range conflictIndicators {
		if strings.Contains(lowerOutput, indicator) {
			return true
		}
	}

	// Also check for rebase-merge directory which indicates in-progress rebase with conflicts
	gitDir := v.repoPath + "/.git"
	if _, err := os.Stat(gitDir + "/rebase-merge"); err == nil {
		// Check if there's a conflicting file
		if files := v.getConflictFiles(); len(files) > 0 {
			return true
		}
	}

	return false
}

// getConflictFiles returns a list of files with conflicts.
func (v *RebaseView) getConflictFiles() []string {
	cmd := exec.Command("git", "-C", v.repoPath, "diff", "--name-only", "--diff-filter=U")
	output, err := cmd.Output()
	if err != nil {
		return []string{}
	}

	var files []string
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

// continueRebase continues the rebase after resolving conflicts.
func (v *RebaseView) continueRebase() tea.Cmd {
	v.executing = true
	v.statusMessage = "Continuing rebase..."

	return func() tea.Msg {
		err := git.ContinueRebase(v.repoPath)
		if err != nil {
			output, _ := exec.Command("git", "-C", v.repoPath, "rebase", "--continue").CombinedOutput()
			outputStr := string(output)

			if v.detectConflict(outputStr) {
				tea.Println(RebaseOutputMsg{Output: "Another conflict detected!"})
				tea.Println(RebaseConflictMsg{
					Message: "Conflict detected!",
					Files:   v.getConflictFiles(),
				})
				return RebaseCompleteMsg{Success: false, Error: fmt.Errorf("conflict detected")}
			}

			tea.Println(RebaseOutputMsg{Output: outputStr})
			return RebaseCompleteMsg{Success: false, Error: err}
		}

		tea.Println(RebaseOutputMsg{Output: "✓ Continued rebase successfully!"})
		return RebaseCompleteMsg{Success: true}
	}
}

// skipRebase skips the current conflicting commit.
func (v *RebaseView) skipRebase() tea.Cmd {
	v.executing = true
	v.statusMessage = "Skipping conflicting commit..."

	return func() tea.Msg {
		err := git.SkipRebase(v.repoPath)
		if err != nil {
			output, _ := exec.Command("git", "-C", v.repoPath, "rebase", "--skip").CombinedOutput()
			outputStr := string(output)

			if v.detectConflict(outputStr) {
				tea.Println(RebaseOutputMsg{Output: "Another conflict detected!"})
				tea.Println(RebaseConflictMsg{
					Message: "Conflict detected!",
					Files:   v.getConflictFiles(),
				})
				return RebaseCompleteMsg{Success: false, Error: fmt.Errorf("conflict detected")}
			}

			tea.Println(RebaseOutputMsg{Output: outputStr})
			return RebaseCompleteMsg{Success: false, Error: err}
		}

		tea.Println(RebaseOutputMsg{Output: "✓ Skipped conflicting commit!"})
		return RebaseCompleteMsg{Success: true}
	}
}

// abortRebase aborts the current rebase.
func (v *RebaseView) abortRebase() tea.Cmd {
	v.executing = true
	v.statusMessage = "Aborting rebase..."
	v.hasConflict = false

	return func() tea.Msg {
		err := git.AbortRebase(v.repoPath)
		if err != nil {
			tea.Println(RebaseOutputMsg{Output: fmt.Sprintf("Failed to abort: %v", err)})
			return RebaseAbortMsg{Success: false, Error: err}
		}

		tea.Println(RebaseOutputMsg{Output: "✓ Rebase aborted!"})
		return RebaseAbortMsg{Success: true}
	}
}

// renderCommitItem renders a single commit item in the list.
func (v *RebaseView) renderCommitItem(commit git.RebaseCommit, index int, selected bool) string {
	th := theme.GetTheme()

	// Determine operation color
	opStyle := th.StatsStyle
	switch commit.Operation {
	case git.RebasePick:
		opStyle = th.DashboardAccentStyle // green
	case git.RebaseReword:
		opStyle = th.InfoStyle // blue
	case git.RebaseEdit:
		opStyle = th.WarningStyle // yellow
	case git.RebaseSquash:
		opStyle = th.DashboardErrorStyle // red
	case git.RebaseFixup:
		opStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("227")) // gold
	case git.RebaseDrop:
		opStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Strikethrough(true) // gray strikethrough
	}

	namePrefix := "  "
	if selected {
		namePrefix = " >"
	}

	var line strings.Builder

	// Operation badge
	opStr := fmt.Sprintf("[%s]", commit.Operation.String())
	line.WriteString(opStyle.Render(fmt.Sprintf("%s%s", namePrefix, opStr)))

	// SHA
	line.WriteString(fmt.Sprintf(" %s", th.CommitStyle.Render(commit.ShortSHA)))

	// Message (truncated if needed)
	msg := commit.Message
	if len(msg) > 50 {
		msg = msg[:47] + "..."
	}
	line.WriteString(fmt.Sprintf(" %s", th.StatsStyle.Render(msg)))

	// Author and date on second line if selected
	if selected {
		line.WriteString(fmt.Sprintf("\n   %s • %s",
			th.MutedTextStyle.Render(commit.Author),
			th.MutedTextStyle.Render(commit.Date)))
	}

	return line.String()
}

// renderTodoList renders the todo list for the preview panel.
func (v *RebaseView) renderTodoList() string {
	var s strings.Builder
	th := theme.GetTheme()

	s.WriteString(th.DashboardTitle.Render(" TODO List "))
	s.WriteString("\n\n")

	if len(v.commits) == 0 {
		s.WriteString(th.MutedTextStyle.Render(" No commits selected"))
		return s.String()
	}

	for i, commit := range v.commits {
		// Operation badge with color
		opStyle := th.StatsStyle
		switch commit.Operation {
		case git.RebasePick:
			opStyle = th.DashboardAccentStyle
		case git.RebaseReword:
			opStyle = th.InfoStyle
		case git.RebaseEdit:
			opStyle = th.WarningStyle
		case git.RebaseSquash:
			opStyle = th.DashboardErrorStyle
		case git.RebaseFixup:
			opStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("227"))
		case git.RebaseDrop:
			opStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Strikethrough(true)
		}

		opStr := fmt.Sprintf("[%s]", commit.Operation.String())
		if i == v.cursor {
			s.WriteString(th.SelectedBranchStyle.Render(fmt.Sprintf(" > %s %s", opStyle.Render(opStr), commit.ShortSHA)))
		} else {
			s.WriteString(fmt.Sprintf("   %s %s", opStyle.Render(opStr), th.CommitStyle.Render(commit.ShortSHA)))
		}
		s.WriteString("\n")
	}

	return s.String()
}

// renderOutputPanel renders the execution output panel.
func (v *RebaseView) renderOutputPanel() string {
	var s strings.Builder
	th := theme.GetTheme()

	s.WriteString(th.DashboardTitle.Render(" Output "))
	s.WriteString("\n\n")

	if v.execOutput == "" {
		s.WriteString(th.MutedTextStyle.Render(" (no output yet)"))
		return s.String()
	}

	lines := strings.Split(v.execOutput, "\n")
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), "conflict") {
			s.WriteString(th.DashboardErrorStyle.Render(" " + line))
		} else if strings.HasPrefix(line, "✓") {
			s.WriteString(th.DashboardAccentStyle.Render(" " + line))
		} else {
			s.WriteString(th.StatsStyle.Render(" " + line))
		}
		s.WriteString("\n")
	}

	return s.String()
}

// renderStatusBar renders the status bar showing rebase progress.
func (v *RebaseView) renderStatusBar() string {
	var s strings.Builder
	th := theme.GetTheme()

	// Status bar background
	s.WriteString(th.DashboardTitle.Render(" ┌──────────────────────────────────────────────────────────────┐"))
	s.WriteString("\n")

	if v.executing {
		// Show rebase in progress indicator
		status := fmt.Sprintf(" │ 🔄 REBASE IN PROGRESS [%d/%d] %s",
			v.rebaseStep+1, v.totalSteps, v.statusMessage)

		// Pad to end of bar
		statusLen := len(status) - 3 // -3 for │ and spaces
		padding := 60 - statusLen
		if padding < 1 {
			padding = 1
		}
		for i := 0; i < padding; i++ {
			status += " "
		}
		status += "│"

		if v.hasConflict {
			s.WriteString(th.DashboardErrorStyle.Render(status))
		} else {
			s.WriteString(th.DashboardAccentStyle.Render(status))
		}
	} else if v.hasConflict {
		status := fmt.Sprintf(" │ ⚠️  CONFLICT DETECTED - Resolve conflicts then [c]ontinue [s]kip [x]abort │")
		s.WriteString(th.DashboardErrorStyle.Render(status))
	} else if v.isRebaseInProgress() {
		status := " │ ⚠️  REBASE IN PROGRESS                              [x] Abort │"
		s.WriteString(th.WarningStyle.Render(status))
	} else {
		s.WriteString(th.MutedTextStyle.Render(" │ Ready                                          │"))
	}

	s.WriteString("\n")
	s.WriteString(th.DashboardTitle.Render(" └──────────────────────────────────────────────────────────────┘"))
	s.WriteString("\n")

	return s.String()
}

// View renders the rebase view.
func (v *RebaseView) View() string {
	th := theme.GetTheme()

	// Loading state
	if v.loading {
		return th.StatsStyle.Render(" Loading rebase planner...")
	}

	var s strings.Builder

	// Header with status bar
	s.WriteString(th.DashboardTitle.Render(" Interactive Rebase Planner "))
	s.WriteString("\n")

	// Status bar showing rebase progress
	s.WriteString(v.renderStatusBar())
	s.WriteString("\n")

	// Repo info
	if v.repo != nil {
		s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" Repository: %s ", filepath.Base(v.repoPath))))
		if v.repo.IsDirty {
			s.WriteString(th.DashboardErrorStyle.Render("● dirty"))
		}
		s.WriteString("\n")
	}

	// Base branch selector
	s.WriteString("\n")
	if v.baseBranch == "" {
		s.WriteString(th.Help.Render(" Base branch: "))
		s.WriteString(th.DashboardErrorStyle.Render("<not selected>"))
		s.WriteString(th.Help.Render(" [b] Select base"))
	} else {
		s.WriteString(th.Help.Render(" Base branch: "))
		s.WriteString(th.DashboardAccentStyle.Render(v.baseBranch))
		s.WriteString(th.Help.Render(" → "))
		s.WriteString(th.BranchStyle.Render(v.repo.CurrentBranch))
		s.WriteString(th.Help.Render(" [b] Change"))
	}
	s.WriteString("\n\n")

	// Check for rebase in progress (outside of execution state)
	if v.isRebaseInProgress() && !v.executing {
		s.WriteString(th.DashboardErrorStyle.Render(" ⚠ Rebase in progress! "))
		s.WriteString(th.Help.Render("[x] Abort rebase\n\n"))
	}

	// Show conflict files if there's a conflict
	if v.hasConflict && len(v.conflictFiles) > 0 {
		s.WriteString(th.DashboardErrorStyle.Render(" ⚠ Conflicts in: "))
		s.WriteString(th.WarningStyle.Render(strings.Join(v.conflictFiles, ", ")))
		s.WriteString("\n\n")
	}

	// Main content - two column layout when not executing
	if !v.executing && len(v.commits) > 0 {
		// Left column - Commit list
		s.WriteString(th.Help.Render(" ↑↓/k j: Navigate • [o] Cycle op • K / J: Move • Enter: Execute "))
		s.WriteString("\n\n")

		for i := range v.commits {
			s.WriteString(v.renderCommitItem(v.commits[i], i, i == v.cursor))
			s.WriteString("\n")
		}

		// Right column - Todo list preview
		s.WriteString("\n")
		s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
		s.WriteString("\n")
		s.WriteString(v.renderTodoList())

		// Operation legend
		s.WriteString("\n\n")
		s.WriteString(th.DashboardTitle.Render(" Operations "))
		s.WriteString("\n\n")
		s.WriteString(fmt.Sprintf(" %s pick   - use commit as-is\n", th.DashboardAccentStyle.Render("[p]")))
		s.WriteString(fmt.Sprintf(" %s reword - change commit message\n", th.InfoStyle.Render("[r]")))
		s.WriteString(fmt.Sprintf(" %s edit   - stop for editing\n", th.WarningStyle.Render("[e]")))
		s.WriteString(fmt.Sprintf(" %s squash - combine with previous\n", th.DashboardErrorStyle.Render("[s]")))
		s.WriteString(fmt.Sprintf(" %s fixup  - squash, discard msg\n", lipgloss.NewStyle().Foreground(lipgloss.Color("227")).Render("[f]")))
		s.WriteString(fmt.Sprintf(" %s drop   - remove commit\n", th.MutedTextStyle.Render("[d]")))
	} else if v.executing {
		// During execution, show progress and output
		s.WriteString(th.Help.Render(" Rebase in progress...\n\n"))

		// Show progress
		if v.totalSteps > 0 {
			progress := float64(v.rebaseStep) / float64(v.totalSteps) * 100
			barLen := 40
			filled := int(progress / 100 * float64(barLen))
			bar := "["
			for i := 0; i < barLen; i++ {
				if i < filled {
					bar += "="
				} else if i == filled {
					bar += ">"
				} else {
					bar += "."
				}
			}
			bar += fmt.Sprintf("] %d%% (%d/%d)", int(progress), v.rebaseStep+1, v.totalSteps)
			s.WriteString(th.StatsStyle.Render(" " + bar))
			s.WriteString("\n\n")
		}

		// Show output
		s.WriteString(v.renderOutputPanel())
	} else {
		// No commits yet
		s.WriteString(th.MutedTextStyle.Render(" Select a base branch to see commits.\n"))
		s.WriteString(th.MutedTextStyle.Render(" Use [b] to select base branch.\n\n"))
		s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
		s.WriteString("\n")
		s.WriteString(th.Help.Render(" b: Select base branch"))
	}

	// Execution confirmation modal
	if v.showExecConfirm {
		s.WriteString("\n\n")
		s.WriteString(th.DashboardAccentStyle.Render(" ┌─────────────────────────────────────────────┐"))
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render(" │ Execute interactive rebase?                 │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf(" │ Base: %s → %s                            │", v.baseBranch, v.repo.CurrentBranch)))
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render(" │ This will modify your branch history!       │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render(" │ Type 'y' to confirm:                         │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render(" └─────────────────────────────────────────────┘"))
	}

	// Abort confirmation modal
	if v.showAbortConfirm {
		s.WriteString("\n\n")
		s.WriteString(th.DashboardErrorStyle.Render(" ┌─────────────────────────────────────────────┐"))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(" │ Abort current rebase?                        │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(" │ This will discard all rebase changes!          │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(" │ Type 'y' to confirm:                         │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(" └─────────────────────────────────────────────┘"))
	}

	// Conflict resolution modal
	if v.showConflictModal {
		s.WriteString("\n\n")
		s.WriteString(th.DashboardErrorStyle.Render(" ╔═════════════════════════════════════════════╗"))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(" ║  ⚠️  CONFLICT DETECTED                       ║"))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(" ╠═════════════════════════════════════════════╣"))
		s.WriteString("\n")
		if len(v.conflictFiles) > 0 {
			s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" ║  Files: %s", v.conflictFiles[0])))
			for i := 1; i < len(v.conflictFiles) && i < 2; i++ {
				s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(", %s", v.conflictFiles[i])))
			}
			if len(v.conflictFiles) > 2 {
				s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" +%d more", len(v.conflictFiles)-2)))
			}
			s.WriteString(th.DashboardErrorStyle.Render("        ║"))
			s.WriteString("\n")
		}
		s.WriteString(th.DashboardErrorStyle.Render(" ╠═════════════════════════════════════════════╣"))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(" ║  [c] Continue  - conflict resolved           ║"))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(" ║  [s] Skip      - skip this commit           ║"))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(" ║  [x] Abort     - abort entire rebase        ║"))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(" ╚═════════════════════════════════════════════╝"))
	}

	// Continue confirmation modal
	if v.showContinueConfirm {
		s.WriteString("\n\n")
		s.WriteString(th.DashboardAccentStyle.Render(" ┌─────────────────────────────────────────────┐"))
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render(" │ Continue rebase after resolving conflicts?   │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render(" │ Type 'y' to confirm:                         │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render(" └─────────────────────────────────────────────┘"))
	}

	// Branch selection modal
	if v.showBranchSelect {
		s.WriteString("\n\n")
		s.WriteString(th.DashboardTitle.Render(" ┌─────────────────────────────────────────────┐"))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(" │ Select base branch for rebase:              │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(" └─────────────────────────────────────────────┘\n"))

		for i, branch := range v.branches {
			prefix := "   "
			if i == v.branchCursor {
				prefix = " > "
			}
			upstream := ""
			if branch.Upstream != "" {
				upstream = " → " + branch.Upstream
			}
			if i == v.branchCursor {
				s.WriteString(th.SelectedBranchStyle.Render(fmt.Sprintf(" %s%s%s", prefix, branch.Name, upstream)))
			} else {
				s.WriteString(th.BranchStyle.Render(fmt.Sprintf(" %s%s%s", prefix, branch.Name, upstream)))
			}
			s.WriteString("\n")
		}
		s.WriteString("\n")
		s.WriteString(th.Help.Render(" ↑↓: Navigate • Enter: Select • Esc: Cancel"))
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

	// Dynamic help based on state
	if v.hasConflict {
		s.WriteString(th.Help.Render(" c: Continue   s: Skip   x: Abort   Esc: Cancel"))
	} else if v.executing {
		s.WriteString(th.Help.Render(" Rebase in progress... (wait for completion)"))
	} else {
		s.WriteString(th.Help.Render(" r: Refresh   b: Select base   o: Cycle op   K/J: Move   Enter: Execute   x: Abort "))
	}

	return s.String()
}

// ShortHelp returns a short help string.
func (v *RebaseView) ShortHelp() string {
	if v.hasConflict {
		return "c: Continue  s: Skip  x: Abort"
	}
	return "b: Base  o: Cycle op  K/J: Move  Enter: Execute  x: Abort"
}

// SetSize updates the view dimensions.
func (v *RebaseView) SetSize(width, height int) {
	v.width = width
	v.height = height
}

// GetRepoPath returns the repository path.
func (v *RebaseView) GetRepoPath() string {
	return v.repoPath
}

// Refresh reloads repository data.
func (v *RebaseView) Refresh() error {
	v.loadData()
	return v.err
}

// KeyBindings returns the keybindings for this view.
func (v *RebaseView) KeyBindings() []components.KeyBinding {
	if v.hasConflict {
		return []components.KeyBinding{
			{Key: "c", Description: "Continue after resolving"},
			{Key: "s", Description: "Skip conflicting commit"},
			{Key: "x", Description: "Abort rebase"},
			{Key: "Esc", Description: "Cancel"},
		}
	}
	return []components.KeyBinding{
		{Key: "r", Description: "Refresh"},
		{Key: "b", Description: "Select base branch"},
		{Key: "o", Description: "Cycle operation"},
		{Key: "↑/k", Description: "Navigate up"},
		{Key: "↓/j", Description: "Navigate down"},
		{Key: "K", Description: "Move commit up"},
		{Key: "J", Description: "Move commit down"},
		{Key: "Enter", Description: "Execute rebase"},
		{Key: "x", Description: "Abort rebase"},
		{Key: "Esc", Description: "Cancel"},
		{Key: "1", Description: "Switch to Overview"},
		{Key: "2", Description: "Switch to Branches"},
		{Key: "3", Description: "Switch to Stashes"},
		{Key: "4", Description: "Switch to Worktrees"},
	}
}

// RebaseOutputMsg is sent during rebase execution with streaming output.
type RebaseOutputMsg struct {
	Output string
}

// RebaseProgressMsg is sent to show rebase progress.
type RebaseProgressMsg struct {
	Step    int
	Total   int
	Message string
}

// RebaseConflictMsg is sent when a conflict is detected.
type RebaseConflictMsg struct {
	Message string
	Files   []string
}

// RebaseCompleteMsg is sent when rebase execution completes.
type RebaseCompleteMsg struct {
	Success bool
	Error   error
}

// RebaseExecMsg is sent when rebase execution completes (legacy).
type RebaseExecMsg struct {
	Success bool
	Output  string
	Error   error
}

// RebaseAbortMsg is sent when rebase abort completes.
type RebaseAbortMsg struct {
	Success bool
	Error   error
}
