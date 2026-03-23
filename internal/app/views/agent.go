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
			// Select agent to view output
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				v.selectedAgent = &item
				v.outputOffset = 0
				v.refreshSelectedAgentOutput()
			}
			return v, nil

		case "d":
			// Deselect agent (clear detail view)
			v.selectedAgent = nil
			v.outputEntries = nil
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
			}

		case "j", "down":
			// Move down in list
			v.filter.Update(msg)
			// Clear selection when navigating
			if v.selectedAgent != nil {
				v.selectedAgent = nil
				v.outputEntries = nil
			}

		case "g":
			// Go to top (vim-style)
			v.filter.Update(msg)

		case "G":
			// Go to bottom (vim-style)
			v.filter.Update(msg)
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
			v.filter.SetHeight(msg.Height)
		}

	case tea.MouseMsg:
		// Handle mouse events for the filter/list
		if v.filter != nil {
			if v.filter.HandleMouse(msg) {
				return v, nil
			}
		}

	case StreamTickMsg:
		// Periodic refresh for streaming output
		if v.selectedAgent != nil {
			v.refreshSelectedAgentOutput()
		}
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

	// Agent ID and status
	namePrefix := "  "
	if selected {
		namePrefix = " >"
	}

	var line strings.Builder
	line.WriteString(fmt.Sprintf("%s%s ", namePrefix, statusStyle.Render(statusIcon)))

	// Agent ID (short)
	shortID := agent.ID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	line.WriteString(th.BranchStyle.Render(shortID))

	// Task (truncated)
	task := agent.Task
	if len(task) > 40 {
		task = task[:37] + "..."
	}
	line.WriteString(fmt.Sprintf(" %s", th.StatsStyle.Render(task)))

	// Elapsed time for running agents
	if agent.State == engine.AgentRunning || agent.State == engine.AgentStarting {
		if agent.StartedAt != nil {
			elapsed := time.Since(*agent.StartedAt)
			line.WriteString(th.MutedTextStyle.Render(fmt.Sprintf(" [%s]", elapsed.Round(time.Second))))
		}
	}

	// Duration for completed agents
	if agent.EndedAt != nil && agent.StartedAt != nil {
		duration := agent.EndedAt.Sub(*agent.StartedAt)
		line.WriteString(th.MutedTextStyle.Render(fmt.Sprintf(" [%s]", duration.Round(time.Second))))
	}

	// State label
	stateLabel := agent.State.String()
	stateStyle := statusStyle
	if agent.State == engine.AgentRunning {
		stateLabel = "running"
		stateStyle = lipgloss.NewStyle().Foreground(th.Info)
	} else if agent.State == engine.AgentComplete {
		stateLabel = "done"
		stateStyle = lipgloss.NewStyle().Foreground(th.Info)
	}
	line.WriteString(fmt.Sprintf(" %s", stateStyle.Render(stateLabel)))

	return line.String()
}

