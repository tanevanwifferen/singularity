package views

import (
	"errors"
	"fmt"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	"github.com/charmbracelet/bubbletea"
)

// PRView provides a TUI interface for creating Pull Requests (GitHub) and Merge Requests (GitLab).
type PRView struct {
	viewBase
	repo     *service.RepoInfo
	branches []service.BranchInfo

	// Branch selection state
	sourceBranchIdx     int
	targetBranchIdx     int
	pendingSourceBranch string // pre-select this branch as source on next load

	// Forge detection
	forgeAuth *service.ForgeAuth

	// PR/MR fields
	title       string
	description string

	// Multi-line description editing state
	editDescription bool
	descLines       []string
	descCursorRow   int
	descCursorCol   int

	// UI state
	loading     bool
	isCreating  bool
	createdMR   *service.MergeRequest
	errorMsg    string
	successMsg  string
	showSuccess bool

	// Help state
	showHelp bool
}

// NewPRView creates a new PR/MR creation view.
func NewPRView(repoPath string) *PRView {
	return &PRView{
		viewBase:        viewBase{repoPath: repoPath, width: 120, height: 30},
		sourceBranchIdx: -1,
		targetBranchIdx: -1,
		editDescription: false,
		descLines:       []string{""},
		descCursorRow:   0,
		descCursorCol:   0,
	}
}

// SetPendingSourceBranch pre-selects a branch as the source on the next data load.
func (v *PRView) SetPendingSourceBranch(name string) {
	v.pendingSourceBranch = name
}

// Init initializes the PR view.
func (v *PRView) Init() tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		v.loadData()
		return RefreshDoneMsg{}
	}
}

// loadData loads repository data and detects forge authentication.
func (v *PRView) loadData() {
	v.errorMsg = ""

	// Open repository
	repo, err := v.services.Repo.Open(v.ctx(), v.repoPath)
	if err != nil {
		v.errorMsg = fmt.Sprintf("Failed to open repo: %v", err)
		v.loading = false
		return
	}
	v.repo = repo
	v.branches = repo.Branches

	// Detect forge authentication
	auth, err := v.services.Forge.DetectAuth(v.ctx())
	if err != nil {
		v.forgeAuth = nil
	} else {
		v.forgeAuth = auth
	}

	// Set source branch: use pending (from worktree view) if set, else current branch
	sourceName := v.pendingSourceBranch
	if sourceName == "" {
		sourceName = v.repo.CurrentBranch
	}
	v.pendingSourceBranch = ""
	for i, b := range v.branches {
		if b.Name == sourceName {
			v.sourceBranchIdx = i
			break
		}
	}
	if v.sourceBranchIdx == -1 && len(v.branches) > 0 {
		v.sourceBranchIdx = 0
	}

	// Set default target branch to main/master or first non-source branch
	v.setDefaultTargetBranch()

	// Auto-generate title
	v.generateTitle()

	// Auto-generate description
	v.generateDescription()

	v.loading = false
}

// setDefaultTargetBranch sets the target branch to main/master or first non-source branch.
func (v *PRView) setDefaultTargetBranch() {
	// Look for main or master
	targetNames := []string{"main", "master", "develop", "devel"}
	for _, name := range targetNames {
		for i, b := range v.branches {
			if b.Name == name && i != v.sourceBranchIdx {
				v.targetBranchIdx = i
				return
			}
		}
	}

	// Default to first branch that's not the source
	for i := range v.branches {
		if i != v.sourceBranchIdx {
			v.targetBranchIdx = i
			return
		}
	}
}

// generateTitle auto-generates a title from the branch comparison.
func (v *PRView) generateTitle() {
	if v.sourceBranchIdx < 0 || v.targetBranchIdx < 0 || len(v.branches) == 0 {
		v.title = ""
		return
	}

	sourceBranch := v.branches[v.sourceBranchIdx].Name
	targetBranch := v.branches[v.targetBranchIdx].Name

	title, err := v.services.MR.GenerateTitle(v.ctx(), v.repoPath, sourceBranch, targetBranch)
	if err != nil {
		v.title = fmt.Sprintf("Merge %s into %s", sourceBranch, targetBranch)
	} else {
		v.title = title
	}
}

