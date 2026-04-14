package views

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	"github.com/charmbracelet/bubbletea"
)

// StashView displays a list of git stash entries with management capabilities.
type StashView struct {
	viewBase
	repo    *git.RepoInfo
	stashes []git.StashEntry
	filter  *components.Filter[git.StashEntry]
	loading bool
	err     error

	// Preview panel state
	showPreview  bool
	previewStash *git.StashEntry

	// Modal states for destructive operations
	showDropConfirm  bool
	dropStashIndex   int
	showClearConfirm bool
	showPopConfirm   bool
	popStashIndex    int

	// New stash input state
	showNewStash      bool
	newStashMessage   string
	newStashInput     components.Filter[byte]
	newStashUntracked bool
}

// NewStashView creates a new stash view.
func NewStashView(repoPath string) *StashView {
	v := &StashView{
		viewBase: viewBase{repoPath: repoPath, width: 80, height: 24},
	}

	// Initialize the filter with stash items
	stashes := []git.StashEntry{}
	v.filter = components.NewFilter(stashes, v.renderStashItem)
	v.filter.SetHeight(v.height)

	return v
}

// Init initializes the stash view.
func (v *StashView) Init() tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		v.loadData()
		return RefreshDoneMsg{}
	}
}

// loadData loads all repository data.
func (v *StashView) loadData() {
	v.err = nil

	repo, err := git.OpenRepo(v.repoPath)
	if err != nil {
		v.err = fmt.Errorf("failed to open repo: %w", err)
		v.loading = false
		return
	}
	v.repo = repo

	stashes, err := git.GetStashList(v.repoPath)
	if err != nil {
		v.err = err
		v.loading = false
		return
	}
	v.stashes = stashes

	// Update filter with new stash list
	v.filter.SetItems(v.stashes)

	v.loading = false
}

// Update handles update events.
func (v *StashView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle modal states first
		if v.showDropConfirm {
			return v, v.handleDropConfirm(msg)
		}
		if v.showClearConfirm {
			return v, v.handleClearConfirm(msg)
		}
		if v.showPopConfirm {
			return v, v.handlePopConfirm(msg)
		}
		if v.showNewStash {
			return v, v.handleNewStashInput(msg)
		}
		if v.showPreview {
			return v, v.handlePreviewKey(msg)
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
		case "enter":
			// Show preview panel for selected stash
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				v.previewStash = &item
				v.showPreview = true
				v.loadStashPreview(v.previewStash.Index)
			}
		case "a":
			// Apply selected stash
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				v.loading = true
				index := item.Index
				return v, func() tea.Msg {
					v.applyStash(index)
					return RefreshDoneMsg{}
				}
			}
		case "p":
			// Show pop confirmation
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				v.popStashIndex = item.Index
				v.showPopConfirm = true
			}
		case "d":
			// Show drop confirmation
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				v.dropStashIndex = item.Index
				v.showDropConfirm = true
			}
		case "D":
			// Show clear all confirmation
			if len(v.stashes) > 0 {
				v.showClearConfirm = true
			}
		case "n":
			// Show new stash input
			v.showNewStash = true
			v.newStashMessage = ""
			v.newStashUntracked = false
			v.newStashInput = *components.NewFilter([]byte{}, func(b byte, i int, s bool) string {
				return string(b)
			})
		case "esc":
			// Clear filter if active
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

// handleDropConfirm handles key events during drop confirmation.
func (v *StashView) handleDropConfirm(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "enter":
		if v.dropStashIndex >= 0 {
			v.dropStash(v.dropStashIndex)
		}
		v.showDropConfirm = false
		v.dropStashIndex = -1
	case "n", "esc":
		v.showDropConfirm = false
		v.dropStashIndex = -1
	}
	return nil
}

// handleClearConfirm handles key events during clear all confirmation.
func (v *StashView) handleClearConfirm(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "enter":
		v.clearAllStashes()
		v.showClearConfirm = false
	case "n", "esc":
		v.showClearConfirm = false
	}
	return nil
}

