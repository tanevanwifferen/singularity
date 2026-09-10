package app

import (
	"os"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"gitlab.com/tanevanwifferen1/singularity/internal/app/views"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// initProjectRouter wires the Workflows view back to the Flows view so 'f'
// can jump there (see workflows.go SetFlowsView). A silently-nil flowsView
// would still compile and run, just degrade every 'f' press to "Flows view
// unavailable" — this drives the real router built by SetProject end to end
// to catch a regression there, rather than asserting on an unexported field.
func TestInitProjectRouterWiresFlowsView(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	proj := &service.Project{Name: "flows-wiring-test"}
	proj.Repos = append(proj.Repos, &service.Repo{Name: "pbd-api", Path: "/src/pbd-api"})

	wf := service.NewFeatureWorkflow(proj, "feat/wired", base)
	for _, wr := range wf.Repos {
		if err := os.MkdirAll(wr.WorktreePath, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", wr.WorktreePath, err)
		}
	}
	if err := service.SaveWorkflows(proj.Name, []*service.FeatureWorkflow{wf}); err != nil {
		t.Fatalf("save workflows: %v", err)
	}

	m := New()
	m.SetProject(proj)

	wv, ok := m.router.GetView("Workflows").(*views.WorkflowsView)
	if !ok {
		t.Fatal("Workflows view not registered on the project router")
	}
	if _, ok := m.router.GetView("Flows").(*views.FlowsView); !ok {
		t.Fatal("Flows view not registered on the project router")
	}
	if err := wv.Refresh(); err != nil {
		t.Fatalf("refresh workflows: %v", err)
	}

	_, cmd := wv.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if cmd == nil {
		t.Fatal("'f' produced no command — initProjectRouter likely left the Workflows view's flowsView nil")
	}
	msg, ok := cmd().(views.ViewChangeMsg)
	if !ok || msg.ViewName != "Flows" {
		t.Fatalf("command = %#v, want ViewChangeMsg{Flows}", msg)
	}
}
