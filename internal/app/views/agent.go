package views

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
	"gitlab.com/tanevanwifferen1/singularity/internal/config"
	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
	"gitlab.com/tanevanwifferen1/singularity/internal/jira"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// jiraAgentMeta tracks metadata for Jira refine/create agents started from the agents view.
type jiraAgentMeta struct {
	IssueKey    string
	Mode        string // "refine" or "create"
	ActionsFile string // path to the actions JSON file
}

// savedProposalID returns the synthetic agent ID for a saved proposal by issue key.
func savedProposalID(issueKey string) string {
	return "saved:" + issueKey
}

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
	focusList agentFocus = iota
	focusOutput
	focusInput
)

// AgentView displays the agent console with a split-pane layout.
type AgentView struct {
	viewBase
	engine       *engine.Engine
	contextFiles []string // Files to inject into agent prompts
	agents       []AgentInfo
	filter       *components.Filter[AgentInfo]
	loading      bool
	err          error

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
	killConfirm components.ConfirmPrompt

	// Message input state (send to running agent stdin)
	showMessageInput bool
	messageInput     string

	// Markdown renderer (cached, recreated on width change)
	mdRenderer      *glamour.TermRenderer
	mdRendererWidth int

	// Output rebuild cache: skip expensive glamour re-render when nothing changed
	outputLastLen   int
	outputLastWidth int

	// Refresh ticker
	refreshInterval time.Duration
	lastRefresh     time.Time

	// Jira ticket picker
	jiraPicker       *JiraPickerState
	jiraConfirmIssue *jira.Issue // issue pending agent-start confirmation
	jiraExtraMsg     string      // custom instructions for the jira agent
	jiraConfirmMode  string      // "implement" (default), "refine", or "create"

	// Jira refine/create agent tracking
	jiraCfg        config.JiraConfig
	jiraClient     *jira.Client
	jiraAgentMeta  map[string]*jiraAgentMeta // agentID -> metadata
	savedProposals []string                  // issue keys for orphaned .jira-actions-*.json files
	approvalView   *ApprovalView
	approvalAgent  string // agentID of the agent whose approval is shown
}

// NewAgentView creates a new agent console view.
func NewAgentView(repoPath string, eng *engine.Engine, contextFiles ...[]string) *AgentView {
	var ctxFiles []string
	if len(contextFiles) > 0 {
		ctxFiles = contextFiles[0]
	}
	v := &AgentView{
		viewBase:         viewBase{repoPath: repoPath, width: 80, height: 24},
		engine:           eng,
		contextFiles:     ctxFiles,
		refreshInterval:  2 * time.Second,
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

// SetEngine sets the agent engine (allows late binding)
func (v *AgentView) SetEngine(eng *engine.Engine) {
	v.engine = eng
}

// SetJiraConfig wires Jira configuration so the Jira ticket picker is available.
func (v *AgentView) SetJiraConfig(cfg config.JiraConfig) {
	v.jiraCfg = cfg
	v.jiraClient = jira.NewClient(cfg.BaseURL, cfg.Email, cfg.APIToken)
	v.jiraAgentMeta = make(map[string]*jiraAgentMeta)
	v.jiraPicker = NewJiraPickerState(cfg)
	if v.jiraPicker != nil {
		v.jiraPicker.SetSize(v.width, v.height)
	}
}

// Init initializes the agent view.
func (v *AgentView) Init() tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		v.loadAgents()
		return RefreshDoneMsg{}
	}
}

// streamTickCmd returns a tea.Cmd that sends a StreamTickMsg after the refresh interval.
func (v *AgentView) streamTickCmd() tea.Cmd {
	return tea.Tick(v.refreshInterval, func(t time.Time) tea.Msg {
		return StreamTickMsg{}
	})
}

// AgentTickCmd schedules the next tick and loads agents asynchronously.
// Called by the app-level update loop so the tick survives view switches.
// loadAgents runs in a goroutine to avoid blocking the Bubble Tea event loop
// (glamour rendering on large outputs can take hundreds of milliseconds).
func (v *AgentView) AgentTickCmd() tea.Cmd {
	return tea.Batch(
		v.streamTickCmd(),
		func() tea.Msg {
			v.loadAgents()
			return RefreshDoneMsg{}
		},
	)
}

// AgentTickStart returns the initial tick command without loading agents.
// Used by the app-level Init to seed the tick chain.
func (v *AgentView) AgentTickStart() tea.Cmd {
	return v.streamTickCmd()
}

