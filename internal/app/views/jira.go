package views

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
	"gitlab.com/tanevanwifferen1/singularity/internal/config"
	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/jira"
	"gitlab.com/tanevanwifferen1/singularity/internal/project"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// issueKeyRe matches a Jira issue key like "PROJ-123".
var issueKeyRe = regexp.MustCompile(`(?i)^[A-Z][A-Z0-9_]+-\d+$`)

// workflowStep constants drive the multi-step workflow modal.
const (
	workflowStepChoose       = "choose"        // pick new vs existing worktree
	workflowStepNewConfirm   = "new-confirm"   // confirm creating a fresh worktree
	workflowStepSelectWT     = "select-wt"     // browse existing worktrees
	workflowStepExistConfirm = "exist-confirm" // confirm running on existing worktree
	workflowStepExtraMsg     = "extra-msg"     // optional extra message for the agent
)

// jiraLoadedMsg carries freshly fetched Jira issues back to the view.
type jiraLoadedMsg struct {
	result *jira.SearchResult
	err    error
}

// jiraWorkflowDoneMsg signals that worktree creation has finished.
type jiraWorkflowDoneMsg struct {
	agentID  string
	repoPath string
	err      error
}

// jiraAIStartedMsg signals that a refine/create agent has started.
type jiraAIStartedMsg struct {
	agentID string
	mode    string // "refine" or "create"
	err     error
}

// jiraAITickMsg triggers periodic polling of agent output.
type jiraAITickMsg struct{}

