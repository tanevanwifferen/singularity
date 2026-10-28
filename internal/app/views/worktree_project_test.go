package views

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbletea"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// projectWorktreeView returns a project-mode worktree view over a workflows
// view, with worktree creation stubbed so no git runs.
func projectWorktreeView(t *testing.T, create func(*service.FeatureWorkflow) error) (*WorktreeView, *WorkflowsView) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	old := createWorkflowWorktrees
	createWorkflowWorktrees = create
	t.Cleanup(func() { createWorkflowWorktrees = old })

	proj := flowTestProject("top", "api", "web")
	wv := NewWorkflowsView(proj)
	v := NewWorktreeView("/src/top")
	v.SetProject(proj)
	v.SetWorkflowsView(wv)
	v.showCreate = true
	return v, wv
}

func typeText(v *WorktreeView, s string) {
	for _, r := range s {
		v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func TestProjectCreateTypesBIntoBranch(t *testing.T) {
	v, _ := projectWorktreeView(t, func(*service.FeatureWorkflow) error { return nil })

	typeText(v, "pbd-1234-billing/bugfix")
	if v.newWorktreeBranch != "pbd-1234-billing/bugfix" {
		t.Errorf("branch = %q, want every rune typed, 'b' included", v.newWorktreeBranch)
	}
	if v.createNewBranch {
		t.Error("'b' toggled createNewBranch in project mode")
	}
}

func TestProjectCreateAdoptsWorkflowIntoWorkflowsView(t *testing.T) {
	v, wv := projectWorktreeView(t, func(fw *service.FeatureWorkflow) error {
		for _, wr := range fw.Repos {
			wr.WorktreeCreated = true
		}
		return nil
	})

	typeText(v, "feat/x")
	_, cmd := v.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter returned no command")
	}
	msg := cmd()
	created, ok := msg.(projectWorktreesCreatedMsg)
	if !ok {
		t.Fatalf("msg = %T, want projectWorktreesCreatedMsg", msg)
	}
	if created.err != nil || len(created.wf.Repos) != 3 {
		t.Fatalf("wf repos = %d, err = %v; want 3 repos, no error", len(created.wf.Repos), created.err)
	}
	if v.showCreate {
		t.Error("dialog still open after enter")
	}

	v.Update(msg)
	if len(wv.workflows) != 1 || wv.workflows[0].BranchName != "feat/x" {
		t.Fatalf("workflows = %v, want one workflow on feat/x", wv.workflows)
	}

	// Creating the same branch again replaces rather than duplicates.
	v.showCreate = true
	typeText(v, "feat/x")
	_, cmd = v.Update(tea.KeyMsg{Type: tea.KeyEnter})
	v.Update(cmd())
	if len(wv.workflows) != 1 {
		t.Errorf("len(workflows) = %d after repeat, want 1", len(wv.workflows))
	}
}

func TestProjectCreateNamesFailedRepos(t *testing.T) {
	v, wv := projectWorktreeView(t, func(fw *service.FeatureWorkflow) error {
		fw.Repos["web"].Error = "create worktree: boom"
		fw.Repos["top"].WorktreeCreated = true
		fw.Repos["api"].WorktreeCreated = true
		return errors.New("1 of 3 worktree creations failed")
	})

	typeText(v, "feat/y")
	_, cmd := v.Update(tea.KeyMsg{Type: tea.KeyEnter})
	v.Update(cmd())

	if v.err == nil || !strings.Contains(v.err.Error(), "web") || !strings.Contains(v.err.Error(), "boom") {
		t.Errorf("err = %v, want it to name the failed repo web", v.err)
	}
	if len(wv.workflows) != 1 {
		t.Errorf("len(workflows) = %d, want the partial workflow adopted", len(wv.workflows))
	}
}

func TestProjectCreateAllFailedAdoptsNothing(t *testing.T) {
	v, wv := projectWorktreeView(t, func(fw *service.FeatureWorkflow) error {
		return errors.New("all 3 worktree creations failed")
	})

	typeText(v, "feat/z")
	_, cmd := v.Update(tea.KeyMsg{Type: tea.KeyEnter})
	v.Update(cmd())

	if len(wv.workflows) != 0 {
		t.Errorf("len(workflows) = %d, want none when every create failed", len(wv.workflows))
	}
	if v.err == nil {
		t.Error("expected an error to be shown")
	}
}

func TestRepoModeCreateIgnoresProjectPath(t *testing.T) {
	v := NewWorktreeView("/src/top")
	v.showCreate = true
	v.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if v.showCreate {
		t.Error("enter with no input should close the dialog")
	}
	if v.proj != nil {
		t.Error("repo-mode view must not carry a project")
	}
}
