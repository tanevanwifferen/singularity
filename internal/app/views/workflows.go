package views

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
	"gitlab.com/tanevanwifferen1/singularity/internal/config"
	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/jira"
	"gitlab.com/tanevanwifferen1/singularity/internal/project"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// pushCheckDoneMsg carries the list of repos that need pushing.
type pushCheckDoneMsg struct {
	repos []string
	force bool
}

// pushDoneMsg signals that batch push has completed.
type pushDoneMsg struct{}

// mrDoneMsg signals that batch MR creation has completed.
type mrDoneMsg struct{}

// branchStatusDoneMsg signals that branch status refresh has completed.
type branchStatusDoneMsg struct{}

// WorkflowTickMsg is sent periodically to refresh workflow agent status.
type WorkflowTickMsg struct{}

// WorkflowsView manages multi-repo feature workflows (worktrees, push, MR, agents).
type WorkflowsView struct {
	viewBase
	proj *project.Project

	// Workflow state
	workflows        []*project.FeatureWorkflow
	selectedWorkflow int
	filter           *components.Filter[*project.FeatureWorkflow]

	// Workflow start modal
	showWorkflowStart   bool
	workflowBranchInput components.TextInput
	workflowBaseDir     string

	// Workflow cleanup confirmation
	cleanupConfirm components.ConfirmPrompt

	// Detach all worktrees for selected workflow
	showDetachWorkflowConfirm bool
	detachWorkflowResult      string

	// Flash messages
	workflowStatusMsg string
	pushResults       string
	mrResults         string

	// Batch push state
	pushConfirm   components.ConfirmPrompt
	pushableRepos []string // repos with commits to push (computed async)

	// Batch MR creation state
	batchMRConfirm components.ConfirmPrompt
	showMRSummary  bool
	mrSummaryLines []string

	// Agent orchestration
	engine           *engine.Engine
	showAgentPrompt  bool
	agentPromptInput components.TextInput

	// Auto-refresh tick for live agent status
	workflowTicking   bool
	workflowAgentSnap *engine.AgentSnapshot

	// Jira ticket picker
	jiraPicker       *JiraPickerState
	jiraConfirmIssue *jira.Issue          // issue pending workflow-start confirmation
	jiraExtraInput   components.TextInput // optional extra instructions for the agent

	// Drill-down diff view
	workflowDiffView *WorkflowDiffView
}

// NewWorkflowsView creates a new workflows view.
func NewWorkflowsView(proj *project.Project) *WorkflowsView {
	v := &WorkflowsView{
		viewBase: viewBase{width: 80, height: 24},
		proj:     proj,
	}
	v.workflowBaseDir = defaultWorkflowBaseDir(proj.Name)
	v.filter = components.NewFilter([]*project.FeatureWorkflow{}, v.renderWorkflowItem)
	v.filter.SetHeight(v.height - 10)
	return v
}

// defaultWorkflowBaseDir returns ~/.worktrees/<projectName>/
func defaultWorkflowBaseDir(projectName string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	return filepath.Join(home, ".worktrees", projectName)
}

// HasActiveWorkflow returns true if any feature workflows exist.
func (v *WorkflowsView) HasActiveWorkflow() bool {
	return len(v.workflows) > 0
}

// SetEngine sets the agent engine.
func (v *WorkflowsView) SetEngine(eng *engine.Engine) {
	v.engine = eng
}

// SetJiraConfig wires Jira configuration so the Jira ticket picker is available.
func (v *WorkflowsView) SetJiraConfig(cfg config.JiraConfig) {
	v.jiraPicker = NewJiraPickerState(cfg)
	if v.jiraPicker != nil {
		v.jiraPicker.SetSize(v.width, v.height)
	}
}

// SetProject updates the project reference.
func (v *WorkflowsView) SetProject(proj *project.Project) {
	v.proj = proj
}

// SetWorkflowDiffView wires the drill-down diff view for showing workflow changes.
func (v *WorkflowsView) SetWorkflowDiffView(dv *WorkflowDiffView) {
	v.workflowDiffView = dv
}

// Init initializes the workflows view.
func (v *WorkflowsView) Init() tea.Cmd {
	return tea.Batch(
		func() tea.Msg {
			v.loadWorkflows()
			return RefreshDoneMsg{}
		},
		v.refreshBranchStatusCmd(),
	)
}

// loadWorkflows loads persisted workflows from disk and discovers new ones.
func (v *WorkflowsView) loadWorkflows() {
	if v.proj == nil {
		return
	}

	key := v.proj.Name
	if len(v.workflows) == 0 {
		if loaded, err := project.LoadWorkflows(key, v.proj); err == nil && len(loaded) > 0 {
			v.workflows = loaded
			v.selectedWorkflow = 0
		}
	}
	v.rebuildFilter()
}

func (v *WorkflowsView) rebuildFilter() {
	items := make([]*project.FeatureWorkflow, len(v.workflows))
	copy(items, v.workflows)
	v.filter.SetItems(items)
}

// projectKey returns a filesystem-safe key for the current project.
func (v *WorkflowsView) projectKey() string {
	if v.proj != nil {
		return v.proj.Name
	}
	return ""
}

// saveWorkflows persists the current workflow state to disk.
func (v *WorkflowsView) saveWorkflows() {
	if key := v.projectKey(); key != "" {
		project.SaveWorkflows(key, v.workflows)
	}
}

// currentWorkflow returns the currently selected workflow, or nil if none.
func (v *WorkflowsView) currentWorkflow() *project.FeatureWorkflow {
	if len(v.workflows) == 0 || v.selectedWorkflow >= len(v.workflows) {
		return nil
	}
	return v.workflows[v.selectedWorkflow]
}

// removeCurrentWorkflow removes the currently selected workflow from the slice.
func (v *WorkflowsView) removeCurrentWorkflow() {
	if len(v.workflows) == 0 {
		return
	}
	idx := v.selectedWorkflow
	v.workflows = append(v.workflows[:idx], v.workflows[idx+1:]...)
	if v.selectedWorkflow >= len(v.workflows) && v.selectedWorkflow > 0 {
		v.selectedWorkflow--
	}
	if len(v.workflows) == 0 {
		v.workflowAgentSnap = nil
	}
	v.rebuildFilter()
}

