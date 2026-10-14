package views

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
	"gitlab.com/tanevanwifferen1/singularity/internal/service/fake"
)

func jiraFlowTestIssue() *service.Issue {
	return &service.Issue{Key: "ABC-7", Summary: "Add retry helper", Type: "Story", Priority: "High"}
}

func TestJiraConfirmTabTogglesFlowMode(t *testing.T) {
	v := NewAgentView("/repo")
	v.SetProject(&service.Project{})
	v.jiraConfirmIssue = jiraFlowTestIssue()

	v.handleJiraAgentConfirm(tea.KeyMsg{Type: tea.KeyTab})
	if v.jiraConfirmMode != "flow" {
		t.Fatalf("mode after first tab = %q, want flow", v.jiraConfirmMode)
	}
	if !strings.Contains(v.View(), "Tab: Agent/Flow") {
		t.Error("footer does not advertise Tab when flow mode is available")
	}
	v.handleJiraAgentConfirm(tea.KeyMsg{Type: tea.KeyTab})
	if v.jiraConfirmMode != "" {
		t.Fatalf("mode after second tab = %q, want agent", v.jiraConfirmMode)
	}
}

func TestJiraConfirmTabIgnoredWithoutProject(t *testing.T) {
	v := NewAgentView("/repo")
	v.jiraConfirmIssue = jiraFlowTestIssue()

	v.handleJiraAgentConfirm(tea.KeyMsg{Type: tea.KeyTab})
	if v.jiraConfirmMode != "" {
		t.Fatalf("mode = %q, want unchanged without a project", v.jiraConfirmMode)
	}
	if strings.Contains(v.View(), "Agent/Flow") {
		t.Error("footer advertises Agent/Flow although flow mode is unavailable")
	}
}

func TestJiraConfirmCtrlWStillDeletesWord(t *testing.T) {
	v := NewAgentView("/repo")
	v.SetProject(&service.Project{})
	v.jiraConfirmIssue = jiraFlowTestIssue()
	v.jiraExtraInput.Insert("keep drop")

	v.handleJiraAgentConfirm(tea.KeyMsg{Type: tea.KeyCtrlW})
	if v.jiraConfirmMode != "" {
		t.Errorf("ctrl+w changed the mode to %q", v.jiraConfirmMode)
	}
	if got := v.jiraExtraInput.Value; got != "keep " {
		t.Errorf("input after ctrl+w = %q, want %q", got, "keep ")
	}
}

func TestJiraConfirmEnterInFlowModeStartsFlow(t *testing.T) {
	var got service.FlowStartRequest
	stub := fake.NewFlowStub()
	stub.StartFn = func(_ context.Context, req service.FlowStartRequest) (*service.Flow, error) {
		got = req
		return &service.Flow{ID: "f1"}, nil
	}
	svcs := fake.New()
	svcs.Flow = stub
	old := createJiraWorkflowDir
	var gotURL string
	createJiraWorkflowDir = func(_ *service.Project, branch, jiraURL string) (string, error) {
		gotURL = jiraURL
		return "/wf/" + branch, nil
	}
	defer func() { createJiraWorkflowDir = old }()

	v := NewAgentView("/repo")
	v.SetServices(svcs)
	v.SetProject(&service.Project{})
	v.jiraCfg.BaseURL = "https://jira.example"
	v.jiraConfirmIssue = jiraFlowTestIssue()
	v.jiraConfirmMode = "flow"
	v.jiraExtraInput.Insert("be careful")

	cmd := v.handleJiraAgentConfirm(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter in flow mode returned no command")
	}
	if v.jiraConfirmIssue != nil || v.jiraConfirmMode != "" {
		t.Error("modal state not reset after Enter")
	}
	msg, ok := cmd().(AgentCreatedMsg)
	if !ok || msg.Err != nil {
		t.Fatalf("result = %#v, want AgentCreatedMsg without error", msg)
	}
	if want := "https://jira.example/browse/ABC-7"; gotURL != want {
		t.Errorf("jiraURL = %q, want %q", gotURL, want)
	}
	if !strings.HasPrefix(got.WorkDir, "/wf/") {
		t.Errorf("flow WorkDir = %q, want the workflow root", got.WorkDir)
	}
	if !strings.Contains(got.Goal, "ABC-7") || !strings.Contains(got.Goal, "be careful") {
		t.Errorf("flow Goal missing ticket or instructions: %q", got.Goal)
	}
	if got.IssueKey != "ABC-7" {
		t.Errorf("IssueKey = %q, want ABC-7", got.IssueKey)
	}
}

func TestStartJiraFlowErrors(t *testing.T) {
	old := createJiraWorkflowDir
	defer func() { createJiraWorkflowDir = old }()
	svcs := fake.New()

	createJiraWorkflowDir = func(*service.Project, string, string) (string, error) {
		return "", errors.New("boom")
	}
	if err := startJiraFlow(context.Background(), svcs, jiraFlowTestIssue(), "b", nil, "", ""); err == nil {
		t.Error("worktree failure was swallowed")
	}

	createJiraWorkflowDir = func(*service.Project, string, string) (string, error) { return "/wf", nil }
	stub := fake.NewFlowStub()
	stub.StartFn = func(context.Context, service.FlowStartRequest) (*service.Flow, error) {
		return nil, errors.New("refused")
	}
	svcs.Flow = stub
	if err := startJiraFlow(context.Background(), svcs, jiraFlowTestIssue(), "b", nil, "", ""); err == nil {
		t.Error("flow start failure was swallowed")
	}
}
