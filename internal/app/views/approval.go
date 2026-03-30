package views

import (
	"fmt"
	"sort"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
	"gitlab.com/tanevanwifferen1/singularity/internal/jira"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// ApprovalItem wraps a JiraAction with UI state.
type ApprovalItem struct {
	Action   jira.JiraAction
	Selected bool // checkbox state
	Expanded bool // show detail
}

// ApprovalDoneMsg is sent when the user finishes (confirm or cancel).
type ApprovalDoneMsg struct {
	Executed bool  // true if actions were executed
	Err      error // execution error, if any
	Results  []ActionResult
}

// ActionResult tracks the outcome of executing one action.
type ActionResult struct {
	Action     jira.JiraAction
	Success    bool
	CreatedKey string // for create_issue: the new issue key
	Err        error
}

// approvalExecDoneMsg is the internal message sent when execution finishes.
type approvalExecDoneMsg struct {
	results []ActionResult
	err     error
}

// ApprovalIterateMsg is sent when the user wants to re-run the agent with feedback.
type ApprovalIterateMsg struct {
	Feedback string
}

// ApprovalView presents JiraActions for review and execution.
type ApprovalView struct {
	viewBase
	items        []ApprovalItem
	cursor       int
	scrollOffset int
	client       *jira.Client

	// Execution state
	executing    bool
	results      []ActionResult
	executionErr error
	done         bool

	// Iterate input
	inputMode bool
	inputText string
}

// NewApprovalView creates an ApprovalView from a list of JiraActions.
// All items are selected by default. Items are sorted by Order field.
func NewApprovalView(actions []jira.JiraAction, client *jira.Client) *ApprovalView {
	sorted := make([]jira.JiraAction, len(actions))
	copy(sorted, actions)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Order < sorted[j].Order
	})

	items := make([]ApprovalItem, len(sorted))
	for i, a := range sorted {
		items[i] = ApprovalItem{
			Action:   a,
			Selected: true,
			Expanded: false,
		}
	}

	return &ApprovalView{
		items:  items,
		client: client,
	}
}

// Init implements tea.Model.
func (v *ApprovalView) Init() tea.Cmd {
	return nil
}

// Actions returns all actions in the view (for passing to iterate agent).
func (v *ApprovalView) Actions() []jira.JiraAction {
	actions := make([]jira.JiraAction, len(v.items))
	for i, item := range v.items {
		actions[i] = item.Action
	}
	return actions
}

// Update implements tea.Model.
func (v *ApprovalView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.SetSize(msg.Width, msg.Height)

	case approvalExecDoneMsg:
		v.executing = false
		v.done = true
		v.results = msg.results
		v.executionErr = msg.err

	case tea.KeyMsg:
		// If execution is in progress, ignore key input
		if v.executing {
			return v, nil
		}

		// Input mode: collect iterate feedback
		if v.inputMode {
			switch msg.String() {
			case "enter":
				feedback := strings.TrimSpace(v.inputText)
				v.inputMode = false
				v.inputText = ""
				return v, func() tea.Msg { return ApprovalIterateMsg{Feedback: feedback} }
			case "esc":
				v.inputMode = false
				v.inputText = ""
			case "ctrl+w":
				v.inputText = components.DeleteWordEnd(v.inputText)
			case "backspace":
				if len(v.inputText) > 0 {
					v.inputText = v.inputText[:len(v.inputText)-1]
				}
			default:
				if msg.Paste && len(msg.Runes) > 0 {
					v.inputText += string(msg.Runes)
				} else if len(msg.Runes) == 1 && msg.Runes[0] >= 32 {
					v.inputText += string(msg.Runes[0])
				}
			}
			return v, nil
		}

		// If done, any enter/esc/q dismisses
		if v.done {
			switch msg.String() {
			case "enter", "esc", "q":
				return v, func() tea.Msg {
					return ApprovalDoneMsg{
						Executed: true,
						Err:      v.executionErr,
						Results:  v.results,
					}
				}
			}
			return v, nil
		}

		switch msg.String() {
		case "j", "down":
			if v.cursor < len(v.items)-1 {
				v.cursor++
				v.ensureCursorVisible()
			}

		case "k", "up":
			if v.cursor > 0 {
				v.cursor--
				v.ensureCursorVisible()
			}

		case " ", "tab":
			if len(v.items) > 0 {
				v.items[v.cursor].Selected = !v.items[v.cursor].Selected
			}

		case "enter":
			if len(v.items) > 0 {
				v.items[v.cursor].Expanded = !v.items[v.cursor].Expanded
			}

		case "a":
			allSelected := v.selectedCount() == len(v.items)
			for i := range v.items {
				v.items[i].Selected = !allSelected
			}

		case "i":
			v.inputMode = true
			v.inputText = ""

		case "x", "ctrl+x":
			if v.selectedCount() > 0 {
				v.executing = true
				return v, v.executeActions()
			}

		case "esc", "q":
			return v, func() tea.Msg {
				return ApprovalDoneMsg{Executed: false}
			}
		}
	}

	return v, nil
}

