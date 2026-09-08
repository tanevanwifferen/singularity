package views

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// The start modal's workflow select, in the branch-picker idiom
// WorktreeView uses: an in-memory components.Filter list over what the
// project already has, opened from the field it fills in. Split from
// flow_keys.go and flow_render.go only for length; this is FlowsView.

// flowWorkflowOption is one row of the workflow picker: a workflow projected
// onto the only two things a flow needs from it — the directory to run in,
// and the repos that directory contains.
type flowWorkflowOption struct {
	// Branch is the workflow's feature branch, empty for the repo-mode
	// fallback, which is not a workflow at all.
	Branch string

	// RootDir is the workflow root: <base-dir>/<branch-with-slashes-as-dashes>,
	// the parent that holds one repo worktree per subdirectory.
	RootDir string

	// Repos are the repo names the flow will cover, one subdirectory of
	// RootDir each.
	Repos []string

	// Missing marks a workflow whose worktrees were removed, so RootDir no
	// longer exists. Such a workflow is listed but cannot be selected: a
	// flow started against it would be refused by Flow.Start anyway.
	Missing bool

	// Fallback marks the repo-mode row, where RootDir is the live checkout
	// because there is no project and so no workflow list.
	Fallback bool
}

// String is what components.Filter matches "/" input against: the branch and
// the repos are what a user would type to find a workflow.
func (o flowWorkflowOption) String() string {
	return fmt.Sprintf("%s %s %s", o.Branch, filepath.Base(o.RootDir), strings.Join(o.Repos, " "))
}

// label is the option's name: the branch, or the checkout's basename for the
// repo-mode fallback.
func (o flowWorkflowOption) label() string {
	if o.Branch != "" {
		return o.Branch
	}
	return filepath.Base(o.RootDir)
}

// flowWorkflowOptionFrom projects a workflow onto a picker row.
//
// The flow's work dir is the workflow ROOT — FeatureWorkflow.WorkflowDir(),
// the parent that holds one worktree subdirectory per repo — and never an
// individual repo's worktree. A project's change spans repos, so the
// implementer and the reviewer must both see every repo; aimed at the root, a
// cross-repo change gets reviewed as ONE change instead of in fragments. That
// root is itself not a git repository, which is fine: Flow.Start only requires
// the work dir to exist and be a directory.
//
// The path comes from WorkflowDir() rather than being re-derived here, so the
// branch-to-directory slug rules stay in exactly one place.
func flowWorkflowOptionFrom(wf *service.FeatureWorkflow) flowWorkflowOption {
	opt := flowWorkflowOption{
		Branch:  wf.Status().BranchName,
		RootDir: wf.WorkflowDir(),
	}
	for name := range wf.Repos {
		opt.Repos = append(opt.Repos, name)
	}
	sort.Strings(opt.Repos)
	if st, err := os.Stat(opt.RootDir); err != nil || !st.IsDir() {
		opt.Missing = true
	}
	return opt
}

// flowRepoFallbackOption is the row repo mode gets: there is no project and
// no Workflows view, so the only directory on offer is the checkout the view
// is pointed at. It is labelled as such rather than dressed up as a workflow.
func flowRepoFallbackOption(repoPath string) flowWorkflowOption {
	return flowWorkflowOption{
		RootDir:  repoPath,
		Repos:    []string{filepath.Base(repoPath)},
		Fallback: true,
	}
}

// loadWorkflowOptions rebuilds the picker's rows and preselects one. Called
// on every open of the start modal, because worktrees come and go between
// opens.
func (v *FlowsView) loadWorkflowOptions() {
	v.workflowOptions = nil
	v.workflowChoice = nil

	if v.workflows == nil {
		v.workflowOptions = []flowWorkflowOption{flowRepoFallbackOption(v.repoPath)}
	} else {
		// Only the Workflows view's own Init loads the persisted list, and
		// the user may have come straight here, so load before concluding
		// the project has no workflows.
		if len(v.workflows.workflows) == 0 {
			v.workflows.loadWorkflows()
		}
		for _, wf := range v.workflows.workflows {
			if wf == nil {
				continue
			}
			v.workflowOptions = append(v.workflowOptions, flowWorkflowOptionFrom(wf))
		}
	}

	// Prefer whatever the Workflows view has selected, then the first
	// usable row. A workflow whose root is gone is never preselected.
	preferred := ""
	if v.workflows != nil {
		if wf := v.workflows.currentWorkflow(); wf != nil {
			preferred = wf.WorkflowDir()
		}
	}
	for i, o := range v.workflowOptions {
		if o.Missing {
			continue
		}
		if v.workflowChoice == nil || o.RootDir == preferred {
			v.workflowChoice = &v.workflowOptions[i]
		}
		if o.RootDir == preferred {
			break
		}
	}

	if v.workflowPicker == nil {
		v.workflowPicker = components.NewFilter([]flowWorkflowOption{}, v.renderWorkflowOption)
	}
	v.workflowPicker.SetItems(v.workflowOptions)
	v.workflowPicker.SetHeight(v.workflowPickerHeight())
	if v.workflowChoice != nil {
		for i, o := range v.workflowOptions {
			if o.RootDir == v.workflowChoice.RootDir {
				v.workflowPicker.SelectAt(i)
				break
			}
		}
	}
}

