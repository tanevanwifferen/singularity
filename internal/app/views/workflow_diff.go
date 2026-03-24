package views

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"singularity/internal/app/components"
	"singularity/internal/git"
	"singularity/internal/project"
	"singularity/internal/theme"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// workflowDiffDoneMsg carries per-repo diff results after concurrent fetching.
type workflowDiffDoneMsg struct {
	results map[string]*repoDiffResult
}

// repoDiffResult holds the diff result for a single repo in a workflow.
type repoDiffResult struct {
	RepoName      string
	WorktreePath  string
	DefaultBranch string // resolved default branch ref (e.g. "main", "origin/main")
	MergeBase     string // merge-base SHA used for diff computation
	Diff          *git.BranchDiff
	Err           error
	// DeepFileStatus maps file path → true if ALL hunks are already in the base branch
	DeepFileStatus map[string]bool
}

// diffItem is a flattened entry in the navigation list — either a repo header or a file.
type diffItem struct {
	IsRepoHeader  bool
	RepoName      string
	File          *git.FileChange
	WorktreePath  string
	DefaultBranch string // resolved default branch ref
	MergeBase     string // merge-base SHA used for diff computation
	// For repo headers: aggregate stats
	TotalFiles     int
	TotalAdditions int
	TotalDeletions int
	Error          string
	// AlreadyInBase: true if all hunks for this file are already in the base branch
	AlreadyInBase bool
}

// hunkStats tracks how many diff hunks are genuinely new vs already in base.
type hunkStats struct {
	Total         int
	AlreadyInBase int
}

// WorkflowDiffView shows all git changes for a workflow, grouped by repo.
type WorkflowDiffView struct {
	workflow *project.FeatureWorkflow
	width    int
	height   int
	loading  bool
	err      error

	// Per-repo diff results
	repoOrder []string
	repoDiffs map[string]*repoDiffResult

	// Flattened navigation list
	items       []diffItem
	selectedIdx int

	// File diff content (shown automatically when navigating)
	showDiff         bool
	currentDiff      string
	parsedDiffLines  []DiffLine
	diffScrollOffset int
	diffHunkStats    hunkStats
}

// NewWorkflowDiffView creates a new workflow diff view.
func NewWorkflowDiffView() *WorkflowDiffView {
	return &WorkflowDiffView{
		width:  120,
		height: 30,
	}
}

