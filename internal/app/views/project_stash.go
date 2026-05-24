package views

import (
	"fmt"
	"sort"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// projectStashDoneMsg signals that a stash operation has completed across all repos.
type projectStashDoneMsg struct {
	op      string // "stash", "apply", "pop"
	results []service.RepoStashResult
}

// projectStashLoadedMsg carries refreshed stash data from all repos.
type projectStashLoadedMsg struct {
	lists []service.RepoStashList
}

// ProjectStashView handles stash operations across all repos in a project.
// It shows a deduplicated list of stash names and lets the user stash all repos
// or apply/pop all stashes with a selected name.
type ProjectStashView struct {
	viewBase
	proj *service.Project

	// Stash data
	repoStashLists []service.RepoStashList
	stashNames     []string // unique stash names across all repos, sorted
	selectedIdx    int
	loading        bool

	// Operation log
	syncLogHelper
	executing bool

	// New stash input
	showNewStash      bool
	newStashName      string
	newStashUntracked bool

	// Confirmation dialogs
	showApplyConfirm bool
	showPopConfirm   bool
}

// NewProjectStashView creates a new project stash view.
func NewProjectStashView(proj *service.Project) *ProjectStashView {
	return &ProjectStashView{
		viewBase:      viewBase{width: 80, height: 24},
		proj:          proj,
		syncLogHelper: syncLogHelper{outputLog: make([]logEntry, 0)},
	}
}

// Init loads stash data from all repos.
func (v *ProjectStashView) Init() tea.Cmd {
	v.loading = true
	return v.loadCmd()
}

func (v *ProjectStashView) loadCmd() tea.Cmd {
	return func() tea.Msg {
		if v.proj == nil {
			return projectStashLoadedMsg{}
		}
		lists := service.ListAllStashes(v.proj)
		return projectStashLoadedMsg{lists: lists}
	}
}

func (v *ProjectStashView) processLoadedLists(lists []service.RepoStashList) {
	v.repoStashLists = lists

	seen := make(map[string]bool)
	var names []string
	for _, rl := range lists {
		for _, e := range rl.Entries {
			if !seen[e.Message] {
				seen[e.Message] = true
				names = append(names, e.Message)
			}
		}
	}
	sort.Strings(names)
	v.stashNames = names

	if v.selectedIdx >= len(names) {
		v.selectedIdx = len(names) - 1
	}
	if v.selectedIdx < 0 {
		v.selectedIdx = 0
	}
	v.loading = false
}

func (v *ProjectStashView) selectedName() string {
	if len(v.stashNames) == 0 || v.selectedIdx < 0 || v.selectedIdx >= len(v.stashNames) {
		return ""
	}
	return v.stashNames[v.selectedIdx]
}

// Update handles input and messages.
func (v *ProjectStashView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if v.showNewStash {
			return v.handleNewStashInput(msg)
		}
		if v.showApplyConfirm {
			return v.handleApplyConfirm(msg)
		}
		if v.showPopConfirm {
			return v.handlePopConfirm(msg)
		}
		if v.executing {
			return v.handleScrollKeys(msg)
		}
		return v.handleKeys(msg)

	case projectStashLoadedMsg:
		v.processLoadedLists(msg.lists)

	case projectStashDoneMsg:
		v.executing = false
		for _, r := range msg.results {
			if r.Error != "" {
				v.addLog(SyncOpNone, "error", fmt.Sprintf("[%s] %s", r.RepoName, r.Error))
			} else if r.Skipped {
				v.addLog(SyncOpNone, "info", fmt.Sprintf("[%s] skipped (no matching stash)", r.RepoName))
			} else {
				v.addLog(SyncOpNone, "success", fmt.Sprintf("[%s] done", r.RepoName))
			}
		}
		return v, v.loadCmd()
	}

	return v, nil
}