// generateDescription auto-generates a description from commits.
func (v *PRView) generateDescription() {
	if v.sourceBranchIdx < 0 || v.targetBranchIdx < 0 || len(v.branches) == 0 {
		v.description = ""
		v.descLines = []string{""}
		return
	}

	sourceBranch := v.branches[v.sourceBranchIdx].Name
	targetBranch := v.branches[v.targetBranchIdx].Name

	desc, err := v.services.MR.GenerateDescription(v.ctx(), v.repoPath, sourceBranch, targetBranch)
	if err != nil || desc == "" {
		v.description = ""
		v.descLines = []string{""}
	} else {
		v.description = desc
		v.descLines = strings.Split(desc, "\n")
		if len(v.descLines) == 0 {
			v.descLines = []string{""}
		}
	}
}

// Update handles update events.
func (v *PRView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle success state
	if v.showSuccess {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "esc", "enter", "q":
				v.showSuccess = false
				v.createdMR = nil
			}
		}
		return v, nil
	}

	// Handle description editing mode
	if v.editDescription {
		return v, v.handleDescriptionInput(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		return v, v.handleKey(msg)

	case RefreshDoneMsg:
		v.loading = false

	case tea.WindowSizeMsg:
		v.width = msg.Width
		v.height = msg.Height
	}

	return v, nil
}

// handleKey handles key events for the main PR view.
func (v *PRView) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "r":
		v.loading = true
		return func() tea.Msg {
			v.loadData()
			return RefreshDoneMsg{}
		}

	case "up", "k":
		v.moveSourceBranchUp()

	case "down", "j":
		v.moveSourceBranchDown()

	case "left":
		v.moveTargetBranchUp()

	case "right":
		v.moveTargetBranchDown()

	case "tab":
		// Toggle between source and target branch selection
		// For now, just move to title editing
		v.startTitleEdit()

	case "e":
		// Edit description
		v.editDescription = true
		v.descCursorRow = 0
		v.descCursorCol = 0

	case "enter":
		// Start editing description on enter in the description area
		v.editDescription = true
		v.descCursorRow = 0
		v.descCursorCol = 0

	case "ctrl+j":
		// Move down in branch selection
		if v.sourceBranchIdx < len(v.branches)-1 {
			if v.branches[v.sourceBranchIdx+1].Name == v.getTargetBranchName() {
				if v.sourceBranchIdx+2 < len(v.branches) {
					v.sourceBranchIdx += 2
				}
			} else {
				v.sourceBranchIdx++
			}
			v.generateTitle()
			v.generateDescription()
		}

	case "ctrl+k":
		// Move up in branch selection
		if v.sourceBranchIdx > 0 {
			v.sourceBranchIdx--
			if v.branches[v.sourceBranchIdx].Name == v.getTargetBranchName() {
				if v.sourceBranchIdx > 0 {
					v.sourceBranchIdx--
				}
			}
			v.generateTitle()
			v.generateDescription()
		}

	case "c":
		// Create PR/MR
		if v.forgeAuth != nil && v.forgeAuth.Valid {
			v.createPR()
		} else {
			v.errorMsg = "No forge authentication found. Please configure gh or glab CLI."
		}

	case "esc":
		if v.errorMsg != "" {
			v.errorMsg = ""
		}

	case "?":
		v.showHelp = !v.showHelp
	}

	return nil
}

