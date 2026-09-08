package views

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
	"gitlab.com/tanevanwifferen1/singularity/internal/service/fake"
)

// flowTestProject is a two-repo project, which is the case the workflow root
// exists for: one change spanning several repos.
func flowTestProject(repos ...string) *service.Project {
	if len(repos) == 0 {
		repos = []string{"pbd-api", "portal-web"}
	}
	proj := &service.Project{Name: "pbd-flow-test"}
	for _, name := range repos {
		proj.Repos = append(proj.Repos, &service.Repo{Name: name, Path: "/src/" + name})
	}
	return proj
}

// projectFlowsView returns a project-mode flows view over a Workflows view
// holding one workflow per branch, all under baseDir. Nothing is created on
// disk here: a caller that wants a usable workflow calls createWorkflowRoot.
func projectFlowsView(t *testing.T, proj *service.Project, baseDir string, branches ...string) (*FlowsView, *fake.FlowStub) {
	t.Helper()
	// Keep LoadWorkflows off the real config dir: an empty workflow list
	// makes the view read the project's persisted state.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	wv := NewWorkflowsView(proj)
	for _, b := range branches {
		wv.workflows = append(wv.workflows, service.NewFeatureWorkflow(proj, b, baseDir))
	}
	wv.rebuildFilter()

	stub := fake.NewFlowStub()
	svcs := fake.New()
	svcs.Flow = stub

	v := NewFlowsView("/home/dev/live-checkout")
	v.SetWorkflowsView(wv)
	v.SetServices(svcs)
	v.SetSize(140, 30)
	return v, stub
}

// createWorkflowRoot materialises what CreateAllWorktrees would leave behind:
// the workflow root with one subdirectory per repo. It deliberately does not
// make the root a git repo, because it is not one.
func createWorkflowRoot(t *testing.T, wf *service.FeatureWorkflow) string {
	t.Helper()
	root := wf.WorkflowDir()
	for _, wr := range wf.Repos {
		if err := os.MkdirAll(wr.WorktreePath, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", wr.WorktreePath, err)
		}
	}
	return root
}

// The flow's work dir is the workflow ROOT — the parent holding one repo
// worktree per subdirectory — taken from FeatureWorkflow.WorkflowDir() rather
// than re-derived, and it satisfies what flow.Manager.Start requires of a
// work dir: an existing directory, git repo or not.
func TestWorkflowRootIsTheWorkDir(t *testing.T) {
	base := t.TempDir()
	proj := flowTestProject()
	v, _ := projectFlowsView(t, proj, base, "feat/retry-backoff")
	wf := v.workflows.workflows[0]
	root := createWorkflowRoot(t, wf)

	if want := filepath.Join(base, "feat-retry-backoff"); root != want {
		t.Fatalf("workflow root = %q, want %q (slashes as dashes under the base dir)", root, want)
	}

	opt := flowWorkflowOptionFrom(wf)
	if opt.RootDir != wf.WorkflowDir() {
		t.Errorf("option root = %q, want WorkflowDir() %q", opt.RootDir, wf.WorkflowDir())
	}
	if opt.Missing {
		t.Error("a workflow whose root exists must be usable")
	}
	if opt.Branch != "feat/retry-backoff" {
		t.Errorf("option branch = %q", opt.Branch)
	}
	if strings.Join(opt.Repos, ",") != "pbd-api,portal-web" {
		t.Errorf("option repos = %v, want both project repos sorted", opt.Repos)
	}

	// What the manager checks: the root exists and is a directory. Each repo
	// is a subdirectory of it, and the root itself is not a git repo.
	st, err := os.Stat(opt.RootDir)
	if err != nil || !st.IsDir() {
		t.Fatalf("work_dir %s must stat as a directory: %v", opt.RootDir, err)
	}
	if _, err := os.Stat(filepath.Join(opt.RootDir, ".git")); err == nil {
		t.Error("the workflow root must not itself be a git repo")
	}
	for _, name := range opt.Repos {
		if st, err := os.Stat(filepath.Join(opt.RootDir, name)); err != nil || !st.IsDir() {
			t.Errorf("repo %s must be a subdirectory of the root: %v", name, err)
		}
	}
}

// A single-repo project changes nothing: the flow still runs in the workflow
// root, which holds that one repo as a subdirectory.
func TestWorkflowRootSingleRepoProject(t *testing.T) {
	base := t.TempDir()
	proj := flowTestProject("pbd-api")
	v, _ := projectFlowsView(t, proj, base, "fix/one-repo")
	wf := v.workflows.workflows[0]
	root := createWorkflowRoot(t, wf)

	opt := flowWorkflowOptionFrom(wf)
	if opt.RootDir != root || opt.Missing {
		t.Fatalf("option = %+v, want the usable root %q", opt, root)
	}
	if len(opt.Repos) != 1 || opt.Repos[0] != "pbd-api" {
		t.Fatalf("repos = %v, want the one repo", opt.Repos)
	}
	if got := filepath.Dir(wf.Repos["pbd-api"].WorktreePath); got != root {
		t.Errorf("the repo worktree %q must sit under the root %q", got, root)
	}
}

