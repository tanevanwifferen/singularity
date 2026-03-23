package views

import (
	"fmt"
	"strings"
	"time"

	"git-frontend/internal/app/components"
	"git-frontend/internal/engine"
	"git-frontend/internal/theme"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// AgentInfo holds agent summary info for display
type AgentInfo struct {
	ID        string
	State     engine.AgentState
	Task      string
	WorkDir   string
	CreatedAt time.Time
	StartedAt *time.Time
	EndedAt   *time.Time
	ExitCode  int
	Error     string
}

// AgentView displays the agent console for server mode.
type AgentView struct {
	repoPath    string
	engine     *engine.Engine
	agents     []AgentInfo
	filter     *components.Filter[AgentInfo]
	loading    bool
	width      int
	height     int
	err        error

	// Selected agent output
	selectedAgent *AgentInfo
	outputOffset  int
	outputEntries []engine.OutputEntry
	logScrollPos  int // scroll position within the log output

	// New agent input state
	showNewAgent   bool
	newAgentTask   string
	newAgentInput  components.Filter[byte]

	// Kill confirmation state
	showKillConfirm bool
	killAgentID    string

	// Refresh ticker for streaming output
	refreshInterval time.Duration
	lastRefresh    time.Time
}

// NewAgentView creates a new agent console view.
func NewAgentView(repoPath string, eng *engine.Engine) *AgentView {
	v := &AgentView{
		repoPath:        repoPath,
		engine:          eng,
		width:           80,
		height:          24,
		refreshInterval: 500 * time.Millisecond,
	}

	// Initialize with empty agent list
	v.filter = components.NewFilter([]AgentInfo{}, v.renderAgentItem)
	v.filter.SetHeight(v.height)

	return v
}

// SetEngine sets the agent engine (allows late binding)
func (v *AgentView) SetEngine(eng *engine.Engine) {
	v.engine = eng
}

// Init initializes the agent view.
func (v *AgentView) Init() tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		v.loadAgents()
		return RefreshDoneMsg{}
	}
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
		info := AgentInfo{
			ID:        a.ID,
			State:     a.State,
			Task:      a.Task,
			WorkDir:   a.WorkDir,
			CreatedAt: a.CreatedAt,
			StartedAt: a.StartedAt,
			EndedAt:   a.EndedAt,
			ExitCode:  a.ExitCode,
			Error:     a.Error,
		}
		v.agents = append(v.agents, info)
	}

	// Update filter with new list
	v.filter.SetItems(v.agents)

	// Update selected agent output if one is selected
	if v.selectedAgent != nil {
		v.refreshSelectedAgentOutput()
	}

	v.loading = false
}

// refreshSelectedAgentOutput updates output for the currently selected agent.
func (v *AgentView) refreshSelectedAgentOutput() {
	if v.selectedAgent == nil || v.engine == nil {
		return
	}

	entries, err := v.engine.GetOutputEntries(v.selectedAgent.ID, v.outputOffset)
	if err != nil {
		return
	}
	v.outputEntries = entries
}

// selectCurrentAgent sets the selected agent to whatever is highlighted in the list.
func (v *AgentView) selectCurrentAgent() {
	if item, idx := v.filter.SelectedItem(); idx >= 0 {
		v.selectedAgent = &item
		v.outputOffset = 0
		v.logScrollPos = 0
		v.refreshSelectedAgentOutput()
	}
}

