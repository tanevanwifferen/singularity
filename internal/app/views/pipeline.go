package views

import (
	"fmt"
	"strings"
	"time"

	"singularity/internal/app/components"
	"singularity/internal/git"
	"singularity/internal/theme"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// PipelineView displays CI/CD pipeline status for branches.
type PipelineView struct {
	repoPath  string
	repo      *git.RepoInfo
	branches  []git.BranchInfo
	pipelines map[string]*git.PipelineInfo
	loading   bool
	err       error
	width     int
	height    int

	// Selection state
	selectedIdx    int
	expandedBranch string

	// Auto-refresh state
	autoRefresh     bool
	refreshInterval time.Duration
	lastRefresh     time.Time

	// Retry state
	retryBranch   string
	showRetryBusy bool
}

// NewPipelineView creates a new pipeline view.
func NewPipelineView(repoPath string) *PipelineView {
	v := &PipelineView{
		repoPath:        repoPath,
		refreshInterval: 30 * time.Second,
		selectedIdx:     0,
	}

	return v
}

// SetRepoPath updates the repository path for this view.
func (v *PipelineView) SetRepoPath(path string) { v.repoPath = path }

// Init initializes the pipeline view.
func (v *PipelineView) Init() tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		v.loadData()
		return RefreshDoneMsg{}
	}
}

// loadData loads all repository and pipeline data.
func (v *PipelineView) loadData() {
	v.err = nil

	repo, err := git.OpenRepo(v.repoPath)
	if err != nil {
		v.err = fmt.Errorf("failed to open repo: %w", err)
		v.loading = false
		return
	}
	v.repo = repo
	v.branches = repo.Branches

	// Get pipeline statuses for all branches
	v.pipelines, err = git.GetBranchPipelineStatuses(v.repoPath, v.branches)
	if err != nil {
		// Don't fail completely on pipeline errors
		v.err = fmt.Errorf("pipeline fetch error: %v", err)
	}

	// Reset selection if out of bounds
	if v.selectedIdx >= len(v.branches) {
		v.selectedIdx = 0
	}

	v.lastRefresh = time.Now()
	v.loading = false
}

// Update handles update events.
func (v *PipelineView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return v.handleKey(msg)

	case RefreshDoneMsg:
		v.loading = false
		v.showRetryBusy = false

	case RetryDoneMsg:
		v.showRetryBusy = false
		if msg.Error != nil {
			v.err = fmt.Errorf("retry failed: %v", msg.Error)
		} else {
			// Refresh after successful retry
			v.loading = true
			return v, func() tea.Msg {
				v.loadData()
				return RefreshDoneMsg{}
			}
		}

	case tea.WindowSizeMsg:
		v.width = msg.Width
		v.height = msg.Height

	case autoRefreshTickMsg:
		if v.autoRefresh {
			v.loading = true
			return v, func() tea.Msg {
				v.loadData()
				return RefreshDoneMsg{}
			}
		}
	}

	return v, nil
}

// handleKey handles key events.
func (v *PipelineView) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r":
		// Refresh
		v.loading = true
		return v, func() tea.Msg {
			v.loadData()
			return RefreshDoneMsg{}
		}

	case "a":
		// Toggle auto-refresh
		v.autoRefresh = !v.autoRefresh

	case "up", "k":
		if v.selectedIdx > 0 {
			v.selectedIdx--
		}

	case "down", "j":
		if v.selectedIdx < len(v.branches)-1 {
			v.selectedIdx++
		}

	case "enter", "right":
		// Expand/collapse selected branch
		if v.selectedIdx < len(v.branches) {
			branch := v.branches[v.selectedIdx].Name
			if v.expandedBranch == branch {
				v.expandedBranch = ""
			} else {
				v.expandedBranch = branch
			}
		}

	case "R":
		// Retry failed pipeline for selected branch
		if v.selectedIdx < len(v.branches) {
			branch := v.branches[v.selectedIdx].Name
			info, ok := v.pipelines[branch]
			if ok && info != nil && info.Status == git.PipelineFailed {
				v.retryBranch = branch
				v.showRetryBusy = true
				return v, func() tea.Msg {
					err := git.RetryPipeline(v.repoPath, branch)
					return RetryDoneMsg{Branch: branch, Error: err}
				}
			}
		}
	}
	return v, nil
}

