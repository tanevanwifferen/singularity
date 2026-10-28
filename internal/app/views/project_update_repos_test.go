package views

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
	"gitlab.com/tanevanwifferen1/singularity/internal/service/fake"
)

// updateReposProject records UpdateRepos calls and serves a fixed Info;
// everything else falls through to the fake stub.
type updateReposProject struct {
	service.ProjectService
	gotHandle service.ProjectHandle
	gotDir    string
	info      *service.ProjectInfo
}

func (p *updateReposProject) UpdateRepos(_ context.Context, h service.ProjectHandle, dir string) (*service.ProjectRepoUpdate, error) {
	p.gotHandle, p.gotDir = h, dir
	return &service.ProjectRepoUpdate{Handle: h, Added: []string{"sub"}, Workflows: []string{"feat/x"}}, nil
}

func (p *updateReposProject) Info(context.Context, service.ProjectHandle) (*service.ProjectInfo, error) {
	return p.info, nil
}

// With a daemon handle, 'u' rescans through ProjectService.UpdateRepos (which
// persists the config and syncs workflow worktrees) rather than only the
// in-memory project, and the shared project takes the daemon's repo list.
func TestProjectViewUpdateReposUsesDaemon(t *testing.T) {
	root := t.TempDir()
	proj := service.NewProject(service.ProjectDef{
		Name:  "p",
		Repos: []service.RepoDef{{Name: "root", Path: root, DefaultBranch: "main"}},
	})
	stub := &updateReposProject{
		ProjectService: fake.New().Project,
		info: &service.ProjectInfo{Repos: []service.RepoSummary{
			{Name: "root", Path: root, DefaultBranch: "main"},
			{Name: "sub", Path: root + "/sub", DefaultBranch: "main"},
		}},
	}
	svcs := fake.New()
	svcs.Project = stub

	v := NewProjectView(proj)
	v.SetServices(svcs)
	v.SetHandle("proj-p")

	_, cmd := v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	if cmd == nil {
		t.Fatal("'u' returned no command; want an async UpdateRepos call")
	}
	msg, ok := cmd().(ProjectReposUpdatedMsg)
	if !ok {
		t.Fatalf("command returned %T, want ProjectReposUpdatedMsg", msg)
	}
	if stub.gotHandle != "proj-p" || stub.gotDir != "" {
		t.Errorf("UpdateRepos(%q, %q), want (proj-p, \"\")", stub.gotHandle, stub.gotDir)
	}

	v.HandleReposUpdated(msg)
	if names := proj.RepoNames(); len(names) != 2 {
		t.Errorf("project repos = %v, want root and sub", names)
	}
	if !strings.Contains(v.updateReposResult, "sub (added)") || !strings.Contains(v.updateReposResult, "feat/x") {
		t.Errorf("result = %q, want the added repo and the synced workflow", v.updateReposResult)
	}
}
