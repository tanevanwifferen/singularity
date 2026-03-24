package views

import (
	"fmt"
	"strings"
	"time"

	"git-frontend/internal/app/components"
	"git-frontend/internal/config"
	"git-frontend/internal/engine"
	"git-frontend/internal/jira"
	"git-frontend/internal/theme"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// AgentInfo holds agent summary info for display
type AgentInfo struct {
	ID          string
	State       engine.AgentState
	Task        string
	Summary     string
	WorkDir     string
	CreatedAt   time.Time
	StartedAt   *time.Time
	EndedAt     *time.Time
	ExitCode    int
	Error       string
	MergeResult string
}

// agentFocus tracks which pane has focus
type agentFocus int

const (
	focusList   agentFocus = iota
	focusOutput
	focusInput
)

// AgentView displays the agent console with a split-pane layout.
type AgentView struct {
	repoPath     string
	engine       *engine.Engine
	contextFiles []string // Files to inject into agent prompts
	agents   []AgentInfo
	filter   *components.Filter[AgentInfo]
	loading  bool
	width    int
	height   int
	err      error

	// Split pane focus
	focus agentFocus

	// Selected agent output
	selectedAgent    *AgentInfo
	outputEntries    []engine.OutputEntry
	outputViewport   viewport.Model
	outputAutoScroll bool

	// New agent input state
	showNewAgent bool
	newAgentTask string

	// Kill confirmation state
	showKillConfirm bool
	killAgentID     string

	// Message input state (send to running agent stdin)
	showMessageInput bool
	messageInput     string

	// Refresh ticker
	refreshInterval time.Duration
	lastRefresh     time.Time

	// Jira ticket picker
	jiraPicker       *JiraPickerState
	jiraConfirmIssue *jira.Issue // issue pending agent-start confirmation
	jiraExtraMsg     string      // custom instructions for the jira agent
}

// NewAgentView creates a new agent console view.
func NewAgentView(repoPath string, eng *engine.Engine, contextFiles ...[]string) *AgentView {
	var ctxFiles []string
	if len(contextFiles) > 0 {
		ctxFiles = contextFiles[0]
	}
	v := &AgentView{
		repoPath:         repoPath,
		engine:           eng,
		contextFiles:     ctxFiles,
		width:            80,
		height:           24,
		refreshInterval:  500 * time.Millisecond,
		outputAutoScroll: true,
		focus:            focusList,
	}

	v.filter = components.NewFilter([]AgentInfo{}, v.renderAgentItem)
	v.filter.SetHeight(v.listHeight())
	v.outputViewport = viewport.New(80, 10)

	return v
}

// listHeight returns the height available for the agent list pane.
func (v *AgentView) listHeight() int {
	if v.selectedAgent != nil {
		// Split mode: 6 lines of overhead (stats, blank separator, divider,
		// output header, blank after viewport, hint/modal line)
		available := v.height - 6
		if available < 6 {
			return max(available/2, 1)
		}
		return available * 2 / 5
	}
	h := v.height - 1 // subtract stats line
	if v.showNewAgent {
		// blank line + top border + content line + hint line + bottom border = 5 lines
		h -= 5
	}
	return max(h, 4)
}

// outputHeight returns the height available for the output pane.
func (v *AgentView) outputHeight() int {
	if v.selectedAgent == nil {
		return 0
	}
	available := v.height - 6
	h := max(available-v.listHeight(), 1)
	if v.showMessageInput {
		// renderModal box: top border + message + blank + hint + bottom border = 5 lines
		// replaces the 1-line hint, so 4 extra lines are needed.
		h = max(h-4, 1)
	}
	return h
}

// SetRepoPath updates the repository path for this view.
func (v *AgentView) SetRepoPath(path string) { v.repoPath = path }

// SetEngine sets the agent engine (allows late binding)
func (v *AgentView) SetEngine(eng *engine.Engine) {
	v.engine = eng
}

// SetJiraConfig wires Jira configuration so the Jira ticket picker is available.
func (v *AgentView) SetJiraConfig(cfg config.JiraConfig) {
	v.jiraPicker = NewJiraPickerState(cfg)
	if v.jiraPicker != nil {
		v.jiraPicker.SetSize(v.width, v.height)
	}
}

// Init initializes the agent view.
func (v *AgentView) Init() tea.Cmd {
	v.loading = true
	return tea.Batch(
		func() tea.Msg {
			v.loadAgents()
			return RefreshDoneMsg{}
		},
		v.streamTickCmd(),
	)
}

// streamTickCmd returns a tea.Cmd that sends a StreamTickMsg after the refresh interval.
func (v *AgentView) streamTickCmd() tea.Cmd {
	return tea.Tick(v.refreshInterval, func(t time.Time) tea.Msg {
		return StreamTickMsg{}
	})
}

// loadAgents loads the current list of agents from the engine.
func (v *AgentView) loadAgents() {
	v.err = nil

	if v.engine == nil {
		v.loading = false
		return
	}

	agentList := v.engine.ListAgents()
	v.agents = make([]AgentInfo, 0, len(agentList))

	for _, a := range agentList {
		snap := a.Snapshot()
		info := AgentInfo{
			ID:          snap.ID,
			State:       snap.State,
			Task:        snap.Task,
			Summary:     snap.Summary,
			WorkDir:     snap.WorkDir,
			CreatedAt:   snap.CreatedAt,
			StartedAt:   snap.StartedAt,
			EndedAt:     snap.EndedAt,
			ExitCode:    snap.ExitCode,
			Error:       snap.Error,
			MergeResult: snap.MergeResult,
		}
		v.agents = append(v.agents, info)
	}

	v.filter.SetItems(v.agents)

	if v.selectedAgent != nil {
		// Update the selected agent's info from the refreshed list
		for _, info := range v.agents {
			if info.ID == v.selectedAgent.ID {
				v.selectedAgent = &info
				break
			}
		}
		v.refreshSelectedAgentOutput()
	} else if len(v.agents) > 0 {
		// Auto-preview the agent under the cursor
		v.syncPreview()
	}

	v.loading = false
}

// refreshSelectedAgentOutput updates output for the currently selected agent.
func (v *AgentView) refreshSelectedAgentOutput() {
	if v.selectedAgent == nil || v.engine == nil {
		return
	}

	entries, err := v.engine.GetOutputEntries(v.selectedAgent.ID, 0)
	if err != nil {
		return
	}
	v.outputEntries = entries
	v.rebuildOutputViewport()
}

// wrapLine wraps a line to fit within maxWidth, preserving a prefix on continuation lines.
func wrapLine(line string, maxWidth int, contPrefix string) []string {
	if maxWidth <= 0 || len(line) <= maxWidth {
		return []string{line}
	}
	var wrapped []string
	for len(line) > 0 {
		cut := maxWidth
		if len(wrapped) > 0 {
			cut = maxWidth - len(contPrefix)
			if cut <= 0 {
				cut = maxWidth
			}
		}
		if cut >= len(line) {
			if len(wrapped) > 0 {
				line = contPrefix + line
			}
			wrapped = append(wrapped, line)
			break
		}
		chunk := line[:cut]
		if len(wrapped) > 0 {
			chunk = contPrefix + chunk
		}
		wrapped = append(wrapped, chunk)
		line = line[cut:]
	}
	return wrapped
}

// rebuildOutputViewport rebuilds the viewport content from output entries.
func (v *AgentView) rebuildOutputViewport() {
	th := theme.GetTheme()
	var lines []string
	w := v.width

	for _, entry := range v.outputEntries {
		switch entry.Source {
		case "text":
			for _, raw := range strings.Split(entry.Content, "\n") {
				lines = append(lines, wrapLine(raw, w, "  ")...)
			}

		case "tool_use":
			style := lipgloss.NewStyle().Foreground(th.Info).Bold(true)
			for _, raw := range strings.Split(entry.Content, "\n") {
				for _, wl := range wrapLine(fmt.Sprintf("  %s", raw), w, "    ") {
					lines = append(lines, style.Render(wl))
				}
			}

		case "tool_result":
			style := th.MutedTextStyle
			if entry.IsError {
				style = th.DashboardErrorStyle
			}
			for _, rl := range strings.Split(entry.Content, "\n") {
				for _, wl := range wrapLine(fmt.Sprintf("    %s", rl), w, "      ") {
					lines = append(lines, style.Render(wl))
				}
			}

		case "system":
			for _, raw := range strings.Split(entry.Content, "\n") {
				for _, wl := range wrapLine(fmt.Sprintf("  %s", raw), w, "    ") {
					lines = append(lines, th.MutedTextStyle.Render(wl))
				}
			}

		case "error":
			for _, raw := range strings.Split(entry.Content, "\n") {
				for _, wl := range wrapLine(fmt.Sprintf("  %s", raw), w, "    ") {
					lines = append(lines, th.DashboardErrorStyle.Render(wl))
				}
			}

		case "result":
			style := lipgloss.NewStyle().Foreground(th.Info)
			for _, raw := range strings.Split(entry.Content, "\n") {
				for _, wl := range wrapLine(fmt.Sprintf("  %s", raw), w, "    ") {
					lines = append(lines, style.Render(wl))
				}
			}

		case "user_input":
			style := lipgloss.NewStyle().Foreground(th.Accent).Bold(true)
			for _, raw := range strings.Split(entry.Content, "\n") {
				for _, wl := range wrapLine(fmt.Sprintf("  > %s", raw), w, "      ") {
					lines = append(lines, style.Render(wl))
				}
			}
		}
	}

	content := strings.Join(lines, "\n")
	v.outputViewport.SetContent(content)

	if v.outputAutoScroll {
		v.outputViewport.GotoBottom()
	}
}

// syncPreview updates the output pane to show the agent under the cursor.
func (v *AgentView) syncPreview() {
	if item, idx := v.filter.SelectedItem(); idx >= 0 {
		if v.selectedAgent == nil || v.selectedAgent.ID != item.ID {
			v.selectAgent(item)
		}
	}
}

// selectAgent selects an agent and opens the output pane.
func (v *AgentView) selectAgent(info AgentInfo) {
	v.selectedAgent = &info
	v.outputAutoScroll = true
	v.recalcLayout()
	v.refreshSelectedAgentOutput()
}

// deselectAgent closes the output pane.
func (v *AgentView) deselectAgent() {
	v.selectedAgent = nil
	v.outputEntries = nil
	v.focus = focusList
	v.recalcLayout()
}

// recalcLayout recalculates the split pane sizes.
func (v *AgentView) recalcLayout() {
	v.filter.SetHeight(v.listHeight())
	if v.selectedAgent != nil {
		v.outputViewport.Width = v.width
		v.outputViewport.Height = v.outputHeight()
	}
}

// Update handles update events.
func (v *AgentView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case jiraPickerLoadedMsg:
		if cmd := v.jiraPicker.HandleMsg(msg); cmd != nil {
			return v, cmd
		}

	case tea.KeyMsg:
		// Handle kill confirmation first
		if v.showKillConfirm {
			return v, v.handleKillConfirm(msg)
		}

		// Handle message input to running agent
		if v.showMessageInput {
			return v, v.handleMessageInput(msg)
		}

		// Handle new agent input
		if v.showNewAgent {
			return v, v.handleNewAgentInput(msg)
		}

		// Handle Jira picker
		if v.jiraPicker.IsOpen() {
			return v, v.handleJiraPickerKey(msg)
		}

		// Handle Jira confirm-start-agent modal
		if v.jiraConfirmIssue != nil {
			return v, v.handleJiraAgentConfirm(msg)
		}

		key := msg.String()

		// Output pane navigation when focused
		if v.focus == focusOutput && v.selectedAgent != nil {
			switch key {
			case "tab":
				v.focus = focusList
				return v, nil
			case "esc", "q":
				v.deselectAgent()
				return v, nil
			case "j", "down":
				v.outputAutoScroll = false
				v.outputViewport.LineDown(1)
				return v, nil
			case "k", "up":
				v.outputAutoScroll = false
				v.outputViewport.LineUp(1)
				return v, nil
			case "g":
				v.outputAutoScroll = false
				v.outputViewport.GotoTop()
				return v, nil
			case "G":
				v.outputAutoScroll = true
				v.outputViewport.GotoBottom()
				return v, nil
			case "ctrl+d", "pgdown":
				v.outputAutoScroll = false
				v.outputViewport.HalfViewDown()
				return v, nil
			case "ctrl+u", "pgup":
				v.outputAutoScroll = false
				v.outputViewport.HalfViewUp()
				return v, nil
			case "i":
				if v.selectedAgent != nil &&
					(v.selectedAgent.State == engine.AgentRunning || v.selectedAgent.State == engine.AgentStarting || v.selectedAgent.State == engine.AgentComplete || v.selectedAgent.State == engine.AgentKilled) {
					v.showMessageInput = true
					v.messageInput = ""
					v.focus = focusInput
					v.recalcLayout()
				}
				return v, nil
			}
			return v, nil
		}

		// List pane keys
		switch key {
		case "r":
			v.loading = true
			return v, func() tea.Msg {
				v.loadAgents()
				return RefreshDoneMsg{}
			}

		case "n":
			v.showNewAgent = true
			v.newAgentTask = ""
			return v, nil

		case "J":
			// Open Jira ticket picker to start an agent from a ticket
			if v.jiraPicker.IsAvailable() {
				return v, v.jiraPicker.Open()
			}
			return v, nil

		case "K":
			// Kill selected agent (capital K to not conflict with vim nav)
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				if item.State == engine.AgentRunning || item.State == engine.AgentStarting {
					v.showKillConfirm = true
					v.killAgentID = item.ID
				}
			}
			return v, nil

		case "enter":
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				v.selectAgent(item)
				v.focus = focusOutput
			}
			return v, nil

		case "tab":
			if v.selectedAgent != nil {
				v.focus = focusOutput
				return v, nil
			}

		case "d", "esc":
			if key == "esc" && v.filter.IsActive() {
				v.filter.Update(msg)
				return v, nil
			}
			if v.selectedAgent != nil {
				v.deselectAgent()
				return v, nil
			}

		case "c":
			if v.engine != nil {
				if item, idx := v.filter.SelectedItem(); idx >= 0 {
					if item.State != engine.AgentRunning && item.State != engine.AgentStarting {
						v.engine.RemoveAgent(item.ID)
						if v.selectedAgent != nil && v.selectedAgent.ID == item.ID {
							v.deselectAgent()
						}
						v.loadAgents()
					}
				}
			}
			return v, nil

		case "/":
			if v.filter != nil {
				v.filter.Update(msg)
			}

		case "j", "down", "k", "up", "g", "G":
			v.filter.Update(msg)
			v.syncPreview()
			return v, nil

		case "pgdown", "ctrl+d":
			if v.selectedAgent != nil {
				v.outputAutoScroll = false
				v.outputViewport.HalfViewDown()
				return v, nil
			}

		case "pgup", "ctrl+u":
			if v.selectedAgent != nil {
				v.outputAutoScroll = false
				v.outputViewport.HalfViewUp()
				return v, nil
			}
		}

		// Pass remaining keys to filter
		if v.filter != nil {
			v.filter.Update(msg)
			v.syncPreview()
		}

	case AgentCreatedMsg:
		if msg.Err != nil {
			v.err = msg.Err
		} else {
			v.loadAgents()
			for _, a := range v.agents {
				if a.ID == msg.ID {
					v.selectAgent(a)
					break
				}
			}
		}

	case RefreshDoneMsg:
		v.loading = false

	case tea.WindowSizeMsg:
		v.width = msg.Width
		v.height = msg.Height
		v.recalcLayout()
		if v.jiraPicker != nil {
			v.jiraPicker.SetSize(msg.Width, msg.Height)
		}

	case tea.MouseMsg:
		if v.filter != nil {
			if v.filter.HandleMouse(msg) {
				return v, nil
			}
		}

	case StreamTickMsg:
		v.loadAgents()
		return v, v.streamTickCmd()
	}

	return v, nil
}

