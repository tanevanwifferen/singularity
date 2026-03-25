package views

import (
	"fmt"
	"strings"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// SyncOperation identifies which remote operation is running.
type SyncOperation int

const (
	SyncOpNone SyncOperation = iota
	SyncOpFetch
	SyncOpPull
	SyncOpPush
	SyncOpRebase
	SyncOpSync // fetch + rebase + push
	SyncOpForcePush
	SyncOpSetUpstream
)

func (op SyncOperation) String() string {
	switch op {
	case SyncOpFetch:
		return "Fetching"
	case SyncOpPull:
		return "Pulling"
	case SyncOpPush:
		return "Pushing"
	case SyncOpRebase:
		return "Rebasing"
	case SyncOpSync:
		return "Syncing"
	case SyncOpForcePush:
		return "Force Pushing"
	case SyncOpSetUpstream:
		return "Setting upstream"
	default:
		return ""
	}
}

// Sync view message types.
type (
	syncSuccessMsg struct {
		op     SyncOperation
		output string
	}
	syncErrorMsg struct {
		op     SyncOperation
		output string
		err    string
	}
	syncStatusRefreshedMsg struct {
		status *git.UpstreamStatus
	}
	// syncStepMsg drives the multi-step sync flow.
	syncStepMsg struct {
		step   int
		output string
	}
)

// SyncView handles push, pull, fetch, rebase, and sync operations.
type SyncView struct {
	viewBase
	loading bool

	// Upstream status
	status    *git.UpstreamStatus
	lastFetch time.Time

	// Operation state
	executing bool
	currentOp SyncOperation
	err       error

	// Output log
	syncLogHelper

	// Confirmation
	confirmOp      SyncOperation
	showConfirm    bool
	confirmMessage string
}

// NewSyncView creates a new sync view.
func NewSyncView(repoPath string) *SyncView {
	return &SyncView{
		viewBase:      viewBase{repoPath: repoPath, width: 80, height: 24},
		syncLogHelper: syncLogHelper{outputLog: make([]logEntry, 0)},
	}
}

// syncMaxLines returns the maximum number of visible log lines for SyncView.
func (v *SyncView) syncMaxLines() int {
	return v.height - 27
}

// Init initializes the sync view.
func (v *SyncView) Init() tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		v.loadStatus()
		return RefreshDoneMsg{}
	}
}

func (v *SyncView) loadStatus() {
	status, err := git.GetUpstreamStatus(v.repoPath)
	if err != nil {
		v.err = err
		v.loading = false
		return
	}
	v.status = status

	if t, err := git.GetLastFetchTime(v.repoPath); err == nil {
		v.lastFetch = t
	}

	v.loading = false
}

// Update handles input and messages.
func (v *SyncView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if v.showConfirm {
			return v.handleConfirm(msg)
		}
		if v.executing {
			return v.handleScrolling(msg)
		}
		return v.handleKeys(msg)

	case syncSuccessMsg:
		v.executing = false
		v.currentOp = SyncOpNone
		v.addLog(msg.op, "success", opDoneLabel(msg.op))
		if msg.output != "" {
			v.addLog(msg.op, "output", msg.output)
		}
		// Refresh status after any operation
		return v, v.refreshStatusCmd()

	case syncErrorMsg:
		v.executing = false
		v.currentOp = SyncOpNone
		v.addLog(msg.op, "error", msg.err)
		if msg.output != "" {
			v.addLog(msg.op, "output", msg.output)
		}
		return v, v.refreshStatusCmd()

	case syncStatusRefreshedMsg:
		if msg.status != nil {
			v.status = msg.status
		}
		if t, err := git.GetLastFetchTime(v.repoPath); err == nil {
			v.lastFetch = t
		}

	case syncStepMsg:
		return v.handleSyncStep(msg)

	case RefreshDoneMsg:
		v.loading = false
	}

	return v, nil
}

func (v *SyncView) handleKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "f":
		return v, v.startOp(SyncOpFetch)
	case "p":
		return v, v.startOp(SyncOpPull)
	case "P":
		if v.status != nil && v.status.Upstream == "" {
			// No upstream - offer to set one
			v.confirmOp = SyncOpSetUpstream
			v.showConfirm = true
			v.confirmMessage = "No upstream set. Push and set upstream to origin? (y/n)"
			return v, nil
		}
		return v, v.startOp(SyncOpPush)
	case "r":
		return v, v.startOp(SyncOpRebase)
	case "S":
		v.confirmOp = SyncOpSync
		v.showConfirm = true
		v.confirmMessage = "Sync: fetch + rebase + push? (y/n)"
		return v, nil
	case "F":
		v.confirmOp = SyncOpForcePush
		v.showConfirm = true
		v.confirmMessage = "Force push with lease? (y/n)"
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

