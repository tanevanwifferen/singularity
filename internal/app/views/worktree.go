package views

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"git-frontend/internal/app/components"
	"git-frontend/internal/git"
	"git-frontend/internal/theme"

	"github.com/charmbracelet/bubbletea"
)

// WorktreeView displays and manages git worktrees.
type WorktreeView struct {
	repoPath    string
	repo        *git.RepoInfo
	worktrees   []git.Worktree
	filter      *components.Filter[git.Worktree]
	loading     bool
	err         error
	width       int
	height      int

	// Modal states
	showCreate      bool
	showRemoveConfirm bool
	showPruneConfirm  bool
	showNewBranchInput bool

	// Create worktree input state
	newWorktreePath  string
	newWorktreePathInput components.Filter[byte]
	newWorktreeBranch string
	newWorktreeBranchInput components.Filter[byte]
	createNewBranch  bool

	// Remove confirmation state
	removeWorktree    *git.Worktree
	removeForce       bool

	// Branch list for selection during create
	branches          []git.BranchInfo
	branchFilter      *components.Filter[git.BranchInfo]
	showBranchPicker  bool
}

// NewWorktreeView creates a new worktree view.
func NewWorktreeView(repoPath string) *WorktreeView {
	v := &WorktreeView{
		repoPath: repoPath,
		width:    80,
		height:   24,
	}

	// Initialize the filter with worktree items
	worktrees := []git.Worktree{}
	v.filter = components.NewFilter(worktrees, v.renderWorktreeItem)
	v.filter.SetHeight(v.height)

	return v
}

// Init initializes the worktree view.
func (v *WorktreeView) Init() tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		v.loadData()
		return RefreshDoneMsg{}
	}
}

// loadData loads all worktree data.
func (v *WorktreeView) loadData() {
	v.err = nil

	repo, err := git.OpenRepo(v.repoPath)
	if err != nil {
		v.err = fmt.Errorf("failed to open repo: %w", err)
		v.loading = false
		return
	}
	v.repo = repo

	worktrees, err := git.GetWorktrees(v.repoPath)
	if err != nil {
		v.err = fmt.Errorf("failed to get worktrees: %w", err)
		v.loading = false
		return
	}
	v.worktrees = worktrees

	// Update filter with new worktree list
	v.filter.SetItems(v.worktrees)

	// Load branches for worktree creation
	v.branches = repo.Branches

	v.loading = false
}

// Update handles update events.
func (v *WorktreeView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle modal states first
		if v.showPruneConfirm {
			return v, v.handlePruneConfirm(msg)
		}
		if v.showRemoveConfirm {
			return v, v.handleRemoveConfirm(msg)
		}
		if v.showBranchPicker {
			return v, v.handleBranchPicker(msg)
		}
		if v.showNewBranchInput {
			return v, v.handleNewBranchInput(msg)
		}
		if v.showCreate {
			return v, v.handleCreateInput(msg)
		}

		// Main view keys
		switch msg.String() {
		case "r":
			v.loading = true
			return v, func() tea.Msg {
				v.loadData()
				return RefreshDoneMsg{}
			}
		case "/":
			// Activate filter mode
			if v.filter != nil {
				v.filter.Update(msg)
			}
		case "n":
			// Show create worktree dialog
			v.showCreate = true
			v.newWorktreePath = ""
			v.newWorktreePathInput = *components.NewFilter([]byte{}, func(b byte, i int, s bool) string {
				return string(b)
			})
			v.newWorktreeBranch = ""
			v.newWorktreeBranchInput = *components.NewFilter([]byte{}, func(b byte, i int, s bool) string {
				return string(b)
			})
			v.createNewBranch = false
		case "d":
			// Show remove confirmation
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				v.removeWorktree = &item
				v.showRemoveConfirm = true
			}
		case "L":
			// Lock selected worktree
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				v.lockWorktree(item.Path)
			}
		case "u":
			// Unlock selected worktree
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				v.unlockWorktree(item.Path)
			}
		case "p":
			// Show prune confirmation
			v.showPruneConfirm = true
		case "enter":
			// Navigate to worktree path
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				v.navigateToWorktree(item.Path)
			}
		case "esc":
			// Clear filter if active, otherwise do nothing
			if v.filter.IsActive() {
				v.filter.Update(msg)
			}
		}

		// Pass to filter for navigation
		if v.filter != nil {
			v.filter.Update(msg)
		}

	case RefreshDoneMsg:
		v.loading = false

	case tea.WindowSizeMsg:
		v.width = msg.Width
		v.height = msg.Height
		if v.filter != nil {
			v.filter.SetHeight(msg.Height)
		}
		if v.branchFilter != nil {
			v.branchFilter.SetHeight(msg.Height)
		}

	case tea.MouseMsg:
		// Handle mouse events for the filter/list
		if v.filter != nil {
			if v.filter.HandleMouse(msg) {
				return v, nil
			}
		}
		if v.branchFilter != nil {
			if v.branchFilter.HandleMouse(msg) {
				return v, nil
			}
		}
	}

	return v, nil
}