// jiraAIOutputMsg carries new output entries from the agent.
type jiraAIOutputMsg struct {
	entries  []engine.OutputEntry
	done     bool
	actions  []jira.JiraAction // non-nil when agent completed and actions parsed
	parseErr error
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

	// Dependencies wired by app
	eng      *engine.Engine
	proj     *project.Project
	repoPath string

	// Search / JQL input mode
	searchMode  bool
	searchInput string

	// Detail pane
	showDetail  bool
	detailIssue *jira.Issue

	// Workflow confirmation modal
	showWorkflowConfirm bool
	workflowIssue       *jira.Issue
	workflowBranch      string
	workflowStatusMsg   string
	workflowStep        string

	// Existing-worktree picker (workflowStepSelectWT)
	existingWTs   []git.Worktree
	selectedWTIdx int

	// Extra message input (workflowStepExtraMsg)
	workflowExtraMsg     string
	workflowFromExisting bool // true if coming from existing-worktree path

	// Refine / Create agent mode
	aiMode          string // "", "refine", "create"
	aiAgentID       string
	aiOutputEntries []engine.OutputEntry
	aiOutputOffset  int
	approvalView    *ApprovalView

	// Text-input for create mode without a ticket
	showTextInput bool
	textInput     string
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

// SetEngine wires the agent engine.
func (v *JiraView) SetEngine(eng *engine.Engine) { v.eng = eng }

// SetProject wires the project (project mode).
func (v *JiraView) SetProject(proj *project.Project) { v.proj = proj }

// SetRepoPath sets the single-repo path (single-repo mode).
func (v *JiraView) SetRepoPath(path string) { v.repoPath = path }

// CapturesInput reports whether the view is consuming all keyboard input.
func (v *JiraView) CapturesInput() bool {
	return v.searchMode || v.showTextInput || v.showWorkflowConfirm || v.aiMode != "" || v.approvalView != nil
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

func (v *JiraView) fetchCmd(query string) tea.Cmd {
	return func() tea.Msg {
		if issueKeyRe.MatchString(strings.TrimSpace(query)) {
			issue, err := v.client.GetIssue(strings.TrimSpace(query))
			if err != nil {
				return jiraLoadedMsg{err: err}
			}
			return jiraLoadedMsg{result: &jira.SearchResult{Total: 1, Issues: []jira.Issue{*issue}}}
		}
		result, err := v.client.SearchIssues(query, 50)
		return jiraLoadedMsg{result: result, err: err}
	}
}

// Update handles messages.
func (v *JiraView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Forward internal approval messages
	if v.approvalView != nil {
		switch msg.(type) {
		case approvalExecDoneMsg:
			_, cmd := v.approvalView.Update(msg)
			return v, cmd
		}
	}

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

	case jiraWorkflowDoneMsg:
		if msg.err != nil {
			v.workflowStatusMsg = fmt.Sprintf("Error: %v", msg.err)
			v.showWorkflowConfirm = false
			return v, nil
		}
		v.showWorkflowConfirm = false
		v.workflowStatusMsg = fmt.Sprintf("Agent started for %s", v.workflowBranch)
		// Navigate to Agents view
		return v, func() tea.Msg {
			return ViewChangeMsg{ViewName: "Agents"}
		}

	case jiraAIStartedMsg:
		if msg.err != nil {
			v.err = msg.err
			v.aiMode = ""
			return v, nil
		}
		v.aiAgentID = msg.agentID
		return v, v.pollAIOutput()

	case jiraAITickMsg:
		if v.aiMode != "" && v.aiAgentID != "" {
			return v, v.fetchAIOutput()
		}

	case jiraAIOutputMsg:
		if len(msg.entries) > 0 {
			v.aiOutputEntries = append(v.aiOutputEntries, msg.entries...)
			v.aiOutputOffset += len(msg.entries)
		}
		if msg.done {
			if msg.parseErr != nil {
				v.err = fmt.Errorf("failed to parse agent output: %w", msg.parseErr)
				v.aiMode = ""
				return v, nil
			}
			if len(msg.actions) > 0 {
				v.approvalView = NewApprovalView(msg.actions, v.client)
				return v, nil
			}
			// No actions produced
			v.aiMode = ""
			v.workflowStatusMsg = "Agent completed but produced no actions"
			return v, nil
		}
		// Keep polling
		return v, v.pollAIOutput()

	case ApprovalDoneMsg:
		v.approvalView = nil
		v.aiMode = ""
		v.aiAgentID = ""
		v.aiOutputEntries = nil
		if msg.Executed {
			v.workflowStatusMsg = "Actions executed successfully"
			if msg.Err != nil {
				v.workflowStatusMsg = fmt.Sprintf("Actions executed with errors: %v", msg.Err)
			}
		}
		return v, nil

	case tea.KeyMsg:
		// Approval view active
		if v.approvalView != nil {
			_, cmd := v.approvalView.Update(msg)
			return v, cmd
		}

		// AI agent running - allow Esc to cancel
		if v.aiMode != "" && v.approvalView == nil {
			if msg.String() == "esc" {
				if v.aiAgentID != "" && v.eng != nil {
					v.eng.KillAgent(v.aiAgentID)
				}
				v.aiMode = ""
				v.aiAgentID = ""
				v.aiOutputEntries = nil
				return v, nil
			}
			return v, nil // swallow other keys while agent runs
		}

		// Workflow confirmation modal
		if v.showWorkflowConfirm {
			return v, v.handleWorkflowConfirm(msg)
		}

		// Detail pane active
		if v.showDetail {
			switch msg.String() {
			case "w":
				if v.detailIssue != nil {
					return v, v.triggerWorkflow(v.detailIssue)
				}
			case "esc":
				v.showDetail = false
				v.detailIssue = nil
			}
			return v, nil
		}

		// Text input for create mode
		if v.showTextInput {
			return v, v.handleTextInput(msg)
		}

		// Search / JQL input mode
		if v.searchMode {
			return v, v.handleSearchInput(msg)
		}

		switch msg.String() {
		case "R":
			v.loading = true
			v.err = nil
			return v, v.fetchCmd(v.defaultJQL())

		case "r":
			// Refine: launch agent on selected ticket
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				return v, v.startAIMode("refine", &item)
			}

		case "c":
			// Create stories from selected ticket, or from raw text if none selected
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				return v, v.startAIMode("create", &item)
			}
			v.showTextInput = true
			v.textInput = ""
			return v, nil

		case "s":
			v.searchMode = true
			v.searchInput = ""
			return v, nil

		case "w":
			if item, idx := v.filter.SelectedItem(); idx >= 0 {
				return v, v.triggerWorkflow(&item)
			}
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

// triggerWorkflow initiates the workflow confirmation modal.
func (v *JiraView) triggerWorkflow(issue *jira.Issue) tea.Cmd {
	v.workflowIssue = issue
	v.workflowBranch = issueToBranchName(issue)
	v.showWorkflowConfirm = true
	v.workflowStep = workflowStepChoose
	v.existingWTs = nil
	v.selectedWTIdx = 0
	v.workflowExtraMsg = ""
	v.workflowFromExisting = false
	return nil
}

// startAIMode launches the appropriate refine/create agent.
func (v *JiraView) startAIMode(mode string, issue *jira.Issue) tea.Cmd {
	v.aiMode = mode
	v.aiAgentID = ""
	v.aiOutputEntries = nil
	v.aiOutputOffset = 0
	v.approvalView = nil

	eng := v.eng
	repoPath := v.repoPath
	if repoPath == "" && v.proj != nil && len(v.proj.Repos) > 0 {
		repoPath = v.proj.Repos[0].Path
	}

	issueCopy := *issue
	cfg := v.cfg

	return func() tea.Msg {
		if eng == nil {
			return jiraAIStartedMsg{err: fmt.Errorf("agent engine not available"), mode: mode}
		}
		var id string
		var err error
		switch mode {
		case "refine":
			id, err = jira.RefineTicket(eng, &issueCopy, repoPath)
		case "create":
			project := cfg.DefaultProject
			if project == "" {
				// extract from issue key
				if idx := strings.Index(issueCopy.Key, "-"); idx > 0 {
					project = issueCopy.Key[:idx]
				}
			}
			id, err = jira.CreateStories(eng, &issueCopy, "", project, repoPath)
		}
		return jiraAIStartedMsg{agentID: id, mode: mode, err: err}
	}
}

// pollAIOutput returns a tea.Cmd that polls agent output after a short delay.
func (v *JiraView) pollAIOutput() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return jiraAITickMsg{}
	})
}

