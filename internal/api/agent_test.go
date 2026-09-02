package api

import (
	"encoding/json"
	"strings"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
)

// TestAgentSnapshotToDTOExitCode locks the contract that exit_code is only
// present on the wire once the agent reached a terminal state. A running
// agent must not report exit_code 0 — that would read as a successful exit.
func TestAgentSnapshotToDTOExitCode(t *testing.T) {
	cases := []struct {
		state    engine.AgentState
		exitCode int
		want     *int
	}{
		{engine.AgentIdle, 0, nil},
		{engine.AgentRouting, 0, nil},
		{engine.AgentStarting, 0, nil},
		{engine.AgentRunning, 0, nil},
		{engine.AgentComplete, 0, intPtr(0)},
		{engine.AgentError, 1, intPtr(1)},
		{engine.AgentKilled, 137, intPtr(137)},
	}
	for _, tc := range cases {
		t.Run(tc.state.String(), func(t *testing.T) {
			dto := AgentSnapshotToDTO(AgentSnapshot{ID: "a", State: tc.state, ExitCode: tc.exitCode})
			if (dto.ExitCode == nil) != (tc.want == nil) {
				t.Fatalf("ExitCode presence = %v, want %v", dto.ExitCode, tc.want)
			}
			if tc.want != nil && *dto.ExitCode != *tc.want {
				t.Errorf("ExitCode = %d, want %d", *dto.ExitCode, *tc.want)
			}
		})
	}
}

// TestAgentSnapshotDTOJSONOmitsExitCodeWhileRunning verifies the JSON shape
// consumers actually see: no exit_code key at all for non-terminal states.
func TestAgentSnapshotDTOJSONOmitsExitCodeWhileRunning(t *testing.T) {
	running := AgentSnapshotToDTO(AgentSnapshot{ID: "a", State: engine.AgentRunning})
	data, err := json.Marshal(running)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "exit_code") {
		t.Errorf("running agent JSON should omit exit_code, got: %s", data)
	}

	done := AgentSnapshotToDTO(AgentSnapshot{ID: "a", State: engine.AgentComplete, ExitCode: 0})
	data, err = json.Marshal(done)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"exit_code":0`) {
		t.Errorf("complete agent JSON should carry exit_code 0, got: %s", data)
	}
}

func intPtr(v int) *int { return &v }
