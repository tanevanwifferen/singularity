package main

import (
	"strings"
	"testing"
)

// TestQueueHelpDoesNotAdvertiseHumanEscalation pins the same contract
// cmd_prime_test.go pins for the primer, on the two surfaces the primer
// tells an orchestrating agent to trust for the flag reference.
//
// This exists because correcting prime.md alone was not enough: the fix
// moved rather than removed, and the binary ended up contradicting its own
// primer. No task reaches waiting_human in this build — the engine never
// reports an agent asking a question — so an agent that reads `singl help
// queue` or `singl queue wait --help` must not learn to write that branch
// or to reach for `queue answer`, which returns CONFLICT for every input.
func TestQueueHelpDoesNotAdvertiseHumanEscalation(t *testing.T) {
	surfaces := map[string]string{
		"singl help queue":        nounUsage["queue"],
		"singl queue wait --help": queueWaitUsage,
	}
	banned := []string{
		"waiting_human",
		"asks a question",
		"waiting for input",
		"reply to a task",
	}
	for name, text := range surfaces {
		if text == "" {
			t.Fatalf("%s: usage text is empty", name)
		}
		for _, b := range banned {
			if strings.Contains(text, b) {
				t.Errorf("%s advertises human escalation (%q), which is unreachable in this build", name, b)
			}
		}
	}

	// The `answer` verb is still listed — it exists and must not silently
	// vanish from the reference — but only as inert.
	queue := nounUsage["queue"]
	if !strings.Contains(queue, "answer") {
		t.Error("queue help no longer lists the answer verb at all")
	}
	if !strings.Contains(queue, "inert") {
		t.Error("queue help lists answer without marking it inert")
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
		{queueWaitFailed, 1},
		{queueWaitTimedOut, 1},
	} {
		if got := tc.outcome.exitCode(); got != tc.want {
			t.Errorf("%s exit code = %d, want %d", tc.outcome, got, tc.want)
		}
	}
	if n := strings.Count(queueWaitUsage, "\n  0  "); n != 1 {
		t.Errorf("queue wait usage documents %d exit-0 outcomes, want exactly 1", n)
	}
	if n := strings.Count(queueWaitUsage, "\n  1  "); n != 2 {
		t.Errorf("queue wait usage documents %d exit-1 outcomes, want 2", n)
	}
}