// LoadAgents is the public entry point for an immediate agent refresh
// without rescheduling the tick (used by WS event handlers).
func (v *AgentView) LoadAgents() {
	v.loadAgents()
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

	// Reconstruct jiraAgentMeta for agents not already tracked (e.g. after restart).
	// Refine agents have Summary "Refine: PROJ-123"; create-stories agents have
	// Summary "Create stories: PROJ-123 ...". The actions file follows the pattern
	// .jira-actions-{ISSUE_KEY}.json relative to the agent's working directory.
	for _, info := range v.agents {
		if _, known := v.jiraAgentMeta[info.ID]; known {
			continue
		}
		var issueKey, mode string
		if strings.HasPrefix(info.Summary, "Refine: ") || strings.HasPrefix(info.Summary, "Refine proposal: ") {
			rest := info.Summary
			if strings.HasPrefix(rest, "Refine proposal: ") {
				rest = strings.TrimPrefix(rest, "Refine proposal: ")
			} else {
				rest = strings.TrimPrefix(rest, "Refine: ")
			}
			issueKey = rest
			mode = "refine"
		} else if strings.HasPrefix(info.Summary, "Create stories: ") {
			rest := strings.TrimPrefix(info.Summary, "Create stories: ")
			// Summary may be "PROJ-123 — description", take just the key part
			if idx := strings.Index(rest, " "); idx > 0 {
				issueKey = rest[:idx]
			} else {
				issueKey = rest
			}
			mode = "create"
		}
		if issueKey == "" || info.WorkDir == "" {
			continue
		}
		actionsFile := filepath.Join(info.WorkDir, fmt.Sprintf(".jira-actions-%s.json", issueKey))
		if _, err := os.Stat(actionsFile); err == nil {
			v.jiraAgentMeta[info.ID] = &jiraAgentMeta{
				IssueKey:    issueKey,
				Mode:        mode,
				ActionsFile: actionsFile,
			}
		}
	}

	// Scan repo for orphaned .jira-actions-*.json files (saved proposals from prior sessions).
	// These are proposals whose agent is no longer in the engine pool.
	if v.jiraAgentMeta != nil && v.repoPath != "" {
		// Build the set of issue keys already covered by live (real) agents.
		// Exclude synthetic saved-proposal entries so they don't mask themselves on refresh.
		liveKeys := make(map[string]bool)
		for agentID, meta := range v.jiraAgentMeta {
			if !strings.HasPrefix(agentID, "saved:") {
				liveKeys[meta.IssueKey] = true
			}
		}
		// Remove stale synthetic entries from prior scans before re-populating.
		for agentID := range v.jiraAgentMeta {
			if strings.HasPrefix(agentID, "saved:") {
				delete(v.jiraAgentMeta, agentID)
			}
		}
		v.savedProposals = nil
		if entries, err := os.ReadDir(v.repoPath); err == nil {
			for _, entry := range entries {
				name := entry.Name()
				if !strings.HasPrefix(name, ".jira-actions-") || !strings.HasSuffix(name, ".json") {
					continue
				}
				issueKey := strings.TrimSuffix(strings.TrimPrefix(name, ".jira-actions-"), ".json")
				if issueKey == "" || liveKeys[issueKey] {
					continue
				}
				actionsFile := filepath.Join(v.repoPath, name)
				actions, err := jira.ParseJiraActions(actionsFile)
				if err != nil || len(actions) == 0 {
					continue
				}
				syntheticID := savedProposalID(issueKey)
				v.savedProposals = append(v.savedProposals, issueKey)
				v.jiraAgentMeta[syntheticID] = &jiraAgentMeta{
					IssueKey:    issueKey,
					Mode:        "refine",
					ActionsFile: actionsFile,
				}
				v.agents = append(v.agents, AgentInfo{
					ID:      syntheticID,
					State:   engine.AgentComplete,
					Summary: fmt.Sprintf("Saved proposal: %s (%d actions)", issueKey, len(actions)),
				})
			}
		}
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

	// Skip expensive glamour re-render when entry count and width are unchanged.
	if len(entries) == v.outputLastLen && v.width == v.outputLastWidth {
		return
	}

	v.outputEntries = entries
	v.rebuildOutputViewport()
}

// markdownRenderer returns a cached glamour renderer for the given width,
// recreating it if the width has changed.
func (v *AgentView) markdownRenderer(width int) *glamour.TermRenderer {
	if v.mdRenderer == nil || v.mdRendererWidth != width {
		r, err := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return nil
		}
		v.mdRenderer = r
		v.mdRendererWidth = width
	}
	return v.mdRenderer
}