// fetchAIOutput returns a tea.Cmd that retrieves new output entries from the agent.
func (v *JiraView) fetchAIOutput() tea.Cmd {
	eng := v.eng
	agentID := v.aiAgentID
	offset := v.aiOutputOffset
	repoPath := v.repoPath
	if repoPath == "" && v.proj != nil && len(v.proj.Repos) > 0 {
		repoPath = v.proj.Repos[0].Path
	}

	return func() tea.Msg {
		entries, err := eng.GetOutputEntries(agentID, offset)
		if err != nil {
			return jiraAIOutputMsg{done: true}
		}

		state, _ := eng.GetStatus(agentID)
		done := state == engine.AgentComplete || state == engine.AgentError || state == engine.AgentKilled

		var actions []jira.JiraAction
		var parseErr error
		if done {
			actionsPath := filepath.Join(repoPath, ".jira-actions.json")
			actions, parseErr = jira.ParseJiraActions(actionsPath)
		}

		return jiraAIOutputMsg{
			entries:  entries,
			done:     done,
			actions:  actions,
			parseErr: parseErr,
		}
	}
}

// handleWorkflowConfirm handles key input in the multi-step workflow modal.
func (v *JiraView) handleWorkflowConfirm(msg tea.KeyMsg) tea.Cmd {
	switch v.workflowStep {

	case workflowStepChoose:
		switch msg.String() {
		case "n":
			v.workflowExtraMsg = ""
			v.workflowStep = workflowStepNewConfirm
		case "e":
			repoPath := v.repoPath
			if repoPath == "" && v.proj != nil && len(v.proj.Repos) > 0 {
				repoPath = v.proj.Repos[0].Path
			}
			if repoPath != "" {
				wts, err := git.GetWorktrees(repoPath)
				if err == nil && len(wts) > 1 {
					v.existingWTs = wts[1:] // skip the main worktree
				} else {
					v.existingWTs = nil
				}
			}
			v.selectedWTIdx = 0
			v.workflowStep = workflowStepSelectWT
		case "esc", "q":
			v.showWorkflowConfirm = false
			v.workflowIssue = nil
			v.workflowStep = ""
		}

	case workflowStepNewConfirm:
		switch msg.String() {
		case "enter":
			issue := v.workflowIssue
			branch := v.workflowBranch
			eng := v.eng
			proj := v.proj
			repoPath := v.repoPath
			jiraURL := v.cfg.BaseURL + "/browse/" + issue.Key
			extraMsg := v.workflowExtraMsg
			v.workflowFromExisting = false
			return func() tea.Msg {
				return startJiraWorkflow(issue, branch, eng, proj, repoPath, jiraURL, extraMsg)
			}
		case "esc":
			v.workflowStep = workflowStepChoose
			v.workflowExtraMsg = ""
		case "ctrl+w":
			v.workflowExtraMsg = components.DeleteWordEnd(v.workflowExtraMsg)
		case "backspace":
			if len(v.workflowExtraMsg) > 0 {
				v.workflowExtraMsg = v.workflowExtraMsg[:len(v.workflowExtraMsg)-1]
			}
		default:
			if msg.Paste && len(msg.Runes) > 0 {
				v.workflowExtraMsg += string(msg.Runes)
			} else if len(msg.Runes) == 1 {
				r := msg.Runes[0]
				if r >= 32 {
					v.workflowExtraMsg += string(r)
				}
			}
		}

	case workflowStepSelectWT:
		switch msg.String() {
		case "up", "k":
			if v.selectedWTIdx > 0 {
				v.selectedWTIdx--
			}
		case "down", "j":
			if v.selectedWTIdx < len(v.existingWTs)-1 {
				v.selectedWTIdx++
			}
		case "enter":
			if len(v.existingWTs) > 0 {
				v.workflowBranch = v.existingWTs[v.selectedWTIdx].Branch
				v.workflowExtraMsg = ""
				v.workflowStep = workflowStepExistConfirm
			}
		case "esc":
			v.workflowStep = workflowStepChoose
		}

	case workflowStepExistConfirm:
		switch msg.String() {
		case "enter":
			if len(v.existingWTs) > 0 && v.selectedWTIdx < len(v.existingWTs) {
				wt := v.existingWTs[v.selectedWTIdx]
				issue := v.workflowIssue
				eng := v.eng
				extraMsg := v.workflowExtraMsg
				v.workflowFromExisting = true
				return func() tea.Msg {
					return startJiraWorkflowExisting(issue, wt.Path, eng, extraMsg)
				}
			}
		case "esc":
			v.workflowStep = workflowStepSelectWT
			v.workflowExtraMsg = ""
		case "ctrl+w":
			v.workflowExtraMsg = components.DeleteWordEnd(v.workflowExtraMsg)
		case "backspace":
			if len(v.workflowExtraMsg) > 0 {
				v.workflowExtraMsg = v.workflowExtraMsg[:len(v.workflowExtraMsg)-1]
			}
		default:
			if msg.Paste && len(msg.Runes) > 0 {
				v.workflowExtraMsg += string(msg.Runes)
			} else if len(msg.Runes) == 1 {
				r := msg.Runes[0]
				if r >= 32 {
					v.workflowExtraMsg += string(r)
				}
			}
		}

	}
	return nil
}