// View renders the agent console.
func (v *AgentView) View() string {
	th := theme.GetTheme()

	var s strings.Builder

	// Header
	s.WriteString(th.DashboardTitle.Render(" Agent Console "))
	s.WriteString("\n\n")

	// Engine stats
	if v.engine != nil {
		stats := v.engine.Stats()
		s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" Agents: %d/%d active | %d completed | %d errors ",
			stats.Active, stats.MaxAgents, stats.Completed, stats.Errored)))
		s.WriteString("\n\n")
	}

	// New agent hint
	if !v.showNewAgent && !v.showKillConfirm {
		s.WriteString(th.Help.Render(" n: New agent | k: Kill | Enter: View output | d: Deselect | c: Clear stopped | /: Search | r: Refresh "))
		s.WriteString("\n\n")
	}

	// Agent list
	s.WriteString(v.filter.View())

	// Agent output panel
	if v.selectedAgent != nil {
		s.WriteString("\n")
		s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" Agent: %s ", v.selectedAgent.ID)))
		s.WriteString("\n\n")

		// Agent details
		s.WriteString(fmt.Sprintf(" %s %s\n", th.BranchStyle.Render("Task:"), th.StatsStyle.Render(v.selectedAgent.Task)))
		s.WriteString(fmt.Sprintf(" %s %s\n", th.BranchStyle.Render("State:"), th.StatsStyle.Render(v.selectedAgent.State.String())))

		if v.selectedAgent.StartedAt != nil {
			s.WriteString(fmt.Sprintf(" %s %s\n", th.BranchStyle.Render("Started:"), th.StatsStyle.Render(v.selectedAgent.StartedAt.Format("15:04:05"))))
		}
		if v.selectedAgent.EndedAt != nil {
			s.WriteString(fmt.Sprintf(" %s %s\n", th.BranchStyle.Render("Ended:"), th.StatsStyle.Render(v.selectedAgent.EndedAt.Format("15:04:05"))))
		}
		if v.selectedAgent.Error != "" {
			s.WriteString(fmt.Sprintf(" %s %s\n", th.BranchStyle.Render("Error:"), th.DashboardErrorStyle.Render(v.selectedAgent.Error)))
		}

		s.WriteString("\n")
		s.WriteString(th.StatsStyle.Render(" Output "))
		s.WriteString("\n")
		s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
		s.WriteString("\n")

		// Output entries
		if len(v.outputEntries) == 0 {
			s.WriteString(th.MutedTextStyle.Render(" No output yet..."))
		} else {
			for _, entry := range v.outputEntries {
				// Only show stdout and stderr, skip system messages in compact view
				if entry.Source == "system" {
					continue
				}

				content := entry.Content
				// Truncate long lines
				if len(content) > 120 {
					content = content[:117] + "..."
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
		}
	}

	// Kill confirmation modal
	if v.showKillConfirm {
		s.WriteString("\n\n")
		s.WriteString(th.DashboardErrorStyle.Render(" ┌─────────────────────────────────────────────┐"))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" │ Kill agent '%s'?  (y/n)                  │", v.killAgentID)))
		s.WriteString("\n")
		s.WriteString(th.DashboardErrorStyle.Render(" └─────────────────────────────────────────────┘"))
	}

	// New agent input modal
	if v.showNewAgent {
		s.WriteString("\n\n")
		s.WriteString(th.DashboardTitle.Render(" ┌─────────────────────────────────────────────┐"))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" │ New agent task: %s", v.newAgentTask)))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(" │ (press Enter to start, Esc to cancel)     │"))
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(" └─────────────────────────────────────────────┘"))
	}

	// Error display
	if v.err != nil {
		s.WriteString("\n\n")
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" Error: %v", v.err)))
	}

	// Footer
	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")
	s.WriteString(th.Help.Render(" n: New agent | k: Kill | Enter: View output | d: Deselect | c: Clear stopped | r: Refresh "))

	return s.String()
}

// ShortHelp returns a short help string.
func (v *AgentView) ShortHelp() string {
	return "n: New agent  k: Kill  Enter: View output  d: Deselect  r: Refresh"
}

// SetSize updates the view dimensions.
func (v *AgentView) SetSize(width, height int) {
	v.width = width
	v.height = height
	if v.filter != nil {
		v.filter.SetHeight(height)
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
func (v *AgentView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "r", Description: "Refresh agent list"},
		{Key: "n", Description: "Start new agent task"},
		{Key: "k", Description: "Kill selected agent"},
		{Key: "Enter", Description: "View selected agent output"},
		{Key: "d", Description: "Deselect / clear detail view"},
		{Key: "c", Description: "Clear stopped agents"},
		{Key: "/", Description: "Search agents"},
		{Key: "↑/k", Description: "Navigate up"},
		{Key: "↓/j", Description: "Navigate down"},
		{Key: "Esc", Description: "Cancel / Clear"},
	}
}

// StreamTickMsg is sent periodically to refresh streaming output.
type StreamTickMsg struct{}