// RetryDoneMsg is sent when a retry operation completes.
type RetryDoneMsg struct {
	Branch string
	Error  error
}

// autoRefreshTickMsg triggers auto-refresh.
type autoRefreshTickMsg struct{}

// View renders the pipeline view.
func (v *PipelineView) View() string {
	th := theme.GetTheme()

	var s strings.Builder

	// Header
	s.WriteString(th.DashboardTitle.Render(" Pipeline Dashboard "))
	s.WriteString("\n\n")

	// Forge info
	auth, _ := git.DetectForgeAuth()
	if auth != nil && auth.Valid {
		s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" Forge: %s ", auth.Type.String())))
	} else {
		s.WriteString(th.DashboardErrorStyle.Render(" No forge authentication "))
	}
	s.WriteString("\n\n")

	// Current branch pipeline status (prominent)
	if v.repo != nil && v.repo.CurrentBranch != "" {
		currentInfo, ok := v.pipelines[v.repo.CurrentBranch]
		s.WriteString(th.DashboardTitle.Render(" Current Branch Pipeline "))
		s.WriteString("\n")
		s.WriteString(fmt.Sprintf(" %s %s\n",
			th.BranchStyle.Render("Branch:"),
			th.DashboardAccentStyle.Render(v.repo.CurrentBranch)))

		if ok && currentInfo != nil && currentInfo.HasPipeline {
			statusStr := git.FormatPipelineStatus(currentInfo.Status)
			statusColor := v.getStatusStyle(currentInfo.Status)
			s.WriteString(fmt.Sprintf(" %s %s\n",
				th.BranchStyle.Render("Status:"),
				statusColor.Render(statusStr)))

			if currentInfo.Pipeline != nil {
				duration := currentInfo.Pipeline.Duration
				if duration > 0 {
					s.WriteString(fmt.Sprintf(" %s %s\n",
						th.BranchStyle.Render("Duration:"),
						th.StatsStyle.Render(fmt.Sprintf("%ds", duration))))
				}
				if currentInfo.Pipeline.WebURL != "" {
					s.WriteString(fmt.Sprintf(" %s %s\n",
						th.BranchStyle.Render("URL:"),
						th.InfoStyle.Render(truncateURL(currentInfo.Pipeline.WebURL))))
				}
			}
		} else {
			s.WriteString(fmt.Sprintf(" %s\n", th.Help.Render("No pipeline found")))
		}
		s.WriteString("\n")
	}

	// Branch list header
	s.WriteString(th.DashboardTitle.Render(" Branch Pipelines "))
	s.WriteString("\n")

	// Controls
	controls := th.Help.Render(" ↑↓: Navigate   Enter: Expand   R: Retry failed   r: Refresh   a: Toggle auto-refresh ")
	if v.autoRefresh {
		controls = th.DashboardAccentStyle.Render(" Auto-refresh: ON  ") + controls[len(th.DashboardAccentStyle.Render(" Auto-refresh: ON  ")):]
	}
	s.WriteString(controls)
	s.WriteString("\n\n")

	// Loading state
	if v.loading {
		s.WriteString(th.StatsStyle.Render(" Loading pipelines..."))
		return s.String()
	}

	// Error state
	if v.err != nil && v.repo == nil {
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" Error: %v", v.err)))
		return s.String()
	}

	// Branch list
	for i, branch := range v.branches {
		if i == v.selectedIdx {
			s.WriteString(v.renderSelectedBranch(branch))
		} else {
			s.WriteString(v.renderBranchItem(branch))
		}
		s.WriteString("\n")
	}

	// Expanded branch details
	if v.expandedBranch != "" {
		s.WriteString("\n")
		s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
		s.WriteString("\n")
		s.WriteString(v.renderExpandedDetails())
	}

	// Retry busy indicator
	if v.showRetryBusy {
		s.WriteString("\n\n")
		s.WriteString(th.DashboardAccentStyle.Render(" Retrying pipeline..."))
	}

	return s.String()
}

