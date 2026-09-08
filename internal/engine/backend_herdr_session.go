package engine

import "fmt"

// UnattendedSessionCommand has no implementation for the herdr backend: the
// interface contract (see backend.go) requires a call that runs to
// completion without ever blocking on interactive input, driven by one exec
// that returns when the answer is ready. herdr's value here is the opposite
// of that — it exists specifically to drive claude's interactive TUI inside
// a herdr-managed pane instead of print mode — so there is nothing behind
// Binary()/Args() that could satisfy this unattended, single-exec shape
// without a herdr pane, a live agent, and a settled-state poll standing in
// for what print mode gives for free.
//
// Callers such as the rebase-conflict-resolution flow in worktree.go need a
// backend that can guarantee this; they should be configured to use claude or
// pi instead, exactly as this error return is designed to make explicit. See
// also the startup check in internal/daemon/cmd.go, which warns when the
// configured default backend cannot satisfy this so the gap surfaces before
// a rebase actually needs it, not after.
func (b *herdrBackend) UnattendedSessionCommand(prompt string) (string, []string, error) {
	return "", nil, fmt.Errorf("herdr backend: no unattended/non-interactive session mode " +
		"(it exists to avoid claude's print mode, which this call would require); " +
		"use the claude or pi backend for unattended sessions")
}