// SetWorkflow sets the workflow to diff and resets view state.
func (v *WorkflowDiffView) SetWorkflow(wf *project.FeatureWorkflow) {
	v.workflow = wf
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

// Init fetches diffs for all repos concurrently.
func (v *WorkflowDiffView) Init() tea.Cmd {
	if v.workflow == nil {
		return nil
	}
	v.loading = true
	wf := v.workflow

	return func() tea.Msg {
		results := make(map[string]*repoDiffResult)
		var mu sync.Mutex
		var wg sync.WaitGroup

		for name, wr := range wf.Repos {
			if !wr.WorktreeCreated {
				continue
			}
			wg.Add(1)
			go func(name string, wr *project.WorkflowRepo) {
				defer wg.Done()
				defaultBranch := wr.DefaultBranch
				if defaultBranch == "" {
					defaultBranch = "main"
				}
				// Resolve to an existing ref (handles origin/main fallback)
				defaultBranch = git.ResolveRef(wr.WorktreePath, defaultBranch)

				// Use merge base so the diff matches what an MR would show
				// (changes introduced by this branch, not diff between branch tips)
				mergeBase := defaultBranch
				if mb, err := git.GetMergeBase(wr.WorktreePath, defaultBranch, "HEAD"); err == nil {
					mergeBase = mb
				}

				diff, err := git.GetBranchDiff(wr.WorktreePath, mergeBase, "HEAD")

				// Deep check: for each changed file, test whether all its hunks
				// are already incorporated in the default branch (squash-merge detection).
				// Compares against the current default branch tip, not the merge-base.
				var fileStatus map[string]bool
				if err == nil && diff != nil && len(diff.Files) > 0 {
					fileStatus = make(map[string]bool, len(diff.Files))
					for _, f := range diff.Files {
						hunks, _, ferr := git.GetDeepFileDiff(wr.WorktreePath, mergeBase, "HEAD", defaultBranch, f.NewPath)
						if ferr != nil {
							continue
						}
						allInBase := len(hunks) > 0
						for _, h := range hunks {
							if !h.AlreadyInBase {
								allInBase = false
								break
							}
						}
						fileStatus[f.NewPath] = allInBase
					}
				}

				mu.Lock()
				results[name] = &repoDiffResult{
					RepoName:       name,
					WorktreePath:   wr.WorktreePath,
					DefaultBranch:  defaultBranch,
					MergeBase:      mergeBase,
					Diff:           diff,
					Err:            err,
					DeepFileStatus: fileStatus,
				}
				mu.Unlock()
			}(name, wr)
		}
		wg.Wait()

		return workflowDiffDoneMsg{results: results}
	}
}

// buildItems flattens repo diffs into a navigable item list.
func (v *WorkflowDiffView) buildItems() {
	v.items = nil
	for _, name := range v.repoOrder {
		rd := v.repoDiffs[name]

		header := diffItem{
			IsRepoHeader:  true,
			RepoName:      name,
			WorktreePath:  rd.WorktreePath,
			DefaultBranch: rd.DefaultBranch,
			MergeBase:     rd.MergeBase,
		}

		if rd.Err != nil {
			header.Error = rd.Err.Error()
			v.items = append(v.items, header)
			continue
		}

		if rd.Diff != nil {
			header.TotalFiles = rd.Diff.FilesChanged
			header.TotalAdditions = rd.Diff.TotalAdditions
			header.TotalDeletions = rd.Diff.TotalDeletions
		}
		v.items = append(v.items, header)

		if rd.Diff != nil {
			for i := range rd.Diff.Files {
				item := diffItem{
					RepoName:      name,
					File:          &rd.Diff.Files[i],
					WorktreePath:  rd.WorktreePath,
					DefaultBranch: rd.DefaultBranch,
					MergeBase:     rd.MergeBase,
				}
				if rd.DeepFileStatus != nil {
					item.AlreadyInBase = rd.DeepFileStatus[rd.Diff.Files[i].NewPath]
				}
				v.items = append(v.items, item)
			}
		}
	}
}

// Update handles messages.
func (v *WorkflowDiffView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case workflowDiffDoneMsg:
		v.loading = false
		v.repoDiffs = msg.results
		v.repoOrder = make([]string, 0, len(msg.results))
		for name := range msg.results {
			v.repoOrder = append(v.repoOrder, name)
		}
		sort.Strings(v.repoOrder)
		v.buildItems()
		v.selectedIdx = 0
		// Skip to first non-header if possible
		for i, item := range v.items {
			if !item.IsRepoHeader {
				v.selectedIdx = i
				break
			}
		}
		// Auto-show diff for the initially selected file
		v.loadSelectedFileDiff()

	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			v.moveCursor(-1)
		case "down", "j":
			v.moveCursor(1)
		case "g":
			if v.showDiff {
				v.diffScrollOffset = 0
			}
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
			if v.showDiff {
				v.closeDiff()
			} else {
				return v, func() tea.Msg {
					return ViewChangeMsg{ViewName: "Workflows"}
				}
			}
		}

	case tea.WindowSizeMsg:
		v.width = msg.Width
		v.height = msg.Height
	}

	return v, nil
}

// moveCursor moves the selection by delta and auto-loads the diff for the new file.
func (v *WorkflowDiffView) moveCursor(delta int) {
	if len(v.items) == 0 {
		return
	}

	newIdx := v.selectedIdx + delta
	// Allow landing on repo headers for visual context
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

	// Auto-load diff for files; clear it for repo headers
	if !v.items[v.selectedIdx].IsRepoHeader {
		v.loadSelectedFileDiff()
	} else {
		v.closeDiff()
	}
}

