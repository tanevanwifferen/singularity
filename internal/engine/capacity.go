package engine

import "path/filepath"

// ActiveCount returns the number of agents StartAgent's capacity check
// counts, i.e. agents for which IsActive reports true.
//
// This is deliberately NOT EngineStats.Active, which omits AgentRouting: a
// scheduler that sizes its dispatch on the stats number over-dispatches for
// the whole duration of a smart-route classifier round trip and then has
// its spawn refused. Anything gating on capacity must use this.
func (e *Engine) ActiveCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()

	n := 0
	for _, a := range e.agents {
		if a.IsActive() {
			n++
		}
	}
	return n
}

// WorkDirOccupied reports whether any agent's subprocess is still resident
// in dir, regardless of that agent's state label or who started it.
//
// This is deliberately NOT built on ActiveAgents/IsActive: those answer a
// bookkeeping question (does this agent hold an engine slot right now?),
// which is rightly false the instant a state goes terminal. Directory
// occupancy is a different question — is a process still there editing
// files? — and a state of killed, complete or error is not proof of that:
// softClose leaves the process running by design, and a backend's session
// process can outlive the BackendResult event that moves the state to
// complete (see terminate's doc comment). An agent the queue never started
// — a bare `agents spawn`, a TUI-killed agent, a Jira AI agent — reaches
// exactly those states without anything else ever terminating it, so asking
// IsActive here would report its directory free while the process is still
// in it.
func (e *Engine) WorkDirOccupied(dir string) bool {
	e.mu.RLock()
	agents := make([]*Agent, 0, len(e.agents))
	for _, a := range e.agents {
		agents = append(agents, a)
	}
	e.mu.RUnlock()

	want := filepath.Clean(dir)
	for _, a := range agents {
		if a.processExited() {
			continue
		}
		if filepath.Clean(a.Snapshot().WorkDir) == want {
			return true
		}
	}
	return false
}

// MaxAgents returns the maximum number of concurrent agents allowed.
// This value is set at construction and never changes.
func (e *Engine) MaxAgents() int {
	return e.maxAgents
}

// Stats returns engine statistics
func (e *Engine) Stats() EngineStats {
	e.mu.RLock()
	defer e.mu.RUnlock()

	stats := EngineStats{
		MaxAgents: e.maxAgents,
	}
	for _, a := range e.agents {
		stats.Total++
		snap := a.Snapshot()
		switch snap.State {
		case AgentRunning, AgentStarting:
			stats.Active++
		case AgentComplete:
			stats.Completed++
		case AgentError:
			stats.Errored++
		case AgentKilled:
			stats.Killed++
		}
	}
	return stats
}

// EngineStats holds summary statistics about the engine
type EngineStats struct {
	Total int `json:"total"`
	// Active counts running/starting agents only, for display. It is not
	// the capacity number: use ActiveCount for anything that gates on the
	// cap, which also counts agents still being routed.
	Active    int `json:"active"`
	Completed int `json:"completed"`
	Errored   int `json:"errored"`
	Killed    int `json:"killed"`
	MaxAgents int `json:"max_agents"`
}