// spawnAgentForWorkflow spawns a single agent at the workflow's BaseDir.
func (v *WorkflowsView) spawnAgentForWorkflow(task string) {
	wf := v.currentWorkflow()
	if wf == nil || v.engine == nil {
		return
	}

	var ctxFiles []string
	if v.proj != nil {
		ctxFiles = v.proj.ContextFiles
	}

	stats := v.engine.Stats()
	available := stats.MaxAgents - stats.Active
	if available < 1 {
		v.workflowStatusMsg = fmt.Sprintf("Engine capacity exceeded: no slots available (%d/%d active)",
			stats.Active, stats.MaxAgents)
		return
	}

	// Build commit instructions listing each repo worktree
	fullTask := task + "\n\n" + buildWorkflowCommitInstructions(wf)

	id, err := v.engine.StartAgent(wf.WorkflowDir(), fullTask, engine.AgentOptions{
		ContextFiles: ctxFiles,
		SmartRoute:   true,
		WorkflowID:   wf.BranchName,
	})
	if err != nil {
		v.workflowStatusMsg = fmt.Sprintf("Agent spawn failed: %v", err)
	} else {
		wf.SetWorkflowAgentID(id)
		v.workflowStatusMsg = fmt.Sprintf(" Agent spawned for '%s'\n   Next: press 'p' to push when ready", wf.BranchName)
	}
}

// Update handles update events.
func (v *WorkflowsView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return v.handleWorkflowsKeyMsg(msg)

	case tea.WindowSizeMsg:
		v.width = msg.Width
		v.height = msg.Height
		if v.filter != nil {
			v.filter.SetHeight(msg.Height - 10)
		}
		if v.jiraPicker != nil {
			v.jiraPicker.SetSize(msg.Width, msg.Height)
		}

	case tea.MouseMsg:
		if v.filter != nil {
			v.filter.HandleMouse(msg)
		}

	default:
		return v.handleWorkflowsMsg(msg)
	}

	return v, nil
}

// handleWorkflowsKeyMsg handles all keyboard input for the workflows view,
// including modal dispatch and the main key switch.
func (v *WorkflowsView) handleWorkflowsKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Clear flash messages on any key press
	v.workflowStatusMsg = ""
	v.pushResults = ""
	v.mrResults = ""
	v.detachWorkflowResult = ""

	// Handle Jira picker
	if v.jiraPicker.IsOpen() {
		return v, v.handleJiraPickerKey(msg)
	}

	// Handle Jira confirm-start-workflow modal
	if v.jiraConfirmIssue != nil {
		return v, v.handleJiraWorkflowConfirm(msg)
	}

	// Handle workflow start modal
	if v.showWorkflowStart {
		return v, v.handleWorkflowStartInput(msg)
	}

	// Handle agent prompt modal
	if v.showAgentPrompt {
		return v, v.handleAgentPromptInput(msg)
	}

	// Handle workflow cleanup confirmation
	if v.showDetachWorkflowConfirm {
		return v, v.handleDetachWorkflowConfirm(msg)
	}

	if handled, cmd := v.cleanupConfirm.HandleKey(msg); handled {
		return v, cmd
	}

	// Handle batch push confirmation
	if handled, cmd := v.pushConfirm.HandleKey(msg); handled {
		return v, cmd
	}

	// Handle MR summary panel
	if v.showMRSummary {
		return v, v.handleMRSummary(msg)
	}

	// Handle batch MR creation confirmation
	if handled, cmd := v.batchMRConfirm.HandleKey(msg); handled {
		return v, cmd
	}

	// If filter is active, let it handle keys
	if v.filter != nil && v.filter.IsActive() {
		v.filter.Update(msg)
		return v, nil
	}

	switch msg.String() {
	case "r":
		return v, tea.Batch(
			func() tea.Msg {
				v.loadWorkflows()
				return RefreshDoneMsg{}
			},
			v.refreshBranchStatusCmd(),
		)
	case "w":
		v.showWorkflowStart = true
		v.workflowBranchInput.Clear()

	case "J":
		// Open Jira ticket picker to create a workflow from a ticket
		if v.jiraPicker.IsAvailable() {
			return v, v.jiraPicker.Open()
		}
	case "D":
		wf := v.currentWorkflow()
		if wf != nil {
			v.cleanupConfirm.Show("Remove Worktrees",
				fmt.Sprintf("Branch: %s\nThis will remove all worktrees and delete\nthe '%s' branch from all repos.", wf.BranchName, wf.BranchName),
				func() tea.Cmd {
					wf := v.currentWorkflow()
					if wf == nil {
						return nil
					}
					return func() tea.Msg {
						wf.RemoveAllWorktrees()
						return RefreshDoneMsg{}
					}
				})
		}
	case "H":
		wf := v.currentWorkflow()
		if wf != nil {
			v.showDetachWorkflowConfirm = true
		}
	case "a":
		v.handleStartAgent()
	case "p":
		return v, v.handleStartPush(false)
	case "P":
		return v, v.handleStartPush(true)
	case "M":
		v.handleStartBatchMR()
	case "d":
		wf := v.currentWorkflow()
		if wf != nil && v.workflowDiffView != nil {
			v.workflowDiffView.SetWorkflow(wf)
			return v, func() tea.Msg {
				return ViewChangeMsg{ViewName: "WorkflowDiff"}
			}
		}
	case "I":
		v.handleImport()
	case "j", "down":
		if len(v.workflows) > 1 && v.selectedWorkflow < len(v.workflows)-1 {
			v.selectedWorkflow++
			v.refreshWorkflowAgentSnap()
		}
	case "k", "up":
		if len(v.workflows) > 1 && v.selectedWorkflow > 0 {
			v.selectedWorkflow--
			v.refreshWorkflowAgentSnap()
		}
	case "/":
		if v.filter != nil {
			v.filter.Update(msg)
		}
	}

	return v, nil
}