// closeDiff resets diff panel state.
func (v *WorkflowDiffView) closeDiff() {
	v.showDiff = false
	v.currentDiff = ""
	v.parsedDiffLines = nil
	v.diffScrollOffset = 0
	v.diffHunkStats = hunkStats{}
}

// loadSelectedFileDiff loads the diff for the currently selected file.
func (v *WorkflowDiffView) loadSelectedFileDiff() {
	if v.selectedIdx >= len(v.items) {
		return
	}
	item := v.items[v.selectedIdx]
	if item.IsRepoHeader || item.File == nil {
		return
	}

	hunks, rawDiff, err := git.GetDeepFileDiff(item.WorktreePath, item.MergeBase, "HEAD", item.DefaultBranch, item.File.NewPath)
	if err != nil {
		v.err = err
		return
	}
	v.currentDiff = rawDiff
	v.parsedDiffLines = parseDeepDiffLines(rawDiff, hunks)
	v.diffHunkStats = computeHunkStats(hunks)
	v.showDiff = true
	v.diffScrollOffset = 0
}

// parseDeepDiffLines parses raw diff output and annotates lines with AlreadyInBase
// based on the filtered hunk results from GetDeepFileDiff.
func parseDeepDiffLines(rawDiff string, filteredHunks []git.FilteredDiffHunk) []DiffLine {
	lines := ParseDiffLines(rawDiff)
	if len(filteredHunks) == 0 {
		return lines
	}
	hunkIdx := -1
	for i := range lines {
		if lines[i].LineType == "@" {
			hunkIdx++
		}
		if hunkIdx >= 0 && hunkIdx < len(filteredHunks) {
			lines[i].AlreadyInBase = filteredHunks[hunkIdx].AlreadyInBase
		}
	}
	return lines
}

// computeHunkStats tallies how many hunks are new vs already in the base branch.
func computeHunkStats(hunks []git.FilteredDiffHunk) hunkStats {
	stats := hunkStats{Total: len(hunks)}
	for _, h := range hunks {
		if h.AlreadyInBase {
			stats.AlreadyInBase++
		}
	}
	return stats
}

// View renders the workflow diff view.
func (v *WorkflowDiffView) View() string {
	th := theme.GetTheme()
	var s strings.Builder

	// Header
	branchName := ""
	if v.workflow != nil {
		branchName = v.workflow.BranchName
	}
	s.WriteString(th.DashboardTitle.Render(" Workflow Changes "))
	if branchName != "" {
		s.WriteString(th.MutedTextStyle.Render(fmt.Sprintf("  %s", branchName)))
	}
	s.WriteString("\n\n")

	if v.loading {
		s.WriteString(th.MutedTextStyle.Render(" Loading diffs across all repos..."))
		s.WriteString("\n")
		return s.String()
	}

	if v.err != nil {
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" Error: %v", v.err)))
		s.WriteString("\n")
	}

	if len(v.items) == 0 {
		s.WriteString(th.MutedTextStyle.Render(" No changes found in any repo."))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render(" Esc: Back to workflows"))
		return s.String()
	}

	// Split panel layout
	leftWidth := 55
	rightWidth := v.width - leftWidth - 3
	if rightWidth < 30 {
		rightWidth = 30
	}

	leftPanel := v.renderItemList(leftWidth)
	rightPanel := v.renderDetailPanel(rightWidth)

	s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel))
	s.WriteString("\n")

	// Footer
	helpText := " j/k: Navigate files  PgUp/PgDn: Scroll diff  g/G: Top/bottom  Esc: Back  r: Refresh"
	s.WriteString(th.Help.Render(helpText))

	return s.String()
}

