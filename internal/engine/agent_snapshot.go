package engine

import "time"

// setState changes state (caller must hold mu).
func (a *Agent) setState(state AgentState) {
	a.State = state
}

// Snapshot returns a point-in-time copy of the agent's mutable fields (thread-safe).
func (a *Agent) Snapshot() AgentSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return AgentSnapshot{
		ID:           a.ID,
		WorkDir:      a.WorkDir,
		Task:         a.Task,
		Summary:      a.Summary,
		State:        a.State,
		CreatedAt:    a.CreatedAt,
		StartedAt:    a.StartedAt,
		EndedAt:      a.EndedAt,
		ExitCode:     a.ExitCode,
		Error:        a.Error,
		TotalCostUSD: a.TotalCostUSD,
		RouteResult:  a.RouteResult,
		MergeResult:  a.MergeResult,
		BackendName:  a.backend.Name(),
	}
}

// AgentSnapshot is a point-in-time copy of an agent's state, safe to read without locks.
type AgentSnapshot struct {
	ID           string
	WorkDir      string
	Task         string
	Summary      string
	State        AgentState
	CreatedAt    time.Time
	StartedAt    *time.Time
	EndedAt      *time.Time
	ExitCode     int
	Error        string
	TotalCostUSD float64
	RouteResult  *ClassificationResult
	MergeResult  string
	BackendName  string
}

// Done returns a channel that closes when the agent subprocess exits.
func (a *Agent) Done() <-chan struct{} {
	return a.done
}

// IsActive returns true if the agent is still running.
func (a *Agent) IsActive() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.State == AgentRunning || a.State == AgentStarting || a.State == AgentRouting
}
