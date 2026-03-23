package views

import (
	"fmt"
	"strings"

	"git-frontend/internal/app/components"
	"git-frontend/internal/config"
	"git-frontend/internal/jira"
	"git-frontend/internal/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// jiraPickerLoadedMsg carries search results back to the picker.
type jiraPickerLoadedMsg struct {
	issues []jira.Issue
	err    error
}

// JiraPickerState manages an inline Jira issue picker with search, list, and preview.
// Embed this struct in views that need to pick a Jira ticket.
type JiraPickerState struct {
	cfg    config.JiraConfig
	client *jira.Client

	open    bool
	loading bool
	err     error

	issues []jira.Issue
	filter *components.Filter[jira.Issue]

	// JQL search input
	searchInput string
	searchMode  bool // typing a full JQL query

	// Detail/preview pane for the selected issue
	previewIssue *jira.Issue

	width  int
	height int
}

// NewJiraPickerState creates a new picker state using the given Jira config.
// Returns nil if Jira is not configured (no BaseURL).
func NewJiraPickerState(cfg config.JiraConfig) *JiraPickerState {
	if cfg.BaseURL == "" {
		return nil
	}
	p := &JiraPickerState{
		cfg:    cfg,
		client: jira.NewClient(cfg.BaseURL, cfg.Email, cfg.APIToken),
		width:  80,
		height: 24,
	}
	p.filter = components.NewFilter([]jira.Issue{}, p.renderItem)
	p.filter.SetHeight(8)
	return p
}

// IsAvailable returns true when the picker has a valid Jira config.
func (p *JiraPickerState) IsAvailable() bool {
	return p != nil && p.cfg.BaseURL != ""
}

// IsOpen returns whether the picker is currently visible.
func (p *JiraPickerState) IsOpen() bool {
	return p != nil && p.open
}

// Open opens the picker and fires an initial issue load command.
func (p *JiraPickerState) Open() tea.Cmd {
	if p == nil {
		return nil
	}
	p.open = true
	p.loading = true
	p.err = nil
	p.searchInput = ""
	p.searchMode = false
	p.previewIssue = nil
	return p.fetchCmd(p.defaultJQL())
}

// Close closes the picker without selecting anything.
func (p *JiraPickerState) Close() {
	if p == nil {
		return
	}
	p.open = false
	p.loading = false
	p.err = nil
	p.searchInput = ""
	p.searchMode = false
	p.previewIssue = nil
}

func (p *JiraPickerState) defaultJQL() string {
	if p.cfg.DefaultProject != "" {
		return "project = " + p.cfg.DefaultProject +
			" AND resolution = Unresolved ORDER BY updated DESC"
	}
	return "resolution = Unresolved ORDER BY updated DESC"
}

func (p *JiraPickerState) fetchCmd(query string) tea.Cmd {
	client := p.client
	return func() tea.Msg {
		if issueKeyRe.MatchString(strings.TrimSpace(query)) {
			issue, err := client.GetIssue(strings.TrimSpace(query))
			if err != nil {
				return jiraPickerLoadedMsg{err: err}
			}
			return jiraPickerLoadedMsg{issues: []jira.Issue{*issue}}
		}
		result, err := client.SearchIssues(query, 50)
		if err != nil {
			return jiraPickerLoadedMsg{err: err}
		}
		return jiraPickerLoadedMsg{issues: result.Issues}
	}
}

// HandleMsg processes messages directed at the picker (call from parent Update).
// Returns the tea.Cmd to run, if any.
func (p *JiraPickerState) HandleMsg(msg tea.Msg) tea.Cmd {
	if p == nil || !p.open {
		return nil
	}
	switch m := msg.(type) {
	case jiraPickerLoadedMsg:
		p.loading = false
		if m.err != nil {
			p.err = m.err
			return nil
		}
		p.err = nil
		p.issues = m.issues
		p.filter.SetItems(p.issues)
		p.syncPreview()
	case tea.WindowSizeMsg:
		p.SetSize(m.Width, m.Height)
	}
	return nil
}

// HandleKey processes a key event when the picker is open.
// Returns (cmd, done, confirmed, selectedIssue):
//   - cmd: tea.Cmd to run
//   - done: true when the picker should close (cancelled or confirmed)
//   - confirmed: true when the user confirmed a selection
//   - selectedIssue: the confirmed issue (only valid when confirmed==true)
func (p *JiraPickerState) HandleKey(msg tea.KeyMsg) (cmd tea.Cmd, done bool, confirmed bool, issue *jira.Issue) {
	if p == nil || !p.open {
		return nil, false, false, nil
	}

	// JQL search mode
	if p.searchMode {
		return p.handleSearchKey(msg)
	}

	key := msg.String()
	switch key {
	case "esc":
		p.Close()
		return nil, true, false, nil

	case "enter":
		if item, idx := p.filter.SelectedItem(); idx >= 0 {
			p.Close()
			return nil, true, true, &item
		}
		return nil, false, false, nil

	case "s":
		p.searchMode = true
		p.searchInput = ""
		return nil, false, false, nil

	case "r":
		p.loading = true
		p.err = nil
		return p.fetchCmd(p.defaultJQL()), false, false, nil

	case "j", "down", "k", "up", "g", "G":
		p.filter.Update(msg)
		p.syncPreview()
		return nil, false, false, nil

	case "/":
		p.filter.Update(msg)
		return nil, false, false, nil

	default:
		p.filter.Update(msg)
		p.syncPreview()
	}
	return nil, false, false, nil
}

func (p *JiraPickerState) handleSearchKey(msg tea.KeyMsg) (cmd tea.Cmd, done bool, confirmed bool, issue *jira.Issue) {
	switch msg.String() {
	case "enter":
		jql := p.searchInput
		p.searchMode = false
		p.searchInput = ""
		if jql == "" {
			jql = p.defaultJQL()
		}
		p.loading = true
		p.err = nil
		return p.fetchCmd(jql), false, false, nil

	case "esc":
		p.searchMode = false
		p.searchInput = ""

	case "backspace":
		if len(p.searchInput) > 0 {
			p.searchInput = p.searchInput[:len(p.searchInput)-1]
		}

	case "ctrl+w":
		p.searchInput = components.DeleteWordEnd(p.searchInput)

	default:
		if msg.Paste && len(msg.Runes) > 0 {
			p.searchInput += string(msg.Runes)
		} else if len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r >= 32 {
				p.searchInput += string(r)
			}
		}
	}
	return nil, false, false, nil
}