// handleDescriptionInput handles key events during multi-line description editing.
func (v *PRView) handleDescriptionInput(msg tea.Msg) tea.Cmd {
	if msg, ok := msg.(tea.KeyMsg); ok {
		switch msg.String() {
		case "esc":
			// Exit description editing, save content
			v.editDescription = false
			v.description = strings.Join(v.descLines, "\n")

		case "enter":
			// Insert new line
			if v.descCursorRow < len(v.descLines)-1 {
				// Split current line at cursor
				line := v.descLines[v.descCursorRow]
				_ = line[:v.descCursorCol] // prefix before cursor (kept for future use)
				after := line[v.descCursorCol:]

				// Insert new line after current
				newLines := make([]string, len(v.descLines)+1)
				copy(newLines[:v.descCursorRow+1], v.descLines[:v.descCursorRow+1])
				newLines[v.descCursorRow+1] = after
				copy(newLines[v.descCursorRow+2:], v.descLines[v.descCursorRow+1:])
				v.descLines = newLines

				// Move cursor to beginning of new line
				v.descCursorRow++
				v.descCursorCol = 0
			} else {
				// Append new line at the end
				v.descLines = append(v.descLines, "")
				v.descCursorRow++
				v.descCursorCol = 0
			}

		case "ctrl+w":
			if v.descCursorCol > 0 {
				line := v.descLines[v.descCursorRow]
				newLine, newCol := components.DeleteWord(line, v.descCursorCol)
				v.descLines[v.descCursorRow] = newLine
				v.descCursorCol = newCol
			}

		case "backspace":
			if v.descCursorCol > 0 {
				// Delete character before cursor
				line := v.descLines[v.descCursorRow]
				v.descLines[v.descCursorRow] = line[:v.descCursorCol-1] + line[v.descCursorCol:]
				v.descCursorCol--
			} else if v.descCursorRow > 0 {
				// Merge with previous line
				prevLine := v.descLines[v.descCursorRow-1]
				currLine := v.descLines[v.descCursorRow]
				v.descLines[v.descCursorRow-1] = prevLine + currLine

				// Remove current line
				newLines := make([]string, len(v.descLines)-1)
				copy(newLines[:v.descCursorRow], v.descLines[:v.descCursorRow])
				copy(newLines[v.descCursorRow:], v.descLines[v.descCursorRow+1:])
				v.descLines = newLines

				v.descCursorRow--
				v.descCursorCol = len(prevLine)
			}

		case "delete":
			if v.descCursorCol < len(v.descLines[v.descCursorRow]) {
				// Delete character after cursor
				line := v.descLines[v.descCursorRow]
				v.descLines[v.descCursorRow] = line[:v.descCursorCol] + line[v.descCursorCol+1:]
			} else if v.descCursorRow < len(v.descLines)-1 {
				// Merge with next line
				currLine := v.descLines[v.descCursorRow]
				nextLine := v.descLines[v.descCursorRow+1]
				v.descLines[v.descCursorRow] = currLine + nextLine

				// Remove next line
				newLines := make([]string, len(v.descLines)-1)
				copy(newLines[:v.descCursorRow+1], v.descLines[:v.descCursorRow+1])
				copy(newLines[v.descCursorRow+1:], v.descLines[v.descCursorRow+2:])
				v.descLines = newLines
			}

		case "up", "ctrl+k":
			if v.descCursorRow > 0 {
				v.descCursorRow--
				if v.descCursorCol > len(v.descLines[v.descCursorRow]) {
					v.descCursorCol = len(v.descLines[v.descCursorRow])
				}
			}

		case "down", "ctrl+j":
			if v.descCursorRow < len(v.descLines)-1 {
				v.descCursorRow++
				if v.descCursorCol > len(v.descLines[v.descCursorRow]) {
					v.descCursorCol = len(v.descLines[v.descCursorRow])
				}
			}

		case "left", "ctrl+h":
			if v.descCursorCol > 0 {
				v.descCursorCol--
			} else if v.descCursorRow > 0 {
				v.descCursorRow--
				v.descCursorCol = len(v.descLines[v.descCursorRow])
			}

		case "right", "ctrl+l":
			if v.descCursorCol < len(v.descLines[v.descCursorRow]) {
				v.descCursorCol++
			} else if v.descCursorRow < len(v.descLines)-1 {
				v.descCursorRow++
				v.descCursorCol = 0
			}

		case "home":
			v.descCursorCol = 0

		case "end":
			v.descCursorCol = len(v.descLines[v.descCursorRow])

		default:
			// Regular character input
			if len(msg.Runes) > 0 {
				r := msg.Runes[0]
				if r >= 32 && r < 127 {
					line := v.descLines[v.descCursorRow]
					v.descLines[v.descCursorRow] = line[:v.descCursorCol] + string(r) + line[v.descCursorCol:]
					v.descCursorCol++
				}
			}
		}
	}
	return nil
}