// Update handles update events.
func (v *AgentView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle kill confirmation first
		if v.showKillConfirm {
			return v, v.handleKillConfirm(msg)
		}

		// Handle new agent input
		if v.showNewAgent {
			return v, v.handleNewAgentInput(msg)
		}

		// Main view keys
		switch msg.String() {
		case "r":
			v.loading = true
			return v, func() tea.Msg {
				v.loadAgents()
				return RefreshDoneMsg{}
			}

		case "n":
			// Start new agent - show task input
			v.showNewAgent = true
			v.newAgentTask = ""
			v.newAgentInput = *components.NewFilter([]byte{}, func(b byte, i int, s bool) string {
				return string(b)
			})
			return v, nil

		case "k":
			// Kill selected agent
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				if item.State == engine.AgentRunning || item.State == engine.AgentStarting {
					v.showKillConfirm = true
					v.killAgentID = item.ID
				}
			}
			return v, nil

		case "enter":
			// Select agent to view output (also auto-scroll to bottom)
			v.selectCurrentAgent()
			if v.selectedAgent != nil {
				v.logScrollPos = v.logLineCount() // jump to bottom
			}
			return v, nil

		case "d":
			// Deselect agent (clear detail view)
			v.selectedAgent = nil
			v.outputEntries = nil
			v.logScrollPos = 0
			return v, nil

		case "c":
			// Clear completed/stopped agents from view
			if v.engine != nil {
				for _, a := range v.agents {
					if a.State != engine.AgentRunning && a.State != engine.AgentStarting {
						v.engine.RemoveAgent(a.ID)
					}
				}
				v.loadAgents()
			}
			return v, nil

		case "/":
			// Activate filter/search mode
			if v.filter != nil {
				v.filter.Update(msg)
			}

		case "esc":
			// Clear filter if active, otherwise deselect
			if v.filter.IsActive() {
				v.filter.Update(msg)
			} else if v.selectedAgent != nil {
				v.selectedAgent = nil
				v.outputEntries = nil
				v.logScrollPos = 0
			}

		case "j", "down", "up":
			v.filter.Update(msg)
			v.selectCurrentAgent()
			return v, nil

		case "g":
			v.filter.Update(msg)
			v.selectCurrentAgent()

		case "G":
			v.filter.Update(msg)
			v.selectCurrentAgent()

		case "ctrl+d":
			// Scroll log down
			if v.selectedAgent != nil {
				v.logScrollPos += 10
				max := v.logLineCount()
				if v.logScrollPos > max {
					v.logScrollPos = max
				}
			}
			return v, nil

		case "ctrl+u":
			// Scroll log up
			if v.selectedAgent != nil {
				v.logScrollPos -= 10
				if v.logScrollPos < 0 {
					v.logScrollPos = 0
				}
			}
			return v, nil
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
			v.filter.SetHeight(msg.Height - 6) // account for header area
		}

	case tea.MouseMsg:
		// Handle mouse events for the filter/list
		if v.filter != nil {
			if v.filter.HandleMouse(msg) {
				v.selectCurrentAgent()
				return v, nil
			}
		}

	case StreamTickMsg:
		// Periodic refresh for streaming output
		if v.selectedAgent != nil {
			oldCount := len(v.outputEntries)
			v.refreshSelectedAgentOutput()
			// Auto-scroll to bottom if we were already at the bottom
			if v.logScrollPos >= v.logLineCountFor(oldCount) {
				v.logScrollPos = v.logLineCount()
			}
		}
		// Also refresh the agent list to update states
		v.loadAgents()
	}

	return v, nil
}

// logLineCount returns the number of displayable log lines.
func (v *AgentView) logLineCount() int {
	return v.logLineCountFor(len(v.outputEntries))
}