// handleKillConfirm handles key events during kill confirmation.
func (v *AgentView) handleKillConfirm(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "enter":
		if v.engine != nil && v.killAgentID != "" {
			v.engine.KillAgent(v.killAgentID)
			v.loadAgents()
		}
		v.showKillConfirm = false
		v.killAgentID = ""
	case "n", "esc":
		v.showKillConfirm = false
		v.killAgentID = ""
	}
	return nil
}

// handleNewAgentInput handles key events during new agent task input.
func (v *AgentView) handleNewAgentInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		task := v.newAgentTask
		v.showNewAgent = false
		v.newAgentTask = ""
		if task != "" && v.engine != nil {
			eng := v.engine
			repoPath := v.repoPath
			ctxFiles := v.contextFiles
			return func() tea.Msg {
				id, err := eng.StartAgent(repoPath, task, engine.AgentOptions{
					ContextFiles: ctxFiles,
					SmartRoute:   true,
					UseWorktree:  true,
				})
				return AgentCreatedMsg{ID: id, Err: err}
			}
		}
	case "esc":
		v.showNewAgent = false
		v.newAgentTask = ""
	case "ctrl+w":
		v.newAgentTask = components.DeleteWordEnd(v.newAgentTask)
	default:
		if msg.Paste && len(msg.Runes) > 0 {
			v.newAgentTask += string(msg.Runes)
		} else if len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r >= 32 && r <= 126 {
				v.newAgentTask += string(r)
			}
		} else if msg.String() == "backspace" && len(v.newAgentTask) > 0 {
			v.newAgentTask = v.newAgentTask[:len(v.newAgentTask)-1]
		}
	}
	return nil
}

