package remote

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// remoteAgentService implements service.AgentService.
//
// The daemon's wire DTO is api.AgentSnapshotDTO (snake_case fields with
// the state rendered as its String() form). The service interface deals
// in service.AgentSnapshot (== engine.AgentSnapshot); dtoToSnapshot
// performs the reverse projection.
type remoteAgentService struct {
	c *client.Client
}

// Start spawns a new agent for the given working dir + task.
func (s *remoteAgentService) Start(ctx context.Context, workDir, prompt string, opts service.AgentOptions) (string, error) {
	return s.c.AgentStart(ctx, workDir, prompt, opts)
}

// Resume creates a new agent inheriting the prior agent's history.
func (s *remoteAgentService) Resume(ctx context.Context, agentID, message string, opts service.AgentOptions) (string, error) {
	return s.c.AgentResume(ctx, agentID, message, opts)
}

// SendInput pushes a message to a running agent's stdin.
func (s *remoteAgentService) SendInput(ctx context.Context, agentID, message string) error {
	return s.c.AgentSendInput(ctx, agentID, message)
}

// Kill terminates the agent's subprocess.
func (s *remoteAgentService) Kill(ctx context.Context, agentID string) error {
	return s.c.AgentKill(ctx, agentID)
}

// Remove drops the agent from the engine's registry.
func (s *remoteAgentService) Remove(ctx context.Context, agentID string) error {
	return s.c.AgentRemove(ctx, agentID)
}

// List returns a snapshot of every agent currently known to the engine.
func (s *remoteAgentService) List(ctx context.Context) ([]service.AgentSnapshot, error) {
	dtos, err := s.c.AgentList(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]service.AgentSnapshot, len(dtos))
	for i := range dtos {
		out[i] = dtoToSnapshot(dtos[i])
	}
	return out, nil
}

// Get returns one agent's snapshot.
func (s *remoteAgentService) Get(ctx context.Context, agentID string) (*service.AgentSnapshot, error) {
	dto, err := s.c.AgentGet(ctx, agentID)
	if err != nil {
		return nil, err
	}
	snap := dtoToSnapshot(*dto)
	return &snap, nil
}

// Output returns buffered output entries from offset onward.
func (s *remoteAgentService) Output(ctx context.Context, agentID string, offset int) ([]service.OutputEntry, error) {
	return s.c.AgentOutput(ctx, agentID, offset)
}

// Stats returns engine-wide counts.
func (s *remoteAgentService) Stats(ctx context.Context) (service.EngineStats, error) {
	return s.c.AgentStats(ctx)
}

// MaxAgents returns the engine's configured max-concurrent cap.
func (s *remoteAgentService) MaxAgents(ctx context.Context) (int, error) {
	return s.c.AgentMaxAgents(ctx)
}

// Subscribe streams AgentEvents for a single agent.
func (s *remoteAgentService) Subscribe(ctx context.Context, agentID string) (<-chan service.AgentEvent, func(), error) {
	return s.c.AgentSubscribe(ctx, agentID)
}

// SubscribeAll streams AgentEvents for every agent.
func (s *remoteAgentService) SubscribeAll(ctx context.Context) (<-chan service.AgentEvent, func(), error) {
	return s.c.AgentSubscribeAll(ctx)
}

// dtoToSnapshot reverses api.AgentSnapshotToDTO. The state string is
// parsed back into its engine.AgentState ordinal; unknown strings yield
// AgentIdle (the zero value) which matches the engine's behavior for
// uninitialised agents.
func dtoToSnapshot(d api.AgentSnapshotDTO) service.AgentSnapshot {
	snap := service.AgentSnapshot{
		ID:           d.ID,
		WorkDir:      d.WorkDir,
		Task:         d.Task,
		Summary:      d.Summary,
		State:        parseAgentState(d.State),
		CreatedAt:    d.CreatedAt,
		StartedAt:    d.StartedAt,
		EndedAt:      d.EndedAt,
		Error:        d.Error,
		TotalCostUSD: d.TotalCostUSD,
		MergeResult:  d.MergeResult,
		// RouteResult intentionally omitted: daemon-side only, not sent
		// across the wire (api.AgentSnapshotDTO has no field for it).
	}
	// ExitCode is only on the wire for terminal states; nil means "not
	// exited yet", which maps back to the engine's zero value.
	if d.ExitCode != nil {
		snap.ExitCode = *d.ExitCode
	}
	return snap
}

// parseAgentState is the inverse of engine.AgentState.String().
func parseAgentState(s string) service.AgentState {
	switch s {
	case "idle":
		return engine.AgentIdle
	case "routing":
		return engine.AgentRouting
	case "starting":
		return engine.AgentStarting
	case "running":
		return engine.AgentRunning
	case "complete":
		return engine.AgentComplete
	case "error":
		return engine.AgentError
	case "killed":
		return engine.AgentKilled
	default:
		return engine.AgentIdle
	}
}
