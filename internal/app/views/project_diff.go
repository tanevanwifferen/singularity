package views

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"git-frontend/internal/app/components"
	"git-frontend/internal/git"
	"git-frontend/internal/project"
	"git-frontend/internal/theme"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// projectDiffDoneMsg carries per-repo workdir diff results after concurrent fetching.
type projectDiffDoneMsg struct {
	results map[string]*repoWorkdirResult
}

// repoWorkdirResult holds the workdir diff result for a single repo.
type repoWorkdirResult struct {
	RepoName string
	RepoPath string
	Diff     *git.WorkdirDiff
	Err      error
}

// projectDiffItem is a flattened navigation entry — repo header or a changed file.
type projectDiffItem struct {
	IsRepoHeader bool
	RepoName     string
	RepoPath     string
	// For file entries
	WorkdirFile *git.WorkdirStatus
	// For repo headers: aggregate stats
	TotalFiles int
	TotalAdds  int
	TotalDels  int
	Error      string
}

// ProjectDiffView shows uncommitted changes across all repos in the project.
type ProjectDiffView struct {
	proj   *project.Project
	width  int
	height int
	loading bool
	err    error

	repoOrder []string
	repoDiffs map[string]*repoWorkdirResult

	items       []projectDiffItem
	selectedIdx int

	showDiff        bool
	currentDiff     string
	parsedDiffLines []DiffLine
	diffScrollOffset int
}

// NewProjectDiffView creates a new project diff view.
func NewProjectDiffView(proj *project.Project) *ProjectDiffView {
	return &ProjectDiffView{
		proj:   proj,
		width:  120,
		height: 30,
	}
}

// SetProject updates the project reference and resets state.
func (v *ProjectDiffView) SetProject(proj *project.Project) {
	v.proj = proj
	v.reset()
}

func (v *ProjectDiffView) reset() {
	v.repoOrder = nil
	v.repoDiffs = nil
	v.items = nil
	v.selectedIdx = 0
	v.showDiff = false
	v.currentDiff = ""
	v.parsedDiffLines = nil
	v.diffScrollOffset = 0
	v.loading = false
	v.err = nil
}

// Init fetches workdir status for all repos concurrently.
func (v *ProjectDiffView) Init() tea.Cmd {
	if v.proj == nil {
		return nil
	}
	v.loading = true
	proj := v.proj

	return func() tea.Msg {
		results := make(map[string]*repoWorkdirResult)
		var mu sync.Mutex
		var wg sync.WaitGroup

		for _, repo := range proj.Repos {
			wg.Add(1)
			go func(r *project.Repo) {
				defer wg.Done()
				diff, err := git.GetWorkdirStatus(r.Path)
				mu.Lock()
				results[r.Name] = &repoWorkdirResult{
					RepoName: r.Name,
					RepoPath: r.Path,
					Diff:     diff,
					Err:      err,
				}
				mu.Unlock()
			}(repo)
		}
		wg.Wait()

		return projectDiffDoneMsg{results: results}
	}
}

// buildItems flattens repo diffs into a navigable list.
func (v *ProjectDiffView) buildItems() {
	v.items = nil
	for _, name := range v.repoOrder {
		rd := v.repoDiffs[name]

		header := projectDiffItem{
			IsRepoHeader: true,
			RepoName:     name,
			RepoPath:     rd.RepoPath,
		}

		if rd.Err != nil {
			header.Error = rd.Err.Error()
			v.items = append(v.items, header)
			continue
		}

		if rd.Diff != nil {
			header.TotalFiles = len(rd.Diff.Files)
			header.TotalAdds = rd.Diff.TotalStagedAdds + rd.Diff.TotalUnstagedAdds
			header.TotalDels = rd.Diff.TotalStagedDels + rd.Diff.TotalUnstagedDels
		}
		v.items = append(v.items, header)

		if rd.Diff != nil {
			for i := range rd.Diff.Files {
				v.items = append(v.items, projectDiffItem{
					RepoName:    name,
					RepoPath:    rd.RepoPath,
					WorkdirFile: &rd.Diff.Files[i],
				})
			}
		}
	}
}