// handlePopConfirm handles key events during pop confirmation.
func (v *StashView) handlePopConfirm(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "enter":
		if v.popStashIndex >= 0 {
			v.loading = true
			index := v.popStashIndex
			v.showPopConfirm = false
			v.popStashIndex = -1
			return func() tea.Msg {
				v.popStash(index)
				return RefreshDoneMsg{}
			}
		}
		v.showPopConfirm = false
		v.popStashIndex = -1
	case "n", "esc":
		v.showPopConfirm = false
		v.popStashIndex = -1
	}
	return nil
}

// handleNewStashInput handles key events during new stash creation.
func (v *StashView) handleNewStashInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		if v.newStashMessage != "" {
			v.loading = true
			stashMessage := v.newStashMessage
			includeUntracked := v.newStashUntracked
			v.showNewStash = false
			v.newStashMessage = ""
			v.newStashUntracked = false
			return func() tea.Msg {
				v.createStash(stashMessage, includeUntracked)
				return RefreshDoneMsg{}
			}
		}
		v.showNewStash = false
		v.newStashMessage = ""
		v.newStashUntracked = false
	case "esc":
		v.showNewStash = false
		v.newStashMessage = ""
		v.newStashUntracked = false
	case "tab":
		// Toggle untracked flag
		v.newStashUntracked = !v.newStashUntracked
	case "ctrl+w":
		v.newStashMessage = components.DeleteWordEnd(v.newStashMessage)
	default:
		// Handle text input for stash message
		if msg.Paste && len(msg.Runes) > 0 {
			v.newStashMessage += string(msg.Runes)
		} else if len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r >= 32 && r <= 126 {
				v.newStashMessage += string(r)
			}
		} else if msg.String() == "backspace" && len(v.newStashMessage) > 0 {
			v.newStashMessage = v.newStashMessage[:len(v.newStashMessage)-1]
		}
	}
	return nil
}

// handlePreviewKey handles key events during preview panel.
func (v *StashView) handlePreviewKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		v.showPreview = false
		v.previewStash = nil
	}
	return nil
}

// loadStashPreview loads the preview data for a stash.
func (v *StashView) loadStashPreview(index int) {
	if v.previewStash == nil {
		return
	}

	entry, err := git.GetStash(v.repoPath, index)
	if err != nil {
		v.err = err
		return
	}
	v.previewStash = entry
}

// applyStash applies the specified stash without dropping it.
func (v *StashView) applyStash(index int) {
	err := git.ApplyStash(v.repoPath, index, false)
	if err != nil {
		v.err = fmt.Errorf("failed to apply stash: %w", err)
		return
	}
	// Refresh data after apply
	v.loadData()
}

// popStash applies the specified stash and drops it.
func (v *StashView) popStash(index int) {
	err := git.ApplyStash(v.repoPath, index, true)
	if err != nil {
		v.err = fmt.Errorf("failed to pop stash: %w", err)
		return
	}
	// Refresh data after pop
	v.loadData()
}

// dropStash drops the specified stash.
func (v *StashView) dropStash(index int) {
	err := git.DropStash(v.repoPath, index)
	if err != nil {
		v.err = fmt.Errorf("failed to drop stash: %w", err)
		return
	}
	// Refresh data after drop
	v.loadData()
}

// clearAllStashes removes all stash entries.
func (v *StashView) clearAllStashes() {
	err := git.ClearStash(v.repoPath)
	if err != nil {
		v.err = fmt.Errorf("failed to clear stashes: %w", err)
		return
	}
	// Refresh data after clear
	v.loadData()
}

// createStash creates a new stash with the given message.
func (v *StashView) createStash(message string, includeUntracked bool) {
	_, err := git.CreateStash(v.repoPath, message, includeUntracked)
	if err != nil {
		v.err = fmt.Errorf("failed to create stash: %w", err)
		return
	}
	// Refresh data after create
	v.loadData()
}

