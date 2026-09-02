package remote

import (
	"testing"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
)

// TestParseAgentState verifies the inverse of engine.AgentState.String().
// Unknown values must fall back to AgentIdle (the zero value) — that's the
// contract documented on parseAgentState.
func TestParseAgentState(t *testing.T) {
	cases := []struct {
		in   string
		want engine.AgentState
	}{
		{"idle", engine.AgentIdle},
		{"routing", engine.AgentRouting},
		{"starting", engine.AgentStarting},
		{"running", engine.AgentRunning},
		{"complete", engine.AgentComplete},
		{"error", engine.AgentError},
		{"killed", engine.AgentKilled},
		{"", engine.AgentIdle},                // empty → fallback
		{"unknown", engine.AgentIdle},         // unknown → fallback
		{"Running", engine.AgentIdle},         // case-sensitive → fallback
		{"agent_running_x", engine.AgentIdle}, // junk → fallback
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := parseAgentState(tc.in); got != tc.want {
				t.Errorf("parseAgentState(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestDtoToSnapshot exercises the full DTO → engine.AgentSnapshot mapping.
// Round-trips through api.AgentSnapshotToDTO are also covered to lock the
// contract that the projection + parse pair is reversible (modulo
// RouteResult which is daemon-only by design).
func TestDtoToSnapshot(t *testing.T) {
	now := time.Now().UTC().Round(time.Millisecond)
	later := now.Add(time.Second)
	exitCode := 3
	dto := api.AgentSnapshotDTO{
		ID:           "agent-123",
		WorkDir:      "/tmp/repo",
		Task:         "fix the bug",
		Summary:      "fixes the bug",
		State:        "error",
		CreatedAt:    now,
		StartedAt:    &now,
		EndedAt:      &later,
		ExitCode:     &exitCode,
		Error:        "boom",
		TotalCostUSD: 0.42,
		MergeResult:  "merged",
	}

	snap := dtoToSnapshot(dto)
	if snap.ID != dto.ID || snap.WorkDir != dto.WorkDir || snap.Task != dto.Task ||
		snap.Summary != dto.Summary || snap.MergeResult != dto.MergeResult ||
		snap.TotalCostUSD != dto.TotalCostUSD || snap.ExitCode != exitCode {
		t.Errorf("scalar field mismatch: %+v vs %+v", snap, dto)
	}
	if snap.State != engine.AgentError {
		t.Errorf("State = %v, want %v", snap.State, engine.AgentError)
	}
	if !snap.CreatedAt.Equal(dto.CreatedAt) {
		t.Errorf("CreatedAt mismatch")
	}
	if snap.StartedAt == nil || !snap.StartedAt.Equal(*dto.StartedAt) {
		t.Errorf("StartedAt mismatch")
	}
	if snap.EndedAt == nil || !snap.EndedAt.Equal(*dto.EndedAt) {
		t.Errorf("EndedAt mismatch")
	}

	// Round-trip: snap → DTO → snap should be stable on every shared field.
	dto2 := api.AgentSnapshotToDTO(snap)
	if dto2.State != dto.State {
		t.Errorf("round-trip State: %q -> %q", dto.State, dto2.State)
	}
	snap2 := dtoToSnapshot(dto2)
	if snap2.State != snap.State {
		t.Errorf("round-trip State (snapshot): %v -> %v", snap.State, snap2.State)
	}
}

// TestDtoToSnapshotNilTimestamps verifies optional pointer fields stay nil
// when the DTO omits them. JSON-decoded payloads often arrive with nil
// pointers when started_at / ended_at are not yet populated.
func TestDtoToSnapshotNilTimestamps(t *testing.T) {
	dto := api.AgentSnapshotDTO{
		ID:    "agent-nil",
		State: "idle",
	}
	snap := dtoToSnapshot(dto)
	if snap.StartedAt != nil {
		t.Errorf("StartedAt should stay nil")
	}
	if snap.EndedAt != nil {
		t.Errorf("EndedAt should stay nil")
	}
	if snap.State != engine.AgentIdle {
		t.Errorf("State should be AgentIdle, got %v", snap.State)
	}
}