// handleWorkflowsMsg handles non-key, non-standard messages for the workflows view
// (refresh, push, MR, tick, and Jira picker messages).
func (v *WorkflowsView) handleWorkflowsMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case RefreshDoneMsg:
		v.refreshWorkflowAgentSnap()
		wf := v.currentWorkflow()
		if wf != nil {
			st := wf.Status()
			switch st.State {
			case project.WorkflowActive:
				created := 0
				for _, wr := range wf.Repos {
					if wr.WorktreeCreated {
						created++
					}
				}
				v.workflowStatusMsg = fmt.Sprintf(" Worktrees created for '%s' across %d repos\n   Next: press 'a' to spawn an agent, or start working in the worktrees", wf.BranchName, created)
			case project.WorkflowDone:
				v.workflowStatusMsg = fmt.Sprintf("Worktrees and branches for '%s' removed", wf.BranchName)
				v.removeCurrentWorkflow()
			}
		}
		v.saveWorkflows()
		return v, v.refreshBranchStatusCmd()

	case branchStatusDoneMsg:
		// Branch statuses updated; re-render is automatic.

	case pushCheckDoneMsg:
		if len(msg.repos) == 0 {
			v.pushResults = "Nothing to push - all repos are up to date"
		} else {
			v.pushableRepos = msg.repos
			sort.Strings(v.pushableRepos)
			force := msg.force
			title := "Push All Repos"
			prompt := fmt.Sprintf("Push %d repo(s) to remote?", len(v.pushableRepos))
			if force {
				title = "Force Push All Repos"
				prompt = fmt.Sprintf("Force push (--force-with-lease) %d repo(s) to remote?", len(v.pushableRepos))
			}
			v.pushConfirm.ShowWithCancel(title, prompt,
				func() tea.Cmd {
					wf := v.currentWorkflow()
					v.pushableRepos = nil
					if wf == nil {
						return nil
					}
					return func() tea.Msg {
						wf.PushAll(force)
						return pushDoneMsg{}
					}
				},
				func() { v.pushableRepos = nil })
		}

	case pushDoneMsg:
		wf := v.currentWorkflow()
		if wf != nil {
			pushed := 0
			total := len(wf.Repos)
			for _, wr := range wf.Repos {
				if wr.Pushed {
					pushed++
				}
			}
			v.pushResults = fmt.Sprintf(" Pushed %d/%d repos\n   Next: press 'M' to create merge requests", pushed, total)
			v.saveWorkflows()
		}

	case mrDoneMsg:
		wf := v.currentWorkflow()
		if wf != nil {
			var lines []string
			for _, wr := range wf.Repos {
				if wr.MRURL != "" {
					title := wr.MRTitle
					if title == "" {
						title = "Merge feature branch"
					}
					lines = append(lines, fmt.Sprintf("  %s: %s — %s", wr.RepoName, title, wr.MRURL))
				}
			}
			v.mrResults = fmt.Sprintf(" Created %d MRs\n   Next: press 'D' to cleanup worktrees when merged", len(lines))
			if len(lines) > 0 {
				v.showMRSummary = true
				v.mrSummaryLines = lines
			}
			v.saveWorkflows()
		}

	case WorkflowTickMsg:
		v.refreshWorkflowAgentSnap()
		if v.hasRunningAgents() {
			return v, v.workflowTickCmd()
		}
		v.workflowTicking = false
		return v, nil

	case jiraPickerLoadedMsg:
		if cmd := v.jiraPicker.HandleMsg(msg); cmd != nil {
			return v, cmd
		}
	}

	return v, nil
}

// --- Jira picker handlers ---

// handleJiraPickerKey delegates key events to the Jira picker.
func (v *WorkflowsView) handleJiraPickerKey(msg tea.KeyMsg) tea.Cmd {
	cmd, done, confirmed, issue := v.jiraPicker.HandleKey(msg)
	if done && confirmed && issue != nil {
		v.jiraConfirmIssue = issue
	}
	return cmd
}

// handleJiraWorkflowConfirm handles input in the Jira workflow confirmation modal.
func (v *WorkflowsView) handleJiraWorkflowConfirm(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		issue := v.jiraConfirmIssue
		extraMsg := v.jiraExtraInput.Value
		v.jiraConfirmIssue = nil
		v.jiraExtraInput.Clear()
		return v.startWorkflowFromJira(issue, extraMsg)
	case "e":
		wf := v.currentWorkflow()
		if wf != nil {
			issue := v.jiraConfirmIssue
			extraMsg := v.jiraExtraInput.Value
			v.jiraConfirmIssue = nil
			v.jiraExtraInput.Clear()
			return v.startWorkflowFromJiraOnExisting(issue, wf, extraMsg)
		}
	case "esc":
		v.jiraConfirmIssue = nil
		v.jiraExtraInput.Clear()
	default:
		v.jiraExtraInput.HandleKey(msg)
	}
	return nil
}

// startWorkflowFromJira creates a FeatureWorkflow from a Jira issue and spawns an agent.
func (v *WorkflowsView) startWorkflowFromJira(issue *jira.Issue, extraMsg string) tea.Cmd {
	if issue == nil || v.proj == nil {
		return nil
	}
	branchName := issueToBranchName(issue)
	baseDir := v.workflowBaseDir
	agentPrompt := buildJiraAgentPrompt(issue, extraMsg)

	wf := project.NewFeatureWorkflow(v.proj, branchName, baseDir)
	v.workflows = append(v.workflows, wf)
	v.selectedWorkflow = len(v.workflows) - 1
	v.rebuildFilter()

	eng := v.engine
	var ctxFiles []string
	if v.proj != nil {
		ctxFiles = v.proj.ContextFiles
	}

	return func() tea.Msg {
		if err := wf.CreateAllWorktrees(); err != nil {
			v.workflowStatusMsg = fmt.Sprintf("Worktree creation failed: %v", err)
			return RefreshDoneMsg{}
		}

		if eng == nil {
			v.workflowStatusMsg = "Agent engine not available"
			return RefreshDoneMsg{}
		}

		// Build full task with commit instructions
		fullTask := agentPrompt + "\n\n" + buildWorkflowCommitInstructions(wf)

		id, err := eng.StartAgent(wf.WorkflowDir(), fullTask, engine.AgentOptions{
			ContextFiles: ctxFiles,
			SmartRoute:   true,
			WorkflowID:   wf.BranchName,
		})
		if err != nil {
			v.workflowStatusMsg = fmt.Sprintf("Agent spawn failed: %v", err)
		} else {
			wf.SetWorkflowAgentID(id)
			v.workflowStatusMsg = fmt.Sprintf("Agent started for %s (%s)", issue.Key, branchName)
		}
		return RefreshDoneMsg{}
	}
}