// Update handles messages.
func (v *ProjectDiffView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case projectDiffDoneMsg:
		v.loading = false
		v.repoDiffs = msg.results
		v.repoOrder = make([]string, 0, len(msg.results))
		for name := range msg.results {
			v.repoOrder = append(v.repoOrder, name)
		}
		sort.Strings(v.repoOrder)
		v.buildItems()
		v.selectedIdx = 0
		for i, item := range v.items {
			if !item.IsRepoHeader {
				v.selectedIdx = i
				break
			}
		}
		v.loadSelectedFileDiff()

	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			v.moveCursor(-1)
		case "down", "j":
			v.moveCursor(1)
		case "g":
			v.diffScrollOffset = 0
		case "G":
			if v.showDiff {
				maxScroll := len(v.parsedDiffLines) - (v.height - 20)
				if maxScroll < 0 {
					maxScroll = 0
				}
				v.diffScrollOffset = maxScroll
			}
		case "pgup", "ctrl+u":
			if v.showDiff {
				pageSize := v.height - 20
				if pageSize < 1 {
					pageSize = 1
				}
				v.diffScrollOffset -= pageSize
				if v.diffScrollOffset < 0 {
					v.diffScrollOffset = 0
				}
			}
		case "pgdown", "ctrl+d":
			if v.showDiff {
				pageSize := v.height - 20
				if pageSize < 1 {
					pageSize = 1
				}
				maxScroll := len(v.parsedDiffLines) - pageSize
				if maxScroll < 0 {
					maxScroll = 0
				}
				v.diffScrollOffset += pageSize
				if v.diffScrollOffset > maxScroll {
					v.diffScrollOffset = maxScroll
				}
			}
		case "r":
			return v, v.Init()
		case "esc":
			return v, func() tea.Msg {
				return ViewChangeMsg{ViewName: "ProjectSync"}
			}
		}

	case tea.WindowSizeMsg:
		v.width = msg.Width
		v.height = msg.Height
	}

	return v, nil
}

func (v *ProjectDiffView) moveCursor(delta int) {
	if len(v.items) == 0 {
		return
	}
	newIdx := v.selectedIdx + delta
	if newIdx < 0 {
		newIdx = 0
	}
	if newIdx >= len(v.items) {
		newIdx = len(v.items) - 1
	}
	if newIdx == v.selectedIdx {
		return
	}
	v.selectedIdx = newIdx
	v.diffScrollOffset = 0
	if !v.items[v.selectedIdx].IsRepoHeader {
		v.loadSelectedFileDiff()
	} else {
		v.showDiff = false
		v.currentDiff = ""
		v.parsedDiffLines = nil
	}
}

// loadSelectedFileDiff loads the combined staged+unstaged diff for the selected file.
func (v *ProjectDiffView) loadSelectedFileDiff() {
	if v.selectedIdx >= len(v.items) {
		return
	}
	item := v.items[v.selectedIdx]
	if item.IsRepoHeader || item.WorkdirFile == nil {
		return
	}

	f := item.WorkdirFile
	stagedDiff, _ := git.GetStagedFileDiff(item.RepoPath, f.Path)
	unstagedDiff, _ := git.GetUnstagedFileDiff(item.RepoPath, f.Path)

	var parts []string
	if stagedDiff != "" {
		parts = append(parts, "=== STAGED ===\n"+stagedDiff)
	}
	if unstagedDiff != "" {
		parts = append(parts, "=== UNSTAGED ===\n"+unstagedDiff)
	}

	v.currentDiff = strings.Join(parts, "\n")
	v.parsedDiffLines = ParseDiffLines(v.currentDiff)
	v.showDiff = true
	v.diffScrollOffset = 0
}

