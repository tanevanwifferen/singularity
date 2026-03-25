package views

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/project"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// projectSyncRepoResult carries the result of a sync operation for one repo.
type projectSyncRepoResult struct {
	repoName string
	op       SyncOperation
	output   string
	err      string
}

// projectSyncDoneMsg signals that a batch sync operation has completed across all repos.
type projectSyncDoneMsg struct {
	op      SyncOperation
	results []projectSyncRepoResult
}

// projectSyncStatusMsg carries refreshed status for all repos.
type projectSyncStatusMsg struct {
	statuses []projectSyncRepoStatus
}

type projectSyncRepoStatus struct {
	name      string
	path      string
	status    *git.UpstreamStatus
	lastFetch time.Time
}

// projectSyncStepMsg drives multi-step sync (fetch+rebase+push) across all repos.
type projectSyncStepMsg struct {
	step    int
	results []projectSyncRepoResult // results from previous step
}

// ProjectSyncView handles sync operations across all repos in a project.
type ProjectSyncView struct {
	viewBase
	proj *project.Project

	// Per-repo status
	repoStatuses []projectSyncRepoStatus
	loading      bool

	// Operation state
	executing bool
	currentOp SyncOperation

	// Output log
	syncLogHelper

	// Confirmation
	confirmOp      SyncOperation
	showConfirm    bool
	confirmMessage string
}

// syncMaxLines returns the maximum number of visible log lines for ProjectSyncView.
func (v *ProjectSyncView) syncMaxLines() int {
	statusLines := len(v.repoStatuses) + 3
	return v.height - statusLines - 22
}

// NewProjectSyncView creates a new project sync view.
func NewProjectSyncView(proj *project.Project) *ProjectSyncView {
	return &ProjectSyncView{
		viewBase:      viewBase{width: 80, height: 24},
		proj:          proj,
		syncLogHelper: syncLogHelper{outputLog: make([]logEntry, 0)},
	}
}

// Init initializes the project sync view.
func (v *ProjectSyncView) Init() tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		v.loadStatuses()
		return RefreshDoneMsg{}
	}
}

func (v *ProjectSyncView) loadStatuses() {
	if v.proj == nil {
		v.loading = false
		return
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	statuses := make([]projectSyncRepoStatus, len(v.proj.Repos))

	for i, repo := range v.proj.Repos {
		wg.Add(1)
		go func(idx int, r *project.Repo) {
			defer wg.Done()
			s := projectSyncRepoStatus{
				name: r.Name,
				path: r.Path,
			}
			if status, err := git.GetUpstreamStatus(r.Path); err == nil {
				s.status = status
			}
			if t, err := git.GetLastFetchTime(r.Path); err == nil {
				s.lastFetch = t
			}
			mu.Lock()
			statuses[idx] = s
			mu.Unlock()
		}(i, repo)
	}
	wg.Wait()

	v.repoStatuses = statuses
	v.loading = false
}

// Update handles input and messages.
func (v *ProjectSyncView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if v.showConfirm {
			return v.handleConfirm(msg)
		}
		if v.executing {
			return v.handleScrolling(msg)
		}
		return v.handleKeys(msg)

	case projectSyncDoneMsg:
		v.executing = false
		v.currentOp = SyncOpNone
		for _, r := range msg.results {
			if r.err != "" {
				v.addLog(r.op, "error", fmt.Sprintf("[%s] %s", r.repoName, r.err))
			} else {
				v.addLog(r.op, "success", fmt.Sprintf("[%s] %s", r.repoName, opDoneLabel(r.op)))
			}
			if r.output != "" {
				v.addLog(r.op, "output", fmt.Sprintf("[%s] %s", r.repoName, r.output))
			}
		}
		return v, v.refreshStatusCmd()

	case projectSyncStepMsg:
		return v.handleSyncStep(msg)

	case projectSyncStatusMsg:
		v.repoStatuses = msg.statuses

	case RefreshDoneMsg:
		v.loading = false
	}

	return v, nil
}