// workflowPickerHeight is the height the picker's list gets: the view minus
// the header, the hint line and the footer the picker draws around it.
func (v *FlowsView) workflowPickerHeight() int {
	return max(v.height-10, 4)
}

// handleWorkflowPicker drives the picker: enter selects, esc goes back to the
// form, everything else is the Filter's — "/" included.
func (v *FlowsView) handleWorkflowPicker(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		v.showWorkflowPicker = false
	case "enter":
		item, idx := v.workflowPicker.SelectedItem()
		if idx < 0 {
			v.showWorkflowPicker = false
			return nil
		}
		if item.Missing {
			v.statusMsg = fmt.Sprintf("%s has no worktrees on disk — recreate them in the Workflows view", item.label())
			return nil
		}
		for i, o := range v.workflowOptions {
			if o.RootDir == item.RootDir {
				v.workflowChoice = &v.workflowOptions[i]
				break
			}
		}
		v.showWorkflowPicker = false
	default:
		v.workflowPicker.Update(msg)
	}
	return nil
}

// renderWorkflowOption renders one picker row: the branch, the repos the flow
// would cover, and the root it would run in.
func (v *FlowsView) renderWorkflowOption(o flowWorkflowOption, index int, selected bool) string {
	th := theme.GetTheme()

	var line strings.Builder
	if selected {
		line.WriteString(th.DashboardAccentStyle.Render(" ► "))
	} else {
		line.WriteString("   ")
	}
	line.WriteString(th.BranchStyle.Render(o.label()))

	switch {
	case o.Fallback:
		line.WriteString(th.WarningStyle.Render("  repo checkout — repo mode has no workflows"))
	case o.Missing:
		line.WriteString(th.DashboardErrorStyle.Render("  unusable — worktree root is gone"))
	default:
		line.WriteString(th.MutedTextStyle.Render(fmt.Sprintf("  %d repo(s): %s",
			len(o.Repos), clipText(strings.Join(o.Repos, ", "), 36))))
	}
	line.WriteString(th.MutedTextStyle.Render("  " + truncatePath(o.RootDir, 44)))
	return line.String()
}

// renderWorkflowPicker renders the picker over the start modal, in the shape
// WorktreeView's branch picker uses. With no workflows at all it says so and
// points at the view that creates them, rather than showing an empty list.
func (v *FlowsView) renderWorkflowPicker() string {
	th := theme.GetTheme()
	var s strings.Builder

	s.WriteString(th.Help.Render(
		" Select the workflow to run the flow in: ↑/k • ↓/j • /: Filter • Enter: Select • Esc: Back "))
	s.WriteString("\n\n")

	if len(v.workflowOptions) == 0 {
		s.WriteString(th.MutedTextStyle.Render(" No workflows in this project yet."))
		s.WriteString("\n\n")
		s.WriteString(th.MutedTextStyle.Render(" Open the Workflows view and press 'n' to create one. A flow runs in a"))
		s.WriteString("\n")
		s.WriteString(th.MutedTextStyle.Render(" workflow's worktrees, so there is nowhere to put one until one exists."))
		s.WriteString("\n")
		return s.String()
	}

	s.WriteString(v.workflowPicker.View())
	s.WriteString("\n")
	if v.statusMsg != "" {
		s.WriteString(th.DashboardErrorStyle.Render(" " + v.statusMsg))
		s.WriteString("\n")
	}
	return s.String()
}

// workflowFieldValue is what the start modal's first field shows.
func (v *FlowsView) workflowFieldValue() string {
	if v.workflowChoice == nil {
		if len(v.workflowOptions) == 0 {
			return "— no workflows; create one in the Workflows view"
		}
		return "— none selected"
	}
	if v.workflowChoice.Fallback {
		return v.workflowChoice.label() + "  (repo checkout)"
	}
	return v.workflowChoice.label()
}

// workflowSummaryLines are the two lines under the workflow field: where the
// flow will run, and which repos that covers — the point being that the user
// sees the whole span of the change before confirming it.
func (v *FlowsView) workflowSummaryLines(innerW int) []string {
	if v.workflowChoice == nil {
		return []string{fmt.Sprintf("  %-13s %s", "Runs in:", "—")}
	}
	o := *v.workflowChoice
	// The label column and the trailing slash take 17 of the inner width;
	// one more keeps the modal's right border off the path.
	room := max(innerW-18, 20)
	lines := []string{fmt.Sprintf("  %-13s %s/", "Runs in:", truncatePath(o.RootDir, room))}
	if o.Fallback {
		return append(lines, fmt.Sprintf("  %-13s %s", "Repos:", "this checkout only — repo mode has no workflows"))
	}
	return append(lines, fmt.Sprintf("  %-13s %s",
		fmt.Sprintf("Repos (%d):", len(o.Repos)), clipText(strings.Join(o.Repos, ", "), room)))
}