// startTitleEdit puts the view into title editing mode.
// For simplicity, title is edited inline via a prompt.
func (v *PRView) startTitleEdit() {
	// Title editing would need a text input component
	// For now, we just show a hint that title is auto-generated
}

// moveSourceBranchUp moves the source branch selection up.
func (v *PRView) moveSourceBranchUp() {
	if v.sourceBranchIdx > 0 {
		v.sourceBranchIdx--
		v.generateTitle()
		v.generateDescription()
	}
}

// moveSourceBranchDown moves the source branch selection down.
func (v *PRView) moveSourceBranchDown() {
	if v.sourceBranchIdx < len(v.branches)-1 {
		// Skip if it would match target
		nextIdx := v.sourceBranchIdx + 1
		if v.branches[nextIdx].Name == v.getTargetBranchName() && nextIdx < len(v.branches)-1 {
			nextIdx++
		}
		v.sourceBranchIdx = nextIdx
		v.generateTitle()
		v.generateDescription()
	}
}

// moveTargetBranchUp moves the target branch selection up.
func (v *PRView) moveTargetBranchUp() {
	for i := v.targetBranchIdx - 1; i >= 0; i-- {
		if v.branches[i].Name != v.branches[v.sourceBranchIdx].Name {
			v.targetBranchIdx = i
			v.generateTitle()
			v.generateDescription()
			return
		}
	}
}

// moveTargetBranchDown moves the target branch selection down.
func (v *PRView) moveTargetBranchDown() {
	for i := v.targetBranchIdx + 1; i < len(v.branches); i++ {
		if v.branches[i].Name != v.branches[v.sourceBranchIdx].Name {
			v.targetBranchIdx = i
			v.generateTitle()
			v.generateDescription()
			return
		}
	}
}

// getSourceBranchName returns the name of the selected source branch.
func (v *PRView) getSourceBranchName() string {
	if v.sourceBranchIdx >= 0 && v.sourceBranchIdx < len(v.branches) {
		return v.branches[v.sourceBranchIdx].Name
	}
	return ""
}

// getTargetBranchName returns the name of the selected target branch.
func (v *PRView) getTargetBranchName() string {
	if v.targetBranchIdx >= 0 && v.targetBranchIdx < len(v.branches) {
		return v.branches[v.targetBranchIdx].Name
	}
	return ""
}

// createPR creates the pull/merge request.
func (v *PRView) createPR() {
	if v.sourceBranchIdx < 0 || v.targetBranchIdx < 0 {
		v.errorMsg = "Please select source and target branches"
		return
	}

	sourceBranch := v.getSourceBranchName()
	targetBranch := v.getTargetBranchName()

	if sourceBranch == targetBranch {
		v.errorMsg = "Source and target branches must be different"
		return
	}

	v.isCreating = true
	v.errorMsg = ""

	go func() {
		mr, err := v.services.MR.Create(v.ctx(), v.repoPath, sourceBranch, targetBranch, v.title, v.description, nil)
		if err != nil {
			v.isCreating = false
			if errors.Is(err, service.ErrMRAlreadyExists) {
				v.successMsg = "A merge request already exists for this branch."
				v.showSuccess = true
			} else {
				v.errorMsg = fmt.Sprintf("Failed to create MR: %v", err)
			}
			return
		}

		v.isCreating = false
		v.createdMR = mr
		v.showSuccess = true
	}()
}