// startJiraWorkflow creates worktrees and spawns an agent.
func startJiraWorkflow(issue *jira.Issue, branch string, eng *engine.Engine, proj *project.Project, repoPath string, jiraURL string, extraMsg string) jiraWorkflowDoneMsg {
	if eng == nil {
		return jiraWorkflowDoneMsg{err: fmt.Errorf("agent engine not available")}
	}

	agentPrompt := buildJiraAgentPrompt(issue, extraMsg)

	// Project mode: create worktrees across all repos via FeatureWorkflow
	if proj != nil {
		baseDir := filepath.Join(os.TempDir(), "singularity-workflows")
		fw := project.NewFeatureWorkflow(proj, branch, baseDir)
		fw.JiraURL = jiraURL
		if err := fw.CreateAllWorktrees(); err != nil {
			return jiraWorkflowDoneMsg{err: fmt.Errorf("create worktrees: %w", err)}
		}
		workDir := fw.WorkflowDir()
		// Use the first worktree's repo path if the workflow dir isn't itself a repo
		if _, statErr := os.Stat(filepath.Join(workDir, ".git")); os.IsNotExist(statErr) {
			for _, wr := range fw.Repos {
				if wr.WorktreeCreated {
					workDir = wr.WorktreePath
					break
				}
			}
		}
		id, err := eng.StartAgent(workDir, agentPrompt, engine.AgentOptions{SmartRoute: true})
		if err != nil {
			return jiraWorkflowDoneMsg{err: fmt.Errorf("start agent: %w", err)}
		}
		fw.SetWorkflowAgentID(id)
		return jiraWorkflowDoneMsg{agentID: id, repoPath: workDir}
	}

	// Single-repo mode
	if repoPath == "" {
		return jiraWorkflowDoneMsg{err: fmt.Errorf("no repository configured")}
	}
	worktreePath := filepath.Join(filepath.Dir(repoPath), branch)
	if err := git.CreateWorktree(repoPath, worktreePath, branch, true); err != nil {
		return jiraWorkflowDoneMsg{err: fmt.Errorf("create worktree: %w", err)}
	}
	id, err := eng.StartAgent(worktreePath, agentPrompt, engine.AgentOptions{SmartRoute: true})
	if err != nil {
		return jiraWorkflowDoneMsg{err: fmt.Errorf("start agent: %w", err)}
	}
	return jiraWorkflowDoneMsg{agentID: id, repoPath: worktreePath}
}