// startWorkflowFromJiraOnExisting spawns an agent for a Jira issue on an already-created workflow
// (reusing its existing worktrees rather than creating new ones).
func (v *WorkflowsView) startWorkflowFromJiraOnExisting(issue *jira.Issue, wf *project.FeatureWorkflow, extraMsg string) tea.Cmd {
	if issue == nil || wf == nil {
		return nil
	}
	agentPrompt := buildJiraAgentPrompt(issue, extraMsg)
	eng := v.engine
	var ctxFiles []string
	if v.proj != nil {
		ctxFiles = v.proj.ContextFiles
	}
	fullTask := agentPrompt + "\n\n" + buildWorkflowCommitInstructions(wf)

	return func() tea.Msg {
		if eng == nil {
			v.workflowStatusMsg = "Agent engine not available"
			return RefreshDoneMsg{}
		}
		id, err := eng.StartAgent(wf.WorkflowDir(), fullTask, engine.AgentOptions{
			ContextFiles: ctxFiles,
			SmartRoute:   true,
			WorkflowID:   wf.BranchName,
		})
		if err != nil {
			v.workflowStatusMsg = fmt.Sprintf("Agent spawn failed: %v", err)
		} else {
			wf.SetWorkflowAgentID(id)
			v.workflowStatusMsg = fmt.Sprintf("Agent started for %s on existing workflow '%s'", issue.Key, wf.BranchName)
		}
		return RefreshDoneMsg{}
	}
}

// buildWorkflowCommitInstructions generates commit instructions for a workflow.
func buildWorkflowCommitInstructions(wf *project.FeatureWorkflow) string {
	var repos []string
	for name, wr := range wf.Repos {
		if wr.WorktreeCreated {
			repos = append(repos, name)
		}
	}
	if len(repos) == 0 {
		return ""
	}

	sort.Strings(repos)

	var b strings.Builder
	b.WriteString("<commit-instructions>\n")
	b.WriteString("IMPORTANT: When you have completed your work, you MUST commit your changes.\n")
	b.WriteString(fmt.Sprintf("You are working on branch '%s'.\n", wf.BranchName))
	if len(repos) > 1 {
		b.WriteString("This workflow spans multiple repositories. Each repo is a separate git repository\n")
		b.WriteString("and MUST be committed independently. Do NOT try to commit from the parent directory.\n\n")
		b.WriteString("Repos to commit (each is a subdirectory of your working directory):\n")
		for _, name := range repos {
			b.WriteString(fmt.Sprintf("  - %s/\n", name))
		}
		b.WriteString("\nFor each repo that has changes, cd into it and run:\n")
		b.WriteString("  git add -A && git commit -m \"<descriptive message>\"\n")
	} else {
		b.WriteString(fmt.Sprintf("Commit your changes in the %s/ subdirectory:\n", repos[0]))
		b.WriteString(fmt.Sprintf("  cd %s && git add -A && git commit -m \"<descriptive message>\"\n", repos[0]))
	}
	b.WriteString("</commit-instructions>")
	return b.String()
}

// --- Modal input handlers ---

func (v *WorkflowsView) handleWorkflowStartInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		if v.workflowBranchInput.Value != "" && v.proj != nil {
			branchName := v.workflowBranchInput.Value
			baseDir := v.workflowBaseDir
			v.showWorkflowStart = false
			v.workflowBranchInput.Clear()
			wf := project.NewFeatureWorkflow(v.proj, branchName, baseDir)
			v.workflows = append(v.workflows, wf)
			v.selectedWorkflow = len(v.workflows) - 1
			v.rebuildFilter()
			return func() tea.Msg {
				wf.CreateAllWorktrees()
				return RefreshDoneMsg{}
			}
		}
		v.showWorkflowStart = false
	case "esc":
		v.showWorkflowStart = false
		v.workflowBranchInput.Clear()
	default:
		v.workflowBranchInput.HandleKey(msg)
	}
	return nil
}

func (v *WorkflowsView) handleAgentPromptInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		if v.agentPromptInput.Value != "" && v.engine != nil && v.currentWorkflow() != nil {
			promptText := v.agentPromptInput.Value
			v.showAgentPrompt = false
			v.agentPromptInput.Clear()
			v.spawnAgentForWorkflow(promptText)
			v.refreshWorkflowAgentSnap()
			return v.ensureWorkflowTick()
		}
		v.showAgentPrompt = false
	case "esc":
		v.showAgentPrompt = false
		v.agentPromptInput.Clear()
	default:
		v.agentPromptInput.HandleKey(msg)
	}
	return nil
}

func (v *WorkflowsView) handleDetachWorkflowConfirm(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "enter":
		wf := v.currentWorkflow()
		v.showDetachWorkflowConfirm = false
		if wf == nil {
			return nil
		}
		var results []string
		for _, wr := range wf.Repos {
			if !wr.WorktreeCreated || wr.WorktreePath == "" || wr.OriginalPath == "" {
				continue
			}
			sha, err := git.GetHEAD(wr.WorktreePath)
			if err != nil {
				results = append(results, fmt.Sprintf("✗ %s: %v", wr.RepoName, err))
				continue
			}
			if err := git.CheckoutDetachedAt(wr.OriginalPath, sha); err != nil {
				results = append(results, fmt.Sprintf("✗ %s: %v", wr.RepoName, err))
			} else {
				results = append(results, fmt.Sprintf("✓ %s: main→%s", wr.RepoName, sha[:7]))
			}
		}
		if len(results) == 0 {
			v.detachWorkflowResult = "⊘ No active worktrees to sync"
		} else {
			v.detachWorkflowResult = strings.Join(results, "  ")
		}
	case "n", "esc":
		v.showDetachWorkflowConfirm = false
	}
	return nil
}