// View renders the PR/MR creation view.
func (v *PRView) View() string {
	th := theme.GetTheme()

	// Success view
	if v.showSuccess && v.createdMR != nil {
		return v.renderSuccessView(th)
	}

	// Main view
	var s strings.Builder

	// Header
	s.WriteString(th.Title.Render("Create Pull Request"))
	s.WriteString(" | ")

	// Forge indicator
	if v.forgeAuth != nil && v.forgeAuth.Valid {
		forgeName := "GitHub"
		if v.forgeAuth.IsGitLab() {
			forgeName = "GitLab"
		}
		s.WriteString(th.DashboardAccentStyle.Render(forgeName))
		s.WriteString(" @ ")
		s.WriteString(th.InfoStyle.Render(v.forgeAuth.Username))
	} else {
		s.WriteString(th.ErrorStyle.Render("No forge auth"))
	}
	s.WriteString("\n\n")

	// Loading state
	if v.loading {
		s.WriteString(th.MutedTextStyle.Render("Loading...\n"))
		return s.String()
	}

	// Branch selection section
	s.WriteString(th.BranchStyle.Render("Source Branch (compare):"))
	s.WriteString("  [")
	if v.forgeAuth != nil && v.forgeAuth.Valid {
		s.WriteString(th.DashboardAccentStyle.Render("↑↓"))
	}
	s.WriteString("]\n")
	for i, b := range v.branches {
		prefix := "  "
		if i == v.sourceBranchIdx {
			prefix = th.DashboardAccentStyle.Render("▶ ")
		}
		branchName := b.Name
		if b.Name == v.repo.CurrentBranch {
			branchName += " (current)"
		}
		if i == v.sourceBranchIdx {
			s.WriteString(prefix + th.SelectedBranchStyle.Render(branchName) + "\n")
		} else {
			s.WriteString(prefix + th.BranchStyle.Render(branchName) + "\n")
		}
	}
	s.WriteString("\n")

	s.WriteString(th.BranchStyle.Render("Target Branch (base):"))
	s.WriteString("  [")
	if v.forgeAuth != nil && v.forgeAuth.Valid {
		s.WriteString(th.DashboardAccentStyle.Render("←→"))
	}
	s.WriteString("]\n")
	for i, b := range v.branches {
		if i == v.targetBranchIdx {
			prefix := th.DashboardAccentStyle.Render("▶ ")
			branchName := b.Name
			if b.Name == v.repo.CurrentBranch {
				branchName += " (current)"
			}
			s.WriteString(prefix + th.SelectedBranchStyle.Render(branchName) + "\n")
		}
	}
	s.WriteString("\n")

	// Title section
	s.WriteString(th.BranchStyle.Render("Title:"))
	s.WriteString(th.DashboardAccentStyle.Render(" (auto-generated)"))
	s.WriteString("\n")
	s.WriteString("  " + th.BranchStyle.Render(v.title) + "\n")
	s.WriteString("\n")

	// Description section
	s.WriteString(th.BranchStyle.Render("Description:"))
	s.WriteString(th.DashboardAccentStyle.Render(" [e] to edit"))
	s.WriteString("\n")

	if v.editDescription {
		// Render description editor
		for i, line := range v.descLines {
			prefix := "  "
			if i == v.descCursorRow {
				prefix = th.DashboardAccentStyle.Render("▶ ")
				// Show cursor
				if v.descCursorCol <= len(line) {
					s.WriteString(prefix + line[:v.descCursorCol])
					s.WriteString(th.DashboardAccentStyle.Render("_"))
					s.WriteString(line[v.descCursorCol:] + "\n")
				} else {
					s.WriteString(prefix + line + th.DashboardAccentStyle.Render("_") + "\n")
				}
			} else {
				s.WriteString(prefix + line + "\n")
			}
		}
		// Add hint at bottom
		s.WriteString("\n")
		s.WriteString(th.MutedTextStyle.Render("  [Enter]=newline [Esc]=done "))
	} else {
		// Render description preview
		descLines := strings.Split(v.description, "\n")
		if len(descLines) == 0 || (len(descLines) == 1 && descLines[0] == "") {
			s.WriteString("  " + th.MutedTextStyle.Render("(no description)") + "\n")
		} else {
			for _, line := range descLines {
				if len(line) > 80 {
					line = line[:77] + "..."
				}
				s.WriteString("  " + th.BranchStyle.Render(line) + "\n")
			}
		}
	}
	s.WriteString("\n")

	// Error message
	if v.errorMsg != "" {
		s.WriteString(th.ErrorStyle.Render("Error: " + v.errorMsg))
		s.WriteString("\n\n")
	}

	// Creating state
	if v.isCreating {
		s.WriteString(th.InfoStyle.Render("Creating PR/MR..."))
		s.WriteString("\n\n")
	}

	// Action buttons
	s.WriteString(th.BranchStyle.Render("[c] Create PR/MR"))
	if v.forgeAuth == nil || !v.forgeAuth.Valid {
		s.WriteString(th.MutedTextStyle.Render(" (requires gh or glab auth)"))
	}
	s.WriteString("  ")
	s.WriteString(th.MutedTextStyle.Render("[r] Refresh\n"))
	s.WriteString(th.MutedTextStyle.Render("[?] Help\n"))

	// Help overlay
	if v.showHelp {
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render("Keyboard Shortcuts:\n"))
		s.WriteString("  ↑/k       Move source branch up\n")
		s.WriteString("  ↓/j       Move source branch down\n")
		s.WriteString("  ←/h       Move target branch up\n")
		s.WriteString("  →/l       Move target branch down\n")
		s.WriteString("  e         Edit description\n")
		s.WriteString("  c         Create PR/MR\n")
		s.WriteString("  r         Refresh\n")
		s.WriteString("  ?         Toggle help\n")
		s.WriteString("  esc       Close/cancel\n")
	}

	return s.String()
}