func (v *ProjectStashView) handleKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if v.selectedIdx < len(v.stashNames)-1 {
			v.selectedIdx++
		}
	case "k", "up":
		if v.selectedIdx > 0 {
			v.selectedIdx--
		}
	case "s":
		v.showNewStash = true
		v.newStashName = ""
		v.newStashUntracked = false
	case "a":
		if v.selectedName() != "" {
			v.showApplyConfirm = true
		}
	case "p":
		if v.selectedName() != "" {
			v.showPopConfirm = true
		}
	case "r":
		v.loading = true
		return v, v.loadCmd()
	case "J":
		v.scrollDown()
	case "K":
		v.scrollUp()
	}
	return v, nil
}

func (v *ProjectStashView) handleNewStashInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyBackspace:
		if len(v.newStashName) > 0 {
			v.newStashName = v.newStashName[:len(v.newStashName)-1]
		}
	case tea.KeyEnter:
		if v.newStashName != "" {
			name := v.newStashName
			untracked := v.newStashUntracked
			v.showNewStash = false
			v.executing = true
			v.addLog(SyncOpNone, "info", fmt.Sprintf("Stashing all repos as %q...", name))
			return v, func() tea.Msg {
				results := service.StashAllRepos(v.proj, name, untracked)
				return projectStashDoneMsg{op: "stash", results: results}
			}
		}
		v.showNewStash = false
	case tea.KeyEsc:
		v.showNewStash = false
		v.newStashName = ""
	case tea.KeyRunes:
		v.newStashName += string(msg.Runes)
	default:
		if msg.String() == "ctrl+u" {
			v.newStashUntracked = !v.newStashUntracked
		}
	}
	return v, nil
}

func (v *ProjectStashView) handleApplyConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		name := v.selectedName()
		v.showApplyConfirm = false
		v.executing = true
		v.addLog(SyncOpNone, "info", fmt.Sprintf("Applying %q across all repos...", name))
		return v, func() tea.Msg {
			results := service.ApplyStashAllRepos(v.proj, name, false)
			return projectStashDoneMsg{op: "apply", results: results}
		}
	case "n", "esc":
		v.showApplyConfirm = false
	}
	return v, nil
}

func (v *ProjectStashView) handlePopConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		name := v.selectedName()
		v.showPopConfirm = false
		v.executing = true
		v.addLog(SyncOpNone, "info", fmt.Sprintf("Popping %q across all repos...", name))
		return v, func() tea.Msg {
			results := service.ApplyStashAllRepos(v.proj, name, true)
			return projectStashDoneMsg{op: "pop", results: results}
		}
	case "n", "esc":
		v.showPopConfirm = false
	}
	return v, nil
}

func (v *ProjectStashView) handleScrollKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "J", "down":
		v.scrollDown()
	case "K", "up":
		v.scrollUp()
	}
	return v, nil
}

// stashLogMaxLines returns how many log lines to show.
func (v *ProjectStashView) stashLogMaxLines() int {
	listHeight := len(v.stashNames) + 4
	available := v.height - listHeight - 12
	if available < 3 {
		available = 3
	}
	return available
}

// View renders the project stash view.
func (v *ProjectStashView) View() string {
	th := theme.GetTheme()
	var s strings.Builder

	title := "Project Stashes"
	if v.proj != nil {
		title = fmt.Sprintf("Project Stashes: %s", v.proj.Name)
	}
	s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" %s ", title)))
	s.WriteString("\n\n")

	if v.loading {
		s.WriteString(th.StatsStyle.Render(" Loading..."))
		s.WriteString("\n")
		return s.String()
	}

	v.renderStashList(&s, th)
	s.WriteString("\n")

	if name := v.selectedName(); name != "" {
		v.renderRepoCoverage(&s, th, name)
		s.WriteString("\n")
	}

	if v.showNewStash {
		untrackedLabel := ""
		if v.newStashUntracked {
			untrackedLabel = " [+untracked]"
		}
		s.WriteString(th.InfoStyle.Render(fmt.Sprintf(" Stash name: %s_%s ", v.newStashName, untrackedLabel)))
		s.WriteString("\n")
		s.WriteString(th.MutedTextStyle.Render(" Ctrl+U: toggle untracked  Enter: confirm  Esc: cancel"))
		s.WriteString("\n\n")
	}

	if v.showApplyConfirm {
		s.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf(" Apply %q to all repos? (y/n) ", v.selectedName())))
		s.WriteString("\n\n")
	}

	if v.showPopConfirm {
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" Pop (apply+drop) %q from all repos? (y/n) ", v.selectedName())))
		s.WriteString("\n\n")
	}

	if v.executing {
		s.WriteString(th.DashboardAccentStyle.Render(" ● Running..."))
		s.WriteString("\n\n")
	}

	v.renderLog(&s, th)

	s.WriteString("\n")
	v.renderKeybindings(&s, th)

	return s.String()
}

