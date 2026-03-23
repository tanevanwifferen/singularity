package views

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"git-frontend/internal/app/components"
	"git-frontend/internal/engine"
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

	// Agent engine for starting merge agents
	engine *engine.Engine

	// Modal states
	showCreate        bool
	showRemoveConfirm bool
	showPruneConfirm  bool
	showNewBranchInput bool
	showAgentConfirm  bool
	agentWorktree     *git.Worktree
	showRebaseConfirm bool
	rebaseWorktree    *git.Worktree

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

// SetEngine sets the agent engine used to start merge agents.
func (v *WorktreeView) SetEngine(eng *engine.Engine) {
	v.engine = eng
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
		if v.showRebaseConfirm {
			return v, v.handleRebaseConfirm(msg)
		}
		if v.showAgentConfirm {
			return v, v.handleAgentConfirm(msg)
		}
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
		case "a":
			// Start agent to merge this worktree branch into main
			if item, idx := v.filter.SelectedItem(); idx >= 0 && item.Branch != "" {
				v.agentWorktree = &item
				v.showAgentConfirm = true
			}
		case "R":
			if item, idx := v.filter.SelectedItem(); idx >= 0 && item.Branch != "" {
				v.rebaseWorktree = &item
				v.showRebaseConfirm = true
			}
		case "m":
			// Open PR/MR creation view with this branch pre-selected
			if item, idx := v.filter.SelectedItem(); idx >= 0 && item.Branch != "" {
				branch := item.Branch
				return v, func() tea.Msg {
					return OpenPRForBranchMsg{Branch: branch}
				}
			}
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

// handleAgentConfirm handles key events during agent-merge confirmation.
func (v *WorktreeView) handleAgentConfirm(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "enter":
		wt := v.agentWorktree
		eng := v.engine
		v.showAgentConfirm = false
		v.agentWorktree = nil
		if wt != nil && eng != nil {
			path := wt.Path
			branch := wt.Branch
			return func() tea.Msg {
				task := fmt.Sprintf(
					"You are in a git worktree for branch '%s'. "+
						"Merge this branch into the main branch (main or master): "+
						"1) fetch origin, 2) checkout main/master, 3) merge '%s', "+
						"4) push to remote. Resolve any merge conflicts carefully.",
					branch, branch,
				)
				id, err := eng.StartAgent(path, task, engine.AgentOptions{SmartRoute: true})
				return AgentCreatedMsg{ID: id, Err: err}
			}
		}
	case "n", "esc":
		v.showAgentConfirm = false
		v.agentWorktree = nil
	}
	return nil
}