// logLineCountFor returns the displayable log line count for a given entry count.
func (v *AgentView) logLineCountFor(entryCount int) int {
	count := 0
	for i := 0; i < entryCount && i < len(v.outputEntries); i++ {
		if v.outputEntries[i].Source != "system" {
			count++
		}
	}
	return count
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
		if v.newAgentTask != "" && v.engine != nil {
			_, err := v.engine.StartAgent(v.repoPath, v.newAgentTask, engine.AgentOptions{})
			if err != nil {
				v.err = fmt.Errorf("failed to start agent: %w", err)
			} else {
				v.loadAgents()
			}
		}
		v.showNewAgent = false
		v.newAgentTask = ""
	case "esc":
		v.showNewAgent = false
		v.newAgentTask = ""
	default:
		// Handle text input for task description
		if len(msg.Runes) == 1 {
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

// renderAgentItem renders a single agent item in the list.
func (v *AgentView) renderAgentItem(agent AgentInfo, index int, selected bool) string {
	th := theme.GetTheme()

	// Status icon and color based on state
	var statusIcon string
	var statusStyle lipgloss.Style

	switch agent.State {
	case engine.AgentIdle:
		statusIcon = "○"
		statusStyle = th.MutedTextStyle
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

	namePrefix := "  "
	if selected {
		namePrefix = " >"
	}

	var line strings.Builder
	line.WriteString(fmt.Sprintf("%s%s ", namePrefix, statusStyle.Render(statusIcon)))

	// Task (truncated to fit left panel)
	task := agent.Task
	if len(task) > 25 {
		task = task[:22] + "..."
	}
	line.WriteString(th.StatsStyle.Render(task))

	// Elapsed time for running agents
	if agent.State == engine.AgentRunning || agent.State == engine.AgentStarting {
		if agent.StartedAt != nil {
			elapsed := time.Since(*agent.StartedAt)
			line.WriteString(th.MutedTextStyle.Render(fmt.Sprintf(" %s", elapsed.Round(time.Second))))
		}
	}

	// State label
	stateLabel := ""
	switch agent.State {
	case engine.AgentRunning:
		stateLabel = "running"
	case engine.AgentComplete:
		stateLabel = "done"
	case engine.AgentError:
		stateLabel = "error"
	case engine.AgentKilled:
		stateLabel = "killed"
	case engine.AgentStarting:
		stateLabel = "starting"
	}
	if stateLabel != "" {
		line.WriteString(fmt.Sprintf(" %s", statusStyle.Render(stateLabel)))
	}

	return line.String()
}

// View renders the agent console with split-pane layout.
func (v *AgentView) View() string {
	th := theme.GetTheme()

	// Calculate panel widths
	leftWidth := v.width * 35 / 100 // 35% for agent list
	if leftWidth < 30 {
		leftWidth = 30
	}
	if leftWidth > 60 {
		leftWidth = 60
	}
	rightWidth := v.width - leftWidth - 3 // 3 for divider
	if rightWidth < 20 {
		rightWidth = 20
	}

	var s strings.Builder

	// Header
	s.WriteString(th.DashboardTitle.Render(" Agent Console "))

	// Engine stats inline
	if v.engine != nil {
		stats := v.engine.Stats()
		s.WriteString(th.StatsStyle.Render(fmt.Sprintf("  %d/%d active  %d done  %d err",
			stats.Active, stats.MaxAgents, stats.Completed, stats.Errored)))
	}
	s.WriteString("\n")

	// Build split panels
	leftPanel := v.renderLeftPanel(leftWidth)
	rightPanel := v.renderRightPanel(rightWidth)

	// Combine panels side by side
	s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel))

	// Kill confirmation modal (overlay)
	if v.showKillConfirm {
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" Kill agent '%s'?  (y/n) ", v.killAgentID)))
	}

	// New agent input modal (overlay)
	if v.showNewAgent {
		boxWidth := v.width - 4
		if boxWidth < 30 {
			boxWidth = 30
		}
		innerWidth := boxWidth - 4

		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" ┌%s┐", strings.Repeat("─", boxWidth-2))))
		s.WriteString("\n")

		prompt := "New agent task: "
		taskText := prompt + v.newAgentTask + "█"
		for len(taskText) > 0 {
			line := taskText
			if len(line) > innerWidth {
				line = taskText[:innerWidth]
				taskText = taskText[innerWidth:]
			} else {
				taskText = ""
			}
			s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" │ %-*s │", innerWidth, line)))
			s.WriteString("\n")
		}

		hint := "(press Enter to start, Esc to cancel)"
		s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" │ %-*s │", innerWidth, hint)))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" └%s┘", strings.Repeat("─", boxWidth-2))))
	}

	// Error display
	if v.err != nil {
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" Error: %v", v.err)))
	}

	return s.String()
}

// renderLeftPanel renders the agent list panel.
func (v *AgentView) renderLeftPanel(width int) string {
	th := theme.GetTheme()
	var s strings.Builder

	// Panel header
	s.WriteString(th.DashboardTitle.Render(" Agents "))
	s.WriteString("\n")

	dividerLen := width - 2
	if dividerLen < 0 {
		dividerLen = 0
	}
	s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %s", strings.Repeat("─", dividerLen))))
	s.WriteString("\n")

	if v.loading {
		s.WriteString(th.MutedTextStyle.Render(" Loading..."))
		padHeight := v.height - 8
		for i := 0; i < padHeight; i++ {
			s.WriteString("\n")
		}
		return lipgloss.NewStyle().Width(width).Render(s.String())
	}

	if len(v.agents) == 0 {
		s.WriteString(th.MutedTextStyle.Render(" No agents"))
		s.WriteString("\n")
		s.WriteString(th.Help.Render(" Press n to start one"))
		padHeight := v.height - 9
		for i := 0; i < padHeight; i++ {
			s.WriteString("\n")
		}
		return lipgloss.NewStyle().Width(width).Render(s.String())
	}

	// Agent list via filter
	s.WriteString(v.filter.View())

	// Help at bottom
	s.WriteString("\n")
	s.WriteString(th.Help.Render(" n:new k:kill c:clear /:search"))

	return lipgloss.NewStyle().Width(width).Render(s.String())
}