func (v *ProjectStashView) renderStashList(s *strings.Builder, th theme.Theme) {
	s.WriteString(th.StatsStyle.Render(" Stash Names (across all repos) "))
	s.WriteString("\n")
	s.WriteString(renderSeparator())

	if len(v.stashNames) == 0 {
		s.WriteString(th.MutedTextStyle.Render(" No stashes found in any repo"))
		s.WriteString("\n")
		return
	}

	maxNameWidth := v.width - 16
	if maxNameWidth < 20 {
		maxNameWidth = 20
	}

	for i, name := range v.stashNames {
		count := 0
		for _, rl := range v.repoStashLists {
			for _, e := range rl.Entries {
				if e.Message == name {
					count++
					break
				}
			}
		}

		cursor := "  "
		if i == v.selectedIdx {
			cursor = "> "
		}

		nameDisplay := name
		if len(nameDisplay) > maxNameWidth {
			nameDisplay = nameDisplay[:maxNameWidth-1] + "…"
		}

		repoLabel := "repo"
		if count != 1 {
			repoLabel = "repos"
		}
		line := fmt.Sprintf("%s%-*s  (%d %s)", cursor, maxNameWidth, nameDisplay, count, repoLabel)

		if i == v.selectedIdx {
			s.WriteString(th.DashboardAccentStyle.Render(line))
		} else {
			s.WriteString(line)
		}
		s.WriteString("\n")
	}
}

func (v *ProjectStashView) renderRepoCoverage(s *strings.Builder, th theme.Theme, name string) {
	var repos []string
	for _, rl := range v.repoStashLists {
		for _, e := range rl.Entries {
			if e.Message == name {
				repos = append(repos, rl.RepoName)
				break
			}
		}
	}
	s.WriteString(th.MutedTextStyle.Render(" Repos: "))
	if len(repos) == 0 {
		s.WriteString(th.MutedTextStyle.Render("none"))
	} else {
		s.WriteString(th.BranchStyle.Render(strings.Join(repos, ", ")))
	}
	s.WriteString("\n")
}

func (v *ProjectStashView) renderLog(s *strings.Builder, th theme.Theme) {
	v.syncLogHelper.renderSyncLog(s, th, v.stashLogMaxLines())
}

func (v *ProjectStashView) renderKeybindings(s *strings.Builder, th theme.Theme) {
	renderSyncKeybindings(s, th, v.KeyBindings())
}

func (v *ProjectStashView) addLog(op SyncOperation, kind, message string) {
	v.syncLogHelper.addLog(op, kind, message, v.stashLogMaxLines())
}

func (v *ProjectStashView) scrollDown() {
	v.syncLogHelper.scrollDown(v.stashLogMaxLines())
}

func (v *ProjectStashView) scrollUp() {
	v.syncLogHelper.scrollUp()
}

// CapturesInput returns true when the view has an active text input or confirmation.
func (v *ProjectStashView) CapturesInput() bool {
	return v.showNewStash || v.showApplyConfirm || v.showPopConfirm
}

// ShortHelp returns short help text for the status bar.
func (v *ProjectStashView) ShortHelp() string {
	return "s: Stash All  a: Apply  p: Pop  r: Refresh  j/k: Navigate"
}

// KeyBindings returns the keybindings for this view.
func (v *ProjectStashView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "s", Description: "Stash all repos"},
		{Key: "a", Description: "Apply selected stash name to all repos"},
		{Key: "p", Description: "Pop (apply+drop) selected stash name from all repos"},
		{Key: "j/k", Description: "Navigate stash names"},
		{Key: "r", Description: "Refresh"},
		{Key: "J/K", Description: "Scroll output log"},
	}
}