// handleRebaseConfirm handles key events during rebase-to-main confirmation.
func (v *WorktreeView) handleRebaseConfirm(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "enter":
		wt := v.rebaseWorktree
		eng := v.engine
		v.showRebaseConfirm = false
		v.rebaseWorktree = nil
		if wt != nil && eng != nil {
			path := wt.Path
			branch := wt.Branch
			return func() tea.Msg {
				mainBranch, conflictFiles, _, rebaseErr := git.RebaseOntoMain(path)
				if rebaseErr == nil {
					pushCmd := exec.Command("git", "-C", path, "push", "--force-with-lease", "origin", branch)
					pushCmd.Run()
					return RefreshDoneMsg{}
				}
				if len(conflictFiles) == 0 {
					return AgentCreatedMsg{Err: rebaseErr}
				}
				rebaseCtx, ctxErr := git.GetRebaseContext(path, mainBranch, conflictFiles)
				if ctxErr != nil {
					rebaseCtx = fmt.Sprintf("(could not gather rebase context: %v)", ctxErr)
				}
				task := fmt.Sprintf(
					"You are in a git worktree at path '%s' on branch '%s'.\n\n"+
						"A `git rebase origin/%s` has already been started and there are merge conflicts.\n\n"+
						"<REBASE CONTEXT>\n%s\n</REBASE CONTEXT>\n\n"+
						"Your job:\n"+
						"1. Read the conflict context above carefully to understand:\n"+
						"   - What this branch is trying to accomplish (from branch commits)\n"+
						"   - What changed on main that caused conflicts (from main's changes)\n"+
						"2. For each conflicted file, resolve the conflict markers intelligently:\n"+
						"   - Preserve the branch's intent while incorporating main's changes\n"+
						"   - Don't just blindly pick one side — think about what both sides are doing\n"+
						"3. After resolving each file, run: git add <file>\n"+
						"4. Once ALL conflicts are resolved, run: git rebase --continue\n"+
						"5. If more conflicts arise, repeat the process\n"+
						"6. When the rebase is complete (no more conflicts), run: git push --force-with-lease origin %s\n\n"+
						"Important: Work in the worktree directory '%s'. Use Read/Edit tools to resolve conflicts by rewriting the conflict regions. Do NOT run git rebase --abort.",
					path, branch, mainBranch, rebaseCtx, branch, path,
				)
				id, err := eng.StartAgent(path, task, engine.AgentOptions{SmartRoute: true})
				return AgentCreatedMsg{ID: id, Err: err}
			}
		}
	case "n", "esc":
		v.showRebaseConfirm = false
		v.rebaseWorktree = nil
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
		if msg.Paste && len(msg.Runes) > 0 {
			v.newWorktreePath += string(msg.Runes)
		} else if len(msg.Runes) == 1 {
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
		if msg.Paste && len(msg.Runes) > 0 {
			v.newWorktreeBranch += string(msg.Runes)
		} else if len(msg.Runes) == 1 {
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
		s.WriteString(th.Help.Render(" Press / to search • ↑/k: Select • Enter: Navigate • n: Create • d: Remove • a: Merge (agent) • R: Rebase to main (agent) • m: Create MR • p: Prune "))
		s.WriteString("\n\n")
		s.WriteString(v.filter.View())
	}

	// Rebase to main confirmation modal
	if v.showRebaseConfirm && v.rebaseWorktree != nil {
		s.WriteString("\n\n")
		s.WriteString(th.DashboardAccentStyle.Render(" ┌─────────────────────────────────────────────────┐"))
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf(" │ Rebase '%s' to main using AI agent?  │", fitStr(v.rebaseWorktree.Branch, 24))))
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render(" │ Agent will resolve conflicts intelligently.     │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render(" │                        (y/n)                   │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render(" └─────────────────────────────────────────────────┘"))
	}

	// Agent merge confirmation modal
	if v.showAgentConfirm && v.agentWorktree != nil {
		s.WriteString("\n\n")
		s.WriteString(th.DashboardAccentStyle.Render(" ┌─────────────────────────────────────────────────┐"))
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf(" │ Start agent to merge '%s' into main?  │", fitStr(v.agentWorktree.Branch, 26))))
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render(" │ The agent will fetch, merge, and push.         │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render(" │                        (y/n)                   │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render(" └─────────────────────────────────────────────────┘"))
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
	s.WriteString(th.Help.Render(" r: Refresh   /: Search   ↑↓: Navigate   Enter: Navigate   n: Create   d: Remove   a: Merge (agent)   R: Rebase to main (agent)   m: Create MR   p: Prune "))

	return s.String()
}

// ShortHelp returns a short help string.
func (v *WorktreeView) ShortHelp() string {
	return "/: Search  ↑↓: Navigate  Enter: Navigate  n: Create  d: Remove  a: Merge (agent)  R: Rebase to main (agent)  m: Create MR  p: Prune"
}

// fitStr pads or truncates s to exactly n runes.
func fitStr(s string, n int) string {
	r := []rune(s)
	if len(r) >= n {
		return string(r[:n])
	}
	return s + strings.Repeat(" ", n-len(r))
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
		{Key: "a", Description: "Start agent to merge branch into main"},
		{Key: "R", Description: "Rebase branch to main using AI agent"},
		{Key: "m", Description: "Create MR/PR for this worktree branch"},
		{Key: "p", Description: "Prune stale worktrees"},
		{Key: "Esc", Description: "Clear filter / Cancel"},
		{Key: "1", Description: "Switch to Overview"},
		{Key: "2", Description: "Switch to Branches"},
		{Key: "3", Description: "Switch to Stashes"},
		{Key: "4", Description: "Switch to Worktrees"},
	}
}
