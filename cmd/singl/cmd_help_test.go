package main

import (
	"strings"
	"testing"
)

// TestQueueHelpDocumentsHumanEscalation pins the same contract
// cmd_prime_test.go pins for the primer, on the two surfaces the primer
// tells an orchestrating agent to trust for the flag reference: a task whose
// agent asked a question is waiting_human, `queue wait` returns early for it,
// and `queue answer` is how it is answered.
func TestQueueHelpDocumentsHumanEscalation(t *testing.T) {
	for name, text := range map[string]string{
		"singl help queue":        nounUsage["queue"],
		"singl queue wait --help": queueWaitUsage,
	} {
		if !strings.Contains(text, "waiting_human") {
			t.Errorf("%s does not mention waiting_human", name)
		}
	}
	if !strings.Contains(nounUsage["queue"], "answer    --id <task-id> --message <text>") {
		t.Error("queue help no longer lists the answer verb")
	}
	if strings.Contains(nounUsage["queue"], "inert") {
		t.Error("queue help still calls answer inert")
	}
	for _, want := range []string{"list", "answer", "blocked"} {
		if !strings.Contains(nounUsage["questions"], want) {
			t.Errorf("questions help does not mention %q", want)
		}
	}
}

// TestQueueWaitUsageMatchesExitCodes keeps the documented exit codes and
// queueWaitOutcome.exitCode from drifting: every outcome an operator can
// actually observe has to appear in the help text with the right code.
func TestQueueWaitUsageMatchesExitCodes(t *testing.T) {
	for _, tc := range []struct {
		outcome queueWaitOutcome
		want    int
	}{
		{queueWaitDone, 0},
		{queueWaitAnswerNeeded, 0},
		{queueWaitFailed, 1},
		{queueWaitTimedOut, 1},
	} {
		if got := tc.outcome.exitCode(); got != tc.want {
			t.Errorf("%s exit code = %d, want %d", tc.outcome, got, tc.want)
		}
	}
	if n := strings.Count(queueWaitUsage, "\n  0  "); n != 2 {
		t.Errorf("queue wait usage documents %d exit-0 outcomes, want 2", n)
	}
	if n := strings.Count(queueWaitUsage, "\n  1  "); n != 2 {
		t.Errorf("queue wait usage documents %d exit-1 outcomes, want 2", n)
	}
}