// View renders the project diff view.
func (v *ProjectDiffView) View() string {
	th := theme.GetTheme()
	var s strings.Builder

	s.WriteString(th.DashboardTitle.Render(" Project Changes "))
	if v.proj != nil {
		s.WriteString(th.MutedTextStyle.Render(fmt.Sprintf("  %s", v.proj.Name)))
	}
	s.WriteString("\n\n")

	if v.loading {
		s.WriteString(th.MutedTextStyle.Render(" Loading changes across all repos..."))
		s.WriteString("\n")
		return s.String()
	}

	if v.err != nil {
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" Error: %v", v.err)))
		s.WriteString("\n")
	}

	if len(v.items) == 0 {
		s.WriteString(th.MutedTextStyle.Render(" No uncommitted changes found in any repo."))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render(" r: Refresh  Esc: Back"))
		return s.String()
	}

	leftWidth := 55
	rightWidth := v.width - leftWidth - 3
	if rightWidth < 30 {
		rightWidth = 30
	}

	leftPanel := v.renderItemList(leftWidth)
	rightPanel := v.renderDetailPanel(rightWidth)

	s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel))
	s.WriteString("\n")
	s.WriteString(th.Help.Render(" j/k: Navigate  PgUp/PgDn: Scroll diff  g/G: Top/bottom  r: Refresh  Esc: Back"))

	return s.String()
}

func (v *ProjectDiffView) renderItemList(width int) string {
	th := theme.GetTheme()
	var s strings.Builder

	s.WriteString(th.DashboardTitle.Render(" Files "))
	s.WriteString("\n")

	dividerLen := width - 2
	if dividerLen < 0 {
		dividerLen = 0
	}
	s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %s ", strings.Repeat("─", dividerLen))))
	s.WriteString("\n")

	// Summary
	totalFiles, totalAdd, totalDel := 0, 0, 0
	for _, name := range v.repoOrder {
		rd := v.repoDiffs[name]
		if rd.Diff != nil {
			totalFiles += len(rd.Diff.Files)
			totalAdd += rd.Diff.TotalStagedAdds + rd.Diff.TotalUnstagedAdds
			totalDel += rd.Diff.TotalStagedDels + rd.Diff.TotalUnstagedDels
		}
	}
	s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %d files across %d repos ", totalFiles, len(v.repoOrder))))
	if totalAdd > 0 {
		s.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf("+%d ", totalAdd)))
	}
	if totalDel > 0 {
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf("-%d", totalDel)))
	}
	s.WriteString("\n")

	visibleLines := v.height - 10
	if visibleLines < 5 {
		visibleLines = 5
	}

	startIdx := v.selectedIdx - visibleLines/2
	if startIdx < 0 {
		startIdx = 0
	}
	endIdx := startIdx + visibleLines
	if endIdx > len(v.items) {
		endIdx = len(v.items)
		startIdx = endIdx - visibleLines
		if startIdx < 0 {
			startIdx = 0
		}
	}

	for i := startIdx; i < endIdx && i < len(v.items); i++ {
		item := v.items[i]
		selected := i == v.selectedIdx
		if item.IsRepoHeader {
			s.WriteString(v.renderRepoHeader(item, selected, width))
		} else {
			s.WriteString(v.renderFileEntry(item, selected, width))
		}
		s.WriteString("\n")
	}

	if len(v.items) > visibleLines {
		s.WriteString(th.Help.Render(fmt.Sprintf(" %d-%d of %d ", startIdx+1, endIdx, len(v.items))))
	}

	return s.String()
}

func (v *ProjectDiffView) renderRepoHeader(item projectDiffItem, selected bool, width int) string {
	th := theme.GetTheme()
	var line strings.Builder

	if selected {
		line.WriteString(th.DashboardAccentStyle.Render(" ► "))
	} else {
		line.WriteString("   ")
	}

	nameStyle := th.DashboardTitle
	if selected {
		nameStyle = th.SelectedBranchStyle
	}
	line.WriteString(nameStyle.Render(item.RepoName))

	if item.Error != "" {
		line.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf("  error: %s", truncate(item.Error, 30))))
	} else if item.TotalFiles == 0 {
		line.WriteString(th.MutedTextStyle.Render("  clean"))
	} else {
		line.WriteString(th.MutedTextStyle.Render(fmt.Sprintf("  %d files", item.TotalFiles)))
		if item.TotalAdds > 0 {
			line.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf(" +%d", item.TotalAdds)))
		}
		if item.TotalDels > 0 {
			line.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" -%d", item.TotalDels)))
		}
	}

	return line.String()
}