// renderItemList renders the left panel with repo-grouped file list.
func (v *WorkflowDiffView) renderItemList(width int) string {
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
	totalFiles := 0
	totalAdd := 0
	totalDel := 0
	for _, name := range v.repoOrder {
		rd := v.repoDiffs[name]
		if rd.Diff != nil {
			totalFiles += rd.Diff.FilesChanged
			totalAdd += rd.Diff.TotalAdditions
			totalDel += rd.Diff.TotalDeletions
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

	// Calculate visible window
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
		scrollInfo := fmt.Sprintf(" %d-%d of %d ", startIdx+1, endIdx, len(v.items))
		s.WriteString(th.Help.Render(scrollInfo))
	}

	return s.String()
}

// renderRepoHeader renders a repo section header.
func (v *WorkflowDiffView) renderRepoHeader(item diffItem, selected bool, width int) string {
	th := theme.GetTheme()
	var line strings.Builder

	if selected {
		line.WriteString(th.DashboardAccentStyle.Render(" ► "))
	} else {
		line.WriteString("   ")
	}

	// Repo name in bold/accent style
	nameStyle := th.DashboardTitle
	if selected {
		nameStyle = th.SelectedBranchStyle
	}
	line.WriteString(nameStyle.Render(item.RepoName))

	if item.Error != "" {
		line.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf("  error: %s", truncate(item.Error, 30))))
	} else if item.TotalFiles == 0 {
		line.WriteString(th.MutedTextStyle.Render("  no changes"))
	} else {
		line.WriteString(th.MutedTextStyle.Render(fmt.Sprintf("  %d files", item.TotalFiles)))
		if item.TotalAdditions > 0 {
			line.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf(" +%d", item.TotalAdditions)))
		}
		if item.TotalDeletions > 0 {
			line.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" -%d", item.TotalDeletions)))
		}
	}

	return line.String()
}

// renderFileEntry renders a file entry within a repo section.
func (v *WorkflowDiffView) renderFileEntry(item diffItem, selected bool, width int) string {
	th := theme.GetTheme()
	var line strings.Builder

	prefix := "     "
	style := th.BranchStyle
	if selected {
		prefix = "   > "
		style = th.SelectedBranchStyle
	}

	// Status indicator
	statusChar := " "
	statusStyle := th.StatsStyle
	if item.File != nil {
		switch item.File.Status {
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
	}

	// Truncate path
	path := ""
	if item.File != nil {
		path = item.File.NewPath
	}
	maxPathLen := width - 25
	if maxPathLen < 10 {
		maxPathLen = 10
	}
	if len(path) > maxPathLen {
		path = "..." + path[len(path)-maxPathLen+3:]
	}

	line.WriteString(prefix)
	line.WriteString(statusStyle.Render(statusChar))
	line.WriteString(" ")
	line.WriteString(style.Render(path))

	if item.File != nil {
		if item.AlreadyInBase {
			line.WriteString(th.MutedTextStyle.Render(" ✓merged"))
		} else {
			if item.File.Additions > 0 {
				line.WriteString(" ")
				line.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf("+%d", item.File.Additions)))
			}
			if item.File.Deletions > 0 {
				line.WriteString(" ")
				line.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf("-%d", item.File.Deletions)))
			}
		}
	}

	return line.String()
}