// The picker offers the project's workflows, refuses one whose worktrees were
// removed, and hands the selected workflow's root to the service as work_dir.
func TestWorkflowPickerSelectsRootAndRefusesMissing(t *testing.T) {
	base := t.TempDir()
	proj := flowTestProject()
	v, stub := projectFlowsView(t, proj, base, "feat/gone", "feat/present")
	present := createWorkflowRoot(t, v.workflows.workflows[1])

	var got service.FlowStartRequest
	stub.StartFn = func(_ context.Context, req service.FlowStartRequest) (*service.Flow, error) {
		got = req
		f := testFlow()
		f.ID = "f7"
		f.WorkDir = req.WorkDir
		return &f, nil
	}

	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if len(v.workflowOptions) != 2 {
		t.Fatalf("picker rows = %d, want one per workflow", len(v.workflowOptions))
	}
	if !v.workflowOptions[0].Missing || v.workflowOptions[1].Missing {
		t.Fatalf("usability = %v/%v, want only the workflow with no root marked missing",
			v.workflowOptions[0].Missing, v.workflowOptions[1].Missing)
	}
	// A workflow whose root is gone is never what the modal opens on.
	if v.workflowChoice == nil || v.workflowChoice.RootDir != present {
		t.Fatalf("preselected %v, want the usable workflow %q", v.workflowChoice, present)
	}

	// Open the picker and try the unusable row: it is refused, in the
	// picker, without changing the choice.
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if !v.showWorkflowPicker {
		t.Fatal("space on the workflow field did not open the picker")
	}
	v.workflowPicker.SelectAt(0)
	v.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !v.showWorkflowPicker || !strings.Contains(v.statusMsg, "no worktrees on disk") {
		t.Errorf("selecting a gone workflow: open=%v status=%q", v.showWorkflowPicker, v.statusMsg)
	}
	if v.workflowChoice.RootDir != present {
		t.Errorf("the refused row must not become the choice: %q", v.workflowChoice.RootDir)
	}

	// j then Enter takes the usable row and closes the picker.
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	v.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if v.showWorkflowPicker {
		t.Fatal("Enter on a usable workflow did not close the picker")
	}
	if v.workflowChoice == nil || v.workflowChoice.RootDir != present {
		t.Fatalf("choice = %v, want %q", v.workflowChoice, present)
	}

	// The modal shows what the flow will cover before it is confirmed.
	modal := v.renderStartModal()
	for _, want := range []string{"feat/present", "Repos (2)", "pbd-api", "portal-web", "not a"} {
		if !strings.Contains(modal, want) {
			t.Errorf("start modal missing %q:\n%s", want, modal)
		}
	}

	v.Update(tea.KeyMsg{Type: tea.KeyTab}) // → goal
	typeInto(v, "cap the delay")
	_, cmd := v.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter produced no start command")
	}
	if msg, ok := cmd().(flowActionMsg); !ok || msg.err != nil {
		t.Fatalf("start result = %#v, want a successful flowActionMsg", msg)
	}
	if got.WorkDir != present {
		t.Errorf("work_dir = %q, want the workflow root %q", got.WorkDir, present)
	}
}

// A project with no workflows shows an empty picker that says where a
// workflow comes from, and the modal cannot be confirmed into an error.
func TestStartModalWithZeroWorkflows(t *testing.T) {
	v, stub := projectFlowsView(t, flowTestProject(), t.TempDir())
	called := false
	stub.StartFn = func(context.Context, service.FlowStartRequest) (*service.Flow, error) {
		called = true
		return nil, service.ErrInvalidRequest
	}

	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if len(v.workflowOptions) != 0 || v.workflowChoice != nil {
		t.Fatalf("options = %+v choice = %v, want nothing on offer", v.workflowOptions, v.workflowChoice)
	}

	v.startInputs[flowFieldGoal].Set("cap the delay")
	if _, cmd := v.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Error("with no workflow to run in, Enter must not start a flow")
	}
	if !v.showStart {
		t.Error("the modal must stay open rather than close on an error")
	}
	if !strings.Contains(v.statusMsg, "Workflows view") {
		t.Errorf("status = %q, want it to point at the Workflows view", v.statusMsg)
	}
	if called {
		t.Error("the service must not have been called at all")
	}

	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	picker := v.renderStartModal()
	if !strings.Contains(picker, "No workflows in this project yet") ||
		!strings.Contains(picker, "Workflows view") {
		t.Errorf("empty picker = %q, want the pointer at the Workflows view", picker)
	}
}

// Repo mode has no project and so no workflow list: the modal falls back to
// the repo path and says so, rather than offering an empty picker.
func TestStartModalRepoModeFallsBackToRepoPath(t *testing.T) {
	v, _ := loadedFlowsView(t)
	if v.workflows != nil {
		t.Fatal("the repo-mode view must have no workflows view wired")
	}

	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if len(v.workflowOptions) != 1 || !v.workflowOptions[0].Fallback {
		t.Fatalf("options = %+v, want the single repo-checkout fallback", v.workflowOptions)
	}
	if v.workflowChoice == nil || v.workflowChoice.RootDir != "/home/dev/singularity" {
		t.Fatalf("choice = %v, want the repo path", v.workflowChoice)
	}

	modal := v.renderStartModal()
	if !strings.Contains(modal, "repo checkout") {
		t.Errorf("the fallback must be labelled as the repo checkout:\n%s", modal)
	}
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if row := v.renderWorkflowOption(v.workflowOptions[0], 0, true); !strings.Contains(row, "repo mode has no workflows") {
		t.Errorf("picker row = %q, want it to explain the fallback", row)
	}
}
