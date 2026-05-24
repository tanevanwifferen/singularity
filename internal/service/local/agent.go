package local

import (
	"context"
	"strings"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// localAgentService implements service.AgentService over the shared
// *engine.Engine.
//
// Subscribe / SubscribeAll deliberately do NOT call engine.OnAgentUpdate —
// the server already owns that single-slot callback to drive its WS
// broadcasts (server.wireEngineCallbacks). Instead, each subscription
// spawns a lightweight per-stream polling goroutine that reads new
// OutputEntries on a short interval and emits state-transition events.
// This keeps the WS surface untouched while honoring the channel/cancel
// contract of the service interface.
type localAgentService struct {
	eng *engine.Engine
}

// Start spawns a new agent.
func (s *localAgentService) Start(ctx context.Context, workDir, prompt string, opts service.AgentOptions) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	if s.eng == nil {
		return "", service.ErrUnavailable
	}
	id, err := s.eng.StartAgent(workDir, prompt, opts)
	if err != nil {
		return id, mapAgentStartErr(err)
	}
	return id, nil
}

// Resume creates a new agent inheriting the prior agent's history.
func (s *localAgentService) Resume(ctx context.Context, agentID, message string, opts service.AgentOptions) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	if s.eng == nil {
		return "", service.ErrUnavailable
	}
	id, err := s.eng.ResumeWithHistory(agentID, message, opts)
	if err != nil {
		return id, mapAgentStartErr(err)
	}
	return id, nil
}

// SendInput pushes a message to a running agent's stdin.
func (s *localAgentService) SendInput(ctx context.Context, agentID, message string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if s.eng == nil {
		return service.ErrUnavailable
	}
	return wrapErr(s.eng.SendInput(agentID, message))
}

// Kill terminates the agent's subprocess.
func (s *localAgentService) Kill(ctx context.Context, agentID string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if s.eng == nil {
		return service.ErrUnavailable
	}
	return wrapErr(s.eng.KillAgent(agentID))
}

// Remove drops the agent from the engine's registry.
func (s *localAgentService) Remove(ctx context.Context, agentID string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if s.eng == nil {
		return service.ErrUnavailable
	}
	return wrapErr(s.eng.RemoveAgent(agentID))
}

// List returns a snapshot of every agent currently known to the engine.
func (s *localAgentService) List(ctx context.Context) ([]service.AgentSnapshot, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	if s.eng == nil {
		return nil, service.ErrUnavailable
	}
	return agentSnapshotsFromEngine(s.eng.ListAgents()), nil
}

// Get returns one agent's snapshot or ErrNotFound.
func (s *localAgentService) Get(ctx context.Context, agentID string) (*service.AgentSnapshot, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	if s.eng == nil {
		return nil, service.ErrUnavailable
	}
	a := s.eng.GetAgent(agentID)
	if a == nil {
		return nil, service.ErrNotFound
	}
	snap := a.Snapshot()
	return &snap, nil
}

// Output returns buffered output entries from offset onward.
func (s *localAgentService) Output(ctx context.Context, agentID string, offset int) ([]service.OutputEntry, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	if s.eng == nil {
		return nil, service.ErrUnavailable
	}
	entries, err := s.eng.GetOutputEntries(agentID, offset)
	if err != nil {
		return nil, wrapErr(err)
	}
	return entries, nil
}

// Stats returns engine-wide counts.
func (s *localAgentService) Stats(ctx context.Context) (service.EngineStats, error) {
	if err := checkCtx(ctx); err != nil {
		return service.EngineStats{}, err
	}
	if s.eng == nil {
		return service.EngineStats{}, service.ErrUnavailable
	}
	return s.eng.Stats(), nil
}

// MaxAgents returns the engine's configured max-concurrent cap.
func (s *localAgentService) MaxAgents(ctx context.Context) (int, error) {
	if err := checkCtx(ctx); err != nil {
		return 0, err
	}
	if s.eng == nil {
		return 0, service.ErrUnavailable
	}
	return s.eng.MaxAgents(), nil
}