// handleMessageInput handles key events during message input to a running agent.
func (v *AgentView) handleMessageInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		if v.messageInput != "" && v.engine != nil && v.selectedAgent != nil {
			err := v.engine.SendInput(v.selectedAgent.ID, v.messageInput)
			if err != nil {
				v.err = fmt.Errorf("send input: %w", err)
			}
		}
		v.showMessageInput = false
		v.messageInput = ""
		v.focus = focusOutput
		v.recalcLayout()
	case "esc":
		v.showMessageInput = false
		v.messageInput = ""
		v.focus = focusOutput
		v.recalcLayout()
	case "ctrl+w":
		v.messageInput = components.DeleteWordEnd(v.messageInput)
	default:
		if msg.Paste && len(msg.Runes) > 0 {
			v.messageInput += string(msg.Runes)
		} else if len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r >= 32 && r <= 126 {
				v.messageInput += string(r)
			}
		} else if msg.String() == "backspace" && len(v.messageInput) > 0 {
			v.messageInput = v.messageInput[:len(v.messageInput)-1]
		}
	}
	return nil
}

// renderAgentItem renders a single agent item in the list.
func (v *AgentView) renderAgentItem(agent AgentInfo, index int, selected bool) string {
	th := theme.GetTheme()

	var statusIcon string
	var statusStyle lipgloss.Style

	switch agent.State {
	case engine.AgentIdle:
		statusIcon = "○"
		statusStyle = th.MutedTextStyle
	case engine.AgentRouting:
		statusIcon = "◌"
		statusStyle = lipgloss.NewStyle().Foreground(th.Info)
	case engine.AgentStarting:
		statusIcon = "◐"
		statusStyle = lipgloss.NewStyle().Foreground(th.Info)
	case engine.AgentRunning:
		statusIcon = "●"
		statusStyle = lipgloss.NewStyle().Foreground(th.Info)
	case engine.AgentComplete:
		statusIcon = "✓"
		statusStyle = lipgloss.NewStyle().Foreground(th.Info)
	case engine.AgentError:
		statusIcon = "✗"
		statusStyle = th.DashboardErrorStyle
	case engine.AgentKilled:
		statusIcon = "⊘"
		statusStyle = th.MutedTextStyle
	default:
		statusIcon = "?"
		statusStyle = th.MutedTextStyle
	}

	prefix := "  "
	if selected {
		prefix = " >"
	}

	var line strings.Builder
	line.WriteString(fmt.Sprintf("%s%s ", prefix, statusStyle.Render(statusIcon)))

	shortID := agent.ID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	line.WriteString(th.BranchStyle.Render(shortID))

	displayText := agent.Summary
	if displayText == "" {
		displayText = agent.Task
	}
	maxTask := v.width - 40 // dynamic based on terminal width
	if maxTask < 15 {
		maxTask = 15
	}
	if len(displayText) > maxTask {
		displayText = displayText[:maxTask-3] + "..."
	}
	line.WriteString(fmt.Sprintf(" %s", th.StatsStyle.Render(displayText)))

	// Time info
	if agent.State == engine.AgentRunning || agent.State == engine.AgentStarting {
		if agent.StartedAt != nil {
			elapsed := time.Since(*agent.StartedAt)
			line.WriteString(th.MutedTextStyle.Render(fmt.Sprintf(" %s", formatDuration(elapsed))))
		}
	} else if agent.EndedAt != nil && agent.StartedAt != nil {
		duration := agent.EndedAt.Sub(*agent.StartedAt)
		line.WriteString(th.MutedTextStyle.Render(fmt.Sprintf(" %s", formatDuration(duration))))
	}

	stateLabel := agent.State.String()
	if agent.State == engine.AgentRunning {
		stateLabel = "running"
	} else if agent.State == engine.AgentComplete {
		stateLabel = "done"
	}
	line.WriteString(fmt.Sprintf(" %s", statusStyle.Render(stateLabel)))

	// Show merge result for worktree-isolated agents
	if agent.MergeResult != "" {
		var mergeIcon string
		switch agent.MergeResult {
		case "merged":
			mergeIcon = th.DashboardAccentStyle.Render(" ⤵merged")
		case "conflict":
			mergeIcon = th.DashboardErrorStyle.Render(" ⤵conflict")
		case "no-changes":
			mergeIcon = th.MutedTextStyle.Render(" ⤵no-changes")
		default:
			mergeIcon = th.DashboardErrorStyle.Render(" ⤵" + agent.MergeResult)
		}
		line.WriteString(mergeIcon)
	}

	return line.String()
}