func (v *WorkflowsView) handleMRSummary(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y":
		wf := v.currentWorkflow()
		if wf != nil {
			text := v.buildMRSummaryText(wf)
			if err := git.CopyToClipboard(text); err == nil {
				v.mrResults = "Copied MR summary to clipboard"
			} else {
				v.mrResults = fmt.Sprintf("Copy failed: %v", err)
			}
		}
	case "esc", "q":
		v.showMRSummary = false
		v.mrSummaryLines = nil
	}
	return nil
}

// buildMRSummaryText builds a colleague-friendly summary of all MRs in a workflow.
func (v *WorkflowsView) buildMRSummaryText(wf *project.FeatureWorkflow) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("MRs for `%s`:\n", wf.BranchName))

	if wf.JiraURL != "" {
		b.WriteString(fmt.Sprintf("Jira: %s\n", wf.JiraURL))
	}

	for _, wr := range wf.Repos {
		if wr.MRURL == "" {
			continue
		}
		title := wr.MRTitle
		if title == "" {
			title = "Merge feature branch"
		}
		b.WriteString(fmt.Sprintf("\n%s — %s\n%s\n", wr.RepoName, title, wr.MRURL))
	}

	return strings.TrimRight(b.String(), "\n")
}

// --- Key action helpers ---

func (v *WorkflowsView) handleStartAgent() {
	wf := v.currentWorkflow()
	if wf != nil && v.engine != nil {
		hasWorktree := false
		for _, wr := range wf.Repos {
			if wr.WorktreeCreated {
				hasWorktree = true
				break
			}
		}
		if hasWorktree {
			v.showAgentPrompt = true
			v.agentPromptInput.Clear()
		} else {
			v.workflowStatusMsg = "No worktrees created yet -- create worktrees first"
		}
	}
}

func (v *WorkflowsView) handleStartPush(force bool) tea.Cmd {
	wf := v.currentWorkflow()
	if wf == nil {
		v.pushResults = "No active workflow"
		return nil
	}
	hasWorktree := false
	for _, wr := range wf.Repos {
		if wr.WorktreeCreated {
			hasWorktree = true
			break
		}
	}
	if !hasWorktree {
		v.pushResults = "Nothing to push - no worktrees created"
		return nil
	}
	// Check async which repos actually have commits to push
	return func() tea.Msg {
		return pushCheckDoneMsg{repos: wf.ReposNeedingPush(), force: force}
	}
}

func (v *WorkflowsView) handleStartBatchMR() {
	wf := v.currentWorkflow()
	if wf == nil {
		v.mrResults = "No active workflow"
		return
	}
	hasPushed := false
	for _, wr := range wf.Repos {
		if wr.Pushed {
			hasPushed = true
			break
		}
	}
	if !hasPushed {
		v.mrResults = "No repos have been pushed yet"
	} else {
		v.batchMRConfirm.Show("Create MRs/PRs",
			fmt.Sprintf("Branch: %s\nCreate MRs/PRs for pushed repos.", wf.BranchName),
			func() tea.Cmd {
				wf := v.currentWorkflow()
				if wf == nil {
					return nil
				}
				return func() tea.Msg {
					wf.CreateAllMRs()
					return mrDoneMsg{}
				}
			})
	}
}

func (v *WorkflowsView) handleImport() {
	if v.proj == nil {
		return
	}
	skip := make(map[string]bool, len(v.workflows))
	for _, wf := range v.workflows {
		skip[wf.BranchName] = true
	}
	discovered, err := project.DiscoverWorkflows(v.proj, skip)
	if err != nil || len(discovered) == 0 {
		if err != nil {
			v.workflowStatusMsg = fmt.Sprintf("Import error: %v", err)
		} else {
			v.workflowStatusMsg = "No new worktree workflows found"
		}
		return
	}
	v.workflows = append(v.workflows, discovered...)
	v.selectedWorkflow = len(v.workflows) - len(discovered)
	v.saveWorkflows()
	names := make([]string, len(discovered))
	for i, wf := range discovered {
		names[i] = wf.BranchName
	}
	v.workflowStatusMsg = fmt.Sprintf("Imported %d workflow(s): %s", len(discovered), strings.Join(names, ", "))
	v.rebuildFilter()
}

// --- Agent tick helpers ---

func (v *WorkflowsView) workflowTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return WorkflowTickMsg{}
	})
}

func (v *WorkflowsView) hasRunningAgents() bool {
	return v.runningAgentCount() > 0
}

func (v *WorkflowsView) runningAgentCount() int {
	if v.engine == nil {
		return 0
	}
	count := 0
	for _, wf := range v.workflows {
		agentID := wf.GetWorkflowAgentID()
		if agentID == "" {
			continue
		}
		agent := v.engine.GetAgent(agentID)
		if agent == nil {
			continue
		}
		snap := agent.Snapshot()
		if snap.State == engine.AgentRunning || snap.State == engine.AgentStarting || snap.State == engine.AgentRouting {
			count++
		}
	}
	return count
}

// refreshBranchStatusCmd returns a command that refreshes branch ahead/behind status for all workflows.
func (v *WorkflowsView) refreshBranchStatusCmd() tea.Cmd {
	workflows := make([]*project.FeatureWorkflow, len(v.workflows))
	copy(workflows, v.workflows)
	return func() tea.Msg {
		for _, wf := range workflows {
			wf.RefreshBranchStatuses()
		}
		return branchStatusDoneMsg{}
	}
}

