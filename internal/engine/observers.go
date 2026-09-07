package engine

import "time"

// observerEntry pairs an additional observer with the token used to remove
// it again. Slice rather than map so notification order stays stable.
type observerEntry struct {
	id int64
	fn func(agentID string)
}

// OnAgentUpdate registers the primary callback that fires whenever an
// agent's state or output changes. The callback receives the agent ID and
// must be non-blocking. Only one primary callback is supported; subsequent
// calls replace the previous one. The daemon uses this slot for its WS
// broadcast hook -- everything else should use AddAgentObserver so the two
// do not evict each other.
func (e *Engine) OnAgentUpdate(fn func(agentID string)) {
	e.updateMu.Lock()
	e.primaryObserver = fn
	e.updateMu.Unlock()
}

// AddAgentObserver registers an additional update callback and returns a
// closure that removes it again. Unlike OnAgentUpdate, repeated calls
// accumulate: every registered observer is notified on every update. The
// callback must be non-blocking -- it runs on the debounce timer goroutine.
//
// The returned remove closure is idempotent.
func (e *Engine) AddAgentObserver(fn func(agentID string)) (remove func()) {
	if fn == nil {
		return func() {}
	}
	e.updateMu.Lock()
	e.observerSeq++
	id := e.observerSeq
	e.extraObservers = append(e.extraObservers, observerEntry{id: id, fn: fn})
	e.updateMu.Unlock()

	return func() {
		e.updateMu.Lock()
		defer e.updateMu.Unlock()
		for i, o := range e.extraObservers {
			if o.id == id {
				e.extraObservers = append(e.extraObservers[:i], e.extraObservers[i+1:]...)
				return
			}
		}
	}
}

// observers snapshots the registered callbacks under the read lock so the
// notification itself runs unlocked (an observer that re-enters the engine
// would otherwise deadlock against a concurrent Add/Remove).
func (e *Engine) observers() []func(agentID string) {
	e.updateMu.RLock()
	defer e.updateMu.RUnlock()
	out := make([]func(string), 0, len(e.extraObservers)+1)
	if e.primaryObserver != nil {
		out = append(out, e.primaryObserver)
	}
	for _, o := range e.extraObservers {
		out = append(out, o.fn)
	}
	return out
}

// notifyUpdate fires the registered observer callback, debounced PER AGENT to
// coalesce rapid bursts of output into a single notification every 50ms. The
// debounce must be per agent: a shared timer would let a chatty agent B
// swallow agent A's pending notification — including A's terminal
// complete/error transition, which then never reaches WS subscribers.
func (e *Engine) notifyUpdate(agentID string) {
	if len(e.observers()) == 0 {
		return
	}

	e.timerMu.Lock()
	if t := e.updateTimers[agentID]; t != nil {
		t.Stop()
	}
	e.updateTimers[agentID] = time.AfterFunc(50*time.Millisecond, func() {
		e.timerMu.Lock()
		delete(e.updateTimers, agentID)
		e.timerMu.Unlock()
		// Re-read the observer set at fire time: a listener registered
		// during the debounce window still gets this notification, and one
		// removed during it does not.
		for _, fn := range e.observers() {
			fn(agentID)
		}
	})
	e.timerMu.Unlock()
}
