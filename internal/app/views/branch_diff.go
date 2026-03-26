package views

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// branchDiffDoneMsg carries per-repo diff results after concurrent fetching.
type branchDiffDoneMsg struct {
	results map[string]*repoDiffResult
}

// BranchDiffRepoEntry describes one repo to include in a branch diff.
type BranchDiffRepoEntry struct {
	Name          string
	Path          string
	DefaultBranch string
}

// BranchDiffView shows all git changes for a branch vs its default branch,
// grouped by repo. It mirrors WorkflowDiffView but operates on a named branch
// instead of a workflow worktree.
type BranchDiffView struct {
	viewBase
	diffNavHelper
	branchName string
	repos      []BranchDiffRepoEntry
	loading    bool
	err        error

	repoOrder []string
	repoDiffs map[string]*repoDiffResult

	items         []diffItem
	diffHunkStats hunkStats
}

// NewBranchDiffView creates a new branch diff view.
func NewBranchDiffView() *BranchDiffView {
	return &BranchDiffView{
		viewBase: viewBase{width: 120, height: 30},
	}
}

// SetBranch configures the branch to diff and resets view state.
func (v *BranchDiffView) SetBranch(branchName string, repos []BranchDiffRepoEntry) {
	v.branchName = branchName
	v.repos = repos
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
	v.diffHunkStats = hunkStats{}
}