// renderRightPanel renders the agent detail and log output panel.
func (v *AgentView) renderRightPanel(width int) string {
	th := theme.GetTheme()
	var s strings.Builder

	// Divider
	dividerStyle := lipgloss.NewStyle().
		Foreground(th.Border).
		SetString(" │ ")

	if v.selectedAgent == nil {
		// Empty state
		s.WriteString(th.DashboardTitle.Render(" Details "))
		s.WriteString("\n")
		dividerLen := width - 2
		if dividerLen < 0 {
			dividerLen = 0
		}
		s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %s", strings.Repeat("─", dividerLen))))
		s.WriteString("\n\n")
		s.WriteString(th.MutedTextStyle.Render(" Select an agent to view details"))
		s.WriteString("\n")
		s.WriteString(th.MutedTextStyle.Render(" Use ↑↓ to navigate, Enter to select"))

		// Pad to fill height
		padHeight := v.height - 10
		for i := 0; i < padHeight; i++ {
			s.WriteString("\n")
		}

		panel := lipgloss.NewStyle().Width(width).Render(s.String())
		return dividerStyle.Render("│") + " " + panel
	}

	agent := v.selectedAgent

	// Panel header with agent ID
	shortID := agent.ID
	if len(shortID) > 16 {
		shortID = shortID[:16]
	}
	s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" Agent: %s ", shortID)))
	s.WriteString("\n")

	dividerLen := width - 2
	if dividerLen < 0 {
		dividerLen = 0
	}
	s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %s", strings.Repeat("─", dividerLen))))
	s.WriteString("\n")

	// Agent metadata
	task := agent.Task
	maxTaskLen := width - 8
	if maxTaskLen > 0 && len(task) > maxTaskLen {
		task = task[:maxTaskLen-3] + "..."
	}
	s.WriteString(fmt.Sprintf(" %s %s\n", th.BranchStyle.Render("Task:"), th.StatsStyle.Render(task)))

	// State with icon
	var stateIcon string
	var stateStyle lipgloss.Style
	switch agent.State {
	case engine.AgentRunning:
		stateIcon = "●"
		stateStyle = lipgloss.NewStyle().Foreground(th.Info)
	case engine.AgentStarting:
		stateIcon = "◐"
		stateStyle = lipgloss.NewStyle().Foreground(th.Info)
	case engine.AgentComplete:
		stateIcon = "✓"
		stateStyle = lipgloss.NewStyle().Foreground(th.Info)
	case engine.AgentError:
		stateIcon = "✗"
		stateStyle = th.DashboardErrorStyle
	case engine.AgentKilled:
		stateIcon = "⊘"
		stateStyle = th.MutedTextStyle
	default:
		stateIcon = "○"
		stateStyle = th.MutedTextStyle
	}
	s.WriteString(fmt.Sprintf(" %s %s %s\n", th.BranchStyle.Render("State:"), stateStyle.Render(stateIcon), stateStyle.Render(agent.State.String())))

	if agent.StartedAt != nil {
		elapsed := ""
		if agent.EndedAt != nil {
			duration := agent.EndedAt.Sub(*agent.StartedAt)
			elapsed = fmt.Sprintf(" (%s)", duration.Round(time.Second))
		} else if agent.State == engine.AgentRunning {
			elapsed = fmt.Sprintf(" (%s)", time.Since(*agent.StartedAt).Round(time.Second))
		}
		s.WriteString(fmt.Sprintf(" %s %s%s\n", th.BranchStyle.Render("Started:"), th.StatsStyle.Render(agent.StartedAt.Format("15:04:05")), th.MutedTextStyle.Render(elapsed)))
	}
	if agent.Error != "" {
		errText := agent.Error
		if len(errText) > width-10 {
			errText = errText[:width-13] + "..."
		}
		s.WriteString(fmt.Sprintf(" %s %s\n", th.BranchStyle.Render("Error:"), th.DashboardErrorStyle.Render(errText)))
	}

	// Log output section
	s.WriteString("\n")
	logHeader := " Log Output "
	if agent.State == engine.AgentRunning {
		logHeader = " Log Output (live) "
	}
	s.WriteString(th.DashboardTitle.Render(logHeader))
	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %s", strings.Repeat("─", dividerLen))))
	s.WriteString("\n")

	// Calculate available lines for log output
	// Header(1) + divider(1) + task(1) + state(1) + started(1) + blank(1) + logheader(1) + logdivider(1) + scrollhint(1) = ~9 lines of chrome
	logHeight := v.height - 14
	if logHeight < 3 {
		logHeight = 3
	}

	// Collect displayable entries (skip system)
	var displayEntries []engine.OutputEntry
	for _, entry := range v.outputEntries {
		if entry.Source != "system" {
			displayEntries = append(displayEntries, entry)
		}
	}

	if len(displayEntries) == 0 {
		s.WriteString(th.MutedTextStyle.Render(" No output yet..."))
		s.WriteString("\n")
		if agent.State == engine.AgentRunning || agent.State == engine.AgentStarting {
			s.WriteString(th.MutedTextStyle.Render(" Waiting for output..."))
		}
		// Pad remaining
		padHeight := logHeight - 2
		for i := 0; i < padHeight; i++ {
			s.WriteString("\n")
		}
	} else {
		// Apply scroll position - show the last logHeight lines by default
		startIdx := len(displayEntries) - logHeight
		if startIdx < 0 {
			startIdx = 0
		}

		// Allow manual scroll override
		if v.logScrollPos < len(displayEntries) {
			startIdx = v.logScrollPos - logHeight
			if startIdx < 0 {
				startIdx = 0
			}
			// But don't go past the end
			if startIdx+logHeight > len(displayEntries) {
				startIdx = len(displayEntries) - logHeight
				if startIdx < 0 {
					startIdx = 0
				}
			}
		}

		endIdx := startIdx + logHeight
		if endIdx > len(displayEntries) {
			endIdx = len(displayEntries)
		}

		maxContentLen := width - 4
		if maxContentLen < 10 {
			maxContentLen = 10
		}

		for i := startIdx; i < endIdx; i++ {
			entry := displayEntries[i]
			content := entry.Content
			if len(content) > maxContentLen {
				content = content[:maxContentLen-3] + "..."
			}

			var sourceStyle lipgloss.Style
			switch entry.Source {
			case "stderr":
				sourceStyle = th.DashboardErrorStyle
			default:
				sourceStyle = th.StatsStyle
			}

			s.WriteString(fmt.Sprintf(" %s\n", sourceStyle.Render(content)))
		}

		// Pad remaining lines
		rendered := endIdx - startIdx
		for i := rendered; i < logHeight; i++ {
			s.WriteString("\n")
		}

		// Scroll indicator
		if len(displayEntries) > logHeight {
			s.WriteString(th.Help.Render(fmt.Sprintf(" %d-%d of %d lines  Ctrl+D/U: scroll", startIdx+1, endIdx, len(displayEntries))))
		}
	}

	// Bottom help
	s.WriteString("\n")
	s.WriteString(th.Help.Render(" d:close  k:kill  r:refresh"))

	panel := lipgloss.NewStyle().Width(width).Render(s.String())
	return dividerStyle.Render("│") + " " + panel
}