func (v *SyncView) handleConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	op, confirmed, dismissed := handleSyncConfirm(msg.String(), v.confirmOp)
	if confirmed {
		v.showConfirm = false
		v.confirmOp = SyncOpNone
		return v, v.startOp(op)
	}
	if dismissed {
		v.showConfirm = false
		v.confirmOp = SyncOpNone
	}
	return v, nil
}

func (v *SyncView) handleScrolling(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		v.scrollDown()
	case "k", "up":
		v.scrollUp()
	}
	return v, nil
}

func (v *SyncView) startOp(op SyncOperation) tea.Cmd {
	v.executing = true
	v.currentOp = op
	v.err = nil
	v.addLog(op, "info", op.String()+"...")

	switch op {
	case SyncOpFetch:
		return func() tea.Msg {
			output, err := git.Fetch(v.repoPath, "")
			if err != nil {
				return syncErrorMsg{op: op, output: output, err: err.Error()}
			}
			return syncSuccessMsg{op: op, output: output}
		}
	case SyncOpPull:
		return func() tea.Msg {
			output, err := git.Pull(v.repoPath)
			if err != nil {
				return syncErrorMsg{op: op, output: output, err: err.Error()}
			}
			return syncSuccessMsg{op: op, output: output}
		}
	case SyncOpPush:
		return func() tea.Msg {
			output, err := git.Push(v.repoPath, false)
			if err != nil {
				return syncErrorMsg{op: op, output: output, err: err.Error()}
			}
			return syncSuccessMsg{op: op, output: output}
		}
	case SyncOpForcePush:
		return func() tea.Msg {
			output, err := git.Push(v.repoPath, true)
			if err != nil {
				return syncErrorMsg{op: op, output: output, err: err.Error()}
			}
			return syncSuccessMsg{op: op, output: output}
		}
	case SyncOpRebase:
		return func() tea.Msg {
			output, err := git.PullRebase(v.repoPath)
			if err != nil {
				return syncErrorMsg{op: op, output: output, err: err.Error()}
			}
			return syncSuccessMsg{op: op, output: output}
		}
	case SyncOpSync:
		// Multi-step: start with fetch
		return func() tea.Msg {
			return syncStepMsg{step: 0}
		}
	case SyncOpSetUpstream:
		return func() tea.Msg {
			output, err := git.SetUpstreamAndPush(v.repoPath, "origin")
			if err != nil {
				return syncErrorMsg{op: SyncOpSetUpstream, output: output, err: err.Error()}
			}
			return syncSuccessMsg{op: SyncOpSetUpstream, output: output}
		}
	}
	return nil
}

func (v *SyncView) handleSyncStep(msg syncStepMsg) (tea.Model, tea.Cmd) {
	switch msg.step {
	case 0:
		// Step 1: Fetch
		v.addLog(SyncOpSync, "info", "Step 1/3: Fetching...")
		return v, func() tea.Msg {
			output, err := git.Fetch(v.repoPath, "")
			if err != nil {
				return syncErrorMsg{op: SyncOpSync, output: output, err: "fetch step failed: " + err.Error()}
			}
			return syncStepMsg{step: 1, output: output}
		}
	case 1:
		// Step 2: Rebase
		if msg.output != "" {
			v.addLog(SyncOpSync, "output", msg.output)
		}
		v.addLog(SyncOpSync, "info", "Step 2/3: Rebasing...")
		return v, func() tea.Msg {
			output, err := git.PullRebase(v.repoPath)
			if err != nil {
				return syncErrorMsg{op: SyncOpSync, output: output, err: "rebase step failed: " + err.Error()}
			}
			return syncStepMsg{step: 2, output: output}
		}
	case 2:
		// Step 3: Push
		if msg.output != "" {
			v.addLog(SyncOpSync, "output", msg.output)
		}
		v.addLog(SyncOpSync, "info", "Step 3/3: Pushing...")
		return v, func() tea.Msg {
			output, err := git.Push(v.repoPath, false)
			if err != nil {
				return syncErrorMsg{op: SyncOpSync, output: output, err: "push step failed: " + err.Error()}
			}
			return syncSuccessMsg{op: SyncOpSync, output: output}
		}
	}
	return v, nil
}

func (v *SyncView) refreshStatusCmd() tea.Cmd {
	return func() tea.Msg {
		status, _ := git.GetUpstreamStatus(v.repoPath)
		return syncStatusRefreshedMsg{status: status}
	}
}

// View renders the sync view.
func (v *SyncView) View() string {
	th := theme.GetTheme()

	var s strings.Builder

	// Header
	s.WriteString(th.DashboardTitle.Render(" Sync "))
	s.WriteString("\n\n")

	if v.loading {
		s.WriteString(th.StatsStyle.Render(" Loading..."))
		s.WriteString("\n")
		return s.String()
	}

	// Status section
	v.renderStatus(&s, th)
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

	// Keybindings help
	s.WriteString("\n")
	v.renderKeybindings(&s, th)

	return s.String()
}