// startJiraWorkflowExisting spawns an agent in an already-existing worktree.
func startJiraWorkflowExisting(issue *jira.Issue, worktreePath string, eng *engine.Engine, extraMsg string) jiraWorkflowDoneMsg {
	if eng == nil {
		return jiraWorkflowDoneMsg{err: fmt.Errorf("agent engine not available")}
	}
	agentPrompt := buildJiraAgentPrompt(issue, extraMsg)
	id, err := eng.StartAgent(worktreePath, agentPrompt, engine.AgentOptions{SmartRoute: true})
	if err != nil {
		return jiraWorkflowDoneMsg{err: fmt.Errorf("start agent: %w", err)}
	}
	return jiraWorkflowDoneMsg{agentID: id, repoPath: worktreePath}
}

// buildJiraAgentPrompt constructs an agent task prompt from a Jira issue.
func buildJiraAgentPrompt(issue *jira.Issue, extraMsg string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Implement Jira ticket %s.\n\n", issue.Key))
	b.WriteString(fmt.Sprintf("Summary: %s\n", issue.Summary))
	b.WriteString(fmt.Sprintf("Type: %s\n", issue.Type))
	b.WriteString(fmt.Sprintf("Priority: %s\n", issue.Priority))
	b.WriteString(fmt.Sprintf("Status: %s\n", issue.Status))
	if issue.Assignee != "" {
		b.WriteString(fmt.Sprintf("Assignee: %s\n", issue.Assignee))
	}
	if len(issue.Labels) > 0 {
		b.WriteString(fmt.Sprintf("Labels: %s\n", strings.Join(issue.Labels, ", ")))
	}
	if issue.Description != "" {
		b.WriteString("\nDescription:\n")
		b.WriteString(issue.Description)
		b.WriteString("\n")
	}
	b.WriteString("\nYou are working in a dedicated worktree/branch for this ticket. " +
		"Implement the requirements described above. When done, ensure all tests pass.")
	if extraMsg != "" {
		b.WriteString("\n\nAdditional instructions:\n")
		b.WriteString(extraMsg)
	}
	return b.String()
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// issueToBranchName derives a git branch name from a Jira issue.
// Format: key-summary-slug (lowercase, hyphens, max 60 chars).
func issueToBranchName(issue *jira.Issue) string {
	slug := strings.ToLower(issue.Summary)
	// Replace non-alphanumeric runs with a hyphen
	slug = nonAlnum.ReplaceAllStringFunc(slug, func(s string) string {
		for _, r := range s {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return "-"
			}
		}
		return "-"
	})
	slug = strings.Trim(slug, "-")
	key := strings.ToLower(issue.Key)
	branch := key + "-" + slug
	if len(branch) > 60 {
		branch = branch[:60]
		branch = strings.TrimRight(branch, "-")
	}
	return branch
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

func (v *JiraView) handleTextInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		text := strings.TrimSpace(v.textInput)
		v.showTextInput = false
		v.textInput = ""
		if text == "" {
			return nil
		}
		return v.startAIFromText(text)

	case "esc":
		v.showTextInput = false
		v.textInput = ""

	case "ctrl+w":
		v.textInput = components.DeleteWordEnd(v.textInput)

	case "backspace":
		if len(v.textInput) > 0 {
			v.textInput = v.textInput[:len(v.textInput)-1]
		}

	default:
		if msg.Paste && len(msg.Runes) > 0 {
			v.textInput += string(msg.Runes)
		} else if len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r >= 32 {
				v.textInput += string(r)
			}
		}
	}
	return nil
}