// handleRemoveConfirm handles key events during remove confirmation.
func (v *WorktreeView) handleRemoveConfirm(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y":
		if v.removeWorktree != nil {
			v.removeWorktreeCmd(v.removeWorktree.Path, v.removeForce)
		}
		v.showRemoveConfirm = false
		v.removeWorktree = nil
		v.removeForce = false
	case "f":
		// Force remove without confirmation
		if v.removeWorktree != nil {
			v.removeWorktreeCmd(v.removeWorktree.Path, true)
		}
		v.showRemoveConfirm = false
		v.removeWorktree = nil
		v.removeForce = false
	case "n", "esc":
		v.showRemoveConfirm = false
		v.removeWorktree = nil
		v.removeForce = false
	}
	return nil
}

// handlePruneConfirm handles key events during prune confirmation.
func (v *WorktreeView) handlePruneConfirm(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "enter":
		v.pruneWorktrees()
		v.showPruneConfirm = false
	case "n", "esc":
		v.showPruneConfirm = false
	}
	return nil
}

// handleCreateInput handles key events during worktree creation.
func (v *WorktreeView) handleCreateInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		// If we have a branch selected, create the worktree
		if v.newWorktreePath != "" && v.newWorktreeBranch != "" {
			v.createWorktree(v.newWorktreePath, v.newWorktreeBranch, v.createNewBranch)
		}
		v.showCreate = false
		v.newWorktreePath = ""
		v.newWorktreeBranch = ""
		v.createNewBranch = false
		v.showBranchPicker = false
	case "b":
		// Toggle between using existing branch and creating new branch
		v.createNewBranch = !v.createNewBranch
	case "esc":
		v.showCreate = false
		v.newWorktreePath = ""
		v.newWorktreeBranch = ""
		v.createNewBranch = false
		v.showBranchPicker = false
	case "ctrl+w":
		v.newWorktreePath = components.DeleteWordEnd(v.newWorktreePath)
	default:
		// Handle text input for path
		if len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r >= 32 && r <= 126 {
				v.newWorktreePath += string(r)
			}
		} else if msg.String() == "backspace" && len(v.newWorktreePath) > 0 {
			v.newWorktreePath = v.newWorktreePath[:len(v.newWorktreePath)-1]
		}
	}
	return nil
}

// handleBranchPicker handles key events during branch selection.
func (v *WorktreeView) handleBranchPicker(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		// Use selected branch
		if item, idx := v.branchFilter.SelectedItem(); idx >= 0 {
			v.newWorktreeBranch = item.Name
		}
		v.showBranchPicker = false
	case "esc":
		v.showBranchPicker = false
	default:
		if v.branchFilter != nil {
			v.branchFilter.Update(msg)
		}
	}
	return nil
}

// handleNewBranchInput handles key events during new branch name input.
func (v *WorktreeView) handleNewBranchInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		if v.newWorktreeBranch != "" {
			v.createNewBranch = true
		}
		v.showNewBranchInput = false
	case "esc":
		v.showNewBranchInput = false
		v.newWorktreeBranch = ""
	case "ctrl+w":
		v.newWorktreeBranch = components.DeleteWordEnd(v.newWorktreeBranch)
	default:
		if len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r >= 32 && r <= 126 {
				v.newWorktreeBranch += string(r)
			}
		} else if msg.String() == "backspace" && len(v.newWorktreeBranch) > 0 {
			v.newWorktreeBranch = v.newWorktreeBranch[:len(v.newWorktreeBranch)-1]
		}
	}
	return nil
}