// formatDuration formats a duration concisely.
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

// View renders the agent console.
func (v *AgentView) View() string {
	th := theme.GetTheme()
	var s strings.Builder

	// Stats line
	if v.engine != nil {
		stats := v.engine.Stats()
		s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" Agents: %d/%d active  %d done  %d errors",
			stats.Active, stats.MaxAgents, stats.Completed, stats.Errored)))
	}

	// Help hint
	if !v.showNewAgent && !v.showKillConfirm && !v.jiraPicker.IsOpen() && v.jiraConfirmIssue == nil {
		s.WriteString("  ")
		jiraHint := ""
		if v.jiraPicker.IsAvailable() {
			jiraHint = "  J:jira"
		}
		s.WriteString(th.Help.Render("n:new  K:kill  enter:view  d:close  c:clear  r:refresh" + jiraHint))
	}
	s.WriteString("\n")

	// Divider
	divider := strings.Repeat("─", v.width)

	// Jira picker overlay
	if v.jiraPicker.IsOpen() {
		s.WriteString("\n")
		s.WriteString(v.jiraPicker.View())
		return s.String()
	}

	// Jira confirm modal
	if v.jiraConfirmIssue != nil {
		issue := v.jiraConfirmIssue
		branch := issueToBranchName(issue)
		input := v.jiraExtraMsg + "█"
		s.WriteString("\n")
		s.WriteString(renderModal("Start Agent from Jira Ticket", []string{
			"",
			fmt.Sprintf("  Ticket:  %s — %s", issue.Key, issue.Summary),
			fmt.Sprintf("  Branch:  %s", branch),
			fmt.Sprintf("  Type:    %s  Priority: %s", issue.Type, issue.Priority),
			"",
			"  Custom instructions (optional):",
			"  " + input,
			"",
			"  Enter: Start   Esc: Cancel",
		}, modalWidth(v.width)))
		return s.String()
	}

	// Agent list pane
	s.WriteString(v.filter.View())

	// New agent input modal (above output pane)
	if v.showNewAgent {
		s.WriteString("\n")
		boxWidth := v.width - 4
		if boxWidth < 30 {
			boxWidth = 30
		}
		innerWidth := boxWidth - 4

		s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" ┌%s┐", strings.Repeat("─", boxWidth-2))))
		s.WriteString("\n")

		taskRunes := []rune("Task: " + v.newAgentTask + "█")
		for len(taskRunes) > 0 {
			chunk := taskRunes
			if len(chunk) > innerWidth {
				chunk = taskRunes[:innerWidth]
				taskRunes = taskRunes[innerWidth:]
			} else {
				taskRunes = nil
			}
			s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" │ %-*s │", innerWidth, string(chunk))))
			s.WriteString("\n")
		}

		s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" │ %-*s │", innerWidth, "Enter: start  Esc: cancel")))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" └%s┘", strings.Repeat("─", boxWidth-2))))
		s.WriteString("\n")
	}

	// Output pane (when agent selected)
	if v.selectedAgent != nil {
		s.WriteString("\n")
		s.WriteString(th.BorderStyle.Render(divider))
		s.WriteString("\n")

		// Output pane header
		header := fmt.Sprintf(" %s", v.selectedAgent.ID)
		header += fmt.Sprintf("  %s", v.selectedAgent.State.String())

		scrollPct := ""
		if v.outputViewport.TotalLineCount() > v.outputViewport.Height {
			pct := v.outputViewport.ScrollPercent() * 100
			scrollPct = fmt.Sprintf(" %d%%", int(pct))
		}

		focusIndicator := " "
		if v.focus == focusOutput {
			focusIndicator = lipgloss.NewStyle().Foreground(th.Accent).Bold(true).Render(">")
		}

		s.WriteString(fmt.Sprintf("%s%s%s\n",
			focusIndicator,
			th.DashboardTitle.Render(header),
			th.MutedTextStyle.Render(scrollPct),
		))

		// Viewport content
		if len(v.outputEntries) == 0 {
			s.WriteString(th.MutedTextStyle.Render(" Waiting for output..."))
			s.WriteString("\n")
		} else {
			s.WriteString(v.outputViewport.View())
			s.WriteString("\n")
		}

		// Message input modal
		if v.showMessageInput {
			msgLines := wrapText("Message: "+v.messageInput+"█", modalWidth(v.width)-4)
			msgLines = append(msgLines, "", "Enter: send   Esc: cancel")
			s.WriteString(renderModal("Send Message", msgLines, modalWidth(v.width)))
		}

		// Output pane hint
		if v.showMessageInput {
			// hint already inside the modal
		} else if v.focus == focusOutput {
			hint := " j/k:scroll  g/G:top/bottom  ctrl+d/u:page  tab:list  esc:close"
			if v.selectedAgent != nil &&
				(v.selectedAgent.State == engine.AgentRunning || v.selectedAgent.State == engine.AgentStarting || v.selectedAgent.State == engine.AgentComplete || v.selectedAgent.State == engine.AgentKilled) {
				hint += "  i:send message"
			}
			s.WriteString(th.Help.Render(hint))
		} else {
			s.WriteString(th.Help.Render(" tab:focus output  esc/d:close"))
		}
	}

	// Kill confirmation modal
	if v.showKillConfirm {
		s.WriteString("\n")
		shortID := v.killAgentID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" Kill agent %s? (y/n) ", shortID)))
	}

	// Error display
	if v.err != nil {
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" Error: %v", v.err)))
	}

	return s.String()
}