// View implements tea.Model.
func (v *ApprovalView) View() string {
	t := theme.GetTheme()

	var sb strings.Builder

	// Title
	sb.WriteString(t.DashboardTitle.Render("Review Proposed Actions"))
	sb.WriteString("\n\n")

	if len(v.items) == 0 {
		sb.WriteString(t.MutedTextStyle.Render("No actions to review."))
		sb.WriteString("\n")
	} else if v.executing {
		sb.WriteString(t.InfoStyle.Render("Executing actions, please wait..."))
		sb.WriteString("\n")
	} else if v.done {
		sb.WriteString(v.renderResults(t))
	} else {
		sb.WriteString(v.renderItems(t))
	}

	// Iterate input field
	if v.inputMode {
		sb.WriteString("\n")
		sb.WriteString(t.DashboardAccentStyle.Render("Iterate: "))
		sb.WriteString(v.inputText)
		sb.WriteString("_")
		sb.WriteString("\n")
	}

	// Hint bar
	sb.WriteString("\n")
	sb.WriteString(t.Help.Render(v.ShortHelp()))

	return sb.String()
}

// availableItemLines returns the number of lines available for rendering items.
// Overhead: title(1) + blank(1) + blank(1) + stats(1) + blank(1) + blank(1) + help(1) = 7
func (v *ApprovalView) availableItemLines() int {
	if v.height <= 7 {
		return 10
	}
	return v.height - 7
}

// ensureCursorVisible adjusts scrollOffset so the cursor item is in view.
func (v *ApprovalView) ensureCursorVisible() {
	if v.cursor < v.scrollOffset {
		v.scrollOffset = v.cursor
		return
	}
	t := theme.GetTheme()
	available := v.availableItemLines()
	linesUsed := 0
	for i := v.scrollOffset; i < len(v.items); i++ {
		if i == v.cursor {
			return // cursor is visible
		}
		linesUsed++ // item line
		if v.items[i].Expanded {
			detail := v.renderDetail(v.items[i].Action, t)
			linesUsed += strings.Count(detail, "\n")
		}
		if linesUsed >= available {
			// cursor is not visible yet; scroll forward by one line
			v.scrollOffset++
			v.ensureCursorVisible()
			return
		}
	}
}

// renderItems renders the scrollable checkbox list.
func (v *ApprovalView) renderItems(t theme.Theme) string {
	var sb strings.Builder
	available := v.availableItemLines()
	linesUsed := 0

	for i := v.scrollOffset; i < len(v.items); i++ {
		if linesUsed >= available {
			break
		}
		item := v.items[i]

		cursor := "  "
		if i == v.cursor {
			cursor = t.DashboardAccentStyle.Render("> ")
		}

		checkbox := "[ ]"
		if item.Selected {
			checkbox = t.InfoStyle.Render("[x]")
		} else {
			checkbox = t.MutedTextStyle.Render("[ ]")
		}

		icon := v.actionIcon(item.Action.Type)
		summary := v.actionSummary(item.Action)

		var line string
		if i == v.cursor {
			line = fmt.Sprintf("%s%s %s %s", cursor, checkbox, t.SelectedBranchStyle.Render(icon), t.SelectedBranchStyle.Render(summary))
		} else {
			line = fmt.Sprintf("%s%s %s %s", cursor, checkbox, t.BranchStyle.Render(icon), summary)
		}

		sb.WriteString(line)

		if item.Action.Reason != "" {
			sb.WriteString("  ")
			sb.WriteString(t.MutedTextStyle.Render(item.Action.Reason))
		}
		sb.WriteString("\n")
		linesUsed++

		if item.Expanded {
			detail := v.renderDetail(item.Action, t)
			sb.WriteString(detail)
			linesUsed += strings.Count(detail, "\n")
		}
	}

	count := v.selectedCount()
	sb.WriteString("\n")
	sb.WriteString(t.StatsStyle.Render(fmt.Sprintf("%d/%d selected", count, len(v.items))))
	if len(v.items) > available || v.scrollOffset > 0 {
		end := v.scrollOffset + available
		if end > len(v.items) {
			end = len(v.items)
		}
		sb.WriteString("  ")
		sb.WriteString(t.MutedTextStyle.Render(fmt.Sprintf("[%d-%d of %d]", v.scrollOffset+1, end, len(v.items))))
	}
	sb.WriteString("\n")

	return sb.String()
}