// rebuildOutputViewport rebuilds the viewport content from output entries.
func (v *AgentView) rebuildOutputViewport() {
	th := theme.GetTheme()
	var lines []string
	w := v.width

	for _, entry := range v.outputEntries {
		switch entry.Source {
		case "text":
			if r := v.markdownRenderer(w); r != nil {
				if rendered, err := r.Render(entry.Content); err == nil {
					rendered = strings.TrimRight(rendered, "\n")
					lines = append(lines, strings.Split(rendered, "\n")...)
					break
				}
			}
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
	v.outputLastLen = len(v.outputEntries)
	v.outputLastWidth = w

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
	v.outputLastLen = -1 // force rebuild for the newly selected agent
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
		return v.handleAgentKeyMsg(msg)

	case AgentCreatedMsg:
		if msg.Err != nil {
			v.err = msg.Err
		} else {
			// Register Jira agent metadata if present
			if msg.jiraMeta != nil && v.jiraAgentMeta != nil {
				v.jiraAgentMeta[msg.ID] = msg.jiraMeta
			}
			v.loadAgents()
			for i, a := range v.agents {
				if a.ID == msg.ID {
					v.filter.SelectAt(i)
					v.selectAgent(a)
					break
				}
			}
		}

	case approvalExecDoneMsg:
		if v.approvalView != nil {
			v.approvalView.Update(msg)
		}

	case ApprovalDoneMsg:
		v.approvalView = nil
		v.approvalAgent = ""

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
		// loadAgents and rescheduling handled by app-level AgentTickCmd.
		// This case is kept so stale in-flight ticks (started before the
		// app-level loop took over) don't produce spurious no-ops.
	}

	return v, nil
}

// handleNewAgentInput handles key events during new agent task input.
func (v *AgentView) handleNewAgentInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		task := v.newAgentTask
		v.showNewAgent = false
		v.newAgentTask = ""
		v.recalcLayout()
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
		v.recalcLayout()
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

// handleAgentKeyMsg dispatches key events based on the current modal/focus state.
func (v *AgentView) handleAgentKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle modal states first (highest priority)
	if v.approvalView != nil {
		_, cmd := v.approvalView.Update(msg)
		return v, cmd
	}
	if handled, cmd := v.killConfirm.HandleKey(msg); handled {
		return v, cmd
	}
	if v.showMessageInput {
		return v, v.handleMessageInput(msg)
	}
	if v.showNewAgent {
		return v, v.handleNewAgentInput(msg)
	}
	if v.jiraPicker.IsOpen() {
		return v, v.handleJiraPickerKey(msg)
	}
	if v.jiraConfirmIssue != nil {
		return v, v.handleJiraAgentConfirm(msg)
	}

	// Output pane navigation when focused
	if v.focus == focusOutput && v.selectedAgent != nil {
		return v.handleOutputPaneKey(msg)
	}

	// List pane keys
	return v.handleListPaneKey(msg)
}

// handleOutputPaneKey handles keys when the output pane has focus.
func (v *AgentView) handleOutputPaneKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "tab":
		v.focus = focusList
		return v, nil
	case "q":
		v.deselectAgent()
		return v, nil
	case "esc":
		v.focus = focusList
		return v, nil
	case "j", "down":
		v.outputAutoScroll = false
		v.outputViewport.LineDown(1)
		if v.outputViewport.ScrollPercent() >= 1.0 {
			v.outputAutoScroll = true
		}
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
		if v.outputViewport.ScrollPercent() >= 1.0 {
			v.outputAutoScroll = true
		}
		return v, nil
	case "ctrl+u", "pgup":
		v.outputAutoScroll = false
		v.outputViewport.HalfViewUp()
		return v, nil
	case "A":
		// Open approval view for completed Jira refine/create agents
		if v.selectedAgent != nil && v.jiraAgentMeta != nil {
			if meta, ok := v.jiraAgentMeta[v.selectedAgent.ID]; ok {
				if v.selectedAgent.State == engine.AgentComplete || v.selectedAgent.State == engine.AgentError || v.selectedAgent.State == engine.AgentKilled {
					actions, err := jira.ParseJiraActions(meta.ActionsFile)
					if err == nil && len(actions) > 0 && v.jiraClient != nil {
						v.approvalView = NewApprovalView(actions, v.jiraClient)
						v.approvalView.SetSize(v.width, v.height)
						v.approvalAgent = v.selectedAgent.ID
					}
				}
			}
		}
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

