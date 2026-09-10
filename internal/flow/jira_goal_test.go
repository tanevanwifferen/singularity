package flow

import (
	"strings"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/jira"
)

func TestGoalFromIssue(t *testing.T) {
	title, goal := GoalFromIssue(&jira.Issue{
		Key:         "PROJ-123",
		Summary:     "Retry on 429",
		Description: "Honour Retry-After.\n\nAcceptance criteria:\n- retries respect the header",
	})
	if title != "PROJ-123: Retry on 429" {
		t.Errorf("title = %q, want key and summary combined", title)
	}
	for _, want := range []string{"Jira issue: PROJ-123", "Summary: Retry on 429", "Acceptance criteria"} {
		if !strings.Contains(goal, want) {
			t.Errorf("goal missing %q:\n%s", want, goal)
		}
	}
}

func TestGoalFromIssueNoDescription(t *testing.T) {
	title, goal := GoalFromIssue(&jira.Issue{Key: "PROJ-1", Summary: "Fix the thing"})
	if title != "PROJ-1: Fix the thing" {
		t.Errorf("title = %q", title)
	}
	if strings.Contains(goal, "\n\n\n") {
		t.Errorf("goal has a dangling blank section with no description:\n%q", goal)
	}
	if !strings.Contains(goal, "Fix the thing") {
		t.Errorf("goal missing summary:\n%s", goal)
	}
}

func TestGoalFromIssueNoSummary(t *testing.T) {
	title, _ := GoalFromIssue(&jira.Issue{Key: "PROJ-1"})
	if title != "PROJ-1" {
		t.Errorf("title = %q, want the bare key when there is no summary", title)
	}
}

func TestGoalFromIssueNil(t *testing.T) {
	title, goal := GoalFromIssue(nil)
	if title != "" || goal != "" {
		t.Errorf("GoalFromIssue(nil) = (%q, %q), want both empty", title, goal)
	}
}