// renderDetail renders the expanded detail for a single action, indented.
func (v *ApprovalView) renderDetail(a jira.JiraAction, t theme.Theme) string {
	indent := "      "
	var lines []string

	switch a.Type {
	case "create_issue":
		lines = append(lines, fmt.Sprintf("Project:     %s", a.Project))
		lines = append(lines, fmt.Sprintf("Type:        %s", a.IssueType))
		lines = append(lines, fmt.Sprintf("Summary:     %s", a.Summary))
		if a.Description != "" {
			lines = append(lines, fmt.Sprintf("Description: %s", a.Description))
		}
		if a.Priority != "" {
			lines = append(lines, fmt.Sprintf("Priority:    %s", a.Priority))
		}
		if a.LinkTo != "" {
			linkType := a.LinkType
			if linkType == "" {
				linkType = "relates_to"
			}
			lines = append(lines, fmt.Sprintf("Link to:     %s (%s)", a.LinkTo, linkType))
		}
		lines = append(lines, fmt.Sprintf("Order:       %d", a.Order))
		if len(a.DependsOnOrder) > 0 {
			deps := make([]string, len(a.DependsOnOrder))
			for i, d := range a.DependsOnOrder {
				deps[i] = fmt.Sprintf("%d", d)
			}
			lines = append(lines, fmt.Sprintf("Depends on:  [%s]", strings.Join(deps, ", ")))
		}

	case "update_field":
		lines = append(lines, fmt.Sprintf("Issue:  %s", a.IssueKey))
		for k, val := range a.Fields {
			lines = append(lines, fmt.Sprintf("  %s = %v", k, val))
		}

	case "comment":
		lines = append(lines, fmt.Sprintf("Issue: %s", a.IssueKey))
		lines = append(lines, fmt.Sprintf("Body:  %s", a.Body))
	}

	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(t.MutedTextStyle.Render(indent + l))
		sb.WriteString("\n")
	}
	return sb.String()
}

// renderResults renders a summary of execution results.
func (v *ApprovalView) renderResults(t theme.Theme) string {
	var sb strings.Builder

	if v.executionErr != nil {
		sb.WriteString(t.DashboardErrorStyle.Render("Execution error: " + v.executionErr.Error()))
		sb.WriteString("\n\n")
	}

	successCount := 0
	failCount := 0
	for _, r := range v.results {
		if r.Success {
			successCount++
		} else {
			failCount++
		}
	}

	sb.WriteString(t.InfoStyle.Render(fmt.Sprintf("Execution complete: %d succeeded, %d failed", successCount, failCount)))
	sb.WriteString("\n\n")

	for _, r := range v.results {
		summary := v.actionSummary(r.Action)
		icon := v.actionIcon(r.Action.Type)
		if r.Success {
			status := t.InfoStyle.Render("OK")
			if r.CreatedKey != "" {
				status = t.InfoStyle.Render("OK " + r.CreatedKey)
			}
			sb.WriteString(fmt.Sprintf("  %s %s  %s\n", icon, summary, status))
		} else {
			errMsg := ""
			if r.Err != nil {
				errMsg = r.Err.Error()
			}
			sb.WriteString(fmt.Sprintf("  %s %s  %s\n", icon, summary, t.DashboardErrorStyle.Render("FAIL: "+errMsg)))
		}
	}

	sb.WriteString("\n")
	sb.WriteString(t.MutedTextStyle.Render("Press Enter or Esc to dismiss."))
	sb.WriteString("\n")

	return sb.String()
}