// ShortHelp returns a short help string.
func (v *AgentView) ShortHelp() string {
	return "n: New agent  k: Kill  ↑↓: Navigate  d: Deselect  r: Refresh"
}

// SetSize updates the view dimensions.
func (v *AgentView) SetSize(width, height int) {
	v.width = width
	v.height = height
	if v.filter != nil {
		v.filter.SetHeight(height - 6)
	}
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

// KeyBindings returns the keybindings for this view.
// CapturesInput returns true when the view is in an input mode
// (new agent task input or kill confirmation) where global keybindings
// should not intercept keystrokes.
func (v *AgentView) CapturesInput() bool {
	return v.showNewAgent || v.showKillConfirm
}

func (v *AgentView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "r", Description: "Refresh agent list"},
		{Key: "n", Description: "Start new agent task"},
		{Key: "k", Description: "Kill selected agent"},
		{Key: "Enter", Description: "View selected agent output"},
		{Key: "d", Description: "Deselect / close detail view"},
		{Key: "c", Description: "Clear stopped agents"},
		{Key: "/", Description: "Search agents"},
		{Key: "↑/↓", Description: "Navigate agents"},
		{Key: "Ctrl+D/U", Description: "Scroll log output"},
		{Key: "Esc", Description: "Cancel / Clear"},
	}
}

// StreamTickMsg is sent periodically to refresh streaming output.
type StreamTickMsg struct{}
