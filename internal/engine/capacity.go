package engine

import "path/filepath"

// ActiveCount returns the number of agents currently occupying a pool slot.
// It is the same number Stats().Active reports — both derive from
// AgentState.Active() — kept as its own accessor because it is cheaper than
// building a full EngineStats and reads better at call sites that only need
// the count. The two used to be computed separately, and a scheduler sizing
// its dispatch on the stats number over-dispatched for the whole duration of
// a smart-route classifier round trip and then had its spawn refused: the
// count the engine enforces and the count it reports must never diverge.
func (e *Engine) ActiveCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.activeCountLocked()
}

// activeCountLocked is ActiveCount with e.mu already held.
func (e *Engine) activeCountLocked() int {
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
		if snap.State.Active() {
			stats.Active++
			continue
		}
		switch snap.State {
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
	// Active is the same count ActiveCount returns: agents currently
	// occupying a pool slot, per AgentState.Active(). Three consumers
	// (workflows.go's spawn gate, cmd_agents.go and cmd_prime.go's "N/M
	// active" display) treat this as a capacity number, so it must not
	// silently drop agents still being routed the way it once did.
	Active    int `json:"active"`
	Completed int `json:"completed"`
	Errored   int `json:"errored"`
	Killed    int `json:"killed"`
	MaxAgents int `json:"max_agents"`
}