func (v *ProjectSyncView) handleKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "f":
		return v, v.startBatchOp(SyncOpFetch)
	case "p":
		return v, v.startBatchOp(SyncOpPull)
	case "P":
		return v, v.startBatchOp(SyncOpPush)
	case "r":
		return v, v.startBatchOp(SyncOpRebase)
	case "S":
		v.confirmOp = SyncOpSync
		v.showConfirm = true
		v.confirmMessage = "Sync all repos: fetch + rebase + push? (y/n)"
		return v, nil
	case "F":
		v.confirmOp = SyncOpForcePush
		v.showConfirm = true
		v.confirmMessage = "Force push all repos with lease? (y/n)"
		return v, nil
	case "j", "down":
		v.scrollDown()
	case "k", "up":
		v.scrollUp()
	case "G":
		v.scrollToBottom()
	case "g":
		v.scrollOffset = 0
	}
	return v, nil
}

func (v *ProjectSyncView) handleConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	op, confirmed, dismissed := handleSyncConfirm(msg.String(), v.confirmOp)
	if confirmed {
		v.showConfirm = false
		v.confirmOp = SyncOpNone
		return v, v.startBatchOp(op)
	}
	if dismissed {
		v.showConfirm = false
		v.confirmOp = SyncOpNone
	}
	return v, nil
}

func (v *ProjectSyncView) handleScrolling(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		v.scrollDown()
	case "k", "up":
		v.scrollUp()
	}
	return v, nil
}

// startBatchOp runs a git operation across all repos concurrently.
func (v *ProjectSyncView) startBatchOp(op SyncOperation) tea.Cmd {
	if v.proj == nil {
		return nil
	}

	v.executing = true
	v.currentOp = op
	v.addLog(op, "info", fmt.Sprintf("%s all %d repos...", op, len(v.proj.Repos)))

	if op == SyncOpSync {
		// Multi-step: start with fetch
		return func() tea.Msg {
			return projectSyncStepMsg{step: 0}
		}
	}

	repos := v.proj.Repos
	return func() tea.Msg {
		results := v.runOpOnAllRepos(op, repos)
		return projectSyncDoneMsg{op: op, results: results}
	}
}

// runOpOnAllRepos executes a single git operation on all repos concurrently.
func (v *ProjectSyncView) runOpOnAllRepos(op SyncOperation, repos []*project.Repo) []projectSyncRepoResult {
	var mu sync.Mutex
	var wg sync.WaitGroup
	results := make([]projectSyncRepoResult, len(repos))

	for i, repo := range repos {
		wg.Add(1)
		go func(idx int, r *project.Repo) {
			defer wg.Done()
			res := projectSyncRepoResult{
				repoName: r.Name,
				op:       op,
			}

			var output string
			var err error

			switch op {
			case SyncOpFetch:
				output, err = git.Fetch(r.Path, "")
			case SyncOpPull:
				output, err = git.Pull(r.Path)
			case SyncOpPush:
				output, err = git.Push(r.Path, false)
			case SyncOpForcePush:
				output, err = git.Push(r.Path, true)
			case SyncOpRebase:
				output, err = git.PullRebase(r.Path)
			}

			res.output = output
			if err != nil {
				res.err = err.Error()
			}

			mu.Lock()
			results[idx] = res
			mu.Unlock()
		}(i, repo)
	}
	wg.Wait()

	return results
}