// syncPreview updates the preview pane to reflect the currently highlighted issue.
func (p *JiraPickerState) syncPreview() {
	if item, idx := p.filter.SelectedItem(); idx >= 0 {
		p.previewIssue = &item
	} else {
		p.previewIssue = nil
	}
}

// SetSize updates dimensions.
func (p *JiraPickerState) SetSize(width, height int) {
	if p == nil {
		return
	}
	p.width = width
	p.height = height
	// List occupies ~1/2 of the picker height
	listH := height / 2
	if listH < 4 {
		listH = 4
	}
	if p.filter != nil {
		p.filter.SetHeight(listH)
	}
}

// renderItem renders a single issue row in the filter list.
func (p *JiraPickerState) renderItem(issue jira.Issue, index int, selected bool) string {
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

	summary := issue.Summary
	maxSummary := p.width - 32
	if maxSummary < 20 {
		maxSummary = 20
	}
	if len([]rune(summary)) > maxSummary {
		summary = string([]rune(summary)[:maxSummary-1]) + "…"
	}
	line.WriteString(th.StatsStyle.Render(summary))

	return line.String()
}

// View renders the picker as an inline modal block.
func (p *JiraPickerState) View() string {
	if p == nil || !p.open {
		return ""
	}
	th := theme.GetTheme()
	w := modalWidth(p.width)

	var s strings.Builder

	// Header
	s.WriteString(renderModal("Search Jira Issues", nil, w))

	// Search input row
	if p.searchMode {
		s.WriteString(fmt.Sprintf(" JQL > %s█\n", p.searchInput))
		s.WriteString(th.Help.Render(" Enter: run   Esc: cancel "))
		s.WriteString("\n\n")
	} else {
		s.WriteString(th.Help.Render(" s: JQL search   /: filter   r: refresh   ↑/↓: navigate   Enter: select   Esc: cancel "))
		s.WriteString("\n\n")
	}

	// Loading / error
	if p.loading {
		s.WriteString(th.MutedTextStyle.Render(" Loading..."))
		s.WriteString("\n")
	} else if p.err != nil {
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" Error: %v", p.err)))
		s.WriteString("\n")
	}

	// Issue list
	if !p.loading {
		s.WriteString(p.filter.View())
	}

	// Preview pane for selected issue
	if p.previewIssue != nil {
		issue := p.previewIssue
		s.WriteString("\n")
		s.WriteString(th.BorderStyle.Render(strings.Repeat("─", w)))
		s.WriteString("\n")
		s.WriteString(fmt.Sprintf(" %s  %s  %s\n",
			th.DashboardAccentStyle.Render(issue.Key),
			statusIcon(issue.Status),
			th.StatsStyle.Render(issue.Summary),
		))
		s.WriteString(fmt.Sprintf(" %s  %s  %s\n",
			th.MutedTextStyle.Render(issue.Type),
			th.MutedTextStyle.Render(issue.Priority),
			statusStyle(issue.Status, th).Render(issue.Status),
		))
		if issue.Assignee != "" {
			s.WriteString(fmt.Sprintf(" %s %s\n",
				th.BranchStyle.Render("Assignee:"),
				th.MutedTextStyle.Render(issue.Assignee),
			))
		}
		branchName := issueToBranchName(issue)
		s.WriteString(fmt.Sprintf(" %s %s\n",
			th.BranchStyle.Render("Branch:"),
			th.MutedTextStyle.Render(branchName),
		))
		if issue.Description != "" {
			desc := issue.Description
			if len(desc) > 200 {
				desc = desc[:197] + "..."
			}
			// Truncate to single line for preview
			if nl := strings.IndexByte(desc, '\n'); nl >= 0 {
				desc = desc[:nl] + "..."
			}
			s.WriteString(fmt.Sprintf(" %s\n", th.MutedTextStyle.Render(desc)))
		}
	}

	return s.String()
}