// handleListPaneKey handles keys when the agent list pane has focus.
func (v *AgentView) handleListPaneKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
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
		v.recalcLayout()
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
				agentID := item.ID
				v.killConfirm.Show("Kill Agent", fmt.Sprintf("Kill agent %s?", agentID), func() tea.Cmd {
					if v.engine != nil {
						v.engine.KillAgent(agentID)
						v.loadAgents()
					}
					return nil
				})
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

	case "i":
		if v.selectedAgent != nil &&
			(v.selectedAgent.State == engine.AgentRunning || v.selectedAgent.State == engine.AgentStarting || v.selectedAgent.State == engine.AgentComplete || v.selectedAgent.State == engine.AgentKilled) {
			v.showMessageInput = true
			v.messageInput = ""
			v.focus = focusInput
			v.recalcLayout()
		}
		return v, nil

	case "esc":
		if v.filter.IsActive() {
			v.filter.Update(msg)
		}
		return v, nil

	case "d":
		if v.selectedAgent != nil {
			v.deselectAgent()
			return v, nil
		}

	case "c":
		if v.engine != nil {
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				if item.State != engine.AgentRunning && item.State != engine.AgentStarting {
					if strings.HasPrefix(item.ID, "saved:") {
						// Synthetic saved-proposal entry: remove from tracking, don't touch engine
						issueKey := strings.TrimPrefix(item.ID, "saved:")
						delete(v.jiraAgentMeta, item.ID)
						for i, k := range v.savedProposals {
							if k == issueKey {
								v.savedProposals = append(v.savedProposals[:i], v.savedProposals[i+1:]...)
								break
							}
						}
					} else {
						v.engine.RemoveAgent(item.ID)
					}
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
			if v.outputViewport.ScrollPercent() >= 1.0 {
				v.outputAutoScroll = true
			}
			return v, nil
		}

	case "pgup", "ctrl+u":
		if v.selectedAgent != nil {
			v.outputAutoScroll = false
			v.outputViewport.HalfViewUp()
			return v, nil
		}

	case "a":
		v.showNewAgent = true
		v.newAgentTask = ""
		v.recalcLayout()
		return v, nil

	case "A":
		// Open approval view for completed Jira refine/create agents
		if v.selectedAgent != nil && v.jiraAgentMeta != nil {
			if meta, ok := v.jiraAgentMeta[v.selectedAgent.ID]; ok {
				if v.selectedAgent.State == engine.AgentComplete || v.selectedAgent.State == engine.AgentError || v.selectedAgent.State == engine.AgentKilled {
					actions, err := jira.ParseJiraActions(meta.ActionsFile)
					if err == nil && len(actions) > 0 && v.jiraClient != nil {
						v.approvalView = NewApprovalView(actions, v.jiraClient)
						v.approvalView.SetSize(v.width, v.height)
						v.approvalAgent = v.selectedAgent.ID
					}
				}
			}
		}
		return v, nil

	case "R":
		// Re-run agent on saved proposal with Jira ticket context
		if v.selectedAgent != nil && v.jiraAgentMeta != nil && v.jiraClient != nil && v.engine != nil {
			if meta, ok := v.jiraAgentMeta[v.selectedAgent.ID]; ok {
				if v.selectedAgent.State == engine.AgentComplete || v.selectedAgent.State == engine.AgentError || v.selectedAgent.State == engine.AgentKilled {
					return v, v.startRefineProposalFromSaved(meta)
				}
			}
		}
		return v, nil
	}

	// Pass remaining keys to filter
	if v.filter != nil {
		v.filter.Update(msg)
		v.syncPreview()
	}
	return v, nil
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
	if !v.showNewAgent && !v.killConfirm.Visible && !v.jiraPicker.IsOpen() && v.jiraConfirmIssue == nil {
		s.WriteString("  ")
		jiraHint := ""
		if v.jiraPicker.IsAvailable() {
			jiraHint = "  J:jira"
		}
		// Show extra hints when a saved proposal or completed Jira agent is selected
		proposalHint := ""
		if v.selectedAgent != nil && v.jiraAgentMeta != nil {
			if _, ok := v.jiraAgentMeta[v.selectedAgent.ID]; ok {
				if v.selectedAgent.State == engine.AgentComplete || v.selectedAgent.State == engine.AgentError || v.selectedAgent.State == engine.AgentKilled {
					proposalHint = "  A:review  R:re-run"
				}
			}
		}
		s.WriteString(th.Help.Render("n:new  K:kill  enter:view  d:close  c:clear  r:refresh" + jiraHint + proposalHint))
	}
	s.WriteString("\n")

	// Divider
	divider := strings.Repeat("─", v.width)

	// Kill confirmation modal
	if v.killConfirm.Visible {
		s.WriteString("\n")
		s.WriteString(v.killConfirm.Render(modalWidth(v.width)))
		return s.String()
	}

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

		// Mode indicator
		modeLabel := "Implement (worktree)"
		if v.jiraConfirmMode == "refine" {
			modeLabel = "Refine ticket"
		} else if v.jiraConfirmMode == "create" {
			modeLabel = "Create stories"
		}

		s.WriteString("\n")
		s.WriteString(renderModal("Start Agent from Jira Ticket", []string{
			"",
			fmt.Sprintf("  Ticket:  %s — %s", issue.Key, issue.Summary),
			fmt.Sprintf("  Branch:  %s", branch),
			fmt.Sprintf("  Type:    %s  Priority: %s", issue.Type, issue.Priority),
			fmt.Sprintf("  Mode:    %s", modeLabel),
			"",
			"  Custom instructions (optional):",
			"  " + input,
			"",
			"  Enter: Start   Ctrl+r: Refine   Ctrl+s: Stories   Esc: Cancel",
		}, modalWidth(v.width)))
		return s.String()
	}

	// Approval view overlay
	if v.approvalView != nil {
		return v.approvalView.View()
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

		// Show review proposal button for completed Jira refine/create agents
		reviewBtn := ""
		if v.jiraAgentMeta != nil {
			if _, ok := v.jiraAgentMeta[v.selectedAgent.ID]; ok {
				if v.selectedAgent.State == engine.AgentComplete || v.selectedAgent.State == engine.AgentError || v.selectedAgent.State == engine.AgentKilled {
					reviewBtn = "  " + lipgloss.NewStyle().Foreground(th.Accent).Bold(true).Render("[ A: Review Proposal ]")
				}
			}
		}

		s.WriteString(fmt.Sprintf("%s%s%s%s\n",
			focusIndicator,
			th.DashboardTitle.Render(header),
			th.MutedTextStyle.Render(scrollPct),
			reviewBtn,
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
			// Show actions hint for completed Jira refine/create agents
			if v.selectedAgent != nil && v.jiraAgentMeta != nil {
				if _, ok := v.jiraAgentMeta[v.selectedAgent.ID]; ok {
					if v.selectedAgent.State == engine.AgentComplete || v.selectedAgent.State == engine.AgentError || v.selectedAgent.State == engine.AgentKilled {
						hint += "  A:review actions"
					}
				}
			}
			s.WriteString(th.Help.Render(hint))
		} else {
			hint := " tab:focus output  esc/d:close"
			if v.selectedAgent != nil && v.jiraAgentMeta != nil {
				if _, ok := v.jiraAgentMeta[v.selectedAgent.ID]; ok {
					if v.selectedAgent.State == engine.AgentComplete || v.selectedAgent.State == engine.AgentError || v.selectedAgent.State == engine.AgentKilled {
						hint += "  A:review proposal"
					}
				}
			}
			s.WriteString(th.Help.Render(hint))
		}
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

// SetSize updates the view dimensions and recalculates the layout.
func (v *AgentView) SetSize(width, height int) {
	v.viewBase.SetSize(width, height)
	v.recalcLayout()
}

// Refresh reloads agent data.
func (v *AgentView) Refresh() error {
	v.loadAgents()
	return v.err
}

// CapturesInput returns true when the view is in an input mode.
func (v *AgentView) CapturesInput() bool {
	return v.showNewAgent || v.killConfirm.Visible || v.showMessageInput || v.focus == focusOutput ||
		v.jiraPicker.IsOpen() || v.jiraConfirmIssue != nil || v.approvalView != nil
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
		mode := v.jiraConfirmMode
		v.jiraConfirmIssue = nil
		v.jiraExtraMsg = ""
		v.jiraConfirmMode = ""
		switch mode {
		case "refine":
			return v.startRefineFromJira(issue, extraMsg)
		case "create":
			return v.startCreateFromJira(issue)
		default:
			return v.startAgentFromJira(issue, extraMsg)
		}
	case "ctrl+r":
		// Toggle to refine mode
		if v.jiraConfirmMode == "refine" {
			v.jiraConfirmMode = ""
		} else {
			v.jiraConfirmMode = "refine"
		}
	case "ctrl+s":
		// Toggle to create stories mode
		if v.jiraConfirmMode == "create" {
			v.jiraConfirmMode = ""
		} else {
			v.jiraConfirmMode = "create"
		}
	case "esc":
		v.jiraConfirmIssue = nil
		v.jiraExtraMsg = ""
		v.jiraConfirmMode = ""
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

// startRefineFromJira starts a Jira refine agent for the given issue.
func (v *AgentView) startRefineFromJira(issue *jira.Issue, focus string) tea.Cmd {
	if issue == nil || v.engine == nil {
		return nil
	}
	eng := v.engine
	repoPath := v.repoPath

	return func() tea.Msg {
		// Use a unique actions file per issue key to avoid conflicts with parallel refines
		actionsFile := fmt.Sprintf(".jira-actions-%s.json", issue.Key)
		id, err := jira.RefineTicket(eng, issue, repoPath, focus, actionsFile)
		if err == nil {
			// We'll register the meta in the AgentCreatedMsg handler
			return AgentCreatedMsg{
				ID:  id,
				Err: nil,
				jiraMeta: &jiraAgentMeta{
					IssueKey:    issue.Key,
					Mode:        "refine",
					ActionsFile: filepath.Join(repoPath, actionsFile),
				},
			}
		}
		return AgentCreatedMsg{ID: id, Err: err}
	}
}

// startCreateFromJira starts a Jira create-stories agent for the given issue.
func (v *AgentView) startCreateFromJira(issue *jira.Issue) tea.Cmd {
	if issue == nil || v.engine == nil {
		return nil
	}
	eng := v.engine
	repoPath := v.repoPath
	cfg := v.jiraCfg

	return func() tea.Msg {
		project := cfg.DefaultProject
		if project == "" {
			if idx := strings.Index(issue.Key, "-"); idx > 0 {
				project = issue.Key[:idx]
			}
		}
		actionsFile := fmt.Sprintf(".jira-actions-%s.json", issue.Key)
		id, err := jira.CreateStories(eng, issue, "", project, repoPath, actionsFile)
		if err == nil {
			return AgentCreatedMsg{
				ID:  id,
				Err: nil,
				jiraMeta: &jiraAgentMeta{
					IssueKey:    issue.Key,
					Mode:        "create",
					ActionsFile: filepath.Join(repoPath, actionsFile),
				},
			}
		}
		return AgentCreatedMsg{ID: id, Err: err}
	}
}

// startRefineProposalFromSaved starts a new agent that refines an existing proposal
// using the live Jira ticket as context. It fetches the ticket, reads the saved
// actions file, then launches RefineProposalWithContext.
func (v *AgentView) startRefineProposalFromSaved(meta *jiraAgentMeta) tea.Cmd {
	if meta == nil || v.jiraClient == nil || v.engine == nil {
		return nil
	}
	client := v.jiraClient
	eng := v.engine
	repoPath := v.repoPath
	issueKey := meta.IssueKey
	actionsFile := meta.ActionsFile

	return func() tea.Msg {
		issue, err := client.GetIssue(issueKey)
		if err != nil {
			return AgentCreatedMsg{Err: fmt.Errorf("fetching %s: %w", issueKey, err)}
		}
		existingActions, err := jira.ParseJiraActions(actionsFile)
		if err != nil {
			return AgentCreatedMsg{Err: fmt.Errorf("reading proposal: %w", err)}
		}
		// Write to the same file so the proposal is updated in place
		relFile := fmt.Sprintf(".jira-actions-%s.json", issueKey)
		id, err := jira.RefineProposalWithContext(eng, issue, existingActions, "", repoPath, relFile)
		if err != nil {
			return AgentCreatedMsg{Err: err}
		}
		return AgentCreatedMsg{
			ID:  id,
			Err: nil,
			jiraMeta: &jiraAgentMeta{
				IssueKey:    issueKey,
				Mode:        "refine",
				ActionsFile: filepath.Join(repoPath, relFile),
			},
		}
	}
}

// StreamTickMsg is sent periodically to refresh streaming output.
type StreamTickMsg struct{}

// AgentUpdateMsg is sent when the engine notifies that an agent's state or output changed.
type AgentUpdateMsg struct {
	AgentID string
}

// AgentCreatedMsg is sent when a new agent has been started (or failed to start).
type AgentCreatedMsg struct {
	ID       string
	Err      error
	jiraMeta *jiraAgentMeta // non-nil for Jira refine/create agents
}