func (v *SyncView) renderStatus(s *strings.Builder, th theme.Theme) {
	s.WriteString(th.StatsStyle.Render(" Branch Status "))
	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")

	if v.status == nil {
		s.WriteString(th.DashboardErrorStyle.Render(" Not on a branch"))
		s.WriteString("\n")
		return
	}

	// Branch
	s.WriteString(fmt.Sprintf(" %s %s",
		th.BranchStyle.Render("Branch:"),
		th.DashboardAccentStyle.Render(v.status.Branch)))

	if v.status.IsDirty {
		s.WriteString(th.DashboardErrorStyle.Render(" (dirty)"))
	}
	s.WriteString("\n")

	// Upstream
	if v.status.Upstream != "" {
		s.WriteString(fmt.Sprintf(" %s %s",
			th.BranchStyle.Render("Upstream:"),
			th.StatsStyle.Render(v.status.Upstream)))
		s.WriteString("\n")

		// Ahead/Behind
		syncLabel := " %s "
		if v.status.Ahead == 0 && v.status.Behind == 0 {
			s.WriteString(fmt.Sprintf(syncLabel, th.StatsStyle.Render("Up to date")))
		} else {
			var parts []string
			if v.status.Ahead > 0 {
				parts = append(parts, th.DashboardAccentStyle.Render(fmt.Sprintf("↑ %d ahead", v.status.Ahead)))
			}
			if v.status.Behind > 0 {
				parts = append(parts, th.DashboardErrorStyle.Render(fmt.Sprintf("↓ %d behind", v.status.Behind)))
			}
			s.WriteString(fmt.Sprintf(syncLabel, strings.Join(parts, "  ")))
		}
		s.WriteString("\n")
	} else {
		s.WriteString(fmt.Sprintf(" %s %s\n",
			th.BranchStyle.Render("Upstream:"),
			th.DashboardErrorStyle.Render("not configured")))
	}

	// Last fetch
	if !v.lastFetch.IsZero() {
		ago := time.Since(v.lastFetch)
		var agoStr string
		switch {
		case ago < time.Minute:
			agoStr = "just now"
		case ago < time.Hour:
			agoStr = fmt.Sprintf("%dm ago", int(ago.Minutes()))
		case ago < 24*time.Hour:
			agoStr = fmt.Sprintf("%dh ago", int(ago.Hours()))
		default:
			agoStr = fmt.Sprintf("%dd ago", int(ago.Hours()/24))
		}
		s.WriteString(fmt.Sprintf(" %s %s\n",
			th.BranchStyle.Render("Last fetch:"),
			th.StatsStyle.Render(agoStr)))
	}
}

func (v *SyncView) renderLog(s *strings.Builder, th theme.Theme) {
	v.syncLogHelper.renderSyncLog(s, th, v.syncMaxLines())
}

func (v *SyncView) renderKeybindings(s *strings.Builder, th theme.Theme) {
	renderSyncKeybindings(s, th, v.KeyBindings())
}

func (v *SyncView) addLog(op SyncOperation, kind, message string) {
	v.syncLogHelper.addLog(op, kind, message, v.syncMaxLines())
}

func (v *SyncView) scrollDown() {
	v.syncLogHelper.scrollDown(v.syncMaxLines())
}

func (v *SyncView) scrollUp() {
	v.syncLogHelper.scrollUp()
}

func (v *SyncView) scrollToBottom() {
	v.syncLogHelper.scrollToBottom(v.syncMaxLines())
}

// CapturesInput returns true when a confirmation dialog is shown.
func (v *SyncView) CapturesInput() bool {
	return v.showConfirm
}

// ShortHelp returns short help text.
func (v *SyncView) ShortHelp() string {
	return "f: Fetch  p: Pull  P: Push  r: Rebase  S: Sync  F: Force push"
}

// Refresh reloads status data.
func (v *SyncView) Refresh() error {
	v.loadStatus()
	return v.err
}

// KeyBindings returns the keybindings for this view.
func (v *SyncView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "f", Description: "Fetch all remotes"},
		{Key: "p", Description: "Pull (fetch + merge)"},
		{Key: "P", Description: "Push to upstream"},
		{Key: "r", Description: "Pull with rebase"},
		{Key: "S", Description: "Sync (fetch + rebase + push)"},
		{Key: "F", Description: "Force push (with lease)"},
		{Key: "j/k", Description: "Scroll output log"},
		{Key: "G/g", Description: "Scroll to bottom/top"},
	}
}
