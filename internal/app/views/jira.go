package views

import (
	"fmt"
	"strings"

	"git-frontend/internal/app/components"
	"git-frontend/internal/config"
	"git-frontend/internal/jira"
	"git-frontend/internal/theme"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// jiraLoadedMsg carries freshly fetched Jira issues back to the view.
type jiraLoadedMsg struct {
	result *jira.SearchResult
	err    error
}

// JiraView displays a browsable, filterable list of Jira issues.
type JiraView struct {
	cfg     config.JiraConfig
	client  *jira.Client
	issues  []jira.Issue
	filter  *components.Filter[jira.Issue]
	loading bool
	err     error
	width   int
	height  int

	// Search / JQL input mode
	searchMode  bool
	searchInput string

	// Detail pane
	showDetail    bool
	detailIssue   *jira.Issue
}

// NewJiraView creates a new Jira issues view.
func NewJiraView(cfg config.JiraConfig) *JiraView {
	v := &JiraView{
		cfg:    cfg,
		client: jira.NewClient(cfg.BaseURL, cfg.Email, cfg.APIToken),
		width:  80,
		height: 24,
	}
	v.filter = components.NewFilter([]jira.Issue{}, v.renderIssueItem)
	v.filter.SetHeight(v.height)
	return v
}

// Init loads issues on first display.
func (v *JiraView) Init() tea.Cmd {
	v.loading = true
	return v.fetchCmd(v.defaultJQL())
}

func (v *JiraView) defaultJQL() string {
	if v.cfg.DefaultProject != "" {
		return "project = " + v.cfg.DefaultProject +
			" AND assignee = currentUser() AND resolution = Unresolved ORDER BY updated DESC"
	}
	return "assignee = currentUser() AND resolution = Unresolved ORDER BY updated DESC"
}

func (v *JiraView) fetchCmd(jql string) tea.Cmd {
	return func() tea.Msg {
		result, err := v.client.SearchIssues(jql, 50)
		return jiraLoadedMsg{result: result, err: err}
	}
}

// Update handles messages.
func (v *JiraView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case jiraLoadedMsg:
		v.loading = false
		if msg.err != nil {
			v.err = msg.err
			return v, nil
		}
		v.err = nil
		v.issues = msg.result.Issues
		v.filter.SetItems(v.issues)
		return v, nil

	case tea.WindowSizeMsg:
		v.width = msg.Width
		v.height = msg.Height
		if v.filter != nil {
			v.filter.SetHeight(msg.Height)
		}
		return v, nil

	case tea.KeyMsg:
		// Detail pane active
		if v.showDetail {
			if msg.String() == "esc" {
				v.showDetail = false
				v.detailIssue = nil
			}
			return v, nil
		}

		// Search / JQL input mode
		if v.searchMode {
			return v, v.handleSearchInput(msg)
		}

		switch msg.String() {
		case "r":
			v.loading = true
			v.err = nil
			return v, v.fetchCmd(v.defaultJQL())

		case "s":
			v.searchMode = true
			v.searchInput = ""
			return v, nil

		case "enter":
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				v.detailIssue = &item
				v.showDetail = true
			}
			return v, nil

		case "esc":
			if v.filter.IsActive() {
				v.filter.Update(msg)
			}
			return v, nil

		case "/":
			v.filter.Update(msg)
			return v, nil
		}

		if v.filter != nil {
			v.filter.Update(msg)
		}

	case tea.MouseMsg:
		if v.filter != nil {
			v.filter.HandleMouse(msg)
		}
	}

	return v, nil
}

func (v *JiraView) handleSearchInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		jql := v.searchInput
		v.searchMode = false
		v.searchInput = ""
		if jql == "" {
			jql = v.defaultJQL()
		}
		v.loading = true
		v.err = nil
		return v.fetchCmd(jql)

	case "esc":
		v.searchMode = false
		v.searchInput = ""

	case "ctrl+w":
		v.searchInput = components.DeleteWordEnd(v.searchInput)

	case "backspace":
		if len(v.searchInput) > 0 {
			v.searchInput = v.searchInput[:len(v.searchInput)-1]
		}

	default:
		if msg.Paste && len(msg.Runes) > 0 {
			v.searchInput += string(msg.Runes)
		} else if len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r >= 32 {
				v.searchInput += string(r)
			}
		}
	}
	return nil
}

// View renders the Jira issues view.
func (v *JiraView) View() string {
	th := theme.GetTheme()

	if v.loading {
		return th.StatsStyle.Render(" Loading Jira issues...")
	}

	var s strings.Builder

	// Header
	title := " Jira Issues "
	if v.cfg.DefaultProject != "" {
		title = fmt.Sprintf(" Jira · %s ", v.cfg.DefaultProject)
	}
	s.WriteString(th.DashboardTitle.Render(title))
	s.WriteString("\n\n")

	// Error
	if v.err != nil {
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" Error: %v", v.err)))
		s.WriteString("\n\n")
	}

	// Detail pane
	if v.showDetail && v.detailIssue != nil {
		s.WriteString(v.renderDetail(v.detailIssue))
		return s.String()
	}

	// Search input
	if v.searchMode {
		s.WriteString(th.DashboardTitle.Render(" JQL Search "))
		s.WriteString("\n")
		s.WriteString(fmt.Sprintf(" > %s_", v.searchInput))
		s.WriteString("\n")
		s.WriteString(th.Help.Render(" Enter: run query   Esc: cancel "))
		s.WriteString("\n\n")
	}

	// Issue count
	s.WriteString(fmt.Sprintf(" %s %s\n\n",
		th.StashStyle.Render("Issues:"),
		th.DashboardAccentStyle.Render(fmt.Sprintf("%d", len(v.issues)))))

	// Filter / list
	if v.filter.IsActive() {
		s.WriteString(v.filter.View())
	} else {
		s.WriteString(th.Help.Render(" / to filter • s: JQL search • r: refresh • Enter: detail • ↑↓: navigate "))
		s.WriteString("\n\n")
		s.WriteString(v.filter.View())
	}

	// Footer
	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")
	s.WriteString(th.Help.Render(" r: Refresh   s: JQL Search   /: Filter   ↑↓: Navigate   Enter: Detail "))

	return s.String()
}

