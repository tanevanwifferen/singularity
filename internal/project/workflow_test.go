package project

import (
	"testing"
)

func TestSanitizeBranchForPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"feature/add-auth", "feature-add-auth"},
		{"bugfix/fix-login/v2", "bugfix-fix-login-v2"},
		{"main", "main"},
		{"release/1.0/hotfix", "release-1.0-hotfix"},
		{"", ""},
	}
	for _, tt := range tests {
		got := sanitizeBranchForPath(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeBranchForPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNewFeatureWorkflow(t *testing.T) {
	proj := NewProject(ProjectDef{
		Name: "TestProject",
		Repos: []RepoDef{
			{Path: "/tmp/frontend", Name: "web", DefaultBranch: "main"},
			{Path: "/tmp/backend", Name: "api", DefaultBranch: "main"},
			{Path: "/tmp/shared", Name: "lib", DefaultBranch: "develop"},
		},
	})

	fw := NewFeatureWorkflow(proj, "feature/add-auth", "/tmp/worktrees")

	if fw.BranchName != "feature/add-auth" {
		t.Errorf("expected branch name 'feature/add-auth', got %q", fw.BranchName)
	}
	if fw.BaseDir != "/tmp/worktrees" {
		t.Errorf("expected base dir '/tmp/worktrees', got %q", fw.BaseDir)
	}
	if fw.State != WorkflowInitializing {
		t.Errorf("expected state WorkflowInitializing, got %v", fw.State)
	}
	if len(fw.Repos) != 3 {
		t.Fatalf("expected 3 repos, got %d", len(fw.Repos))
	}

	// Check worktree paths use sanitized branch name
	web := fw.Repos["web"]
	if web == nil {
		t.Fatal("expected 'web' repo in workflow")
	}
	if web.WorktreePath != "/tmp/worktrees/feature-add-auth/web" {
		t.Errorf("expected worktree path '/tmp/worktrees/feature-add-auth/web', got %q", web.WorktreePath)
	}
	if web.OriginalPath != "/tmp/frontend" {
		t.Errorf("expected original path '/tmp/frontend', got %q", web.OriginalPath)
	}

	api := fw.Repos["api"]
	if api == nil {
		t.Fatal("expected 'api' repo in workflow")
	}
	if api.WorktreePath != "/tmp/worktrees/feature-add-auth/api" {
		t.Errorf("expected worktree path '/tmp/worktrees/feature-add-auth/api', got %q", api.WorktreePath)
	}

	lib := fw.Repos["lib"]
	if lib == nil {
		t.Fatal("expected 'lib' repo in workflow")
	}
	if lib.WorktreePath != "/tmp/worktrees/feature-add-auth/lib" {
		t.Errorf("expected worktree path '/tmp/worktrees/feature-add-auth/lib', got %q", lib.WorktreePath)
	}
}

func TestNewFeatureWorkflow_SimpleBranch(t *testing.T) {
	proj := NewProject(ProjectDef{
		Name: "Simple",
		Repos: []RepoDef{
			{Path: "/tmp/repo", Name: "app", DefaultBranch: "main"},
		},
	})

	fw := NewFeatureWorkflow(proj, "hotfix", "/work")

	app := fw.Repos["app"]
	if app == nil {
		t.Fatal("expected 'app' repo in workflow")
	}
	if app.WorktreePath != "/work/hotfix/app" {
		t.Errorf("expected worktree path '/work/hotfix/app', got %q", app.WorktreePath)
	}
}

func TestWorkflowStatus_Initial(t *testing.T) {
	proj := NewProject(ProjectDef{
		Name: "Test",
		Repos: []RepoDef{
			{Path: "/tmp/a", Name: "web", DefaultBranch: "main"},
			{Path: "/tmp/b", Name: "api", DefaultBranch: "main"},
		},
	})

	fw := NewFeatureWorkflow(proj, "feature/test", "/tmp/wt")
	status := fw.Status()

	if status.BranchName != "feature/test" {
		t.Errorf("expected branch 'feature/test', got %q", status.BranchName)
	}
	if status.State != WorkflowInitializing {
		t.Errorf("expected state WorkflowInitializing, got %v", status.State)
	}
	if status.TotalRepos != 2 {
		t.Errorf("expected 2 total repos, got %d", status.TotalRepos)
	}
	if status.WorktreesCreated != 0 {
		t.Errorf("expected 0 worktrees created, got %d", status.WorktreesCreated)
	}
	if status.Pushed != 0 {
		t.Errorf("expected 0 pushed, got %d", status.Pushed)
	}
	if status.MRsCreated != 0 {
		t.Errorf("expected 0 MRs created, got %d", status.MRsCreated)
	}
	if status.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", status.Errors)
	}
}

func TestWorkflowStatus_AfterManualUpdates(t *testing.T) {
	proj := NewProject(ProjectDef{
		Name: "Test",
		Repos: []RepoDef{
			{Path: "/tmp/a", Name: "web", DefaultBranch: "main"},
			{Path: "/tmp/b", Name: "api", DefaultBranch: "main"},
			{Path: "/tmp/c", Name: "lib", DefaultBranch: "main"},
		},
	})

	fw := NewFeatureWorkflow(proj, "feature/x", "/tmp/wt")

	// Simulate some repos having been created/pushed/etc.
	fw.Repos["web"].WorktreeCreated = true
	fw.Repos["web"].Pushed = true
	fw.Repos["web"].MRURL = "https://github.com/org/web/pull/1"

	fw.Repos["api"].WorktreeCreated = true
	fw.Repos["api"].Error = "push: connection refused"

	// lib has no worktree yet

	fw.State = WorkflowActive

	status := fw.Status()
	if status.TotalRepos != 3 {
		t.Errorf("expected 3 total repos, got %d", status.TotalRepos)
	}
	if status.WorktreesCreated != 2 {
		t.Errorf("expected 2 worktrees created, got %d", status.WorktreesCreated)
	}
	if status.Pushed != 1 {
		t.Errorf("expected 1 pushed, got %d", status.Pushed)
	}
	if status.MRsCreated != 1 {
		t.Errorf("expected 1 MR created, got %d", status.MRsCreated)
	}
	if status.Errors != 1 {
		t.Errorf("expected 1 error, got %d", status.Errors)
	}
	if status.State != WorkflowActive {
		t.Errorf("expected state WorkflowActive, got %v", status.State)
	}
}

func TestWorkflowGetRepo(t *testing.T) {
	proj := NewProject(ProjectDef{
		Name: "Test",
		Repos: []RepoDef{
			{Path: "/tmp/a", Name: "web", DefaultBranch: "main"},
		},
	})

	fw := NewFeatureWorkflow(proj, "test-branch", "/tmp/wt")

	wr := fw.GetRepo("web")
	if wr == nil {
		t.Fatal("expected to find 'web' repo")
	}
	if wr.RepoName != "web" {
		t.Errorf("expected repo name 'web', got %q", wr.RepoName)
	}

	wr = fw.GetRepo("nonexistent")
	if wr != nil {
		t.Error("expected nil for nonexistent repo")
	}
}

func TestWorkflowSetAgentID(t *testing.T) {
	proj := NewProject(ProjectDef{
		Name: "Test",
		Repos: []RepoDef{
			{Path: "/tmp/a", Name: "web", DefaultBranch: "main"},
		},
	})

	fw := NewFeatureWorkflow(proj, "test-branch", "/tmp/wt")

	fw.SetAgentID("web", "agent-123")
	wr := fw.GetRepo("web")
	if wr.AgentID != "agent-123" {
		t.Errorf("expected agent ID 'agent-123', got %q", wr.AgentID)
	}

	// Setting agent ID on nonexistent repo should not panic
	fw.SetAgentID("nonexistent", "agent-456")
}

func TestWorkflowStateString(t *testing.T) {
	tests := []struct {
		state WorkflowState
		want  string
	}{
		{WorkflowInitializing, "initializing"},
		{WorkflowActive, "active"},
		{WorkflowPushingAll, "pushing"},
		{WorkflowCreatingMRs, "creating_mrs"},
		{WorkflowCleaningUp, "cleaning_up"},
		{WorkflowDone, "done"},
		{WorkflowState(99), "unknown"},
	}
	for _, tt := range tests {
		got := tt.state.String()
		if got != tt.want {
			t.Errorf("WorkflowState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}