// renderStashItem renders a single stash item in the list.
func (v *StashView) renderStashItem(stash git.StashEntry, index int, selected bool) string {
	th := theme.GetTheme()

	// Stash name
	nameStyle := th.StashStyle
	if selected {
		nameStyle = th.SelectedStashStyle
	}
	namePrefix := "  "
	if selected {
		namePrefix = " >"
	}

	var line strings.Builder
	line.WriteString(nameStyle.Render(fmt.Sprintf("%sstash@{%d}", namePrefix, stash.Index)))

	// Stash message
	line.WriteString(fmt.Sprintf(" %s", th.StatsStyle.Render(stash.Message)))

	// Author and date on second line if selected
	if selected {
		dateStr := stash.Date.Format(time.RFC822)
		line.WriteString(fmt.Sprintf("\n   %s • %s",
			th.MutedTextStyle.Render(stash.Author),
			th.MutedTextStyle.Render(dateStr)))
	}

	return line.String()
}

// View renders the stash view.
func (v *StashView) View() string {
	th := theme.GetTheme()

	// Loading state
	if v.loading {
		return th.StatsStyle.Render(" Loading stashes...")
	}

	// Error state
	if v.err != nil && v.repo == nil {
		return th.DashboardErrorStyle.Render(fmt.Sprintf(" Error: %v", v.err))
	}

	var s strings.Builder

	// Header
	s.WriteString(th.DashboardTitle.Render(" Stash Manager "))
	s.WriteString("\n\n")

	// Repo info line
	if v.repo != nil {
		s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" Repository: %s ", filepath.Base(v.repoPath))))
		if v.repo.IsDirty {
			s.WriteString(th.DashboardErrorStyle.Render("● dirty"))
		}
		s.WriteString("\n")

		// Stash count
		s.WriteString(fmt.Sprintf(" %s %s stash(es)\n",
			th.StashStyle.Render("Stashes:"),
			th.DashboardAccentStyle.Render(fmt.Sprintf("%d", len(v.stashes)))))
	}
	s.WriteString("\n")

	// Filter hint or active filter
	if v.filter.IsActive() {
		s.WriteString(v.filter.View())
	} else {
		// Show help hint first
		s.WriteString(th.Help.Render(" Press / to search • ↑/k: Select • Enter: Preview • a: Apply • p: Pop • d: Drop • n: New • D: Clear All "))
		s.WriteString("\n\n")
		s.WriteString(v.filter.View())
	}

	// Preview panel
	if v.showPreview && v.previewStash != nil {
		s.WriteString("\n")
		s.WriteString(renderSeparator())
		s.WriteString(th.DashboardTitle.Render(" Stash Preview "))
		s.WriteString("\n\n")

		s.WriteString(fmt.Sprintf(" %s %s\n",
			th.StashStyle.Render("Stash:"),
			th.DashboardAccentStyle.Render(fmt.Sprintf("stash@{%d}", v.previewStash.Index))))

		s.WriteString(fmt.Sprintf(" %s %s\n",
			th.BranchStyle.Render("Message:"),
			th.StatsStyle.Render(v.previewStash.Message)))

		if v.previewStash.Author != "" {
			s.WriteString(fmt.Sprintf(" %s %s\n",
				th.BranchStyle.Render("Author:"),
				th.StatsStyle.Render(v.previewStash.Author)))
		}

		if !v.previewStash.Date.IsZero() {
			s.WriteString(fmt.Sprintf(" %s %s\n",
				th.BranchStyle.Render("Date:"),
				th.StatsStyle.Render(v.previewStash.Date.Format(time.RFC822))))
		}

		s.WriteString("\n")

		// Files in stash
		if len(v.previewStash.Files) > 0 {
			s.WriteString(th.DashboardTitle.Render(" Files "))
			s.WriteString("\n\n")
			for _, file := range v.previewStash.Files {
				s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" %s\n", file)))
			}
		} else {
			s.WriteString(th.StatsStyle.Render(" No file changes recorded "))
		}

		s.WriteString("\n\n")
		s.WriteString(th.Help.Render(" ESC: Close preview "))
	}

	// Drop confirmation modal
	if v.showDropConfirm {
		s.WriteString("\n\n")
		s.WriteString(th.DashboardErrorStyle.Render(" ┌─────────────────────────────────────────────┐"))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" │ Drop stash@{%d}? This cannot be undone! (y/n) │", v.dropStashIndex)))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(" └─────────────────────────────────────────────┘"))
	}

	// Clear all confirmation modal
	if v.showClearConfirm {
		s.WriteString("\n\n")
		s.WriteString(th.DashboardErrorStyle.Render(" ┌─────────────────────────────────────────────┐"))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" │ Clear ALL %d stashes? This cannot be undone! │", len(v.stashes))))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(" │ Type 'y' to confirm: "))
		s.WriteString(th.DashboardErrorStyle.Render("                              │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(" └─────────────────────────────────────────────┘"))
	}

	// Pop confirmation modal
	if v.showPopConfirm {
		s.WriteString("\n\n")
		s.WriteString(th.DashboardAccentStyle.Render(" ┌─────────────────────────────────────────────┐"))
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf(" │ Pop stash@{%d}? (apply and drop) (y/n)    │", v.popStashIndex)))
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render(" └─────────────────────────────────────────────┘"))
	}

	// New stash input
	if v.showNewStash {
		s.WriteString("\n\n")
		s.WriteString(th.DashboardTitle.Render(" ┌─────────────────────────────────────────────┐"))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(" │ Create new stash                            │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" │ Message: %s", v.newStashMessage)))
		s.WriteString("\n")

		untrackedStr := "[-u]"
		if v.newStashUntracked {
			untrackedStr = "[+u]"
		}
		s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" │ Include untracked: %s (Tab to toggle)     │", untrackedStr)))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(" │ (press Enter to create, Esc to cancel)     │"))
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
	s.WriteString(renderSeparator())
	s.WriteString(th.Help.Render(" r: Refresh   /: Search   ↑↓: Navigate   Enter: Preview   a: Apply   p: Pop   d: Drop   n: New   D: Clear All "))

	return s.String()
}