func (v *ProjectDiffView) renderFileEntry(item projectDiffItem, selected bool, width int) string {
	th := theme.GetTheme()
	var line strings.Builder

	prefix := "     "
	style := th.BranchStyle
	if selected {
		prefix = "   > "
		style = th.SelectedBranchStyle
	}

	f := item.WorkdirFile

	// Status character: prefer staged, fall back to unstaged
	statusChar := "?"
	statusStyle := th.MutedTextStyle
	status := f.StagedStatus
	if status == "" || status == "?" {
		status = f.UnstagedStatus
	}
	switch status {
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
	}

	// Staged indicator
	stageIndicator := ""
	if f.StagedStatus != "" && f.StagedStatus != "?" && f.StagedStatus != " " {
		stageIndicator = th.DashboardAccentStyle.Render("S")
	} else {
		stageIndicator = " "
	}

	path := f.Path
	maxPathLen := width - 28
	if maxPathLen < 10 {
		maxPathLen = 10
	}
	if len(path) > maxPathLen {
		path = "..." + path[len(path)-maxPathLen+3:]
	}

	line.WriteString(prefix)
	line.WriteString(statusStyle.Render(statusChar))
	line.WriteString(stageIndicator)
	line.WriteString(" ")
	line.WriteString(style.Render(path))

	totalAdds := f.StagedAdditions + f.UnstagedAdditions
	totalDels := f.StagedDeletions + f.UnstagedDeletions
	if totalAdds > 0 {
		line.WriteString(" ")
		line.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf("+%d", totalAdds)))
	}
	if totalDels > 0 {
		line.WriteString(" ")
		line.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf("-%d", totalDels)))
	}

	return line.String()
}

func (v *ProjectDiffView) renderDetailPanel(width int) string {
	th := theme.GetTheme()
	var s strings.Builder

	s.WriteString(th.DashboardTitle.Render(" Details "))
	s.WriteString("\n")

	dividerLen := width - 2
	if dividerLen < 0 {
		dividerLen = 0
	}
	s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %s ", strings.Repeat("─", dividerLen))))
	s.WriteString("\n")

	if len(v.items) == 0 || v.selectedIdx >= len(v.items) {
		s.WriteString(th.Help.Render(" Select a file to view details"))
		return s.String()
	}

	item := v.items[v.selectedIdx]

	if item.IsRepoHeader {
		s.WriteString(th.StatsStyle.Render(" Repo: "))
		s.WriteString(th.BranchStyle.Render(item.RepoName))
		s.WriteString("\n")
		s.WriteString(th.StatsStyle.Render(" Path: "))
		s.WriteString(th.MutedTextStyle.Render(item.RepoPath))
		s.WriteString("\n\n")
		if item.Error != "" {
			s.WriteString(th.DashboardErrorStyle.Render(" Error: " + item.Error))
			s.WriteString("\n")
		} else {
			s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %d files changed", item.TotalFiles)))
			if item.TotalAdds > 0 {
				s.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf("  +%d", item.TotalAdds)))
			}
			if item.TotalDels > 0 {
				s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf("  -%d", item.TotalDels)))
			}
			s.WriteString("\n")
		}
		return s.String()
	}

	f := item.WorkdirFile
	if f == nil {
		return s.String()
	}

	s.WriteString(th.StatsStyle.Render(" Repo: "))
	s.WriteString(th.BranchStyle.Render(item.RepoName))
	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(" Path: "))
	s.WriteString(th.BranchStyle.Render(f.Path))
	s.WriteString("\n")

	// Staged status
	if f.StagedStatus != "" && f.StagedStatus != " " && f.StagedStatus != "?" {
		s.WriteString(th.StatsStyle.Render(" Staged: "))
		s.WriteString(th.DashboardAccentStyle.Render(expandStatus(f.StagedStatus)))
		if f.StagedAdditions > 0 || f.StagedDeletions > 0 {
			s.WriteString(th.Help.Render(fmt.Sprintf(" (+%d/-%d)", f.StagedAdditions, f.StagedDeletions)))
		}
		s.WriteString("\n")
	}

	// Unstaged status
	if f.UnstagedStatus != "" && f.UnstagedStatus != " " && f.UnstagedStatus != "?" {
		s.WriteString(th.StatsStyle.Render(" Unstaged: "))
		s.WriteString(th.WarningStyle.Render(expandStatus(f.UnstagedStatus)))
		if f.UnstagedAdditions > 0 || f.UnstagedDeletions > 0 {
			s.WriteString(th.Help.Render(fmt.Sprintf(" (+%d/-%d)", f.UnstagedAdditions, f.UnstagedDeletions)))
		}
		s.WriteString("\n")
	}

	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %s ", strings.Repeat("─", dividerLen-2))))
	s.WriteString("\n")

	if v.showDiff && len(v.parsedDiffLines) > 0 {
		s.WriteString(v.renderDiffWithGutter(width))
	} else if v.showDiff && v.currentDiff == "" {
		s.WriteString(th.Help.Render(" No diff content available"))
		s.WriteString("\n")
	} else if !v.showDiff {
		s.WriteString(th.Help.Render(" Loading diff..."))
		s.WriteString("\n")
	}

	return s.String()
}