// ShortHelp returns a short help string.
func (v *AgentView) ShortHelp() string {
	return "n:new  K:kill  enter:view  d:close  c:clear  r:refresh"
}

// SetSize updates the view dimensions.
func (v *AgentView) SetSize(width, height int) {
	v.width = width
	v.height = height
	v.recalcLayout()
}

// GetRepoPath returns the repository path.
func (v *AgentView) GetRepoPath() string {
	return v.repoPath
}

// Refresh reloads agent data.
func (v *AgentView) Refresh() error {
	v.loadAgents()
	return v.err
}

// CapturesInput returns true when the view is in an input mode.
func (v *AgentView) CapturesInput() bool {
	return v.showNewAgent || v.showKillConfirm || v.showMessageInput || v.focus == focusOutput ||
		v.jiraPicker.IsOpen() || v.jiraConfirmIssue != nil
}

// CapturesKey returns true for keys the agent view needs when the output pane is visible.
// This lets Tab switch focus between list and output instead of cycling views.
func (v *AgentView) CapturesKey(key string) bool {
	if v.selectedAgent != nil && key == "tab" {
		return true
	}
	return false
}

// KeyBindings returns the keybindings for this view.
func (v *AgentView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "r", Description: "Refresh agent list"},
		{Key: "n", Description: "Start new agent task"},
		{Key: "K", Description: "Kill selected agent"},
		{Key: "Enter", Description: "View agent output"},
		{Key: "d/Esc", Description: "Close output pane"},
		{Key: "c", Description: "Clear stopped agents"},
		{Key: "Tab", Description: "Switch focus between list and output"},
		{Key: "i", Description: "Send message to running agent"},
		{Key: "/", Description: "Search agents"},
		{Key: "j/k", Description: "Navigate"},
	}
}