func (v *JiraView) startAIFromText(text string) tea.Cmd {
	v.aiMode = "create"
	v.aiAgentID = ""
	v.aiOutputEntries = nil
	v.aiOutputOffset = 0
	v.approvalView = nil

	eng := v.eng
	repoPath := v.repoPath
	if repoPath == "" && v.proj != nil && len(v.proj.Repos) > 0 {
		repoPath = v.proj.Repos[0].Path
	}

	project := v.cfg.DefaultProject

	return func() tea.Msg {
		if eng == nil {
			return jiraAIStartedMsg{err: fmt.Errorf("agent engine not available"), mode: "create"}
		}
		id, err := jira.CreateStories(eng, nil, text, project, repoPath)
		return jiraAIStartedMsg{agentID: id, mode: "create", err: err}
	}
}

// View renders the Jira issues view.
func (v *JiraView) View() string {
	th := theme.GetTheme()

	if v.loading {
		return th.StatsStyle.Render(" Loading Jira issues...")
	}

	var s strings.Builder

	// Approval view
	if v.approvalView != nil {
		s.WriteString(v.approvalView.View())
		return s.String()
	}

	// AI agent running
	if v.aiMode != "" {
		modeLabel := "Refining"
		if v.aiMode == "create" {
			modeLabel = "Creating stories"
		}
		s.WriteString(th.InfoStyle.Render(fmt.Sprintf(" %s... (Esc to cancel)", modeLabel)))
		s.WriteString("\n\n")
		// Show last few output entries
		start := 0
		if len(v.aiOutputEntries) > 15 {
			start = len(v.aiOutputEntries) - 15
		}
		for _, entry := range v.aiOutputEntries[start:] {
			prefix := ""
			switch entry.Source {
			case "tool_use":
				prefix = th.BranchStyle.Render("→ ")
			case "tool_result":
				prefix = th.MutedTextStyle.Render("  ")
			case "text":
				prefix = "  "
			case "system":
				prefix = th.MutedTextStyle.Render("⚙ ")
			case "error":
				prefix = th.DashboardErrorStyle.Render("✗ ")
			}
			line := entry.Content
			if len(line) > v.width-4 {
				line = line[:v.width-7] + "..."
			}
			s.WriteString(prefix + line + "\n")
		}
		return s.String()
	}

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

	// Text input for create mode
	if v.showTextInput {
		s.WriteString(th.DashboardTitle.Render(" Create Stories from Text "))
		s.WriteString("\n")
		s.WriteString(fmt.Sprintf(" > %s█", v.textInput))
		s.WriteString("\n")
		s.WriteString(th.Help.Render(" Paste or type a requirement, then Enter to start · Esc: cancel "))
		s.WriteString("\n\n")
	}

	// Search input
	if v.searchMode {
		s.WriteString(th.DashboardTitle.Render(" Search "))
		s.WriteString("\n")
		s.WriteString(fmt.Sprintf(" > %s_", v.searchInput))
		s.WriteString("\n")
		s.WriteString(th.Help.Render(" Enter: run query   Esc: cancel   (issue key e.g. PROJ-123 or JQL) "))
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
		s.WriteString(th.Help.Render(" / to filter • s: search • R: refresh • r: refine • c: create • Enter: detail • ↑↓: navigate "))
		s.WriteString("\n\n")
		s.WriteString(v.filter.View())
	}

	// Workflow modal (multi-step)
	if v.showWorkflowConfirm && v.workflowIssue != nil {
		s.WriteString("\n")
		s.WriteString(v.renderWorkflowModal())
	}

	// Status message (e.g., after workflow starts)
	if v.workflowStatusMsg != "" {
		s.WriteString("\n")
		s.WriteString(th.DashboardAccentStyle.Render(" " + v.workflowStatusMsg + " "))
	}

	// Footer
	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")
	s.WriteString(th.Help.Render(" R: Refresh   s: Search   /: Filter   ↑↓: Navigate   Enter: Detail   w: Workflow   r: Refine   c: Create "))

	return s.String()
}