// renderSuccessView renders the success view after PR/MR creation.
func (v *PRView) renderSuccessView(th theme.Theme) string {
	var s strings.Builder

	if v.createdMR == nil {
		s.WriteString(th.InfoStyle.Render(v.successMsg))
		s.WriteString("\n\n")
		s.WriteString(th.MutedTextStyle.Render("Press [esc] or [enter] to continue..."))
		return s.String()
	}

	s.WriteString(th.Title.Render("✓ PR/MR Created Successfully!"))
	s.WriteString("\n\n")

	if v.createdMR != nil {
		s.WriteString(th.BranchStyle.Render("Title: ") + v.createdMR.Title + "\n")
		s.WriteString(th.BranchStyle.Render("Number: #") + fmt.Sprintf("%d", v.createdMR.Number) + "\n")
		s.WriteString(th.BranchStyle.Render("URL: ") + th.InfoStyle.Render(v.createdMR.URL) + "\n")
		s.WriteString(th.BranchStyle.Render("Author: ") + v.createdMR.Author + "\n")
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render("Open the URL in your browser to view the PR/MR.\n"))
	}

	s.WriteString("\n")
	s.WriteString(th.MutedTextStyle.Render("Press [esc] or [enter] to continue..."))

	return s.String()
}

// ShortHelp returns a short help string for the PR view.
func (v *PRView) ShortHelp() string {
	return "[c] Create PR  [↑↓] Source  [←→] Target  [e] Edit description  [?] Help"
}