func (v *ProjectSyncView) handleSyncStep(msg projectSyncStepMsg) (tea.Model, tea.Cmd) {
	repos := v.proj.Repos

	// Log results from previous step
	if msg.results != nil {
		for _, r := range msg.results {
			if r.err != "" {
				v.addLog(SyncOpSync, "error", fmt.Sprintf("[%s] %s", r.repoName, r.err))
			} else if r.output != "" {
				v.addLog(SyncOpSync, "output", fmt.Sprintf("[%s] %s", r.repoName, r.output))
			}
		}
	}

	switch msg.step {
	case 0:
		// Step 1: Fetch all
		v.addLog(SyncOpSync, "info", "Step 1/3: Fetching all repos...")
		return v, func() tea.Msg {
			results := v.runOpOnAllRepos(SyncOpFetch, repos)
			// Check for any errors - if any repo failed fetch, abort
			for _, r := range results {
				if r.err != "" {
					return projectSyncDoneMsg{op: SyncOpSync, results: results}
				}
			}
			return projectSyncStepMsg{step: 1, results: results}
		}
	case 1:
		// Step 2: Rebase all
		v.addLog(SyncOpSync, "info", "Step 2/3: Rebasing all repos...")
		return v, func() tea.Msg {
			results := v.runOpOnAllRepos(SyncOpRebase, repos)
			for _, r := range results {
				if r.err != "" {
					return projectSyncDoneMsg{op: SyncOpSync, results: results}
				}
			}
			return projectSyncStepMsg{step: 2, results: results}
		}
	case 2:
		// Step 3: Push all
		v.addLog(SyncOpSync, "info", "Step 3/3: Pushing all repos...")
		return v, func() tea.Msg {
			results := v.runOpOnAllRepos(SyncOpPush, repos)
			return projectSyncDoneMsg{op: SyncOpSync, results: results}
		}
	}
	return v, nil
}

func (v *ProjectSyncView) refreshStatusCmd() tea.Cmd {
	return func() tea.Msg {
		if v.proj == nil {
			return projectSyncStatusMsg{}
		}

		var mu sync.Mutex
		var wg sync.WaitGroup
		statuses := make([]projectSyncRepoStatus, len(v.proj.Repos))

		for i, repo := range v.proj.Repos {
			wg.Add(1)
			go func(idx int, r *project.Repo) {
				defer wg.Done()
				s := projectSyncRepoStatus{
					name: r.Name,
					path: r.Path,
				}
				if status, err := git.GetUpstreamStatus(r.Path); err == nil {
					s.status = status
				}
				if t, err := git.GetLastFetchTime(r.Path); err == nil {
					s.lastFetch = t
				}
				mu.Lock()
				statuses[idx] = s
				mu.Unlock()
			}(i, repo)
		}
		wg.Wait()

		return projectSyncStatusMsg{statuses: statuses}
	}
}

// View renders the project sync view.
func (v *ProjectSyncView) View() string {
	th := theme.GetTheme()

	var s strings.Builder

	// Header
	title := "Project Sync"
	if v.proj != nil {
		title = fmt.Sprintf("Project Sync: %s", v.proj.Name)
	}
	s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" %s ", title)))
	s.WriteString("\n\n")

	if v.loading {
		s.WriteString(th.StatsStyle.Render(" Loading..."))
		s.WriteString("\n")
		return s.String()
	}

	// Per-repo status table
	v.renderRepoStatuses(&s, th)
	s.WriteString("\n")

	// Legend
	v.renderLegend(&s, th)
	s.WriteString("\n")

	// Confirmation dialog
	if v.showConfirm {
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" %s ", v.confirmMessage)))
		s.WriteString("\n\n")
	}

	// Currently executing
	if v.executing {
		spinner := "●"
		s.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf(" %s %s... ", spinner, v.currentOp)))
		s.WriteString("\n\n")
	}

	// Output log
	v.renderLog(&s, th)

	// Keybindings
	s.WriteString("\n")
	v.renderKeybindings(&s, th)

	return s.String()
}