func (v *WorkflowsView) refreshWorkflowAgentSnap() {
	wf := v.currentWorkflow()
	if wf == nil || v.engine == nil {
		v.workflowAgentSnap = nil
		return
	}
	agentID := wf.GetWorkflowAgentID()
	if agentID == "" {
		v.workflowAgentSnap = nil
		return
	}
	agent := v.engine.GetAgent(agentID)
	if agent == nil {
		v.workflowAgentSnap = nil
		return
	}
	s := agent.Snapshot()
	v.workflowAgentSnap = &s
}

func (v *WorkflowsView) ensureWorkflowTick() tea.Cmd {
	if !v.workflowTicking && v.hasRunningAgents() {
		v.workflowTicking = true
		return v.workflowTickCmd()
	}
	return nil
}

// --- View rendering ---

func (v *WorkflowsView) renderWorkflowItem(wf *project.FeatureWorkflow, index int, selected bool) string {
	th := theme.GetTheme()
	st := wf.Status()

	var line strings.Builder

	// Selection indicator
	if selected {
		line.WriteString(th.DashboardAccentStyle.Render(" ► "))
	} else {
		line.WriteString("   ")
	}

	// Branch name
	if selected {
		line.WriteString(th.SelectedBranchStyle.Render(st.BranchName))
	} else {
		line.WriteString(th.BranchStyle.Render(st.BranchName))
	}

	// State indicator
	switch st.State {
	case project.WorkflowActive:
		line.WriteString(th.DashboardAccentStyle.Render("  ● active"))
	case project.WorkflowDone:
		line.WriteString(th.StatsStyle.Render("  ✓ done"))
	case project.WorkflowInitializing:
		line.WriteString(th.MutedTextStyle.Render("  … init"))
	case project.WorkflowPushingAll:
		line.WriteString(th.DashboardAccentStyle.Render("  ↑ pushing"))
	case project.WorkflowCreatingMRs:
		line.WriteString(th.DashboardAccentStyle.Render("  MR creating"))
	case project.WorkflowCleaningUp:
		line.WriteString(th.MutedTextStyle.Render("  … cleanup"))
	}

	// Agent status
	agentID := wf.GetWorkflowAgentID()
	if agentID != "" && v.engine != nil {
		agent := v.engine.GetAgent(agentID)
		if agent != nil {
			snap := agent.Snapshot()
			switch snap.State {
			case engine.AgentRunning, engine.AgentStarting, engine.AgentRouting:
				line.WriteString(th.DashboardAccentStyle.Render("  agent running"))
			case engine.AgentComplete:
				line.WriteString(th.StatsStyle.Render("  agent done"))
			case engine.AgentError, engine.AgentKilled:
				line.WriteString(th.DashboardErrorStyle.Render("  agent failed"))
			}
		}
	}

	// Repo count
	line.WriteString(th.MutedTextStyle.Render(fmt.Sprintf("  %d repos", st.TotalRepos)))

	// Push indicator
	if st.Pushed > 0 {
		line.WriteString(th.StatsStyle.Render(fmt.Sprintf("  ↑%d", st.Pushed)))
	}

	// MR indicator
	if st.MRsCreated > 0 {
		line.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf("  MR:%d", st.MRsCreated)))
	}

	return line.String()
}

// View renders the workflows view.
func (v *WorkflowsView) View() string {
	th := theme.GetTheme()
	var s strings.Builder

	// Header
	s.WriteString(th.DashboardTitle.Render(" Feature Workflows "))
	if v.proj != nil {
		s.WriteString(th.MutedTextStyle.Render(fmt.Sprintf("  %s", v.proj.Name)))
	}
	if n := v.runningAgentCount(); n > 0 {
		s.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf("  %d agent(s) running", n)))
	}
	if v.jiraPicker.IsAvailable() {
		s.WriteString(th.MutedTextStyle.Render("  J: Jira ticket"))
	}
	s.WriteString("\n\n")

	// Jira picker overlay
	if v.jiraPicker.IsOpen() {
		s.WriteString(v.jiraPicker.View())
		return s.String()
	}

	// Jira workflow confirm modal — returns early (full overlay)
	if v.jiraConfirmIssue != nil {
		s.WriteString(v.renderJiraConfirmModal())
		return s.String()
	}

	s.WriteString(v.renderModals())
	s.WriteString(v.renderFlashMessages())
	s.WriteString(v.renderWorkflowList())

	// Footer
	s.WriteString("\n")
	s.WriteString(renderSeparator())
	s.WriteString(v.renderFooterHelp())

	return s.String()
}

// renderJiraConfirmModal renders the Jira ticket confirmation overlay.
func (v *WorkflowsView) renderJiraConfirmModal() string {
	th := theme.GetTheme()
	issue := v.jiraConfirmIssue
	branch := issueToBranchName(issue)
	input := v.jiraExtraInput.RenderPlain()
	lines := []string{
		"",
		fmt.Sprintf("  Ticket: %s — %s", th.DashboardAccentStyle.Render(issue.Key), issue.Summary),
		fmt.Sprintf("  Branch: %s", th.MutedTextStyle.Render(branch)),
		"",
		"  Custom instructions (optional):",
		"  " + input,
		"",
	}
	if wf := v.currentWorkflow(); wf != nil {
		lines = append(lines,
			fmt.Sprintf("  e: Use selected workflow (%s)", th.MutedTextStyle.Render(wf.BranchName)),
		)
	}
	lines = append(lines, "  Enter: New worktrees  Esc: Cancel")
	return renderModal("Start Workflow from Jira", lines, modalWidth(v.width)) + "\n"
}