// renderWorkflowModal renders the appropriate modal for the current workflow step.
func (v *JiraView) renderWorkflowModal() string {
	mw := modalWidth(v.width)
	issue := v.workflowIssue

	switch v.workflowStep {
	case workflowStepChoose:
		return renderModal("Start Workflow", []string{
			fmt.Sprintf("Ticket: %s – %s", issue.Key, truncate(issue.Summary, mw-20)),
			"",
			"n  New worktree + branch",
			"e  Use existing worktree",
			"",
			"Esc: cancel",
		}, mw)

	case workflowStepNewConfirm:
		input := v.workflowExtraMsg + "█"
		return renderModal("New Worktree", []string{
			fmt.Sprintf("Ticket: %s", issue.Key),
			fmt.Sprintf("Branch: %s", v.workflowBranch),
			"",
			"Custom instructions (optional):",
			input,
			"",
			"Enter: start · Esc: cancel",
		}, mw)

	case workflowStepSelectWT:
		lines := []string{
			fmt.Sprintf("Ticket: %s", issue.Key),
			"Select a worktree (↑/↓ · Enter · Esc: back)",
			"",
		}
		if len(v.existingWTs) == 0 {
			lines = append(lines, "  (no worktrees found)")
		} else {
			for i, wt := range v.existingWTs {
				prefix := "  "
				if i == v.selectedWTIdx {
					prefix = " >"
				}
				label := wt.Branch
				if label == "" {
					label = filepath.Base(wt.Path)
				}
				lines = append(lines, fmt.Sprintf("%s %s", prefix, truncate(label, mw-6)))
			}
		}
		return renderModal("Existing Worktrees", lines, mw)

	case workflowStepExistConfirm:
		wt := v.existingWTs[v.selectedWTIdx]
		label := wt.Branch
		if label == "" {
			label = filepath.Base(wt.Path)
		}
		input := v.workflowExtraMsg + "█"
		return renderModal("Run on Worktree", []string{
			fmt.Sprintf("Ticket: %s", issue.Key),
			fmt.Sprintf("Worktree: %s", truncate(label, mw-12)),
			fmt.Sprintf("Path:     %s", truncate(wt.Path, mw-10)),
			"",
			"Custom instructions (optional):",
			input,
			"",
			"Enter: start · Esc: cancel",
		}, mw)

	}
	return ""
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
	s.WriteString(th.Help.Render(" Esc: back to list   w: start workflow "))

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
	return "R: Refresh  s: Search  /: Filter  r: Refine  c: Create  w: Workflow"
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
		{Key: "R", Description: "Refresh issues"},
		{Key: "s", Description: "Search (issue key or JQL)"},
		{Key: "/", Description: "Filter list"},
		{Key: "r", Description: "Refine selected ticket"},
		{Key: "c", Description: "Create stories from selected ticket"},
		{Key: "w", Description: "Start workflow for selected ticket"},
		{Key: "↑/k", Description: "Navigate up"},
		{Key: "↓/j", Description: "Navigate down"},
		{Key: "Enter", Description: "Show issue detail"},
		{Key: "Esc", Description: "Back / clear filter"},
	}
}
