package views

import (
	"context"
	"errors"
	"sync"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/config"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
	"gitlab.com/tanevanwifferen1/singularity/internal/service/fake"
)

// stubJiraService records the queries it was handed so a test can assert the
// call actually reached the service layer instead of being short-circuited by
// an unwired dependency.
type stubJiraService struct {
	service.JiraService

	mu       sync.Mutex
	searched []string
	fetched  []string
	comments []string
}

func (s *stubJiraService) SearchIssues(_ context.Context, jql string, _ int) (*service.SearchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.searched = append(s.searched, jql)
	return &service.SearchResult{
		Total:  1,
		Issues: []service.Issue{{Key: "PBD-1", Summary: "first"}},
	}, nil
}

func (s *stubJiraService) GetIssue(_ context.Context, key string) (*service.Issue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fetched = append(s.fetched, key)
	return &service.Issue{Key: key, Summary: "one"}, nil
}

func (s *stubJiraService) AddComment(_ context.Context, key, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.comments = append(s.comments, key+": "+body)
	return nil
}

func (s *stubJiraService) calls() (searched, fetched, comments []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.searched...),
		append([]string(nil), s.fetched...),
		append([]string(nil), s.comments...)
}

func stubJiraServices() (*service.Services, *stubJiraService) {
	svcs := fake.New()
	stub := &stubJiraService{}
	svcs.Jira = stub
	return svcs, stub
}

var testJiraCfg = config.JiraConfig{
	Enabled:        true,
	BaseURL:        "https://example.atlassian.net",
	Email:          "dev@example.com",
	APIToken:       "token",
	DefaultProject: "PBD",
}

// TestAgentViewJiraPickerUsesServices locks the wiring the Agents view needs:
// SetJiraConfig builds the picker after the router has already handed the
// view its services, so the picker has to be given them too. Without that the
// picker answered every load and every search with ErrUnavailable
// ("service unavailable") and never reached the daemon at all.
func TestAgentViewJiraPickerUsesServices(t *testing.T) {
	svcs, stub := stubJiraServices()
	v := NewAgentView("/tmp/repo")
	v.SetServices(svcs)
	v.SetJiraConfig(testJiraCfg)

	msg, ok := v.jiraPicker.Open()().(jiraPickerLoadedMsg)
	if !ok {
		t.Fatalf("Open() produced %T, want jiraPickerLoadedMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("picker open: unexpected error %v", msg.err)
	}
	if len(msg.issues) != 1 {
		t.Fatalf("picker open: got %d issues, want 1", len(msg.issues))
	}

	searched, _, _ := stub.calls()
	if len(searched) != 1 {
		t.Fatalf("SearchIssues called %d times, want 1", len(searched))
	}
}

// TestAgentViewJiraPickerConfigBeforeServices covers the reverse wiring order:
// a config reload rebuilds the picker, and a later SetServices must reach it.
func TestAgentViewJiraPickerConfigBeforeServices(t *testing.T) {
	svcs, stub := stubJiraServices()
	v := NewAgentView("/tmp/repo")
	v.SetJiraConfig(testJiraCfg)
	v.SetServices(svcs)

	msg, ok := v.jiraPicker.fetchCmd("PBD-42")().(jiraPickerLoadedMsg)
	if !ok {
		t.Fatalf("fetch produced %T, want jiraPickerLoadedMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("picker key lookup: unexpected error %v", msg.err)
	}
	_, fetched, _ := stub.calls()
	if len(fetched) != 1 || fetched[0] != "PBD-42" {
		t.Fatalf("GetIssue calls = %v, want [PBD-42]", fetched)
	}
}

// TestWorkflowsViewJiraPickerUsesServices is the Workflows-view twin of the
// Agents-view test above; it builds its picker through the same path.
func TestWorkflowsViewJiraPickerUsesServices(t *testing.T) {
	svcs, stub := stubJiraServices()
	v := NewWorkflowsView(&service.Project{Name: "proj"})
	v.SetServices(svcs)
	v.SetJiraConfig(testJiraCfg)

	msg, ok := v.jiraPicker.Open()().(jiraPickerLoadedMsg)
	if !ok {
		t.Fatalf("Open() produced %T, want jiraPickerLoadedMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("picker open: unexpected error %v", msg.err)
	}
	searched, _, _ := stub.calls()
	if len(searched) != 1 {
		t.Fatalf("SearchIssues called %d times, want 1", len(searched))
	}
}

// TestJiraPickerUnwiredErrorIsDiagnosable keeps the genuinely-unwired case
// distinguishable from a daemon that is really unavailable: it still matches
// service.ErrUnavailable (callers use errors.Is) but says which component is
// missing its services.
func TestJiraPickerUnwiredErrorIsDiagnosable(t *testing.T) {
	p := NewJiraPickerState(testJiraCfg)
	if p == nil {
		t.Fatal("NewJiraPickerState returned nil for a configured Jira")
	}
	msg, ok := p.fetchCmd("project = PBD")().(jiraPickerLoadedMsg)
	if !ok {
		t.Fatalf("fetch produced %T, want jiraPickerLoadedMsg", msg)
	}
	if !errors.Is(msg.err, service.ErrUnavailable) {
		t.Fatalf("err = %v, want it to match service.ErrUnavailable", msg.err)
	}
	if msg.err.Error() == service.ErrUnavailable.Error() {
		t.Errorf("err message %q is indistinguishable from a real daemon outage", msg.err)
	}
}

// TestJiraViewApprovalGetsServices covers the same wiring gap one component
// over: the approval view executes the accepted Jira actions through its own
// services reference, so the parent has to hand it one at construction.
func TestJiraViewApprovalGetsServices(t *testing.T) {
	svcs, stub := stubJiraServices()
	v := NewJiraView(testJiraCfg)
	v.SetServices(svcs)
	v.aiMode = "refine"
	v.aiAgentID = "agent-1"

	v.Update(jiraAIOutputMsg{
		done: true,
		actions: []service.JiraAction{
			{Type: "comment", Order: 1, IssueKey: "PBD-7", Body: "looks good"},
		},
	})
	if v.approvalView == nil {
		t.Fatal("approval view was not created for a completed agent with actions")
	}

	msg, ok := v.approvalView.executeActions()().(approvalExecDoneMsg)
	if !ok {
		t.Fatalf("execute produced %T, want approvalExecDoneMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("execute: unexpected error %v", msg.err)
	}
	_, _, comments := stub.calls()
	if len(comments) != 1 {
		t.Fatalf("AddComment calls = %v, want one entry", comments)
	}
}