// Subscribe streams AgentEvents for a single agent via a lightweight poller.
func (s *localAgentService) Subscribe(ctx context.Context, agentID string) (<-chan service.AgentEvent, func(), error) {
	if err := checkCtx(ctx); err != nil {
		return nil, nil, err
	}
	if s.eng == nil {
		return nil, nil, service.ErrUnavailable
	}
	if s.eng.GetAgent(agentID) == nil {
		return nil, nil, service.ErrNotFound
	}
	ch, cancel := s.startPoller(ctx, agentID)
	return ch, cancel, nil
}

// SubscribeAll streams AgentEvents for every agent currently in the engine
// plus any added during the lifetime of the subscription.
func (s *localAgentService) SubscribeAll(ctx context.Context) (<-chan service.AgentEvent, func(), error) {
	if err := checkCtx(ctx); err != nil {
		return nil, nil, err
	}
	if s.eng == nil {
		return nil, nil, service.ErrUnavailable
	}
	ch, cancel := s.startPoller(ctx, "")
	return ch, cancel, nil
}

// startPoller spawns a poller goroutine. agentID == "" means "all agents".
func (s *localAgentService) startPoller(ctx context.Context, agentID string) (<-chan service.AgentEvent, func()) {
	cctx, cancel := context.WithCancel(ctx)
	out := make(chan service.AgentEvent, 64)

	go func() {
		defer close(out)
		offsets := make(map[string]int)
		lastState := make(map[string]engine.AgentState)
		// Initial "started" snapshot for targeted single-agent subs.
		if agentID != "" {
			if a := s.eng.GetAgent(agentID); a != nil {
				snap := a.Snapshot()
				lastState[agentID] = snap.State
				sendEvent(cctx, out, service.AgentEvent{
					Kind:      service.AgentEventStarted,
					AgentID:   agentID,
					State:     snap.State,
					Timestamp: time.Now(),
				})
			}
		}

		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-cctx.Done():
				return
			case <-ticker.C:
			}

			ids := s.idsToPoll(agentID)
			for _, id := range ids {
				a := s.eng.GetAgent(id)
				if a == nil {
					continue
				}
				snap := a.Snapshot()
				offset := offsets[id]
				entries, err := s.eng.GetOutputEntries(id, offset)
				if err == nil && len(entries) > 0 {
					for i := range entries {
						entry := entries[i]
						sendEvent(cctx, out, service.AgentEvent{
							Kind:      service.AgentEventOutput,
							AgentID:   id,
							Output:    &entry,
							Timestamp: entry.Timestamp,
						})
					}
					offsets[id] = offset + len(entries)
				}
				prev, seen := lastState[id]
				if !seen || prev != snap.State {
					ev := service.AgentEvent{
						Kind:      service.AgentEventState,
						AgentID:   id,
						State:     snap.State,
						Timestamp: time.Now(),
					}
					switch snap.State {
					case engine.AgentComplete:
						ev.Kind = service.AgentEventComplete
					case engine.AgentError, engine.AgentKilled:
						ev.Kind = service.AgentEventError
						ev.Err = snap.Error
					}
					sendEvent(cctx, out, ev)
					lastState[id] = snap.State
				}
			}
		}
	}()

	return out, cancel
}

func (s *localAgentService) idsToPoll(agentID string) []string {
	if agentID != "" {
		return []string{agentID}
	}
	agents := s.eng.ListAgents()
	out := make([]string, 0, len(agents))
	for _, a := range agents {
		out = append(out, a.ID)
	}
	return out
}

// sendEvent does a non-blocking-ish send: it respects ctx cancellation and
// drops on full channel rather than blocking the poller indefinitely.
func sendEvent(ctx context.Context, ch chan<- service.AgentEvent, ev service.AgentEvent) {
	select {
	case ch <- ev:
	case <-ctx.Done():
	}
}

// mapAgentStartErr maps the engine's stringly-typed "agent limit reached"
// error into the service sentinel.
func mapAgentStartErr(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "agent limit reached") {
		return service.ErrAgentLimit
	}
	return wrapErr(err)
}