func (v *JiraView) renderDetail(issue *jira.Issue) string {
	th := theme.GetTheme()
	var s strings.Builder

	s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" %s ", issue.Key)))
	s.WriteString("\n\n")

	s.WriteString(fmt.Sprintf(" %s %s\n",
		th.BranchStyle.Render("Summary:"),
		th.StatsStyle.Render(issue.Summary)))

	s.WriteString(fmt.Sprintf(" %s %s\n",
		th.BranchStyle.Render("Type:"),
		th.StatsStyle.Render(issue.Type)))

	s.WriteString(fmt.Sprintf(" %s %s\n",
		th.BranchStyle.Render("Status:"),
		statusStyle(issue.Status, th)))

	s.WriteString(fmt.Sprintf(" %s %s\n",
		th.BranchStyle.Render("Priority:"),
		th.StatsStyle.Render(issue.Priority)))

	if issue.Assignee != "" {
		s.WriteString(fmt.Sprintf(" %s %s\n",
			th.BranchStyle.Render("Assignee:"),
			th.StatsStyle.Render(issue.Assignee)))
	}

	if issue.Sprint != "" {
		s.WriteString(fmt.Sprintf(" %s %s\n",
			th.BranchStyle.Render("Sprint:"),
			th.StatsStyle.Render(issue.Sprint)))
	}

	if len(issue.Labels) > 0 {
		s.WriteString(fmt.Sprintf(" %s %s\n",
			th.BranchStyle.Render("Labels:"),
			th.MutedTextStyle.Render(strings.Join(issue.Labels, ", "))))
	}

	if issue.Description != "" {
		s.WriteString("\n")
		s.WriteString(th.DashboardTitle.Render(" Description "))
		s.WriteString("\n\n")
		// Wrap description to view width
		for _, line := range wordWrap(issue.Description, v.width-4) {
			s.WriteString(" " + line + "\n")
		}
	}

	s.WriteString("\n")
	s.WriteString(th.Help.Render(" Esc: back to list "))

	return s.String()
}

// renderIssueItem renders one issue row in the filter list.
func (v *JiraView) renderIssueItem(issue jira.Issue, index int, selected bool) string {
	th := theme.GetTheme()

	prefix := "  "
	keyStyle := th.BranchStyle
	if selected {
		prefix = " >"
		keyStyle = th.SelectedBranchStyle
	}

	var line strings.Builder
	line.WriteString(keyStyle.Render(fmt.Sprintf("%s%-12s", prefix, issue.Key)))
	line.WriteString(" ")
	line.WriteString(statusIcon(issue.Status))
	line.WriteString(" ")

	// Truncate summary to fit
	summary := issue.Summary
	maxSummary := v.width - 30
	if maxSummary < 20 {
		maxSummary = 20
	}
	if len([]rune(summary)) > maxSummary {
		summary = string([]rune(summary)[:maxSummary-1]) + "…"
	}
	line.WriteString(th.StatsStyle.Render(summary))

	if selected && issue.Assignee != "" {
		line.WriteString(fmt.Sprintf("\n   %s", th.MutedTextStyle.Render(issue.Assignee)))
	}

	return line.String()
}

// statusIcon returns a compact icon for a Jira status.
func statusIcon(status string) string {
	lower := strings.ToLower(status)
	switch {
	case strings.Contains(lower, "done") || strings.Contains(lower, "closed") || strings.Contains(lower, "resolved"):
		return "✓"
	case strings.Contains(lower, "progress") || strings.Contains(lower, "review"):
		return "●"
	case strings.Contains(lower, "block"):
		return "✗"
	default:
		return "○"
	}
}

// statusStyle returns a styled status string.
func statusStyle(status string, th theme.Theme) lipgloss.Style {
	lower := strings.ToLower(status)
	switch {
	case strings.Contains(lower, "done") || strings.Contains(lower, "closed"):
		return th.DashboardAccentStyle
	case strings.Contains(lower, "progress"):
		return th.InfoStyle
	case strings.Contains(lower, "block"):
		return th.DashboardErrorStyle
	default:
		return th.StatsStyle
	}
}

// ShortHelp returns a short help string.
func (v *JiraView) ShortHelp() string {
	return "r: Refresh  s: JQL Search  /: Filter  ↑↓: Navigate  Enter: Detail  Esc: Back"
}

// SetSize updates the view dimensions.
func (v *JiraView) SetSize(width, height int) {
	v.width = width
	v.height = height
	if v.filter != nil {
		v.filter.SetHeight(height)
	}
}

// KeyBindings returns the keybindings for this view.
func (v *JiraView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "r", Description: "Refresh issues"},
		{Key: "s", Description: "JQL search"},
		{Key: "/", Description: "Filter list"},
		{Key: "↑/k", Description: "Navigate up"},
		{Key: "↓/j", Description: "Navigate down"},
		{Key: "Enter", Description: "Show issue detail"},
		{Key: "Esc", Description: "Back / clear filter"},
	}
}