// createWorktree creates a new worktree.
func (v *WorktreeView) createWorktree(path, branch string, createBranch bool) {
	err := git.CreateWorktree(v.repoPath, path, branch, createBranch)
	if err != nil {
		v.err = fmt.Errorf("failed to create worktree: %w", err)
		return
	}
	// Refresh data after create
	v.loadData()
}

// removeWorktreeCmd removes a worktree.
func (v *WorktreeView) removeWorktreeCmd(worktreePath string, force bool) {
	err := git.RemoveWorktree(v.repoPath, worktreePath, force)
	if err != nil {
		v.err = fmt.Errorf("failed to remove worktree: %w", err)
		return
	}
	// Refresh data after remove
	v.loadData()
}

// pruneWorktrees prunes stale worktree references.
func (v *WorktreeView) pruneWorktrees() {
	err := git.PruneWorktrees(v.repoPath)
	if err != nil {
		v.err = fmt.Errorf("failed to prune worktrees: %w", err)
		return
	}
	// Refresh data after prune
	v.loadData()
}

// lockWorktree locks a worktree.
func (v *WorktreeView) lockWorktree(worktreePath string) {
	err := git.LockWorktree(v.repoPath, worktreePath)
	if err != nil {
		v.err = fmt.Errorf("failed to lock worktree: %w", err)
		return
	}
	// Refresh data after lock
	v.loadData()
}

// unlockWorktree unlocks a worktree.
func (v *WorktreeView) unlockWorktree(worktreePath string) {
	err := git.UnlockWorktree(v.repoPath, worktreePath)
	if err != nil {
		v.err = fmt.Errorf("failed to unlock worktree: %w", err)
		return
	}
	// Refresh data after unlock
	v.loadData()
}

// navigateToWorktree attempts to navigate to the worktree path.
func (v *WorktreeView) navigateToWorktree(path string) {
	cmd := exec.Command("cd", path)
	if err := cmd.Run(); err != nil {
		v.err = fmt.Errorf("failed to navigate to worktree: %w", err)
	}
}

// renderWorktreeItem renders a single worktree item in the list.
func (v *WorktreeView) renderWorktreeItem(worktree git.Worktree, index int, selected bool) string {
	th := theme.GetTheme()

	// Path
	pathStyle := th.BranchStyle
	if selected {
		pathStyle = th.SelectedBranchStyle
	}
	pathPrefix := "  "
	if selected {
		pathPrefix = " >"
	}

	var line strings.Builder
	line.WriteString(pathStyle.Render(fmt.Sprintf("%s%s", pathPrefix, filepath.Base(worktree.Path))))

	// Full path as secondary info
	line.WriteString(th.StatsStyle.Render(fmt.Sprintf(" (%s)", worktree.Path)))

	// Branch
	if worktree.Branch != "" {
		line.WriteString(fmt.Sprintf(" @ %s", th.DashboardAccentStyle.Render(worktree.Branch)))
	} else {
		line.WriteString(th.DashboardErrorStyle.Render(" (detached)"))
	}

	// HEAD
	if len(worktree.HEAD) >= 7 {
		line.WriteString(fmt.Sprintf(" %s", th.InfoStyle.Render(worktree.HEAD[:7])))
	}

	// Lock status
	if worktree.Locked {
		line.WriteString(th.DashboardErrorStyle.Render(" 🔒"))
	}

	return line.String()
}