// handleJiraPickerKey delegates key events to the Jira picker.
func (v *AgentView) handleJiraPickerKey(msg tea.KeyMsg) tea.Cmd {
	cmd, done, confirmed, issue := v.jiraPicker.HandleKey(msg)
	if done && confirmed && issue != nil {
		v.jiraConfirmIssue = issue
		v.jiraExtraMsg = ""
	}
	return cmd
}

// handleJiraAgentConfirm handles input in the Jira agent confirmation modal.
func (v *AgentView) handleJiraAgentConfirm(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		issue := v.jiraConfirmIssue
		extraMsg := v.jiraExtraMsg
		v.jiraConfirmIssue = nil
		v.jiraExtraMsg = ""
		return v.startAgentFromJira(issue, extraMsg)
	case "esc":
		v.jiraConfirmIssue = nil
		v.jiraExtraMsg = ""
	case "ctrl+w":
		v.jiraExtraMsg = components.DeleteWordEnd(v.jiraExtraMsg)
	case "backspace":
		if len(v.jiraExtraMsg) > 0 {
			v.jiraExtraMsg = v.jiraExtraMsg[:len(v.jiraExtraMsg)-1]
		}
	default:
		if msg.Paste && len(msg.Runes) > 0 {
			v.jiraExtraMsg += string(msg.Runes)
		} else if len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r >= 32 {
				v.jiraExtraMsg += string(r)
			}
		}
	}
	return nil
}

// startAgentFromJira starts a new agent for the given Jira ticket using a worktree.
func (v *AgentView) startAgentFromJira(issue *jira.Issue, extraMsg string) tea.Cmd {
	if issue == nil || v.engine == nil {
		return nil
	}
	eng := v.engine
	repoPath := v.repoPath
	ctxFiles := v.contextFiles
	agentPrompt := buildJiraAgentPrompt(issue, extraMsg)

	return func() tea.Msg {
		id, err := eng.StartAgent(repoPath, agentPrompt, engine.AgentOptions{
			ContextFiles: ctxFiles,
			SmartRoute:   true,
			UseWorktree:  true,
		})
		return AgentCreatedMsg{ID: id, Err: err}
	}
}

// StreamTickMsg is sent periodically to refresh streaming output.
type StreamTickMsg struct{}

// AgentCreatedMsg is sent when a new agent has been started (or failed to start).
type AgentCreatedMsg struct {
	ID  string
	Err error
}