func (v *ProjectSyncView) renderRepoStatuses(s *strings.Builder, th theme.Theme) {
	s.WriteString(th.StatsStyle.Render(" Repository Status "))
	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")

	if len(v.repoStatuses) == 0 {
		s.WriteString(th.StatsStyle.Render(" No repositories"))
		s.WriteString("\n")
		return
	}

	for _, rs := range v.repoStatuses {
		var line strings.Builder

		// Repo name (fixed width)
		nameWidth := 20
		name := rs.name
		if len(name) > nameWidth {
			name = name[:nameWidth-1] + "…"
		}
		line.WriteString(fmt.Sprintf(" %-*s", nameWidth, name))

		if rs.status == nil {
			line.WriteString(th.DashboardErrorStyle.Render("  no status"))
			s.WriteString(line.String())
			s.WriteString("\n")
			continue
		}

		// Branch
		branchWidth := 18
		branch := rs.status.Branch
		if len(branch) > branchWidth {
			branch = branch[:branchWidth-1] + "…"
		}
		line.WriteString(th.BranchStyle.Render(fmt.Sprintf("%-*s", branchWidth, branch)))

		// Dirty indicator
		if rs.status.IsDirty {
			line.WriteString(th.DashboardErrorStyle.Render("● "))
		} else {
			line.WriteString("  ")
		}

		// Ahead/behind
		if rs.status.Upstream == "" {
			line.WriteString(th.MutedTextStyle.Render("⊘ no upstream"))
		} else if rs.status.Ahead == 0 && rs.status.Behind == 0 {
			line.WriteString(th.StatsStyle.Render("✓ synced"))
		} else {
			var parts []string
			if rs.status.Ahead > 0 {
				parts = append(parts, th.DashboardAccentStyle.Render(fmt.Sprintf("↑%d", rs.status.Ahead)))
			}
			if rs.status.Behind > 0 {
				parts = append(parts, th.DashboardErrorStyle.Render(fmt.Sprintf("↓%d", rs.status.Behind)))
			}
			line.WriteString(strings.Join(parts, " "))
		}

		s.WriteString(line.String())
		s.WriteString("\n")
	}
}

func (v *ProjectSyncView) renderLegend(s *strings.Builder, th theme.Theme) {
	s.WriteString(th.MutedTextStyle.Render(" Legend: "))
	s.WriteString(th.DashboardErrorStyle.Render("●"))
	s.WriteString(th.MutedTextStyle.Render(" dirty  "))
	s.WriteString(th.StatsStyle.Render("✓"))
	s.WriteString(th.MutedTextStyle.Render(" synced  "))
	s.WriteString(th.DashboardAccentStyle.Render("↑"))
	s.WriteString(th.MutedTextStyle.Render(" ahead  "))
	s.WriteString(th.DashboardErrorStyle.Render("↓"))
	s.WriteString(th.MutedTextStyle.Render(" behind  "))
	s.WriteString(th.MutedTextStyle.Render("⊘"))
	s.WriteString(th.MutedTextStyle.Render(" no upstream"))
	s.WriteString("\n")
}

func (v *ProjectSyncView) renderLog(s *strings.Builder, th theme.Theme) {
	v.syncLogHelper.renderSyncLog(s, th, v.syncMaxLines())
}

func (v *ProjectSyncView) renderKeybindings(s *strings.Builder, th theme.Theme) {
	renderSyncKeybindings(s, th, v.KeyBindings())
}

func (v *ProjectSyncView) addLog(op SyncOperation, kind, message string) {
	v.syncLogHelper.addLog(op, kind, message, v.syncMaxLines())
}

func (v *ProjectSyncView) scrollDown() {
	v.syncLogHelper.scrollDown(v.syncMaxLines())
}

func (v *ProjectSyncView) scrollUp() {
	v.syncLogHelper.scrollUp()
}

func (v *ProjectSyncView) scrollToBottom() {
	v.syncLogHelper.scrollToBottom(v.syncMaxLines())
}

// CapturesInput returns true when a confirmation dialog is shown.
func (v *ProjectSyncView) CapturesInput() bool {
	return v.showConfirm
}

// ShortHelp returns short help text.
func (v *ProjectSyncView) ShortHelp() string {
	return "f: Fetch All  p: Pull All  P: Push All  r: Rebase All  S: Sync All  F: Force Push All"
}

// Refresh reloads status data.
func (v *ProjectSyncView) Refresh() error {
	v.loadStatuses()
	return nil
}

// KeyBindings returns the keybindings for this view.
func (v *ProjectSyncView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "f", Description: "Fetch all repos"},
		{Key: "p", Description: "Pull all repos"},
		{Key: "P", Description: "Push all repos"},
		{Key: "r", Description: "Pull --rebase all repos"},
		{Key: "S", Description: "Sync all (fetch + rebase + push)"},
		{Key: "F", Description: "Force push all (with lease)"},
		{Key: "j/k", Description: "Scroll output log"},
		{Key: "G/g", Description: "Scroll to bottom/top"},
	}
}