// renderModals renders all modal overlays for the workflows view.
func (v *WorkflowsView) renderModals() string {
	th := theme.GetTheme()
	var s strings.Builder

	if v.showWorkflowStart {
		lines := []string{
			"",
			fmt.Sprintf("  Branch name: %s", v.workflowBranchInput.Render(th.InfoStyle)),
			"",
			"  This creates worktrees for all repos in the",
			fmt.Sprintf("  project under %s/<branch>/", v.workflowBaseDir),
			"",
			"  Enter: Create  Esc: Cancel",
		}
		s.WriteString(renderModal("Start Feature Workflow", lines, modalWidth(v.width)))
		s.WriteString("\n")
	}

	if v.showAgentPrompt {
		wf := v.currentWorkflow()
		wfName := ""
		wfDir := ""
		if wf != nil {
			wfName = wf.BranchName
			wfDir = wf.WorkflowDir()
		}
		lines := []string{
			"",
			fmt.Sprintf("  Workflow: %s", th.InfoStyle.Render(wfName)),
			fmt.Sprintf("  Working dir: %s", th.MutedTextStyle.Render(wfDir)),
			"",
			fmt.Sprintf("  Task: %s", v.agentPromptInput.Render(th.InfoStyle)),
			"",
			"  The agent will work across all repo worktrees.",
			"",
			"  Enter: Spawn  Ctrl+Enter: Newline  Esc: Cancel",
		}
		s.WriteString(renderModal("Spawn Agent", lines, modalWidth(v.width)))
		s.WriteString("\n")
	}

	if v.showDetachWorkflowConfirm {
		if wf := v.currentWorkflow(); wf != nil {
			lines := []string{
				"",
				fmt.Sprintf("  Branch: %s", th.InfoStyle.Render(wf.BranchName)),
				fmt.Sprintf("  Checkout main dir of %d repo(s) as detached HEAD", len(wf.Repos)),
				"  at each worktree's current HEAD commit?",
				"",
				"  y: Confirm  n/Esc: Cancel",
			}
			s.WriteString(renderModal("Sync Main Dir to Workflow", lines, modalWidth(v.width)))
			s.WriteString("\n")
		}
	}

	if v.detachWorkflowResult != "" {
		if strings.HasPrefix(v.detachWorkflowResult, "✗") {
			s.WriteString(th.DashboardErrorStyle.Render(" " + v.detachWorkflowResult))
		} else {
			s.WriteString(th.DashboardAccentStyle.Render(" " + v.detachWorkflowResult))
		}
		s.WriteString("\n")
	}

	if v.cleanupConfirm.Visible {
		s.WriteString(v.cleanupConfirm.Render(modalWidth(v.width)))
		s.WriteString("\n")
	}

	if v.pushConfirm.Visible {
		s.WriteString(v.pushConfirm.Render(modalWidth(v.width)))
		s.WriteString("\n")
	}

	if v.batchMRConfirm.Visible {
		s.WriteString(v.batchMRConfirm.Render(modalWidth(v.width)))
		s.WriteString("\n")
	}

	if v.showMRSummary && len(v.mrSummaryLines) > 0 {
		lines := []string{""}
		lines = append(lines, v.mrSummaryLines...)
		lines = append(lines, "", "  y: Copy to clipboard  Esc: Dismiss")
		s.WriteString(renderModal("Merge Requests Created", lines, modalWidth(v.width)))
		s.WriteString("\n")
	}

	return s.String()
}

// renderFlashMessages renders transient status messages.
func (v *WorkflowsView) renderFlashMessages() string {
	th := theme.GetTheme()
	flashMsg := v.workflowStatusMsg
	if flashMsg == "" {
		flashMsg = v.pushResults
	}
	if flashMsg == "" {
		flashMsg = v.mrResults
	}
	if flashMsg == "" {
		return ""
	}

	var s strings.Builder
	for _, line := range strings.Split(flashMsg, "\n") {
		if strings.Contains(line, "Next:") {
			s.WriteString(th.MutedTextStyle.Render(" " + line))
		} else if strings.Contains(line, "✗") || strings.Contains(line, "failed") || strings.Contains(line, "error") {
			s.WriteString(th.DashboardErrorStyle.Render(" " + line))
		} else {
			s.WriteString(th.DashboardAccentStyle.Render(" " + line))
		}
		s.WriteString("\n")
	}
	s.WriteString("\n")
	return s.String()
}

// renderWorkflowList renders the list of active workflows.
func (v *WorkflowsView) renderWorkflowList() string {
	th := theme.GetTheme()
	var s strings.Builder

	if len(v.workflows) == 0 {
		s.WriteString(th.MutedTextStyle.Render(" No feature workflows active."))
		s.WriteString("\n\n")
		s.WriteString(th.MutedTextStyle.Render(" Press 'w' to start a new workflow, or 'I' to import existing worktrees."))
		s.WriteString("\n")
		return s.String()
	}

	s.WriteString(th.StatsStyle.Render(fmt.Sprintf(" %d workflow(s)", len(v.workflows))))
	s.WriteString("\n")
	s.WriteString(renderSeparator())

	for i, wf := range v.workflows {
		selected := i == v.selectedWorkflow
		s.WriteString(v.renderWorkflowItem(wf, i, selected))
		s.WriteString("\n")

		if selected {
			st := wf.Status()
			if st.State == project.WorkflowActive {
				var hint string
				switch {
				case st.MRsCreated > 0:
					hint = "    next: D to cleanup worktrees once merged"
				case st.Pushed > 0:
					hint = "    next: M to create merge requests"
				default:
					hint = "    next: p to push branches, a to spawn agent"
				}
				s.WriteString(th.MutedTextStyle.Render(hint))
				s.WriteString("\n")
			}

			s.WriteString(v.renderRepoDetail(wf))
		}
	}

	return s.String()
}