// renderDetailPanel renders the right panel with file details and diff content.
func (v *WorkflowDiffView) renderDetailPanel(width int) string {
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
		// Show repo summary
		s.WriteString(th.StatsStyle.Render(" Repo: "))
		s.WriteString(th.BranchStyle.Render(item.RepoName))
		s.WriteString("\n")
		s.WriteString(th.StatsStyle.Render(" Path: "))
		s.WriteString(th.MutedTextStyle.Render(item.WorktreePath))
		s.WriteString("\n")
		s.WriteString(th.StatsStyle.Render(" Base: "))
		s.WriteString(th.MutedTextStyle.Render(item.DefaultBranch))
		s.WriteString("\n\n")
		if item.Error != "" {
			s.WriteString(th.DashboardErrorStyle.Render(" Error: " + item.Error))
			s.WriteString("\n")
		} else {
			s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %d files changed", item.TotalFiles)))
			if item.TotalAdditions > 0 {
				s.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf("  +%d", item.TotalAdditions)))
			}
			if item.TotalDeletions > 0 {
				s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf("  -%d", item.TotalDeletions)))
			}
			s.WriteString("\n")
		}
		return s.String()
	}

	// File details
	if item.File != nil {
		s.WriteString(th.StatsStyle.Render(" Repo: "))
		s.WriteString(th.BranchStyle.Render(item.RepoName))
		s.WriteString("\n")
		s.WriteString(th.StatsStyle.Render(" Path: "))
		s.WriteString(th.BranchStyle.Render(item.File.NewPath))
		s.WriteString("\n")

		statusStr := item.File.Status
		statusStyle := th.StatsStyle
		switch item.File.Status {
		case "A":
			statusStr = "Added"
			statusStyle = th.DashboardAccentStyle
		case "M":
			statusStr = "Modified"
			statusStyle = th.WarningStyle
		case "D":
			statusStr = "Deleted"
			statusStyle = th.DashboardErrorStyle
		case "R":
			statusStr = "Renamed"
			statusStyle = th.InfoStyle
		}
		s.WriteString(th.StatsStyle.Render(" Status: "))
		s.WriteString(statusStyle.Render(statusStr))
		s.WriteString("\n")

		s.WriteString(th.StatsStyle.Render(" Changes: "))
		if item.File.Additions > 0 {
			s.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf("+%d", item.File.Additions)))
			s.WriteString(th.StatsStyle.Render(" / "))
		}
		if item.File.Deletions > 0 {
			s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf("-%d", item.File.Deletions)))
		}
		if item.File.Additions == 0 && item.File.Deletions == 0 {
			s.WriteString(th.Help.Render("No line changes"))
		}
		s.WriteString("\n")
	}

	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %s ", strings.Repeat("─", dividerLen-2))))
	s.WriteString("\n")

	if v.showDiff && v.diffHunkStats.Total > 0 {
		if v.diffHunkStats.AlreadyInBase == v.diffHunkStats.Total {
			s.WriteString(th.MutedTextStyle.Render(fmt.Sprintf(
				" ✓ All %d hunk(s) already incorporated in base branch",
				v.diffHunkStats.Total,
			)))
		} else if v.diffHunkStats.AlreadyInBase > 0 {
			s.WriteString(th.MutedTextStyle.Render(fmt.Sprintf(
				" ✓ %d/%d hunk(s) already in base  |  %d genuinely new",
				v.diffHunkStats.AlreadyInBase, v.diffHunkStats.Total,
				v.diffHunkStats.Total-v.diffHunkStats.AlreadyInBase,
			)))
		}
		if v.diffHunkStats.AlreadyInBase > 0 {
			s.WriteString("\n")
		}
	}
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

// renderDiffWithGutter renders diff content with line numbers in a gutter.
func (v *WorkflowDiffView) renderDiffWithGutter(width int) string {
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

		// Dim lines that belong to hunks already incorporated in the base branch
		if line.AlreadyInBase {
			lineStyle = th.MutedTextStyle
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

// ShortHelp returns a short help string.
func (v *WorkflowDiffView) ShortHelp() string {
	branch := ""
	if v.workflow != nil {
		branch = v.workflow.BranchName
	}
	return fmt.Sprintf("Workflow: %s  j/k: Navigate  Enter: Diff  Esc: Back", branch)
}

// SetSize updates the view dimensions.
func (v *WorkflowDiffView) SetSize(width, height int) {
	v.width = width
	v.height = height
}

// CapturesKey returns true for keys this view handles directly.
func (v *WorkflowDiffView) CapturesKey(key string) bool {
	switch key {
	case "j", "k", "up", "down", "pgup", "pgdown", "ctrl+u", "ctrl+d", "g", "G", "r", "esc":
		return true
	}
	return false
}

// KeyBindings returns the keybindings for this view.
func (v *WorkflowDiffView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "↑/k", Description: "Navigate files"},
		{Key: "↓/j", Description: "Navigate files"},
		{Key: "PgUp/Ctrl+U", Description: "Scroll diff up"},
		{Key: "PgDn/Ctrl+D", Description: "Scroll diff down"},
		{Key: "g/G", Description: "Diff top/bottom"},
		{Key: "r", Description: "Refresh"},
		{Key: "Esc", Description: "Back to workflows"},
	}
}