func (v *ProjectDiffView) renderDiffWithGutter(width int) string {
	th := theme.GetTheme()
	var s strings.Builder

	gutterWidth := 6
	diffWidth := width - gutterWidth - 1
	if diffWidth < 10 {
		diffWidth = 10
	}

	headerLines := 12
	footerLines := 2
	visibleLines := v.height - headerLines - footerLines
	if visibleLines < 5 {
		visibleLines = 5
	}

	startIdx := v.diffScrollOffset
	endIdx := startIdx + visibleLines
	if endIdx > len(v.parsedDiffLines) {
		endIdx = len(v.parsedDiffLines)
		startIdx = endIdx - visibleLines
		if startIdx < 0 {
			startIdx = 0
		}
	}

	for i := startIdx; i < endIdx; i++ {
		line := v.parsedDiffLines[i]
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

	totalLines := len(v.parsedDiffLines)
	if totalLines > visibleLines {
		scrollInfo := fmt.Sprintf(" %d-%d of %d  [PgUp/PgDn scroll, g/G top/bottom]", startIdx+1, endIdx, totalLines)
		s.WriteString(th.Help.Render(scrollInfo))
		s.WriteString("\n")
	}

	return s.String()
}

// expandStatus returns a human-readable status string.
func expandStatus(s string) string {
	switch s {
	case "A":
		return "Added"
	case "M":
		return "Modified"
	case "D":
		return "Deleted"
	case "R":
		return "Renamed"
	case "C":
		return "Copied"
	default:
		return s
	}
}

// ShortHelp returns a short help string.
func (v *ProjectDiffView) ShortHelp() string {
	name := ""
	if v.proj != nil {
		name = v.proj.Name
	}
	return fmt.Sprintf("Project: %s  j/k: Navigate  PgUp/PgDn: Scroll diff  r: Refresh  Esc: Back", name)
}

// SetSize updates the view dimensions.
func (v *ProjectDiffView) SetSize(width, height int) {
	v.width = width
	v.height = height
}

// CapturesKey returns true for keys this view handles directly.
func (v *ProjectDiffView) CapturesKey(key string) bool {
	switch key {
	case "j", "k", "up", "down", "pgup", "pgdown", "ctrl+u", "ctrl+d", "g", "G", "r", "esc":
		return true
	}
	return false
}

// KeyBindings returns the keybindings for this view.
func (v *ProjectDiffView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "↑/k", Description: "Navigate files"},
		{Key: "↓/j", Description: "Navigate files"},
		{Key: "PgUp/Ctrl+U", Description: "Scroll diff up"},
		{Key: "PgDn/Ctrl+D", Description: "Scroll diff down"},
		{Key: "g/G", Description: "Diff top/bottom"},
		{Key: "r", Description: "Refresh"},
		{Key: "Esc", Description: "Back to sync"},
	}
}