// renderRepoDetail shows per-repo status for the selected workflow.
func (v *WorkflowsView) renderRepoDetail(wf *project.FeatureWorkflow) string {
	th := theme.GetTheme()
	var s strings.Builder

	repoNames := make([]string, 0, len(wf.Repos))
	for name := range wf.Repos {
		repoNames = append(repoNames, name)
	}
	sort.Strings(repoNames)

	s.WriteString("\n")
	for _, name := range repoNames {
		wr := wf.Repos[name]
		var parts []string

		// Status icon
		if wr.Error != "" {
			parts = append(parts, th.DashboardErrorStyle.Render("✗"))
		} else if wr.WorktreeCreated {
			parts = append(parts, th.StatsStyle.Render("✓"))
		} else {
			parts = append(parts, th.MutedTextStyle.Render("·"))
		}

		// Repo name
		parts = append(parts, th.BranchStyle.Render(wr.RepoName))

		// Indicators
		if wr.WorktreeCreated {
			parts = append(parts, th.MutedTextStyle.Render("W"))
		}
		if wr.Pushed {
			parts = append(parts, th.StatsStyle.Render("↑"))
		}
		if wr.MRURL != "" {
			parts = append(parts, th.DashboardAccentStyle.Render("MR"))
		}

		// Branch ahead/behind vs default branch
		if wr.AheadDefault > 0 {
			parts = append(parts, th.DashboardAccentStyle.Render(fmt.Sprintf("+%d", wr.AheadDefault)))
		}
		if wr.BehindDefault > 0 {
			parts = append(parts, th.DashboardErrorStyle.Render(fmt.Sprintf("-%d", wr.BehindDefault)))
		}
		if wr.AheadDefault == 0 && wr.BehindDefault == 0 && (wr.WorktreeCreated || wr.Pushed) {
			parts = append(parts, th.MutedTextStyle.Render("≡"))
		}

		// Remote tracking status
		if wr.HasRemote {
			if wr.AheadRemote > 0 {
				parts = append(parts, th.DashboardAccentStyle.Render(fmt.Sprintf("↑%d", wr.AheadRemote)))
			}
			if wr.BehindRemote > 0 {
				parts = append(parts, th.DashboardErrorStyle.Render(fmt.Sprintf("↓%d", wr.BehindRemote)))
			}
		}

		if wr.Error != "" {
			parts = append(parts, th.DashboardErrorStyle.Render(wr.Error))
		}

		s.WriteString("      ")
		s.WriteString(strings.Join(parts, " "))
		s.WriteString("\n")

		if wr.MRURL != "" {
			mrLabel := wr.MRTitle
			if mrLabel == "" {
				mrLabel = wr.MRURL
			}
			s.WriteString("        ")
			s.WriteString(th.MutedTextStyle.Render("↳ " + mrLabel))
			s.WriteString("\n")
			s.WriteString("        ")
			s.WriteString(th.MutedTextStyle.Render("  " + wr.MRURL))
			s.WriteString("\n")
		}
	}

	return s.String()
}

// renderFooterHelp returns contextual help text.
func (v *WorkflowsView) renderFooterHelp() string {
	th := theme.GetTheme()

	if v.CapturesInput() {
		return th.Help.Render("Enter: Confirm  Esc: Cancel")
	}

	jiraHint := ""
	if v.jiraPicker.IsAvailable() {
		jiraHint = "  J Jira"
	}
	if len(v.workflows) > 0 {
		return th.Help.Render(" w New" + jiraHint + "  a Agent  d Diff  p Push  M MRs  D Cleanup  H Detach  I Import  ↑↓ Select  r Refresh")
	}
	return th.Help.Render(" w New Workflow  I Import  r Refresh" + jiraHint)
}

// ShortHelp returns a contextual short help string.
func (v *WorkflowsView) ShortHelp() string {
	if v.CapturesInput() {
		return "Enter: Confirm  Esc: Cancel"
	}
	if len(v.workflows) > 0 {
		wf := v.currentWorkflow()
		wfLabel := ""
		if wf != nil {
			wfLabel = wf.BranchName
		}
		jiraHint := ""
		if v.jiraPicker.IsAvailable() {
			jiraHint = "  J Jira ticket"
		}
		return fmt.Sprintf("Workflow: %s  w New%s  a Agent  d Diff  p Push  M MRs  D Cleanup  H Detach  I Import", wfLabel, jiraHint)
	}
	jiraHint := ""
	if v.jiraPicker.IsAvailable() {
		jiraHint = "  J Jira ticket"
	}
	return "w New Workflow  I Import  r Refresh" + jiraHint
}

// CapturesInput returns true when the view is in an input mode.
func (v *WorkflowsView) CapturesInput() bool {
	return v.showWorkflowStart || v.cleanupConfirm.Visible || v.showAgentPrompt ||
		v.pushConfirm.Visible || v.batchMRConfirm.Visible || v.showMRSummary ||
		v.showDetachWorkflowConfirm ||
		v.jiraPicker.IsOpen() || v.jiraConfirmIssue != nil
}

// CapturesKey returns true for keys this view handles directly.
func (v *WorkflowsView) CapturesKey(key string) bool {
	switch key {
	case "r", "w", "a", "p", "d", "D", "H", "I", "M", "J", "j", "k", "up", "down", "/":
		return true
	}
	return false
}

// SetSize updates the view dimensions and resizes the filter.
func (v *WorkflowsView) SetSize(width, height int) {
	v.viewBase.SetSize(width, height)
	if v.filter != nil {
		v.filter.SetHeight(height - 10)
	}
}

// Refresh reloads workflow data.
func (v *WorkflowsView) Refresh() error {
	v.loadWorkflows()
	return nil
}

// KeyBindings returns the keybindings for this view.
func (v *WorkflowsView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "d", Description: "View changes (diff vs default branch)"},
		{Key: "w", Description: "Start new feature workflow"},
		{Key: "a", Description: "Spawn agent for selected workflow"},
		{Key: "p", Description: "Push all repos in selected workflow"},
		{Key: "P", Description: "Force push (--force-with-lease) all repos in selected workflow"},
		{Key: "M", Description: "Create MRs/PRs for all pushed repos"},
		{Key: "D", Description: "Cleanup selected workflow (remove worktrees)"},
		{Key: "H", Description: "Sync main dir repos to worktree HEADs (detached)"},
		{Key: "I", Description: "Import workflows from existing worktrees"},
		{Key: "↑/k", Description: "Select previous workflow"},
		{Key: "↓/j", Description: "Select next workflow"},
		{Key: "/", Description: "Filter"},
		{Key: "r", Description: "Refresh"},
	}
}