// View renders the worktree view.
func (v *WorktreeView) View() string {
	th := theme.GetTheme()

	// Loading state
	if v.loading {
		return th.StatsStyle.Render(" Loading worktrees...")
	}

	// Error state
	if v.err != nil && v.repo == nil {
		return th.DashboardErrorStyle.Render(fmt.Sprintf(" Error: %v", v.err))
	}

	var s strings.Builder

	// Header
	s.WriteString(th.DashboardTitle.Render(" Worktree Manager "))
	s.WriteString("\n\n")

	// Repo info line
	if v.repo != nil {
		s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" Repository: %s ", filepath.Base(v.repoPath))))
		if v.repo.IsDirty {
			s.WriteString(th.DashboardErrorStyle.Render("● dirty"))
		}
		s.WriteString("\n")
	}
	s.WriteString("\n")

	// Branch picker (shown when selecting branch for new worktree)
	if v.showBranchPicker && v.branchFilter != nil {
		s.WriteString(th.Help.Render(" Select branch: ↑/k: Up • ↓/j: Down • Enter: Select • Esc: Cancel "))
		s.WriteString("\n\n")
		s.WriteString(v.branchFilter.View())
		s.WriteString("\n\n")
		return s.String()
	}

	// Filter hint or active filter
	if v.filter.IsActive() {
		s.WriteString(v.filter.View())
	} else {
		// Show filter hint first line
		s.WriteString(th.Help.Render(" Press / to search • ↑/k: Select • Enter: Navigate • n: Create • d: Remove • l: Lock • u: Unlock • p: Prune "))
		s.WriteString("\n\n")
		s.WriteString(v.filter.View())
	}

	// Remove confirmation modal
	if v.showRemoveConfirm && v.removeWorktree != nil {
		s.WriteString("\n\n")
		s.WriteString(th.DashboardErrorStyle.Render(" ┌─────────────────────────────────────────────┐"))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" │ Remove worktree '%s'?  (y/n, f=force) │", filepath.Base(v.removeWorktree.Path))))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(" └─────────────────────────────────────────────┘"))
	}

	// Prune confirmation modal
	if v.showPruneConfirm {
		s.WriteString("\n\n")
		s.WriteString(th.DashboardTitle.Render(" ┌─────────────────────────────────────────────┐"))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(" │ Prune stale worktrees?  (y/n)             │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(" └─────────────────────────────────────────────┘"))
	}

	// Create worktree dialog
	if v.showCreate {
		s.WriteString("\n\n")
		s.WriteString(th.DashboardTitle.Render(" ┌─────────────────────────────────────────────┐"))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(" │ Create New Worktree                        │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(" │                                            │"))
		s.WriteString("\n")
		s.WriteString(fmt.Sprintf(" │ Path: %s%s", v.newWorktreePath, strings.Repeat(" ", max(0, 40-len(v.newWorktreePath)))))
		s.WriteString("\n")
		s.WriteString(fmt.Sprintf(" │ Branch: %s%s", v.newWorktreeBranch, strings.Repeat(" ", max(0, 37-len(v.newWorktreeBranch)))))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(" │                                            │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(" │ Press Enter to create, Esc to cancel      │"))
		s.WriteString("\n")
		if v.createNewBranch {
			s.WriteString(th.DashboardAccentStyle.Render(" │ [b] Use existing branch (currently new)   │"))
		} else {
			s.WriteString(th.Help.Render(" │ [b] Create new branch                      │"))
		}
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(" └─────────────────────────────────────────────┘"))
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
	s.WriteString(th.Help.Render(" r: Refresh   /: Search   ↑↓: Navigate   Enter: Navigate   n: Create   d: Remove   l: Lock   u: Unlock   p: Prune "))

	return s.String()
}

// ShortHelp returns a short help string.
func (v *WorktreeView) ShortHelp() string {
	return "/: Search  ↑↓: Navigate  Enter: Navigate  n: Create  d: Remove  l: Lock  u: Unlock  p: Prune"
}

// SetSize updates the view dimensions.
func (v *WorktreeView) SetSize(width, height int) {
	v.width = width
	v.height = height
	if v.filter != nil {
		v.filter.SetHeight(height)
	}
	if v.branchFilter != nil {
		v.branchFilter.SetHeight(height)
	}
}

// GetRepoPath returns the repository path.
func (v *WorktreeView) GetRepoPath() string {
	return v.repoPath
}

// Refresh reloads repository data.
func (v *WorktreeView) Refresh() error {
	v.loadData()
	return v.err
}

// KeyBindings returns the keybindings for this view.
func (v *WorktreeView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "r", Description: "Refresh worktree list"},
		{Key: "/", Description: "Activate search filter"},
		{Key: "↑/k", Description: "Navigate up"},
		{Key: "↓/j", Description: "Navigate down"},
		{Key: "Enter", Description: "Navigate to worktree"},
		{Key: "n", Description: "Create new worktree"},
		{Key: "d", Description: "Remove selected worktree"},
		{Key: "L", Description: "Lock selected worktree"},
		{Key: "u", Description: "Unlock selected worktree"},
		{Key: "p", Description: "Prune stale worktrees"},
		{Key: "Esc", Description: "Clear filter / Cancel"},
		{Key: "1", Description: "Switch to Overview"},
		{Key: "2", Description: "Switch to Branches"},
		{Key: "3", Description: "Switch to Stashes"},
		{Key: "4", Description: "Switch to Worktrees"},
	}
}