// executeActions returns a tea.Cmd that runs all selected actions.
func (v *ApprovalView) executeActions() tea.Cmd {
	// Capture the selected items at the time of execution
	selected := make([]ApprovalItem, 0, len(v.items))
	for _, item := range v.items {
		if item.Selected {
			selected = append(selected, item)
		}
	}

	client := v.client

	return func() tea.Msg {
		results := make([]ActionResult, 0, len(selected))

		// Map from Order number to created issue key (for linking)
		orderToKey := make(map[int]string)

		// Track create_issue results that need linking
		type createResult struct {
			action jira.JiraAction
			key    string
		}
		var created []createResult

		for _, item := range selected {
			a := item.Action
			switch a.Type {
			case "create_issue":
				issue, err := client.CreateIssue(a.Project, a.IssueType, a.Summary, a.Description, a.Priority)
				if err != nil {
					results = append(results, ActionResult{
						Action:  a,
						Success: false,
						Err:     err,
					})
				} else {
					results = append(results, ActionResult{
						Action:     a,
						Success:    true,
						CreatedKey: issue.Key,
					})
					orderToKey[a.Order] = issue.Key
					created = append(created, createResult{action: a, key: issue.Key})
				}

			case "update_field":
				err := client.UpdateFields(a.IssueKey, a.Fields)
				if err != nil {
					results = append(results, ActionResult{
						Action:  a,
						Success: false,
						Err:     err,
					})
				} else {
					results = append(results, ActionResult{
						Action:  a,
						Success: true,
					})
				}

			case "comment":
				err := client.AddComment(a.IssueKey, a.Body)
				if err != nil {
					results = append(results, ActionResult{
						Action:  a,
						Success: false,
						Err:     err,
					})
				} else {
					results = append(results, ActionResult{
						Action:  a,
						Success: true,
					})
				}
			}
		}

		// After all creates, link issues using LinkTo and DependsOnOrder
		var linkErr error
		for _, cr := range created {
			a := cr.action

			// Link to explicit LinkTo target
			if a.LinkTo != "" {
				linkType := a.LinkType
				if linkType == "" {
					linkType = "relates_to"
				}
				if err := client.LinkIssues(cr.key, a.LinkTo, linkType); err != nil {
					linkErr = fmt.Errorf("linking %s -> %s: %w", cr.key, a.LinkTo, err)
				}
			}

			// Link to depends_on_order issues
			for _, depOrder := range a.DependsOnOrder {
				if depKey, ok := orderToKey[depOrder]; ok {
					if err := client.LinkIssues(cr.key, depKey, "is_blocked_by"); err != nil && linkErr == nil {
						linkErr = fmt.Errorf("linking dependency %s -> %s: %w", cr.key, depKey, err)
					}
				}
			}
		}

		return approvalExecDoneMsg{
			results: results,
			err:     linkErr,
		}
	}
}

// selectedCount returns the number of selected items.
func (v *ApprovalView) selectedCount() int {
	count := 0
	for _, item := range v.items {
		if item.Selected {
			count++
		}
	}
	return count
}

// actionSummary returns a one-line summary for a JiraAction.
func (v *ApprovalView) actionSummary(a jira.JiraAction) string {
	switch a.Type {
	case "create_issue":
		if a.Summary != "" {
			return a.Summary
		}
		return fmt.Sprintf("Create %s in %s", a.IssueType, a.Project)
	case "update_field":
		return fmt.Sprintf("Update %s", a.IssueKey)
	case "comment":
		return fmt.Sprintf("Comment on %s", a.IssueKey)
	default:
		return a.Type
	}
}

// actionIcon returns an icon string for an action type.
func (v *ApprovalView) actionIcon(typ string) string {
	switch typ {
	case "update_field":
		return "~"
	case "create_issue":
		return "+"
	case "comment":
		return "#"
	default:
		return "?"
	}
}

// CapturesInput always returns true since ApprovalView is modal-like.
func (v *ApprovalView) CapturesInput() bool { return true }

// ShortHelp returns the keybinding hint string.
func (v *ApprovalView) ShortHelp() string {
	if v.inputMode {
		return "Enter: Send  Esc: Cancel"
	}
	return "↑↓: Navigate  Space: Toggle  a: Toggle all  Enter: Expand  i: Iterate  x: Execute  Esc: Cancel"
}
