package views

import (
	"fmt"
	"path/filepath"
	"strings"

	"git-frontend/internal/app/components"
	"git-frontend/internal/git"
	"git-frontend/internal/theme"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// RebaseView provides visual interactive rebase planning.
type RebaseView struct {
	repoPath    string
	repo        *git.RepoInfo
	loading     bool
	err         error
	width       int
	height      int

	// Branch selection state
	showBranchSelect bool
	branches         []git.BranchInfo
	branchCursor     int

	// Rebase planning state
	commits       []git.RebaseCommit
	cursor        int
	baseBranch    string

	// Modal states
	showExecConfirm bool
	showAbortConfirm bool

	// Execution state
	executing bool
	execOutput string
}

// NewRebaseView creates a new rebase view.
func NewRebaseView(repoPath string) *RebaseView {
	v := &RebaseView{
		repoPath:   repoPath,
		width:      80,
		height:     24,
		cursor:     0,
		branchCursor: 0,
	}

	return v
}

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
			if len(v.commits) > 0 {
				v.showExecConfirm = true
			}

		case "x":
			// Abort any in-progress rebase
			if v.isRebaseInProgress() {
				v.showAbortConfirm = true
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

// executeRebase starts the rebase execution.
func (v *RebaseView) executeRebase() tea.Cmd {
	return func() tea.Msg {
		todo := git.GenerateTodoList(v.commits)
		err := git.StartInteractiveRebase(v.repoPath, v.baseBranch, v.commits)
		return RebaseExecMsg{
			Success: err == nil,
			Output:  todo,
			Error:   err,
		}
	}
}

// abortRebase aborts the current rebase.
func (v *RebaseView) abortRebase() tea.Cmd {
	return func() tea.Msg {
		err := git.AbortRebase(v.repoPath)
		return RebaseAbortMsg{
			Success: err == nil,
			Error:   err,
		}
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

// View renders the rebase view.
func (v *RebaseView) View() string {
	th := theme.GetTheme()

	// Loading state
	if v.loading {
		return th.StatsStyle.Render(" Loading rebase planner...")
	}

	var s strings.Builder

	// Header
	s.WriteString(th.DashboardTitle.Render(" Interactive Rebase Planner "))
	s.WriteString("\n\n")

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

	// Check for rebase in progress
	if v.isRebaseInProgress() {
		s.WriteString(th.DashboardErrorStyle.Render(" ⚠ Rebase in progress! "))
		s.WriteString(th.Help.Render("[x] Abort rebase\n\n"))
	}

	// Main content - two column layout
	if len(v.commits) > 0 {
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
		s.WriteString(th.DashboardErrorStyle.Render(" │ This will discard all rebase changes!        │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(" │ Type 'y' to confirm:                         │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(" └─────────────────────────────────────────────┘"))
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
	s.WriteString(th.Help.Render(" r: Refresh   b: Select base   o: Cycle op   K/J: Move   Enter: Execute   x: Abort "))

	return s.String()
}

// ShortHelp returns a short help string.
func (v *RebaseView) ShortHelp() string {
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

// RebaseExecMsg is sent when rebase execution completes.
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
