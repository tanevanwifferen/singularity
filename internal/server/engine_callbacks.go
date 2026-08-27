package server

import (
	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
)

// wireEngineCallbacks registers the OnAgentUpdate hook on the engine so the
// server can emit agent_output / agent_complete / agent_error WS events
// without needing a separate polling loop. Called once from New.
//
// The hook is invoked by the engine whenever any agent's output buffer
// advances or its state transitions; we read what's new since our last
// broadcast offset and emit one agent_output frame per OutputEntry, plus
// agent_complete / agent_error on terminal-state transitions.
func (s *Server) wireEngineCallbacks() {
	if s.engine == nil {
		return
	}
	s.engine.OnAgentUpdate(func(agentID string) {
		s.broadcastAgentUpdate(agentID)
	})
}

// broadcastAgentUpdate is the body of the OnAgentUpdate callback. Split out
// so it stays readable and to allow direct invocation from tests.
func (s *Server) broadcastAgentUpdate(agentID string) {
	if s.engine == nil {
		return
	}
	agent := s.engine.GetAgent(agentID)
	if agent == nil {
		return
	}
	snap := agent.Snapshot()

	// Claim the new output entries atomically: overlapping callbacks for the
	// same agent must not both read the same offset and broadcast the same
	// entries twice. The claim (read offset, fetch, advance offset) happens
	// under outputMu; the actual broadcasts happen outside it.
	s.outputMu.Lock()
	offset := s.agentOutputOffsets[agentID]
	entries, err := s.engine.GetOutputEntries(agentID, offset)
	if err == nil {
		s.agentOutputOffsets[agentID] = offset + len(entries)
	}
	// Terminal lifecycle events must be broadcast exactly once per agent;
	// claim that under the same lock.
	emitTerminal := false
	switch snap.State {
	case engine.AgentComplete, engine.AgentError, engine.AgentKilled:
		if !s.terminalBroadcast[agentID] {
			s.terminalBroadcast[agentID] = true
			emitTerminal = true
		}
	default:
		// Non-terminal state (e.g. a resumed agent running again): re-arm
		// the terminal broadcast for this agent's next completion.
		delete(s.terminalBroadcast, agentID)
	}
	s.outputMu.Unlock()

	if err == nil {
		for _, entry := range entries {
			entryCopy := entry
			s.wsBroadcast(api.WSMessage{
				Type: api.WSEventAgentOutput,
				Payload: api.AgentOutputPayload{
					AgentID: agentID,
					Entry:   &entryCopy,
				},
			})
		}
	}

	// Emit lifecycle events on terminal states.
	if !emitTerminal {
		return
	}
	switch snap.State {
	case engine.AgentComplete:
		s.wsBroadcast(api.WSMessage{
			Type: api.WSEventAgentComplete,
			Payload: api.AgentCompletePayload{
				AgentID:  agentID,
				State:    snap.State.String(),
				ExitCode: snap.ExitCode,
			},
		})
	case engine.AgentError, engine.AgentKilled:
		s.wsBroadcast(api.WSMessage{
			Type: api.WSEventAgentError,
			Payload: api.AgentErrorPayload{
				AgentID: agentID,
				Error:   snap.Error,
			},
		})
	}
}

// broadcastAgentStarted emits the canonical agent_started WS payload from
// the post-Start hook. Called by the Agent.Start handler after the daemon
// returns a fresh agent ID; this is the place that fixes the COVERAGE.md
// §3e payload-mismatch (server now sends agent_id, work_dir, task — the
// shape the TUI's wsAgentIDPayload already expects).
func (s *Server) broadcastAgentStarted(agentID, task, workDir string) {
	s.wsBroadcast(api.WSMessage{
		Type: api.WSEventAgentStarted,
		Payload: api.AgentStartedPayload{
			AgentID: agentID,
			Task:    task,
			WorkDir: workDir,
		},
	})
}