// Init fetches diffs for all repos concurrently.
func (v *BranchDiffView) Init() tea.Cmd {
	if v.branchName == "" || len(v.repos) == 0 {
		return nil
	}
	v.loading = true
	repos := v.repos
	branch := v.branchName

	return func() tea.Msg {
		results := make(map[string]*repoDiffResult)
		var mu sync.Mutex
		var wg sync.WaitGroup

		for _, r := range repos {
			wg.Add(1)
			go func(r BranchDiffRepoEntry) {
				defer wg.Done()

				defaultBranch := r.DefaultBranch
				if defaultBranch == "" {
					defaultBranch = "main"
				}
				defaultBranch = git.ResolveRef(r.Path, defaultBranch)

				// Use merge base so the diff matches what an MR would show.
				mergeBase := defaultBranch
				if mb, err := git.GetMergeBase(r.Path, defaultBranch, branch); err == nil {
					mergeBase = mb
				}

				diff, err := git.GetBranchDiff(r.Path, mergeBase, branch)

				// Deep check: detect hunks already incorporated in the base branch.
				var fileStatus map[string]bool
				if err == nil && diff != nil && len(diff.Files) > 0 {
					fileStatus = make(map[string]bool, len(diff.Files))
					for _, f := range diff.Files {
						hunks, _, ferr := git.GetDeepFileDiff(r.Path, mergeBase, branch, defaultBranch, f.NewPath)
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
				results[r.Name] = &repoDiffResult{
					RepoName:       r.Name,
					WorktreePath:   r.Path,
					DefaultBranch:  defaultBranch,
					MergeBase:      mergeBase,
					Diff:           diff,
					Err:            err,
					DeepFileStatus: fileStatus,
				}
				mu.Unlock()
			}(r)
		}
		wg.Wait()
		return branchDiffDoneMsg{results: results}
	}
}

// buildItems flattens repo diffs into a navigable item list.
func (v *BranchDiffView) buildItems() {
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
func (v *BranchDiffView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case branchDiffDoneMsg:
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
					return ViewChangeMsg{ViewName: "Branches"}
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
func (v *BranchDiffView) moveCursor(delta int) {
	newIdx := clampIndex(v.selectedIdx+delta, len(v.items))
	if newIdx < 0 || newIdx == v.selectedIdx {
		return
	}
	v.selectedIdx = newIdx
	v.diffScrollOffset = 0

	if !v.items[v.selectedIdx].IsRepoHeader {
		v.loadSelectedFileDiff()
	} else {
		v.closeDiff()
	}
}

// closeDiff resets diff panel state.
func (v *BranchDiffView) closeDiff() {
	v.diffNavHelper.closeDiff()
	v.diffHunkStats = hunkStats{}
}

// loadSelectedFileDiff loads the diff for the currently selected file.
func (v *BranchDiffView) loadSelectedFileDiff() {
	if v.selectedIdx >= len(v.items) {
		return
	}
	item := v.items[v.selectedIdx]
	if item.IsRepoHeader || item.File == nil {
		return
	}

	hunks, rawDiff, err := git.GetDeepFileDiff(item.WorktreePath, item.MergeBase, v.branchName, item.DefaultBranch, item.File.NewPath)
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

// View renders the branch diff view.
func (v *BranchDiffView) View() string {
	th := theme.GetTheme()
	var s strings.Builder

	s.WriteString(th.DashboardTitle.Render(" Branch Changes "))
	if v.branchName != "" {
		s.WriteString(th.MutedTextStyle.Render(fmt.Sprintf("  %s", v.branchName)))
	}
	s.WriteString("\n\n")

	if v.loading {
		s.WriteString(th.MutedTextStyle.Render(" Loading diffs..."))
		s.WriteString("\n")
		return s.String()
	}

	if v.err != nil {
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" Error: %v", v.err)))
		s.WriteString("\n")
	}

	if len(v.items) == 0 {
		s.WriteString(th.MutedTextStyle.Render(" No changes found."))
		s.WriteString("\n\n")
		s.WriteString(th.Help.Render(" Esc: Back to branches"))
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

	s.WriteString(th.Help.Render(" j/k: Navigate files  PgUp/PgDn: Scroll diff  g/G: Top/bottom  Esc: Back  r: Refresh"))

	return s.String()
}

// renderItemList renders the left panel with repo-grouped file list.
func (v *BranchDiffView) renderItemList(width int) string {
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

	startIdx, endIdx := calcViewport(v.height, 10, v.selectedIdx, len(v.items))

	for i := startIdx; i < endIdx && i < len(v.items); i++ {
		item := v.items[i]
		selected := i == v.selectedIdx

		if item.IsRepoHeader {
			s.WriteString(renderBranchDiffRepoHeader(item, selected, th))
		} else {
			s.WriteString(renderBranchDiffFileEntry(item, selected, width, th))
		}
		s.WriteString("\n")
	}

	if endIdx-startIdx < len(v.items) {
		scrollInfo := fmt.Sprintf(" %d-%d of %d ", startIdx+1, endIdx, len(v.items))
		s.WriteString(th.Help.Render(scrollInfo))
	}

	return s.String()
}

func renderBranchDiffRepoHeader(item diffItem, selected bool, th theme.Theme) string {
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

func renderBranchDiffFileEntry(item diffItem, selected bool, width int, th theme.Theme) string {
	var line strings.Builder

	prefix := "     "
	style := th.BranchStyle
	if selected {
		prefix = "   > "
		style = th.SelectedBranchStyle
	}

	statusChar := " "
	statusStyle := th.StatsStyle
	if item.File != nil {
		statusChar, statusStyle = fileStatusIndicator(item.File.Status, th)
	}

	path := ""
	if item.File != nil {
		path = item.File.NewPath
	}
	path = truncatePath(path, width-25)

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
func (v *BranchDiffView) renderDetailPanel(width int) string {
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

func (v *BranchDiffView) renderDiffWithGutter(width int) string {
	return renderDiffWithGutter(v.parsedDiffLines, v.diffScrollOffset, width, v.height, 12, 2, true, "[PgUp/PgDn scroll, g/G top/bottom]")
}

// ShortHelp returns a short help string.
func (v *BranchDiffView) ShortHelp() string {
	return fmt.Sprintf("Branch: %s  j/k: Navigate  Esc: Back", v.branchName)
}

// CapturesKey returns true for keys this view handles directly.
func (v *BranchDiffView) CapturesKey(key string) bool {
	switch key {
	case "j", "k", "up", "down", "pgup", "pgdown", "ctrl+u", "ctrl+d", "g", "G", "r", "esc":
		return true
	}
	return false
}

// KeyBindings returns the keybindings for this view.
func (v *BranchDiffView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "↑/k", Description: "Navigate files"},
		{Key: "↓/j", Description: "Navigate files"},
		{Key: "PgUp/Ctrl+U", Description: "Scroll diff up"},
		{Key: "PgDn/Ctrl+D", Description: "Scroll diff down"},
		{Key: "g/G", Description: "Diff top/bottom"},
		{Key: "r", Description: "Refresh"},
		{Key: "Esc", Description: "Back to branches"},
	}
}