// renderBranchItem renders a single branch item in the list.
func (v *PipelineView) renderBranchItem(branch git.BranchInfo) string {
	th := theme.GetTheme()
	info, ok := v.pipelines[branch.Name]

	var statusStr string
	var statusStyle lipgloss.Style

	if ok && info != nil && info.HasPipeline {
		statusStr = git.FormatPipelineStatus(info.Status)
		statusStyle = v.getStatusStyle(info.Status)
	} else {
		statusStr = "○ no pipeline"
		statusStyle = th.Help
	}

	return fmt.Sprintf("  %s %s %s",
		v.getStatusIcon(info),
		th.BranchStyle.Render(branch.Name),
		statusStyle.Render(statusStr))
}

// renderSelectedBranch renders the selected branch item.
func (v *PipelineView) renderSelectedBranch(branch git.BranchInfo) string {
	th := theme.GetTheme()
	info, ok := v.pipelines[branch.Name]

	var statusStr string
	var statusStyle lipgloss.Style

	if ok && info != nil && info.HasPipeline {
		statusStr = git.FormatPipelineStatus(info.Status)
		statusStyle = v.getStatusStyle(info.Status)
	} else {
		statusStr = "○ no pipeline"
		statusStyle = th.Help
	}

	expanded := ""
	if v.expandedBranch == branch.Name {
		expanded = th.DashboardAccentStyle.Render(" ▼")
	} else {
		expanded = "  "
	}

	return fmt.Sprintf(" >%s %s %s%s",
		expanded,
		th.SelectedBranchStyle.Render(branch.Name),
		statusStyle.Render(statusStr),
		v.renderRetryHint(info))
}

// renderRetryHint shows retry hint for failed pipelines.
func (v *PipelineView) renderRetryHint(info *git.PipelineInfo) string {
	th := theme.GetTheme()
	if info != nil && info.Status == git.PipelineFailed {
		return th.Help.Render(" [R]etry")
	}
	return ""
}

// renderExpandedDetails renders expanded job details for a branch.
func (v *PipelineView) renderExpandedDetails() string {
	th := theme.GetTheme()
	info, ok := v.pipelines[v.expandedBranch]

	if !ok || info == nil || info.Pipeline == nil {
		return th.Help.Render(" No pipeline details available")
	}

	var s strings.Builder

	s.WriteString(th.DashboardTitle.Render(" Pipeline Details "))
	s.WriteString(fmt.Sprintf(" for %s\n\n", th.DashboardAccentStyle.Render(v.expandedBranch)))

	pipeline := info.Pipeline

	// Pipeline info
	s.WriteString(fmt.Sprintf(" %s %s\n",
		th.BranchStyle.Render("Pipeline ID:"),
		th.StatsStyle.Render(fmt.Sprintf("%d", pipeline.ID))))
	s.WriteString(fmt.Sprintf(" %s %s\n",
		th.BranchStyle.Render("Ref:"),
		th.StatsStyle.Render(pipeline.Ref)))
	s.WriteString(fmt.Sprintf(" %s %s\n",
		th.BranchStyle.Render("SHA:"),
		th.StatsStyle.Render(pipeline.SHA[:minInt(7, len(pipeline.SHA))])))
	s.WriteString(fmt.Sprintf(" %s %s\n",
		th.BranchStyle.Render("Status:"),
		v.getStatusStyle(pipeline.Status).Render(git.FormatPipelineStatus(pipeline.Status))))

	if pipeline.Duration > 0 {
		s.WriteString(fmt.Sprintf(" %s %s\n",
			th.BranchStyle.Render("Duration:"),
			th.StatsStyle.Render(fmt.Sprintf("%ds", pipeline.Duration))))
	}

	s.WriteString("\n")

	// Jobs
	if len(pipeline.Jobs) > 0 {
		s.WriteString(th.DashboardTitle.Render(" Jobs "))
		s.WriteString("\n")
		for _, job := range pipeline.Jobs {
			statusStr := git.FormatPipelineStatus(job.Status)
			statusStyle := v.getStatusStyle(job.Status)
			s.WriteString(fmt.Sprintf("  %s %s %s\n",
				v.getJobStatusIcon(job.Status),
				th.InfoStyle.Render(job.Name),
				statusStyle.Render(statusStr)))
		}
	}

	return s.String()
}