// ShortHelp returns a short help string.
func (v *StashView) ShortHelp() string {
	return "/: Search  ↑↓: Navigate  Enter: Preview  a: Apply  p: Pop  d: Drop  n: New  D: Clear All"
}

// SetSize updates the view dimensions and resizes the filter.
func (v *StashView) SetSize(width, height int) {
	v.viewBase.SetSize(width, height)
	if v.filter != nil {
		v.filter.SetHeight(height)
	}
}

// Refresh reloads repository data.
func (v *StashView) Refresh() error {
	v.loadData()
	return v.err
}

// KeyBindings returns the keybindings for this view.
func (v *StashView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "r", Description: "Refresh stash list"},
		{Key: "/", Description: "Activate search filter"},
		{Key: "↑/k", Description: "Navigate up"},
		{Key: "↓/j", Description: "Navigate down"},
		{Key: "Enter", Description: "Preview selected stash"},
		{Key: "a", Description: "Apply selected stash"},
		{Key: "p", Description: "Pop (apply + drop) selected stash"},
		{Key: "d", Description: "Drop selected stash"},
		{Key: "n", Description: "Create new stash"},
		{Key: "D", Description: "Clear all stashes"},
		{Key: "Esc", Description: "Clear filter / Cancel"},
		{Key: "1", Description: "Switch to Overview"},
		{Key: "2", Description: "Switch to Branches"},
		{Key: "3", Description: "Switch to Stashes"},
		{Key: "4", Description: "Switch to Worktrees"},
	}
}