// getStatusIcon returns an icon for pipeline status.
func (v *PipelineView) getStatusIcon(info *git.PipelineInfo) string {
	if info == nil || !info.HasPipeline {
		return "○"
	}
	switch info.Status {
	case git.PipelineSuccess:
		return "✓"
	case git.PipelineFailed:
		return "✗"
	case git.PipelineRunning:
		return "●"
	case git.PipelinePending:
		return "○"
	case git.PipelineCanceled:
		return "⊘"
	case git.PipelineSkipped:
		return "⊝"
	default:
		return "?"
	}
}

// getJobStatusIcon returns an icon for job status.
func (v *PipelineView) getJobStatusIcon(status git.PipelineStatus) string {
	switch status {
	case git.PipelineSuccess:
		return "✓"
	case git.PipelineFailed:
		return "✗"
	case git.PipelineRunning:
		return "●"
	case git.PipelinePending:
		return "○"
	case git.PipelineCanceled:
		return "⊘"
	case git.PipelineSkipped:
		return "⊝"
	default:
		return "?"
	}
}

// getStatusStyle returns the lipgloss style for a pipeline status.
func (v *PipelineView) getStatusStyle(status git.PipelineStatus) lipgloss.Style {
	th := theme.GetTheme()
	switch status {
	case git.PipelineSuccess:
		return lipgloss.NewStyle().Foreground(th.Info)
	case git.PipelineFailed:
		return lipgloss.NewStyle().Foreground(th.Error)
	case git.PipelineRunning:
		return lipgloss.NewStyle().Foreground(th.Warning)
	case git.PipelinePending:
		return lipgloss.NewStyle().Foreground(th.MutedText)
	default:
		return th.Help
	}
}

// ShortHelp returns a short help string.
func (v *PipelineView) ShortHelp() string {
	return "↑↓: Navigate  Enter: Expand  R: Retry  r: Refresh  a: Auto-refresh"
}

// SetSize updates the view dimensions.
func (v *PipelineView) SetSize(width, height int) {
	v.width = width
	v.height = height
}

// GetRepoPath returns the repository path.
func (v *PipelineView) GetRepoPath() string {
	return v.repoPath
}

// KeyBindings returns the keybindings for this view.
func (v *PipelineView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "r", Description: "Refresh pipeline status"},
		{Key: "a", Description: "Toggle auto-refresh"},
		{Key: "↑/k", Description: "Navigate up"},
		{Key: "↓/j", Description: "Navigate down"},
		{Key: "Enter", Description: "Expand/collapse job details"},
		{Key: "R", Description: "Retry failed pipeline"},
		{Key: "1", Description: "Switch to Overview"},
		{Key: "2", Description: "Switch to Branches"},
		{Key: "3", Description: "Switch to Stashes"},
		{Key: "4", Description: "Switch to Worktrees"},
	}
}

// truncateURL truncates a URL for display.
func truncateURL(url string) string {
	if len(url) <= 60 {
		return url
	}
	return url[:57] + "..."
}

// minInt returns the minimum of two integers.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
